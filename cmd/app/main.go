package main

import (
	"context"
	"flag"
	"log/slog"
	"os"

	"seedwright/ext"
	"seedwright/internal/app"

	_ "github.com/mattn/go-sqlite3"
)

const usage = `seedwright — a lightweight web UI for stable-diffusion.cpp

Usage:
  seedwright [options] [config.yaml]

Options:
  --help, -h    Show this help message
  --version     Show version information

Positional arguments:
  config.yaml   Path to configuration file (default: config.yaml)

Environment:
  No environment variables are required. All configuration
  comes from the YAML file.

Example:
  ./seedwright
  ./seedwright /etc/seedwright/config.yaml
  seedwright --help

`

func main() {
	// Parse flags before positional args.
	debug := flag.Bool("debug", false, "enable debug-level logging")
	flag.Usage = func() {
		os.Stdout.Write([]byte(usage))
	}
	flag.Parse()

	// Enable debug-level logging so SyncFromStorage traces are visible.
	if *debug {
		slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
			Level: slog.LevelDebug,
		})))
	}

	// Build remaining positional args (skip flags like --help).
	args := flag.Args()

	cfgPath := "config.yaml"
	if len(args) > 0 {
		cfgPath = args[0]
	}

	ctx := context.Background()

	// Bootstrap core services (config, DB, storage, repos, JobService, mux).
	a, err := app.Bootstrap(cfgPath, *debug)
	if err != nil {
		slog.Error("bootstrap failed", "error", err)
		os.Exit(1)
	}
	defer a.DB.Close()

	// Register bundled extensions (routes, hooks, schema migration).
	if err := ext.RegisterAll(ctx, a, a.Config); err != nil {
		slog.Error("register extensions failed", "error", err)
		os.Exit(1)
	}

	// Run startup cleanup (cancel stuck jobs) and S3→SQLite sync.
	if err := a.SyncAndCleanup(ctx); err != nil {
		slog.Error("sync and cleanup failed", "error", err)
		os.Exit(1)
	}

	// Sync bundled extensions (S3→SQLite rebuild).
	if err := ext.SyncAll(ctx, a, a.Config); err != nil {
		slog.Error("sync extensions failed", "error", err)
		os.Exit(1)
	}

	// Start HTTP server and block until shutdown.
	if err := a.Serve(ctx); err != nil {
		slog.Error("server error", "error", err)
		os.Exit(1)
	}
}
