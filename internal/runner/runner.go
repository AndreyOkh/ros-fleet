package runner

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/username/real-mikrotik-backup/internal/config"
	"github.com/username/real-mikrotik-backup/internal/mikrotik"
	"github.com/username/real-mikrotik-backup/internal/notifier"
	"github.com/username/real-mikrotik-backup/internal/storage"
)

// Runner coordinates the backup pipeline across all configured routers.
type Runner struct {
	cfg      *config.Config
	storage  *storage.Storage
	notifier *notifier.TelegramNotifier
	logger   *slog.Logger
}

// New creates a new Runner instance.
func New(cfg *config.Config, logger *slog.Logger) *Runner {
	return &Runner{
		cfg:      cfg,
		storage:  storage.New(cfg.BackupDir),
		notifier: notifier.NewTelegramNotifier(cfg.Telegram, logger),
		logger:   logger.With(slog.String("component", "runner")),
	}
}

// Run executes the backup process for all routers concurrently up to Concurrency limit.
func (r *Runner) Run(ctx context.Context) (notifier.SummaryReport, error) {
	startTime := time.Now()
	r.logger.Info("Starting MikroTik backup run",
		slog.Int("routers_count", len(r.cfg.Routers)),
		slog.Int("concurrency", r.cfg.Concurrency),
	)

	sem := make(chan struct{}, r.cfg.Concurrency)
	var wg sync.WaitGroup
	var mu sync.Mutex

	reports := make([]notifier.RouterReport, 0, len(r.cfg.Routers))

	for _, routerCfg := range r.cfg.Routers {
		// Stop scheduling new backups if shutdown signal received
		select {
		case <-ctx.Done():
			r.logger.Warn("Global context cancelled, aborting remaining router backups",
				slog.String("router", routerCfg.Name),
			)
			mu.Lock()
			reports = append(reports, notifier.RouterReport{
				Name:    routerCfg.Name,
				Host:    routerCfg.Host,
				Success: false,
				Err:     fmt.Errorf("backup aborted: %w", ctx.Err()),
			})
			mu.Unlock()
			continue
		case sem <- struct{}{}:
		}

		wg.Add(1)
		go func(rc config.RouterConfig) {
			defer func() {
				<-sem
				wg.Done()
			}()

			rep := r.backupRouter(ctx, rc)

			mu.Lock()
			reports = append(reports, rep)
			mu.Unlock()
		}(routerCfg)
	}

	wg.Wait()

	endTime := time.Now()
	totalDuration := endTime.Sub(startTime)

	successCount := 0
	failedCount := 0
	for _, rep := range reports {
		if rep.Success {
			successCount++
		} else {
			failedCount++
		}
	}

	summary := notifier.SummaryReport{
		StartTime:    startTime,
		EndTime:      endTime,
		Duration:     totalDuration,
		TotalRouters: len(reports),
		SuccessCount: successCount,
		FailedCount:  failedCount,
		Routers:      reports,
	}

	r.logger.Info("Completed backup run for all routers",
		slog.Duration("duration", totalDuration),
		slog.Int("success", successCount),
		slog.Int("failed", failedCount),
	)

	// Step: Local retention cleanup
	removedDirs, rotateErr := r.storage.Rotate(r.cfg.RetentionDays, r.logger)
	if rotateErr != nil {
		r.logger.Error("Local retention rotation encountered errors", slog.Any("error", rotateErr))
	} else if removedDirs > 0 {
		r.logger.Info("Local retention cleanup completed", slog.Int("removed_directories", removedDirs))
	}

	// Step: Send notification to Telegram
	if notifyErr := r.notifier.SendReport(ctx, summary); notifyErr != nil {
		r.logger.Error("Failed to send Telegram notification", slog.Any("error", notifyErr))
	}

	return summary, nil
}

// backupRouter performs the atomic backup lifecycle for a single router.
func (r *Runner) backupRouter(parentCtx context.Context, rc config.RouterConfig) notifier.RouterReport {
	start := time.Now()
	log := r.logger.With(slog.String("router", rc.Name), slog.String("host", rc.Host))

	log.Info("Starting backup for router")

	timeout := time.Duration(r.cfg.TimeoutSec) * time.Second
	ctx, cancel := context.WithTimeout(parentCtx, timeout)
	defer cancel()

	report := notifier.RouterReport{
		Name: rc.Name,
		Host: rc.Host,
	}

	defer func() {
		report.Duration = time.Since(start)
		if report.Success {
			log.Info("Router backup completed successfully",
				slog.Duration("duration", report.Duration),
				slog.Int64("rsc_size", report.RscSize),
				slog.Int64("backup_size", report.BackupSize),
			)
		} else {
			log.Error("Router backup failed",
				slog.Duration("duration", report.Duration),
				slog.Any("error", report.Err),
			)
		}
	}()

	// 1. Prepare local directory structure
	destPaths, err := r.storage.PrepareDestination(rc.Name, start)
	if err != nil {
		report.Err = fmt.Errorf("local storage preparation failed: %w", err)
		return report
	}

	// 2. Connect to RouterOS via SSH & SFTP
	client, err := mikrotik.Connect(ctx, rc, timeout, r.cfg.InsecureIgnoreHostKey, r.logger)
	if err != nil {
		report.Err = fmt.Errorf("connection failed: %w", err)
		return report
	}
	defer client.Close()

	// 3. Initiate text export (.rsc)
	log.Debug("Creating configuration export (.rsc)")
	if err := client.CreateExport(ctx, destPaths.BaseFilename, rc.ExportSensitive); err != nil {
		report.Err = fmt.Errorf("export command failed: %w", err)
		return report
	}

	// 4. Initiate binary backup (.backup)
	log.Debug("Creating binary system backup (.backup)")
	if err := client.CreateBackup(ctx, destPaths.BaseFilename, rc.EncryptBackup, rc.BackupPassword); err != nil {
		report.Err = fmt.Errorf("backup command failed: %w", err)
		return report
	}

	// 5. Wait for files to finish writing on RouterOS flash/disk
	fileWaitTimeout := timeout / 2
	if fileWaitTimeout < 30*time.Second {
		fileWaitTimeout = 30 * time.Second
	}

	remoteRscPath, _, err := client.WaitForFile(ctx, destPaths.BaseFilename, "rsc", fileWaitTimeout)
	if err != nil {
		report.Err = fmt.Errorf("waiting for .rsc file failed: %w", err)
		return report
	}

	remoteBackupPath, _, err := client.WaitForFile(ctx, destPaths.BaseFilename, "backup", fileWaitTimeout)
	if err != nil {
		report.Err = fmt.Errorf("waiting for .backup file failed: %w", err)
		return report
	}

	// 6. Download .rsc via SFTP
	rscFile, err := client.OpenRemoteFile(remoteRscPath)
	if err != nil {
		report.Err = fmt.Errorf("failed to open remote rsc file: %w", err)
		return report
	}
	defer rscFile.Close()

	rscBytes, err := r.storage.SaveStream(rscFile, destPaths.LocalRscPath)
	_ = rscFile.Close()
	if err != nil {
		report.Err = fmt.Errorf("failed to download and save rsc file: %w", err)
		return report
	}
	report.RscSize = rscBytes

	// 7. Download .backup via SFTP
	backupFile, err := client.OpenRemoteFile(remoteBackupPath)
	if err != nil {
		report.Err = fmt.Errorf("failed to open remote backup file: %w", err)
		return report
	}
	defer backupFile.Close()

	backupBytes, err := r.storage.SaveStream(backupFile, destPaths.LocalBackupPath)
	_ = backupFile.Close()
	if err != nil {
		report.Err = fmt.Errorf("failed to download and save backup file: %w", err)
		return report
	}
	report.BackupSize = backupBytes

	// 8. Verify local files exist and are not empty
	if _, err := r.storage.VerifyFile(destPaths.LocalRscPath); err != nil {
		report.Err = fmt.Errorf("rsc local file integrity check failed: %w", err)
		return report
	}
	if _, err := r.storage.VerifyFile(destPaths.LocalBackupPath); err != nil {
		report.Err = fmt.Errorf("backup local file integrity check failed: %w", err)
		return report
	}

	// 9. Remove temporary files from RouterOS (Critical to preserve flash memory)
	log.Debug("Cleaning up temporary backup files from RouterOS flash/disk")
	_ = client.RemoveFile(ctx, remoteRscPath)
	_ = client.RemoveFile(ctx, remoteBackupPath)

	report.Success = true
	return report
}
