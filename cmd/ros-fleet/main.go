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
	"github.com/AndreyOkh/ros-fleet/internal/state"
	"github.com/AndreyOkh/ros-fleet/internal/web"
)

var (
	// Version metadata populated via -ldflags
	version = "2.0.0"
	commit  = "none"
	date    = "unknown"
)

func main() {
	configPath := flag.String("config", "config.json", "Path to JSON configuration file")
	runOnce := flag.Bool("once", false, "Run backup once and exit immediately without starting web dashboard")
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
	)

	// Thread-safe configuration manager
	cfgMgr, err := config.NewManager(*configPath)
	if err != nil {
		logger.Error("Failed to load configuration", slog.Any("error", err))
		os.Exit(1)
	}

	initialCfg := cfgMgr.Get()
	logger.Info("Configuration loaded successfully",
		slog.Int("routers_count", len(initialCfg.Routers)),
		slog.String("backup_dir", initialCfg.BackupDir),
		slog.Int("concurrency", initialCfg.Concurrency),
		slog.String("interval", initialCfg.ScheduleInterval),
		slog.Bool("web_enabled", initialCfg.Web.Enabled),
	)

	// In-memory telemetry & state registry
	registry := state.NewRegistry()
	registry.InitFromConfig(initialCfg.Routers)

	// Graceful shutdown context
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGINT)
	defer cancel()

	fleetRunner := runner.New(cfgMgr, registry, logger)

	// Mode 1: Single execution
	if *runOnce {
		logger.Info("Executing single backup run")
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

	// Mode 2: Daemon with Web UI and Scheduler
	var webServer *web.Server
	if initialCfg.Web.Enabled {
		var srvErr error
		webServer, srvErr = web.NewServer(cfgMgr, fleetRunner, registry, logger)
		if srvErr != nil {
			logger.Error("Failed to initialize web server", slog.Any("error", srvErr))
			os.Exit(1)
		}
		if err := webServer.Start(); err != nil {
			logger.Error("Failed to start web server", slog.Any("error", err))
			os.Exit(1)
		}
	}

	// Execute initial backup run on startup
	logger.Info("Executing initial fleet backup and telemetry poll on startup")
	if _, err := fleetRunner.Run(ctx); err != nil {
		logger.Error("Initial backup run failed", slog.Any("error", err))
	}

	interval := initialCfg.ParsedScheduleInterval()
	logger.Info("Running in daemon mode", slog.Duration("interval", interval))

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			logger.Info("Shutdown signal received, initiating graceful termination...")

			if webServer != nil {
				shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
				_ = webServer.Shutdown(shutdownCtx)
				shutdownCancel()
			}

			logger.Info("ros-fleet terminated gracefully")
			return

		case tickTime := <-ticker.C:
			// Read current interval from config in case it was updated
			currentCfg := cfgMgr.Get()
			if newInterval := currentCfg.ParsedScheduleInterval(); newInterval != interval {
				interval = newInterval
				ticker.Reset(interval)
				logger.Info("Scheduler interval updated", slog.Duration("new_interval", interval))
			}

			logger.Info("Triggering scheduled fleet backup run", slog.Time("trigger_time", tickTime))
			if _, err := fleetRunner.Run(ctx); err != nil {
				logger.Error("Scheduled backup run failed", slog.Any("error", err))
			}
		}
	}
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
