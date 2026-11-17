package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/AndreyOkh/ros-fleet/internal/config"
	"github.com/AndreyOkh/ros-fleet/internal/runner"
	"github.com/AndreyOkh/ros-fleet/internal/scheduler"
	"github.com/AndreyOkh/ros-fleet/internal/state"
	"github.com/AndreyOkh/ros-fleet/internal/web"
)

var (
	// Version metadata populated via -ldflags
	version = "2.1.0"
	commit  = "none"
	date    = "unknown"
)

func main() {
	configPath := flag.String("config", "config.json", "Path to JSON configuration file")
	runOnce := flag.Bool("once", false, "Run backup once and exit immediately without starting web dashboard")
	runNow := flag.Bool("run-now", false, "Execute an immediate backup run on startup before waiting for schedule")
	logLevelStr := flag.String("log-level", "info", "Log level (debug, info, warn, error)")
	logFormat := flag.String("log-format", "text", "Log format (text, json)")
	showVersion := flag.Bool("version", false, "Print application version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Printf("ros-fleet version %s (commit %s, built at %s)\n", version, commit, date)
		os.Exit(0)
	}

	logger := setupLogger(*logLevelStr, *logFormat)
	slog.SetDefault(logger)

	logger.Info("Initializing ros-fleet",
		slog.String("version", version),
		slog.String("config", *configPath),
		slog.Bool("once", *runOnce),
		slog.Bool("run_now", *runNow),
	)

	// 1. Thread-safe configuration manager
	cfgMgr, err := config.NewManager(*configPath)
	if err != nil {
		logger.Error("Failed to load configuration", slog.Any("error", err))
		os.Exit(1)
	}

	initialCfg := cfgMgr.Get()
	logger.Info("Configuration loaded successfully",
		slog.Int("routers_count", len(initialCfg.Routers)),
		slog.String("binary_backup_dir", initialCfg.BinaryBackupDir),
		slog.String("export_dir", initialCfg.ExportDir),
		slog.String("state_file", initialCfg.StateFile),
		slog.Int("concurrency", initialCfg.Concurrency),
		slog.Any("schedule_times", initialCfg.Schedule.Times),
		slog.String("schedule_tz", initialCfg.Schedule.Timezone),
		slog.Bool("web_enabled", initialCfg.Web.Enabled),
		slog.Bool("git_enabled", initialCfg.Git.Enabled),
	)

	// 2. Persistent state manager (state.json)
	stateMgr, err := state.NewManager(initialCfg.StateFile, logger)
	if err != nil {
		logger.Error("Failed to initialize state manager", slog.Any("error", err))
		os.Exit(1)
	}
	stateMgr.InitFromConfig(initialCfg.Routers, initialCfg.Defaults)

	// 3. Time-of-day scheduler
	sched, err := scheduler.New(initialCfg.Schedule.Times, initialCfg.Schedule.Timezone, logger)
	if err != nil {
		logger.Error("Failed to initialize scheduler", slog.Any("error", err))
		os.Exit(1)
	}

	// 4. Graceful shutdown context
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGINT)
	defer cancel()

	fleetRunner := runner.New(cfgMgr, stateMgr, logger)

	// Mode A: Single execution and exit (-once)
	if *runOnce {
		logger.Info("Executing single backup run (-once)")
		summary, err := fleetRunner.Run(ctx)
		if err != nil {
			logger.Error("Backup run encountered fatal error", slog.Any("error", err))
			os.Exit(1)
		}

		if summary.FailedCount > 0 {
			logger.Warn("Single backup run finished with errors",
				slog.Int("failed", summary.FailedCount),
				slog.Int("success", summary.SuccessCount),
			)
			os.Exit(2)
		}

		logger.Info("Single backup run completed successfully")
		return
	}

	// Mode B: Daemon with Web UI and Time-of-Day Scheduler
	var webServer *web.Server
	if initialCfg.Web.Enabled {
		webServer, err = web.NewServer(cfgMgr, fleetRunner, stateMgr, sched, logger)
		if err != nil {
			logger.Error("Failed to initialize web server", slog.Any("error", err))
			os.Exit(1)
		}
		if err := webServer.Start(); err != nil {
			logger.Error("Failed to start web server", slog.Any("error", err))
			os.Exit(1)
		}
	}

	// Optional initial backup run ONLY if explicit flag -run-now was provided
	if *runNow {
		logger.Info("Executing startup backup run (-run-now requested)")
		if _, err := fleetRunner.Run(ctx); err != nil {
			logger.Error("Startup backup run failed", slog.Any("error", err))
		}
	} else {
		logger.Info("Auto-backup on startup skipped (waiting for scheduled time or manual trigger in Web UI)")
	}

	// Launch time-of-day scheduler in background
	go sched.Start(ctx, func(taskCtx context.Context) {
		if _, err := fleetRunner.Run(taskCtx); err != nil {
			logger.Error("Scheduled fleet backup failed", slog.Any("error", err))
		}
	})

	// Wait for shutdown signal
	<-ctx.Done()
	logger.Info("Shutdown signal received, initiating graceful termination...")

	if webServer != nil {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = webServer.Shutdown(shutdownCtx)
		shutdownCancel()
	}

	logger.Info("ros-fleet terminated gracefully")
}

func setupLogger(levelStr, format string) *slog.Logger {
	var level slog.Level
	switch strings.ToLower(levelStr) {
	case "debug":
		level = slog.LevelDebug
	case "warn", "warning":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		level = slog.LevelInfo
	}

	opts := &slog.HandlerOptions{
		Level: level,
	}

	var handler slog.Handler
	if strings.ToLower(format) == "json" {
		handler = slog.NewJSONHandler(os.Stdout, opts)
	} else {
		handler = slog.NewTextHandler(os.Stdout, opts)
	}

	return slog.New(handler)
}
