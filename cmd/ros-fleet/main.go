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
	"github.com/AndreyOkh/ros-fleet/internal/updater"
	versionPkg "github.com/AndreyOkh/ros-fleet/internal/version"
	"github.com/AndreyOkh/ros-fleet/internal/web"
)

var (
	// Backward-compatible ldflags targets in package main
	version = ""
	commit  = ""
	date    = ""
)

func init() {
	if version != "" && versionPkg.Version == "" {
		versionPkg.Version = version
	}
	if commit != "" && versionPkg.Commit == "" {
		versionPkg.Commit = commit
	}
	if date != "" && versionPkg.BuildDate == "" {
		versionPkg.BuildDate = date
	}
}

func main() {
	configPath := flag.String("config", "config.json", "Path to JSON configuration file")
	runOnce := flag.Bool("once", false, "Run backup once and exit immediately without starting web dashboard")
	runNow := flag.Bool("run-now", false, "Execute an immediate backup run on startup before waiting for schedule")
	logLevelStr := flag.String("log-level", "info", "Log level (debug, info, warn, error)")
	logFormat := flag.String("log-format", "text", "Log format (text, json)")
	showVersion := flag.Bool("version", false, "Print application version and exit")
	selfUpdate := flag.Bool("self-update", false, "Perform self-update to latest release")
	noUpdateCheck := flag.Bool("no-update-check", false, "Disable automatic check for updates on startup and in background (for offline/isolated environments)")
	disableUpdateCheck := flag.Bool("disable-update-check", false, "Alias for -no-update-check")
	sslFlag := flag.Bool("ssl", true, "Enable SSL/TLS encryption for web dashboard")
	flag.Parse()

	var sslExplicit *bool
	flag.Visit(func(f *flag.Flag) {
		if f.Name == "ssl" {
			v := *sslFlag
			sslExplicit = &v
		}
	})

	if *showVersion {
		v := versionPkg.Get()
		fmt.Println(v.String())
		os.Exit(0)
	}

	if *selfUpdate {
		if updater.IsRunningInDocker() {
			fmt.Print("ros-fleet is running inside a Docker container.\n" +
				"Self-update of binary is disabled. Please pull the latest container image:\n\n" +
				"    docker compose pull ros-fleet\n" +
				"    docker compose up -d ros-fleet\n")
			os.Exit(0)
		}
		logger := setupLogger(*logLevelStr, *logFormat)
		v := versionPkg.Get()
		if err := updater.RunSelfUpdate(context.Background(), "AndreyOkh/ros-fleet", v.Version, logger); err != nil {
			logger.Error("Self-update failed", slog.Any("error", err))
			os.Exit(1)
		}
		os.Exit(0)
	}

	logger := setupLogger(*logLevelStr, *logFormat)
	slog.SetDefault(logger)

	v := versionPkg.Get()
	logger.Info("Initializing ros-fleet",
		slog.String("version", v.Version),
		slog.String("config", *configPath),
		slog.Bool("once", *runOnce),
		slog.Bool("run_now", *runNow),
		slog.Bool("no_update_check", *noUpdateCheck || *disableUpdateCheck),
	)

	// 1. Thread-safe configuration manager
	cfgMgr, err := config.NewManager(*configPath)
	if err != nil {
		logger.Error("Failed to load configuration", slog.Any("error", err))
		os.Exit(1)
	}

	if sslExplicit != nil {
		cfgMgr.OverrideSSLEnabled(*sslExplicit)
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
		slog.Bool("ssl_enabled", initialCfg.IsSSLEnabled()),
		slog.String("ssl_cert_file", initialCfg.Web.SSLCertFile),
		slog.String("ssl_key_file", initialCfg.Web.SSLKeyFile),
		slog.Bool("git_enabled", initialCfg.Git.Enabled),
		slog.Bool("update_check_enabled", initialCfg.IsUpdateCheckEnabled()),
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
	var checker *updater.Checker
	updateCheckEnabled := initialCfg.IsUpdateCheckEnabled() && !*noUpdateCheck && !*disableUpdateCheck

	if updateCheckEnabled {
		checker = updater.NewChecker("AndreyOkh/ros-fleet", v.Version, logger)
		checker.Start(ctx)
	} else {
		logger.Info("Automatic update checks are disabled",
			slog.Bool("flag_no_update_check", *noUpdateCheck || *disableUpdateCheck),
			slog.Bool("config_enabled", initialCfg.IsUpdateCheckEnabled()),
		)
	}

	var webServer *web.Server
	if initialCfg.Web.Enabled {
		webServer, err = web.NewServer(cfgMgr, fleetRunner, stateMgr, sched, checker, logger)
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
