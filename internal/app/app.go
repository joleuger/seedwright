package app

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"seedwright/internal/authz"
	"seedwright/internal/config"
	"seedwright/internal/data"
	"seedwright/internal/data/querybuilder"
	"seedwright/internal/extdep"
	"seedwright/internal/server"
	"seedwright/internal/storage"
)

// App holds all dependencies needed by the server and extensions.
type App struct {
	Config *config.Config
	// ConfigPath is the path the config was loaded from (empty when the
	// app booted from first-run defaults with no config file). The
	// onboarding extension writes config.yaml back to this path.
	ConfigPath string
	DB         *sql.DB // shared connection, foreign_keys=ON
	Storage           storage.StorageBackend
	Projects          data.ProjectRepository
	Elements          data.ElementRepository
	Jobs              data.JobRepository
	JobService        *data.JobService
	// Authz is the access-control enforcer. nil when no auth: block is
	// configured (backward compat: core routes without RequireAction work
	// unchanged when auth is disabled).
	Authz authz.Enforcer
	Mux               http.Handler                 // top-level handler
	serveMux          *http.ServeMux               // internal mux (extensions register routes here)
	Hooks             *server.Hooks                // zero value is safe to use: empty slices
	QueryBuilder      *querybuilder.Builder        // extension filter/sort/join registry
	EnabledExtensions []string                     // registered extension keys (e.g. "joleuger/batch")
	// ExtDeps is the extension dependency graph, set by ext.RegisterAll
	// before any extension Initialize runs. Extensions use it to check
	// whether dependencies are enabled or already initialized (IsEnabled,
	// IsInitialized — both nil-safe). Nil until RegisterAll runs.
	ExtDeps *extdep.Graph
}

// GetServeMux returns the internal *http.ServeMux for extension route registration.
// Extensions should use this instead of a.Mux (which is the top-level http.Handler).
func (a *App) GetServeMux() *http.ServeMux {
	return a.serveMux
}

// Bootstrap loads config and builds storage/DB/repos/JobService/mux,
// and registers core's own routes on Mux. It does not run cleanup,
// sync, or start serving.
func Bootstrap(configPath string, debug bool) (*App, error) {
	// Load and validate configuration.
	cfg, err := config.Load(configPath)
	if err != nil {
		return nil, err
	}

	// Initialize the access-control enforcer (authz).
	// When no auth: block is present in config, auth is effectively
	// disabled — Authz will be nil and core routes without RequireAction
	// work unchanged. When auth is present, BuildEnforcerWithConfig reads
	// auth.engine from AuthConfig, builds the selected Enforcer, and
	// verifies the bootstrap invariant (root has global admin).
	var enforcer authz.Enforcer
	var resolver authz.IdentityResolver
	if cfg.Auth != nil {
		resolver = authz.StaticResolver{Principal: cfg.Auth.Principal}

		var err error
		enforcer, err = authz.BuildEnforcerWithConfig(
			context.Background(), cfg.Auth, resolver, cfg.Auth.Engine,
		)
		if err != nil {
			return nil, fmt.Errorf("build enforcer (engine=%q): %w", cfg.Auth.Engine, err)
		}
		// Defensive: ensure we never return a nil enforcer when auth is configured.
		// BuildEnforcerWithConfig should always return a non-nil enforcer on success,
		// but this catch prevents a nil-pointer panic in RequireAction if something
		// goes wrong downstream.
		if enforcer == nil {
			enforcer = &authz.StaticEnforcer{Principal: cfg.Auth.Principal}
		}
	}

	// Open SQLite cache.
	db, err := data.OpenSQLite(cfg.Database.SQLiteDatabase)
	if err != nil {
		return nil, err
	}

	// Create storage client (s3 or file, selected by config).
	store, err := storage.NewStorageBackend(cfg.StorageNode)
	if err != nil {
		db.Close()
		return nil, err
	}

	// Wire repositories.
	qb := querybuilder.NewBuilder()
	projRepo := data.NewProjectRepository(db, store)
	elemRepo := data.NewElementRepository(db, store, qb)
	jobRepo := data.NewJobRepository(db)

	// Wire OwnerUpdater into StaticEnforcer if the enforcer is a StaticEnforcer
	// (i.e. auth.engine is "static" or "ext/joleuger/static"). This lets the
	// control-plane claim-ownership page set primary_owner on projects.
	if enforcer != nil {
		if se, ok := enforcer.(*authz.StaticEnforcer); ok {
			se.OwnerUpdater = projRepo
		}
	}

	// Create JobService with backend resolver.
	jobService := &data.JobService{
		JobRepo:   jobRepo,
		ElmRepo:   elemRepo,
		Storage:   store,
		SDCPPBase: cfg.SDCPP.Backends[0].BaseURL,
		BackendResolver: func(name string) (string, error) {
			return cfg.BackendURL(name)
		},
		PollInterval: 2 * time.Second,
		Timeout:      10 * time.Minute,
		// Hooks will be wired by extensions if needed.
	}

	// Create HTTP server with REST-style routes.
	hooks := &server.Hooks{
		SettingsSavers: map[string]server.SettingsSaver{},
	}
	// Build the ControlPlaneAuthenticator from config. When no control_plane
	// is configured (empty string), core's DenyAllAuthenticator is used —
	// nobody can claim ownership until a real extension is set up.
	var cpAuth authz.ControlPlaneAuthenticator = authz.DenyAllAuthenticator{}
	if cfg.Auth != nil && cfg.Auth.ControlPlane != "" {
		// Control plane authenticators are provided by extensions. Core does
		// not build them itself — the extension must register one via an
		// init() that calls RegisterControlPlaneAuthenticator(). This keeps
		// core's surface minimal and gives extensions full ownership of
		// their own authentication mechanisms.
		if built, ok := authz.BuildControlPlaneAuthenticator(cfg.Auth.ControlPlane); ok {
			cpAuth = built
			slog.Info("control plane authenticator loaded", "key", cfg.Auth.ControlPlane)
		} else {
			slog.Warn(
				"control plane authenticator not found — falling back to DenyAll",
				"key", cfg.Auth.ControlPlane,
			)
			cpAuth = authz.DenyAllAuthenticator{}
		}
	}

	coreMux := server.New(&server.Config{
		Title:                cfg.Application.Title,
		PathPrefix:           cfg.Server.PathPrefix,
		Storage:              store,
		ProjectRepo:          projRepo,
		ElementRepo:          elemRepo,
		JobService:           jobService,
		BackendNames:         cfg.BackendNames(),
		DefaultBackend:       cfg.DefaultBackend(),
		BackendArchitecture:  cfg.BackendArchitecture,
		Hooks:                hooks,
		Debug:                debug,
		Authz:                enforcer,
		IdentityResolver:     resolver,
		ControlPlaneAuthenticator: cpAuth,
		EnabledExtensions:    func() []string {
			if cfg.Extensions == nil {
				return nil
			}
			keys := make([]string, 0, len(cfg.Extensions))
			for k := range cfg.Extensions {
				keys = append(keys, k)
			}
			return keys
		}(),
	})
	// Extract the underlying *http.ServeMux so extensions can register routes.
	sMux := coreMux.(*http.ServeMux)

	// Apply path-prefix stripping and debug logging. The mux is first
	// wrapped with debug logging (so every request is traced), then
	// prefix-stripped. This mirrors the old order inside server.New
	// before prefix stripping was moved to the caller.
	var httpHandler http.Handler = coreMux
	if debug {
		httpHandler = server.NewDebugLogging(coreMux, debug)
	}
	if cfg.Server.PathPrefix != "" {
		httpHandler = server.NewStripPrefix(cfg.Server.PathPrefix, httpHandler)
	}

	return &App{
		Config:       cfg,
		ConfigPath:   configPath,
		DB:           db,
		Storage:      store,
		Projects:     projRepo,
		Elements:     elemRepo,
		Jobs:         jobRepo,
		JobService:   jobService,
		Authz:        enforcer,
		Mux:          httpHandler,
		serveMux:     sMux,
		Hooks:        hooks,
		QueryBuilder: qb,
	}, nil
}

// SyncAndCleanup runs core's startup cleanup (CancelStuckJobs) and
// core's S3→SQLite sync. Call this AFTER extensions have registered
// (so their hooks/routes exist) and BEFORE extension sync (so
// elements/projects rows exist for extension foreign keys to
// resolve against.
func (a *App) SyncAndCleanup(ctx context.Context) error {
	// Clean up stuck jobs from previous runs (e.g., after restart).
	projects, err := a.Projects.ListProjects(ctx, false)
	if err != nil {
		return err
	}
	for _, project := range projects {
		if err := a.JobService.CancelStuckJobs(ctx, project); err != nil {
			slog.Warn("cancel stuck jobs (non-fatal)", "project", project, "error", err)
		}
	}

	// Sync S3 → SQLite.
	if err := a.Elements.SyncFromStorage(ctx); err != nil {
		return err
	}

	return nil
}

// Serve blocks on http.ListenAndServe. Call last.
func (a *App) Serve(ctx context.Context) error {
	slog.Info("seedwright ready",
		"listen", a.Config.Server.Listen,
		"backends", a.Config.BackendNames(),
		"database", a.Config.Database.SQLiteDatabase,
		"storage", a.Config.Storage.Endpoint,
		"bucket", a.Config.Storage.Bucket,
	)

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		if err := http.ListenAndServe(a.Config.Server.Listen, a.Mux); err != nil {
			slog.Error("server error", "error", err)
			os.Exit(1)
		}
	}()

	<-quit
	slog.Info("shutting down")
	return nil
}
