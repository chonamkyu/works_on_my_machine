package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"

	"github.com/nk/platform/api/internal/auth"
	"github.com/nk/platform/api/internal/db"
	"github.com/nk/platform/api/internal/generator"
	"github.com/nk/platform/api/internal/handler"
	"github.com/nk/platform/api/internal/llm"
	"github.com/nk/platform/api/internal/oauth"
	"github.com/nk/platform/api/internal/portreg"
	"github.com/nk/platform/api/internal/reflection"
	"github.com/nk/platform/api/internal/runner"
	"github.com/nk/platform/api/internal/scheduler"
	"github.com/nk/platform/api/internal/store"
	"github.com/nk/platform/api/internal/verify"
)

func main() {
	ctx := context.Background()

	pool, err := db.Open(ctx)
	if err != nil {
		log.Fatalf("db open: %v", err)
	}
	defer pool.Close()

	taskStore := store.NewMemory(pool)
	taskStore.Seed(ctx)

	authMW := auth.New(os.Getenv("PLATFORM_AUTH_DEV") == "1")
	auth.SetAdminEnv(os.Getenv("PLATFORM_ADMIN_USERS"))
	auth.SetAdminChecker(taskStore)

	bus := store.NewBus()
	bus.AttachPg(pool)

	files, err := store.NewWorkflowFS(envOr("PLATFORM_WORKFLOW_ROOT", defaultWorkflowRoot()))
	if err != nil {
		log.Fatalf("workflow fs: %v", err)
	}

	sdkPath := envOr("PLATFORM_SDK_PATH", defaultSDKPath())
	if _, err := os.Stat(sdkPath); err != nil {
		log.Printf("warning: SDK path %q does not exist — Node runs will fail until @platform/sdk is built", sdkPath)
	}

	memStore := store.NewMemoryStore(pool)
	evoStore := store.NewEvolutionStore(pool)

	credStore, err := store.NewCredentialStore(pool)
	if err != nil {
		log.Fatalf("credentials: %v", err)
	}

	// Provider-agnostic OpenAI-compatible embeddings. Honour both new
	// (OPENAI_*) and legacy (PLATFORM_LLMGATE_*) env names.
	if base := firstNonEmpty(envOr("OPENAI_BASE_URL", ""), envOr("PLATFORM_LLMGATE_URL", "")); base != "" {
		key := firstNonEmpty(os.Getenv("OPENAI_API_KEY"), os.Getenv("PLATFORM_LLMGATE_KEY"))
		embedder := llm.NewLlmgateEmbedder(base, key, envOr("PLATFORM_EMBEDDING_MODEL", "qwen3-embedding-0.6b"))
		memStore.SetEmbedder(embedder)
	}

	reflectClient := reflection.New(envOr("PLATFORM_REFLECTION_URL", "http://localhost:8082"))
	evoEngine := runner.NewEvolutionEngine(reflectClient, evoStore, taskStore, files, memStore, bus)
	hook := runner.ReflectionHook(reflectClient, memStore, bus, taskStore, evoEngine)

	mockRunner := runner.NewMock(taskStore, bus)
	ports := portreg.New()
	nodeRunner := runner.NewNode(taskStore, bus, files, sdkPath, hook, credStore, ports)
	dispatch := runner.NewDispatcher(files, nodeRunner, mockRunner)

	gen := generator.New(envOr("PLATFORM_GENERATOR_URL", "http://localhost:8081"))

	oauthMgr := oauth.NewManager(credStore, envOr("PLATFORM_PUBLIC_URL", "http://localhost:8787"))

	h := handler.New(taskStore, bus, dispatch, files, gen, memStore, evoStore, evoEngine, credStore, oauthMgr)
	h.Ports = ports
	h.Node = nodeRunner
	h.Verify = verify.New(envOr("PLATFORM_VERIFY_URL", "http://localhost:8083"))
	h.Reflection = reflectClient

	// Anything still marked "running" or "queued" at boot survived a process
	// restart — it can't still be in flight, so cancel it. Cleans up the
	// orphan run rows that otherwise stack up in the Runs tab forever.
	if n, err := taskStore.SweepOrphanRuns(); err != nil {
		log.Printf("orphan run sweep failed: %v", err)
	} else if n > 0 {
		log.Printf("orphan run sweep: cancelled %d stale rows", n)
	}

	// Phase 4: nightly skill-evolver. Looks at the user's accumulated memory
	// + corrections and asks reflection to propose new skills, which the
	// user reviews in Settings -> Skill proposals. Set tick to 30s for now
	// so dev/test sees it fire; production should use 12h.
	skillEvolveTick := 12 * time.Hour
	if v := envOr("PLATFORM_SKILL_EVOLVE_TICK", ""); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			skillEvolveTick = d
		}
	}
	evolver := runner.NewSkillEvolver(
		reflectClient, memStore, taskStore, bus,
		func(uid string) []reflection.ExistingSkill {
			out := []reflection.ExistingSkill{}
			dir := filepath.Join(getPromptsRoot(), "users", uid, "skills")
			if entries, err := os.ReadDir(dir); err == nil {
				for _, e := range entries {
					if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
						continue
					}
					body, _ := os.ReadFile(filepath.Join(dir, e.Name()))
					out = append(out, reflection.ExistingSkill{
						Name: strings.TrimSuffix(e.Name(), ".md"),
						Body: string(body),
					})
				}
			}
			return out
		},
		func(uid string, res *reflection.SkillProposeResult) {
			_, _ = taskStore.CreateSkillProposal(&store.SkillProposal{
				UserID:      uid,
				SkillName:   res.SkillName,
				SkillBody:   res.SkillBody,
				Rationale:   res.Rationale,
				Confidence:  res.Confidence,
				AppliesWhen: res.AppliesWhen,
			})
		},
		skillEvolveTick,
	)
	evolver.Start()
	defer evolver.Stop()
	h.SkillEvolver = evolver

	// Daily memory pruner: non-pinned, never-read entries older than 90d get
	// soft-archived so retrieval stays focused. Pinned entries never expire.
	pruneStop := make(chan struct{})
	go func() {
		// Settle wait so the boot path doesn't immediately compete with
		// migrations + supervisor warm-up.
		time.Sleep(2 * time.Minute)
		t := time.NewTicker(24 * time.Hour)
		defer t.Stop()
		runPrune := func() {
			n, err := memStore.PruneStale(store.DefaultUserID, 90, 60, 0)
			if err != nil {
				log.Printf("memory prune failed: %v", err)
				return
			}
			if n > 0 {
				log.Printf("memory prune: archived %d stale entries", n)
			}
		}
		runPrune()
		for {
			select {
			case <-pruneStop:
				return
			case <-t.C:
				runPrune()
			}
		}
	}()
	defer close(pruneStop)

	sched := scheduler.NewCron(taskStore, dispatch)
	sched.Start()
	stopCron := make(chan struct{})
	go sched.PollLoop(15*time.Second, stopCron)
	defer func() { close(stopCron); sched.Stop() }()

	// Long-running service workflows (kind=service) — daemon supervisor.
	// The reflection hook feeds every crash back through Reflection +
	// Evolution so the service heals itself over time.
	svc := runner.NewServiceManager(taskStore, nodeRunner, bus, hook, ports)
	svc.Start()
	defer svc.Stop()
	h.Services = svc

	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(120 * time.Second))

	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   []string{"http://localhost:5173"},
		AllowedMethods:   []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type", "X-Workspace-ID"},
		AllowCredentials: true,
		MaxAge:           300,
	}))

	r.Get("/healthz", h.Health)
	r.Post("/wh/{taskId}/{token}", h.Webhook)
	r.Get("/oauth/{provider}/callback", h.OAuthCallback)

	r.Route("/v1", func(r chi.Router) {
		r.Use(authMW.Authenticate)
		r.Use(h.UpsertUserMiddleware)
		r.Use(func(next http.Handler) http.Handler { return authMW.Workspace(taskStore)(next) })

		r.Get("/tasks", h.ListTasks)
		r.Post("/tasks", h.CreateTask)
		r.Get("/tasks/{id}", h.GetTask)
		r.Patch("/tasks/{id}", h.UpdateTask)
		r.Delete("/tasks/{id}", h.DeleteTask)
		r.Post("/tasks/{id}/runs", h.TriggerRun)
		// SSE routes moved to optional-auth group below
		r.Get("/tasks/{id}/code", h.GetTaskCode)
		r.Get("/tasks/{id}/runs", h.ListRuns)
		r.Patch("/tasks/{id}/triggers/{trigId}", h.SetTriggerEnabled)
		r.Patch("/tasks/{id}/service", h.SetServiceEnabled)
		r.Get("/tasks/{id}/service", h.ServiceStatus)

		r.Get("/archive/tasks", h.ListArchivedTasks)
		r.Get("/archive/tasks/{id}", h.GetArchivedTask)

		r.Get("/tasks/{id}/verification", h.GetVerification)
		r.Get("/tasks/{id}/verification/screenshot", h.GetVerificationScreenshot)
		r.Get("/tasks/{id}/dryrun", h.GetDryRun)
		r.Post("/tasks/{id}/dryrun", h.TriggerDryRun)
		r.Get("/tasks/{id}/ports", h.ListTaskPorts)

		r.Get("/profile", h.GetUserProfile)
		r.Put("/profile", h.PutUserProfile)
		r.Get("/profile/suggestions", h.ListProfileSuggestions)
		r.Post("/profile/suggestions/{id}", h.ResolveProfileSuggestion)

		r.Post("/skills/propose", h.ProposeSkill)
		r.Post("/skills", h.AcceptSkill)
		r.Get("/skills", h.ListUserSkills)
		r.Delete("/skills/{name}", h.DeleteUserSkill)

		r.Get("/skill-proposals", h.ListSkillProposals)
		r.Post("/skill-proposals/{id}/accept", h.AcceptSkillProposal)
		r.Post("/skill-proposals/{id}/reject", h.RejectSkillProposal)
		r.Post("/skill-proposals/evolve", h.TriggerSkillEvolve)

		r.Get("/memory/stats", h.MemoryStats)
		r.Post("/memory/prune", h.PruneMemory)

		r.Get("/runs/{id}", h.GetRun)
		r.Post("/runs/{id}/cancel", h.CancelRun)
		// SSE routes moved to optional-auth group below

		r.Post("/chat/{sessionId}/messages", h.ChatMessage)
		r.Post("/tasks/{id}/chat", h.TaskChat)
		r.Get("/chat/{sessionId}/messages", h.ListChatMessages)
		r.Get("/sessions", h.ListSessions)
		r.Post("/sessions", h.CreateSession)

		r.Get("/memory", h.ListMemory)
		r.Delete("/memory/{id}", h.DeleteMemory)

		r.Get("/evolution", h.ListEvolutionPRs)
		r.Get("/evolution/{id}", h.GetEvolutionPR)
		r.Post("/evolution/{id}/accept", h.AcceptEvolutionPR)
		r.Post("/evolution/{id}/reject", h.RejectEvolutionPR)
		r.Post("/tasks/{id}/evolve", h.ProposeEvolutionPR)

		r.Get("/credentials", h.ListCredentials)
		r.Post("/credentials", h.CreateCredential)
		r.Delete("/credentials/{id}", h.DeleteCredential)

		r.Get("/connectors", h.ListConnectors)
		r.Get("/models", h.ListModels)

		r.Get("/oauth/providers", h.ListOAuthProviders)
		r.Post("/oauth/start", h.StartOAuth)

		r.Get("/discover", h.DiscoverTasks)
		r.Post("/tasks/{id}/fork", h.ForkTask)

		// New: me + workspaces
		r.Get("/me", h.Me)
		r.Get("/workspaces", h.ListWorkspaces)
		r.Post("/workspaces", h.CreateWorkspace)
		r.Post("/workspaces/{id}/members", h.InviteMember)
		r.Delete("/workspaces/{id}/members/{uid}", h.RemoveMember)
		r.Get("/workspaces/{id}/members", h.ListMembers)

		// SSE / streaming routes: use Optional auth so EventSource without an
		// Authorization header is not rejected; Workspace resolution still runs
		// (it is a no-op when there are no claims).
		r.Group(func(r chi.Router) {
			r.Use(authMW.Optional)
			r.Use(func(next http.Handler) http.Handler { return authMW.Workspace(taskStore)(next) })
			r.Get("/tasks/{id}/events", h.TaskEventsStream)
			r.Get("/runs/{id}/stream", h.RunStream)
			r.Get("/users/{id}/activity", h.ActivityStream)
		})

		// Admin sub-group (guarded by AdminOnly middleware)
		r.Group(func(r chi.Router) {
			r.Use(authMW.AdminOnly)
			r.Post("/admin/slack/provision", h.ProvisionSlackApp)
			r.Post("/admin/slack/refresh-scopes", h.RefreshSlackScopes)
			r.Get("/admin/users", h.ListUsers)
			r.Post("/admin/users/{sub}/admin", h.SetUserAdmin)
			r.Post("/admin/users/{sub}/disabled", h.SetUserDisabled)
			r.Get("/admin/audit", h.ListAudit)
		})
	})

	// All HTTP methods need to reverse-proxy through to the workflow — the
	// dashboard might serve POST /api/* JSON endpoints, PUT uploads, etc.
	r.HandleFunc("/d/{taskId}/*", h.ServeDashboard)

	addr := envOr("PLATFORM_API_ADDR", ":8787")
	log.Printf("api listening on %s", addr)
	if err := http.ListenAndServe(addr, r); err != nil {
		log.Fatal(err)
	}
}

func getPromptsRoot() string {
	if v := os.Getenv("PLATFORM_GENERATOR_PROMPTS_DIR"); v != "" {
		return v
	}
	wd, _ := os.Getwd()
	return filepath.Join(filepath.Dir(wd), "generator", "prompts")
}

func defaultWorkflowRoot() string {
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".platform", "workflows")
	}
	return "/tmp/platform/workflows"
}

func defaultSDKPath() string {
	wd, _ := os.Getwd()
	return filepath.Join(filepath.Dir(wd), "sdk")
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
