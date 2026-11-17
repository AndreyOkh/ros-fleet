package runner

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"time"

	"github.com/AndreyOkh/ros-fleet/internal/config"
	"github.com/AndreyOkh/ros-fleet/internal/gitrepo"
	"github.com/AndreyOkh/ros-fleet/internal/mikrotik"
	"github.com/AndreyOkh/ros-fleet/internal/notifier"
	"github.com/AndreyOkh/ros-fleet/internal/state"
	"github.com/AndreyOkh/ros-fleet/internal/storage"
)

// Runner coordinates the backup pipeline, git sync, and state updates.
type Runner struct {
	cfgMgr   *config.Manager
	storage  *storage.Storage
	notifier *notifier.TelegramNotifier
	stateMgr *state.StateManager
	gitRepo  *gitrepo.GitRepository
	logger   *slog.Logger
	mu       sync.Mutex
}

// New creates a new Runner instance.
func New(cfgMgr *config.Manager, stateMgr *state.StateManager, logger *slog.Logger) *Runner {
	cfg := cfgMgr.Get()
	return &Runner{
		cfgMgr:   cfgMgr,
		storage:  storage.New(cfg.BinaryBackupDir, cfg.ExportDir),
		notifier: notifier.NewTelegramNotifier(cfg.Telegram, logger),
		stateMgr: stateMgr,
		gitRepo:  gitrepo.New(cfg.ExportDir, cfg.Git, logger),
		logger:   logger.With(slog.String("component", "runner")),
	}
}

// GitStatus returns a snapshot of current Git repository status.
func (r *Runner) GitStatus() gitrepo.GitStatus {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.gitRepo == nil {
		return gitrepo.GitStatus{}
	}
	return r.gitRepo.GetStatus()
}

// Run executes the backup process for all enabled routers.
func (r *Runner) Run(ctx context.Context) (notifier.SummaryReport, error) {
	cfg := r.cfgMgr.Get()

	r.mu.Lock()
	r.storage = storage.New(cfg.BinaryBackupDir, cfg.ExportDir)
	r.gitRepo = gitrepo.New(cfg.ExportDir, cfg.Git, r.logger)
	r.mu.Unlock()

	// Ensure Git repo is initialized
	if cfg.Git.Enabled {
		if err := r.gitRepo.EnsureRepo(ctx); err != nil {
			r.logger.Warn("Failed to ensure Git repository", slog.Any("error", err))
		}
	}

	// Sync state manager with latest configured devices
	r.stateMgr.InitFromConfig(cfg.Routers, cfg.Defaults)

	var activeRouters []config.EffectiveRouterConfig
	for _, rc := range cfg.Routers {
		if rc.IsEnabled() {
			activeRouters = append(activeRouters, rc.Effective(cfg.Defaults))
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
	var reportsMu sync.Mutex

	reports := make([]notifier.RouterReport, 0, len(activeRouters))

	for _, effRouter := range activeRouters {
		select {
		case <-ctx.Done():
			r.logger.Warn("Global context cancelled, aborting remaining router backups",
				slog.String("router", effRouter.Name),
			)
			reportsMu.Lock()
			reports = append(reports, notifier.RouterReport{
				Name:    effRouter.Name,
				Host:    effRouter.Host,
				Success: false,
				Err:     fmt.Errorf("backup aborted: %w", ctx.Err()),
			})
			reportsMu.Unlock()
			continue
		case sem <- struct{}{}:
		}

		wg.Add(1)
		go func(eff config.EffectiveRouterConfig) {
			defer func() {
				<-sem
				wg.Done()
			}()

			rep := r.backupRouter(ctx, eff, cfg)

			reportsMu.Lock()
			reports = append(reports, rep)
			reportsMu.Unlock()
		}(effRouter)
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

	// Unified Git Commit and Push after all routers have been processed
	if cfg.Git.Enabled {
		if _, gitErr := r.gitRepo.CommitAndPush(ctx); gitErr != nil {
			r.logger.Warn("Git commit/push cycle encountered error", slog.Any("error", gitErr))
		}
	}

	// Automated retention cleanup for binary backups
	deletedFiles, freedBytes, rotateErr := r.storage.Rotate(cfg.RetentionDays, r.logger)
	if rotateErr != nil {
		r.logger.Error("Binary backup retention rotation encountered errors", slog.Any("error", rotateErr))
	} else if deletedFiles > 0 {
		r.logger.Info("Binary backup retention cleanup completed",
			slog.Int("deleted_files", deletedFiles),
			slog.Int64("freed_bytes", freedBytes),
		)
	}

	// Telegram notification
	if notifyErr := r.notifier.SendReport(ctx, summary); notifyErr != nil {
		r.logger.Error("Failed to send Telegram notification", slog.Any("error", notifyErr))
	}

	return summary, nil
}

// RunSingle executes backup for a single router on-demand.
func (r *Runner) RunSingle(ctx context.Context, routerName string) (notifier.RouterReport, error) {
	cfg := r.cfgMgr.Get()
	rc, found := r.cfgMgr.GetRouter(routerName)
	if !found {
		return notifier.RouterReport{}, fmt.Errorf("router %q not found in configuration", routerName)
	}

	r.mu.Lock()
	r.storage = storage.New(cfg.BinaryBackupDir, cfg.ExportDir)
	r.gitRepo = gitrepo.New(cfg.ExportDir, cfg.Git, r.logger)
	r.mu.Unlock()

	eff := rc.Effective(cfg.Defaults)
	report := r.backupRouter(ctx, eff, cfg)

	if report.Success && cfg.Git.Enabled {
		if _, gitErr := r.gitRepo.CommitAndPush(ctx); gitErr != nil {
			r.logger.Warn("Git commit/push for single router encountered error", slog.Any("error", gitErr))
		}
	}

	return report, report.Err
}

// RefreshDevice connects to a router, queries hardware/firmware telemetry, and updates state.
func (r *Runner) RefreshDevice(ctx context.Context, routerName string) (*mikrotik.DeviceInfo, error) {
	cfg := r.cfgMgr.Get()
	rc, found := r.cfgMgr.GetRouter(routerName)
	if !found {
		return nil, fmt.Errorf("router %q not found in configuration", routerName)
	}

	eff := rc.Effective(cfg.Defaults)
	timeout := time.Duration(cfg.TimeoutSec) * time.Second
	opCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	client, err := mikrotik.Connect(opCtx, eff, timeout, r.logger)
	if err != nil {
		r.stateMgr.SetOffline(routerName, err)
		return nil, fmt.Errorf("connection failed: %w", err)
	}
	defer client.Close()

	info, err := client.FetchDeviceInfo(opCtx)
	if err != nil {
		r.stateMgr.SetOffline(routerName, err)
		return nil, fmt.Errorf("failed to fetch device info: %w", err)
	}

	r.stateMgr.UpdateDeviceInfo(routerName, info.Model, info.Version, info.Uptime)
	return info, nil
}

// backupRouter performs the atomic backup lifecycle for a single router.
func (r *Runner) backupRouter(parentCtx context.Context, eff config.EffectiveRouterConfig, cfg config.Config) notifier.RouterReport {
	start := time.Now()
	log := r.logger.With(slog.String("router", eff.Name), slog.String("host", eff.Host))

	log.Info("Starting backup for router")
	r.stateMgr.SetRunning(eff.Name)

	timeout := time.Duration(cfg.TimeoutSec) * time.Second
	ctx, cancel := context.WithTimeout(parentCtx, timeout)
	defer cancel()

	report := notifier.RouterReport{
		Name: eff.Name,
		Host: eff.Host,
	}

	defer func() {
		report.Duration = time.Since(start)
		r.stateMgr.UpdateBackupResult(eff.Name, report.Success, report.Err, report.RscSize, report.BackupSize)

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

	// 1. Prepare destination paths
	destPaths, err := r.storage.PrepareDestination(eff.Name, start)
	if err != nil {
		report.Err = fmt.Errorf("local storage preparation failed: %w", err)
		return report
	}

	// 2. Connect via SSH/SFTP
	client, err := mikrotik.Connect(ctx, eff, timeout, r.logger)
	if err != nil {
		r.stateMgr.SetOffline(eff.Name, err)
		report.Err = fmt.Errorf("connection failed: %w", err)
		return report
	}
	defer client.Close()

	// 3. Collect/Refresh telemetry while connected
	if info, devErr := client.FetchDeviceInfo(ctx); devErr == nil && info != nil {
		r.stateMgr.UpdateDeviceInfo(eff.Name, info.Model, info.Version, info.Uptime)
	}

	// 4. Create RSC export
	if err := client.CreateExport(ctx, destPaths.BaseFilename, eff.ExportSensitive); err != nil {
		report.Err = fmt.Errorf("export command failed: %w", err)
		return report
	}

	// 5. Create binary backup
	if err := client.CreateBackup(ctx, destPaths.BaseFilename, eff.EncryptBackup, eff.BackupPassword); err != nil {
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

	// 7. Download .rsc file, normalize RouterOS date/time comment, and save to exportDir
	rscFile, err := client.OpenRemoteFile(remoteRscPath)
	if err != nil {
		report.Err = fmt.Errorf("failed to open remote rsc file: %w", err)
		return report
	}
	rawRscBytes, err := io.ReadAll(rscFile)
	_ = rscFile.Close()
	if err != nil {
		report.Err = fmt.Errorf("failed to read remote rsc file: %w", err)
		return report
	}

	normalizedRsc := gitrepo.NormalizeRSC(rawRscBytes)
	rscSize, err := r.storage.SaveBytes(normalizedRsc, destPaths.LocalRscPath)
	if err != nil {
		report.Err = fmt.Errorf("failed to save normalized rsc export: %w", err)
		return report
	}
	report.RscSize = rscSize

	// 8. Download .backup binary file and save to binaryDir
	backupFile, err := client.OpenRemoteFile(remoteBackupPath)
	if err != nil {
		report.Err = fmt.Errorf("failed to open remote backup file: %w", err)
		return report
	}
	backupBytes, err := r.storage.SaveStream(backupFile, destPaths.LocalBackupPath)
	_ = backupFile.Close()
	if err != nil {
		report.Err = fmt.Errorf("failed to download and save binary backup file: %w", err)
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
