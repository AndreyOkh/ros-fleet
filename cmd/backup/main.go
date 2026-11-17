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

	"github.com/username/real-mikrotik-backup/internal/config"
	"github.com/username/real-mikrotik-backup/internal/runner"
)

var (
	// Version info (can be populated via -ldflags during build)
	version = "1.0.0"
	commit  = "none"
	date    = "unknown"
)

func main() {
	configPath := flag.String("config", "config.json", "Path to JSON configuration file")
	runOnce := flag.Bool("once", false, "Run backup once and exit immediately")
	logLevelStr := flag.String("log-level", "info", "Log level (debug, info, warn, error)")
	logFormat := flag.String("log-format", "text", "Log format (text, json)")
	showVersion := flag.Bool("version", false, "Print application version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Printf("RealMikrotikBackup version %s (commit %s, built at %s)\n", version, commit, date)
		os.Exit(0)
	}

	logger := setupLogger(*logLevelStr, *logFormat)
	slog.SetDefault(logger)

	logger.Info("Initializing RealMikrotikBackup",
		slog.String("version", version),
		slog.String("config", *configPath),
		slog.Bool("once", *runOnce),
	)

	cfg, err := config.Load(*configPath)
	if err != nil {
		logger.Error("Failed to load configuration", slog.Any("error", err))
		os.Exit(1)
	}

	logger.Info("Configuration loaded successfully",
		slog.Int("routers_count", len(cfg.Routers)),
		slog.String("backup_dir", cfg.BackupDir),
		slog.Int("concurrency", cfg.Concurrency),
		slog.String("interval", cfg.ScheduleInterval),
	)

	// Graceful shutdown context
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGINT)
	defer cancel()

	backupRunner := runner.New(cfg, logger)

	if *runOnce {
		logger.Info("Executing single backup run")
		summary, err := backupRunner.Run(ctx)
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

	// Daemon mode: Execute immediately on startup, then run on interval
	interval := cfg.ParsedScheduleInterval()
	logger.Info("Running in daemon mode", slog.Duration("interval", interval))

	// Initial backup execution
	logger.Info("Executing initial backup run on startup")
	if _, err := backupRunner.Run(ctx); err != nil {
		logger.Error("Initial backup run failed", slog.Any("error", err))
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			logger.Info("Shutdown signal received, shutting down gracefully...")
			return
		case tickTime := <-ticker.C:
			logger.Info("Triggering scheduled backup run", slog.Time("trigger_time", tickTime))
			if _, err := backupRunner.Run(ctx); err != nil {
				logger.Error("Scheduled backup run failed", slog.Any("error", err))
			}
		}
	}
}

// setupLogger initializes slog.Logger with specified format and level.
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
