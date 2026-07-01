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
	"github.com/nk/platform/api/internal/supervisor"
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
	taskStore.Seed(ctx, "dev")

	authMW := auth.New(os.Getenv("PLATFORM_AUTH_DEV") == "1")
	auth.SetAdminEnv(os.Getenv("PLATFORM_ADMIN_USERS"))
	auth.SetAdminChecker(taskStore)
	if secret := firstNonEmpty(os.Getenv("PLATFORM_PROXY_SECRET"), os.Getenv("PLATFORM_ENCRYPTION_KEY")); secret != "" {
		auth.SetProxySecret(secret)
	} else {
		// Fail-closed: without a signing secret the proxy/iframe cookie auth
		// cannot mint or verify tokens. The server still runs; the proxy just
		// refuses to issue sessions until a secret is configured.
		log.Printf("WARNING: proxy/iframe auth is DISABLED — set PLATFORM_PROXY_SECRET or PLATFORM_ENCRYPTION_KEY to enable proxy-session cookies")
	}

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
	// Read the proxy-cookie Secure attribute once at startup rather than
	// per-request in SetProxySession.
	h.ProxyCookieSecure = strings.HasPrefix(os.Getenv("PLATFORM_PUBLIC_URL"), "https")

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
				WorkspaceID: uid, // GlobalScope for the evolver
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
			workspaces := memStore.ListAllWorkspaceIDs()
			if len(workspaces) == 0 {
				workspaces = []string{"dev"} // fallback for empty DB
			}
			for _, ws := range workspaces {
				n, err := memStore.PruneStale(ws, 90, 60, 0)
				if err != nil {
					log.Printf("memory prune failed for %s: %v", ws, err)
					continue
				}
				if n > 0 {
					log.Printf("memory prune: archived %d stale entries for workspace %s", n, ws)
				}
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

	sup, err := supervisor.NewClient(envOr("PLATFORM_RUNTIME", "process"), supervisor.Deps{
		Store:   taskStore,
		Bus:     bus,
		Files:   files,
		Creds:   credStore,
		Ports:   ports,
		SDKPath: sdkPath,
		Node:    "",
		Hook:    hook,
	})
	if err != nil {
		log.Fatalf("supervisor: %v", err)
	}
	h.Sup = sup
	if err := sup.BootServices(); err != nil {
		log.Printf("supervisor boot: %v", err)
	}

	// Idle reaper: periodically stop workspace runtimes that have no active
	// service task. The process backend's ReapIdle is a no-op so this goroutine
	// is safe to start regardless of the configured runtime.
	reapTick := 5 * time.Minute
	if v := envOr("PLATFORM_IDLE_REAP_TICK", ""); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			reapTick = d
		} else {
			log.Printf("PLATFORM_IDLE_REAP_TICK: invalid duration %q, using default %v", v, reapTick)
		}
	}
	reapStop := make(chan struct{})
	go func() {
		t := time.NewTicker(reapTick)
		defer t.Stop()
		for {
			select {
			case <-reapStop:
				return
			case <-t.C:
				if err := sup.ReapIdle(); err != nil {
					log.Printf("idle reaper: %v", err)
				}
			}
		}
	}()
	defer close(reapStop)

	// MCP health ping loop.
	mcpPingTick := 60 * time.Second
	if v := envOr("PLATFORM_MCP_PING_TICK", ""); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			mcpPingTick = d
		} else {
			log.Printf("PLATFORM_MCP_PING_TICK: invalid duration %q, using default %v", v, mcpPingTick)
		}
	}
	mcpCtx, mcpCancel := context.WithCancel(ctx)
	go taskStore.RunMCPHealthLoop(mcpCtx, mcpPingTick)
	defer mcpCancel()

	sched := scheduler.NewCron(taskStore, sup)
	sched.Start()
	stopCron := make(chan struct{})
	go sched.PollLoop(15*time.Second, stopCron)
	defer func() { close(stopCron); sched.Stop() }()

	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(120 * time.Second))

	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   corsOrigins(),
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
		r.Get("/tasks/{id}/triggers", h.ListTaskTriggers)

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
		r.Post("/proxy-session", h.SetProxySession)

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

		// Global skill repository — reads open to any authenticated caller;
		// writes gated by AdminOnly.
		// NOTE: /resolve must be registered before /{name} so chi routes the
		// literal path correctly (chi prefers static segments over URL params
		// at the same position, so this ordering is correct; the explicit
		// registration below makes the intent explicit regardless).
		r.Get("/skill-repo", h.ListGlobalSkills)
		r.Get("/skill-repo/resolve", h.ResolveGlobalSkill)
		r.Get("/skill-repo/{name}", h.GetGlobalSkill)
		r.Get("/skill-repo/{name}/versions/{n}", h.GetGlobalSkillVersion)
		r.Group(func(r chi.Router) {
			r.Use(authMW.AdminOnly)
			r.Post("/skill-repo", h.CreateGlobalSkill)
			r.Post("/skill-repo/{name}/versions", h.PublishGlobalSkillVersion)
			r.Post("/skill-repo/{name}/aliases", h.MoveGlobalSkillAlias)
			r.Delete("/skill-repo/{name}", h.DeleteGlobalSkill)
		})

		// MCP registry — reads open to any authenticated caller.
		// NOTE: /mcp/tools (static) is registered before /mcp/{id}/tools so chi
		// routes the literal path correctly (chi prefers static segments over URL
		// params at the same position, so this ordering is correct).
		r.Get("/mcp", h.ListMCPServers)
		r.Get("/mcp/tools", h.ListAllMCPTools) // static before /{id} routes
		r.Get("/mcp/{id}/tools", h.ListMCPTools)
		r.Get("/mcp/{id}/stats", h.GetMCPServerStats)
		r.Post("/mcp/{id}/ping", h.PingMCPServer)
		r.Post("/mcp/{id}/call", h.CallMCPTool)
		r.Group(func(r chi.Router) {
			r.Use(authMW.AdminOnly)
			r.Post("/mcp", h.CreateMCPServer)
			r.Put("/mcp/{id}", h.UpdateMCPServer)
			r.Delete("/mcp/{id}", h.DeleteMCPServer)
			r.Post("/mcp/{id}/tools/{tool}/disabled", h.SetMCPToolDisabled)
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
	//
	// Both proxy routes apply Optional auth + Workspace resolution so the auth
	// context is populated. Iframes cannot set Authorization headers, so they
	// carry identity via the signed platform_proxy cookie (set by POST
	// /v1/proxy-session). Requests with no identity are rejected 403 by the
	// handler — there is no unauthenticated pass-through in production.
	r.Group(func(r chi.Router) {
		r.Use(authMW.Optional)
		r.Use(func(next http.Handler) http.Handler { return authMW.Workspace(taskStore)(next) })
		r.HandleFunc("/d/{taskId}/*", h.ServeDashboard)
		r.HandleFunc("/w/{ws}/d/{taskId}/*", h.ServeWorkspaceDashboard)
	})

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

// corsOrigins returns the allowed CORS origins, read from the comma-separated
// PLATFORM_CORS_ORIGINS env var. When unset it defaults to the local Vite dev
// origin. Entries are trimmed and empties dropped.
func corsOrigins() []string {
	raw := os.Getenv("PLATFORM_CORS_ORIGINS")
	if raw == "" {
		return []string{"http://localhost:5173"}
	}
	var origins []string
	for _, o := range strings.Split(raw, ",") {
		if o = strings.TrimSpace(o); o != "" {
			origins = append(origins, o)
		}
	}
	if len(origins) == 0 {
		return []string{"http://localhost:5173"}
	}
	return origins
}
