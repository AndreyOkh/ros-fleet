package runner

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/AndreyOkh/ros-fleet/internal/config"
	"github.com/AndreyOkh/ros-fleet/internal/mikrotik"
	"github.com/AndreyOkh/ros-fleet/internal/notifier"
	"github.com/AndreyOkh/ros-fleet/internal/state"
	"github.com/AndreyOkh/ros-fleet/internal/storage"
)

// Runner coordinates the backup pipeline and telemetry collection.
type Runner struct {
	cfgMgr   *config.Manager
	storage  *storage.Storage
	notifier *notifier.TelegramNotifier
	registry *state.Registry
	logger   *slog.Logger
}

// New creates a new Runner instance.
func New(cfgMgr *config.Manager, registry *state.Registry, logger *slog.Logger) *Runner {
	cfg := cfgMgr.Get()
	return &Runner{
		cfgMgr:   cfgMgr,
		storage:  storage.New(cfg.BackupDir),
		notifier: notifier.NewTelegramNotifier(cfg.Telegram, logger),
		registry: registry,
		logger:   logger.With(slog.String("component", "runner")),
	}
}

// Run executes the backup process for all enabled routers concurrently up to Concurrency limit.
func (r *Runner) Run(ctx context.Context) (notifier.SummaryReport, error) {
	cfg := r.cfgMgr.Get()
	// Update storage baseDir in case it changed
	r.storage = storage.New(cfg.BackupDir)

	// Synchronize registry with configured devices
	r.registry.InitFromConfig(cfg.Routers)

	var activeRouters []config.RouterConfig
	for _, rc := range cfg.Routers {
		if rc.IsEnabled() {
			activeRouters = append(activeRouters, rc)
		} else {
			r.logger.Debug("Skipping disabled router", slog.String("router", rc.Name))
		}
	}

	startTime := time.Now()
	r.logger.Info("Starting MikroTik backup run",
		slog.Int("total_configured", len(cfg.Routers)),
		slog.Int("active_routers", len(activeRouters)),
		slog.Int("concurrency", cfg.Concurrency),
	)

	sem := make(chan struct{}, cfg.Concurrency)
	var wg sync.WaitGroup
	var mu sync.Mutex

	reports := make([]notifier.RouterReport, 0, len(activeRouters))

	for _, routerCfg := range activeRouters {
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

			rep := r.backupRouter(ctx, rc, cfg)

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

	r.logger.Info("Completed backup run for all active routers",
		slog.Duration("duration", totalDuration),
		slog.Int("success", successCount),
		slog.Int("failed", failedCount),
	)

	// Retention cleanup
	removedDirs, rotateErr := r.storage.Rotate(cfg.RetentionDays, r.logger)
	if rotateErr != nil {
		r.logger.Error("Local retention rotation encountered errors", slog.Any("error", rotateErr))
	} else if removedDirs > 0 {
		r.logger.Info("Local retention cleanup completed", slog.Int("removed_directories", removedDirs))
	}

	// Send notification to Telegram
	if notifyErr := r.notifier.SendReport(ctx, summary); notifyErr != nil {
		r.logger.Error("Failed to send Telegram notification", slog.Any("error", notifyErr))
	}

	return summary, nil
}

// RunSingle executes a backup for a single router on-demand.
func (r *Runner) RunSingle(ctx context.Context, routerName string) (notifier.RouterReport, error) {
	cfg := r.cfgMgr.Get()
	rc, found := r.cfgMgr.GetRouter(routerName)
	if !found {
		return notifier.RouterReport{}, fmt.Errorf("router %q not found in configuration", routerName)
	}

	report := r.backupRouter(ctx, rc, cfg)
	return report, report.Err
}

// RefreshDevice connects to a router, queries hardware/firmware telemetry, and updates state.
func (r *Runner) RefreshDevice(ctx context.Context, routerName string) (*mikrotik.DeviceInfo, error) {
	cfg := r.cfgMgr.Get()
	rc, found := r.cfgMgr.GetRouter(routerName)
	if !found {
		return nil, fmt.Errorf("router %q not found in configuration", routerName)
	}

	timeout := time.Duration(cfg.TimeoutSec) * time.Second
	opCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	client, err := mikrotik.Connect(opCtx, rc, timeout, cfg.InsecureIgnoreHostKey, r.logger)
	if err != nil {
		r.registry.SetOffline(routerName, err)
		return nil, fmt.Errorf("connection failed: %w", err)
	}
	defer client.Close()

	info, err := client.FetchDeviceInfo(opCtx)
	if err != nil {
		r.registry.SetOffline(routerName, err)
		return nil, fmt.Errorf("failed to fetch device info: %w", err)
	}

	r.registry.UpdateDeviceInfo(routerName, info.Model, info.Version, info.Uptime)
	return info, nil
}

// backupRouter performs the atomic backup lifecycle for a single router.
func (r *Runner) backupRouter(parentCtx context.Context, rc config.RouterConfig, cfg config.Config) notifier.RouterReport {
	start := time.Now()
	log := r.logger.With(slog.String("router", rc.Name), slog.String("host", rc.Host))

	log.Info("Starting backup for router")
	r.registry.SetRunning(rc.Name)

	timeout := time.Duration(cfg.TimeoutSec) * time.Second
	ctx, cancel := context.WithTimeout(parentCtx, timeout)
	defer cancel()

	report := notifier.RouterReport{
		Name: rc.Name,
		Host: rc.Host,
	}

	defer func() {
		report.Duration = time.Since(start)
		r.registry.UpdateBackupResult(rc.Name, report.Success, report.Err, report.RscSize, report.BackupSize)

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

	// 1. Prepare local storage path
	destPaths, err := r.storage.PrepareDestination(rc.Name, start)
	if err != nil {
		report.Err = fmt.Errorf("local storage preparation failed: %w", err)
		return report
	}

	// 2. Connect via SSH/SFTP
	client, err := mikrotik.Connect(ctx, rc, timeout, cfg.InsecureIgnoreHostKey, r.logger)
	if err != nil {
		r.registry.SetOffline(rc.Name, err)
		report.Err = fmt.Errorf("connection failed: %w", err)
		return report
	}
	defer client.Close()

	// 3. Collect/Refresh telemetry while connected
	if info, devErr := client.FetchDeviceInfo(ctx); devErr == nil && info != nil {
		r.registry.UpdateDeviceInfo(rc.Name, info.Model, info.Version, info.Uptime)
	}

	// 4. Create RSC export
	if err := client.CreateExport(ctx, destPaths.BaseFilename, rc.ExportSensitive); err != nil {
		report.Err = fmt.Errorf("export command failed: %w", err)
		return report
	}

	// 5. Create binary backup
	if err := client.CreateBackup(ctx, destPaths.BaseFilename, rc.EncryptBackup, rc.BackupPassword); err != nil {
		report.Err = fmt.Errorf("backup command failed: %w", err)
		return report
	}

	// 6. Wait for files on RouterOS storage
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

	// 7. Download .rsc file
	rscFile, err := client.OpenRemoteFile(remoteRscPath)
	if err != nil {
		report.Err = fmt.Errorf("failed to open remote rsc file: %w", err)
		return report
	}
	rscBytes, err := r.storage.SaveStream(rscFile, destPaths.LocalRscPath)
	_ = rscFile.Close()
	if err != nil {
		report.Err = fmt.Errorf("failed to download and save rsc file: %w", err)
		return report
	}
	report.RscSize = rscBytes

	// 8. Download .backup file
	backupFile, err := client.OpenRemoteFile(remoteBackupPath)
	if err != nil {
		report.Err = fmt.Errorf("failed to open remote backup file: %w", err)
		return report
	}
	backupBytes, err := r.storage.SaveStream(backupFile, destPaths.LocalBackupPath)
	_ = backupFile.Close()
	if err != nil {
		report.Err = fmt.Errorf("failed to download and save backup file: %w", err)
		return report
	}
	report.BackupSize = backupBytes

	// 9. Verify local files
	if _, err := r.storage.VerifyFile(destPaths.LocalRscPath); err != nil {
		report.Err = fmt.Errorf("rsc local file integrity check failed: %w", err)
		return report
	}
	if _, err := r.storage.VerifyFile(destPaths.LocalBackupPath); err != nil {
		report.Err = fmt.Errorf("backup local file integrity check failed: %w", err)
		return report
	}

	// 10. Clean up temporary files from RouterOS flash
	_ = client.RemoveFile(ctx, remoteRscPath)
	_ = client.RemoveFile(ctx, remoteBackupPath)

	report.Success = true
	return report
}
