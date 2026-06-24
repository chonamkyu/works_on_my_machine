package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"

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
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type"},
		AllowCredentials: true,
		MaxAge:           300,
	}))

	r.Get("/healthz", h.Health)
	r.Post("/wh/{taskId}/{token}", h.Webhook)
	r.Get("/oauth/{provider}/callback", h.OAuthCallback)

	r.Route("/v1", func(r chi.Router) {
		r.Get("/tasks", h.ListTasks)
		r.Post("/tasks", h.CreateTask)
		r.Get("/tasks/{id}", h.GetTask)
		r.Patch("/tasks/{id}", h.UpdateTask)
		r.Delete("/tasks/{id}", h.DeleteTask)
		r.Post("/tasks/{id}/runs", h.TriggerRun)
		r.Get("/tasks/{id}/events", h.TaskEventsStream)
		r.Get("/tasks/{id}/code", h.GetTaskCode)
		r.Get("/tasks/{id}/runs", h.ListRuns)
		r.Patch("/tasks/{id}/triggers/{trigId}", h.SetTriggerEnabled)
		r.Patch("/tasks/{id}/service", h.SetServiceEnabled)
		r.Get("/tasks/{id}/service", h.ServiceStatus)

		r.Get("/runs/{id}", h.GetRun)
		r.Post("/runs/{id}/cancel", h.CancelRun)
		r.Get("/runs/{id}/stream", h.RunStream)

		r.Get("/users/{id}/activity", h.ActivityStream)

		r.Post("/chat/{sessionId}/messages", h.ChatMessage)
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
		r.Post("/admin/slack/provision", h.ProvisionSlackApp)
		r.Post("/admin/slack/refresh-scopes", h.RefreshSlackScopes)

		r.Get("/discover", h.DiscoverTasks)
		r.Post("/tasks/{id}/fork", h.ForkTask)
	})

	r.Get("/d/{taskId}/*", h.ServeDashboard)

	addr := envOr("PLATFORM_API_ADDR", ":8787")
	log.Printf("api listening on %s", addr)
	if err := http.ListenAndServe(addr, r); err != nil {
		log.Fatal(err)
	}
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
