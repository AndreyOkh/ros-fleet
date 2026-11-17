package storage

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var (
	// illegalChars matches characters that are unsafe for filenames/directories across Linux/Windows.
	illegalChars = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)
	// dateDirPattern matches YYYY-MM-DD directories.
	dateDirPattern = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
)

// DestinationPaths holds generated local file paths for a backup run.
type DestinationPaths struct {
	DateDir        string
	BaseFilename   string
	LocalRscPath   string
	LocalBackupPath string
}

// Storage manages local backup files, writing, verification, and retention.
type Storage struct {
	baseDir string
}

// New creates a new Storage manager for the specified backup directory.
func New(baseDir string) *Storage {
	return &Storage{
		baseDir: filepath.Clean(baseDir),
	}
}

// SanitizeName cleans a router name to make it safe for filesystem usage.
func SanitizeName(name string) string {
	cleaned := illegalChars.ReplaceAllString(name, "_")
	cleaned = strings.Trim(cleaned, "._-")
	if cleaned == "" {
		cleaned = "unnamed_router"
	}
	return cleaned
}

// PrepareDestination prepares the destination folder and target file paths for a router backup.
func (s *Storage) PrepareDestination(routerName string, t time.Time) (DestinationPaths, error) {
	safeRouterName := SanitizeName(routerName)
	dateStr := t.Format("2006-01-02")
	timeStr := t.Format("2006-01-02_15-04-05")

	dateDir := filepath.Join(s.baseDir, safeRouterName, dateStr)
	if err := os.MkdirAll(dateDir, 0o755); err != nil {
		return DestinationPaths{}, fmt.Errorf("failed to create directory %q: %w", dateDir, err)
	}

	baseFilename := fmt.Sprintf("%s_%s", safeRouterName, timeStr)
	return DestinationPaths{
		DateDir:         dateDir,
		BaseFilename:    baseFilename,
		LocalRscPath:    filepath.Join(dateDir, baseFilename+".rsc"),
		LocalBackupPath: filepath.Join(dateDir, baseFilename+".backup"),
	}, nil
}

// SaveStream atomically writes data from reader to targetPath via a temporary file.
func (s *Storage) SaveStream(reader io.Reader, targetPath string) (int64, error) {
	tmpPath := targetPath + ".tmp"

	file, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return 0, fmt.Errorf("failed to create temp file %q: %w", tmpPath, err)
	}

	var writeErr error
	var written int64

	defer func() {
		_ = file.Close()
		if writeErr != nil {
			_ = os.Remove(tmpPath)
		}
	}()

	written, writeErr = io.Copy(file, reader)
	if writeErr != nil {
		return 0, fmt.Errorf("failed during write to temp file: %w", writeErr)
	}

	if writeErr = file.Sync(); writeErr != nil {
		return 0, fmt.Errorf("failed to sync temp file to disk: %w", writeErr)
	}

	if written == 0 {
		writeErr = errors.New("downloaded file is empty (0 bytes)")
		return 0, writeErr
	}

	if err := file.Close(); err != nil {
		writeErr = fmt.Errorf("failed to close temp file: %w", err)
		return 0, writeErr
	}

	if err := os.Rename(tmpPath, targetPath); err != nil {
		_ = os.Remove(tmpPath)
		return 0, fmt.Errorf("failed to rename temp file to target %q: %w", targetPath, err)
	}

	return written, nil
}

// VerifyFile checks that a file exists and is not empty.
func (s *Storage) VerifyFile(path string) (int64, error) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, fmt.Errorf("file verification failed for %q: %w", path, err)
	}
	if info.Size() == 0 {
		return 0, fmt.Errorf("file %q is empty (0 bytes)", path)
	}
	return info.Size(), nil
}

// Rotate removes backup directories and files older than retentionDays.
// Returns the count of removed date directories.
func (s *Storage) Rotate(retentionDays int, logger *slog.Logger) (int, error) {
	if retentionDays <= 0 {
		if logger != nil {
			logger.Debug("Retention policy is disabled (retention_days <= 0)")
		}
		return 0, nil
	}

	if _, err := os.Stat(s.baseDir); os.IsNotExist(err) {
		return 0, nil
	}

	// Calculate cutoff start of day:
	now := time.Now()
	cutoffDate := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).AddDate(0, 0, -retentionDays)

	if logger != nil {
		logger.Info("Starting backup retention cleanup",
			slog.Int("retention_days", retentionDays),
			slog.String("cutoff_date", cutoffDate.Format("2006-01-02")),
		)
	}

	routerEntries, err := os.ReadDir(s.baseDir)
	if err != nil {
		return 0, fmt.Errorf("failed to read backup base directory %q: %w", s.baseDir, err)
	}

	removedCount := 0

	for _, routerEntry := range routerEntries {
		if !routerEntry.IsDir() {
			continue
		}

		routerPath := filepath.Join(s.baseDir, routerEntry.Name())
		dateEntries, err := os.ReadDir(routerPath)
		if err != nil {
			if logger != nil {
				logger.Warn("Failed to read router directory for rotation",
					slog.String("path", routerPath),
					slog.Any("error", err),
				)
			}
			continue
		}

		remainingSubdirs := 0
		for _, dateEntry := range dateEntries {
			subPath := filepath.Join(routerPath, dateEntry.Name())

			if dateEntry.IsDir() && dateDirPattern.MatchString(dateEntry.Name()) {
				entryDate, parseErr := time.Parse("2006-01-02", dateEntry.Name())
				if parseErr == nil && entryDate.Before(cutoffDate) {
					if logger != nil {
						logger.Info("Removing expired backup directory",
							slog.String("router", routerEntry.Name()),
							slog.String("date", dateEntry.Name()),
							slog.String("path", subPath),
						)
					}
					if removeErr := os.RemoveAll(subPath); removeErr != nil {
						if logger != nil {
							logger.Error("Failed to delete expired backup directory",
								slog.String("path", subPath),
								slog.Any("error", removeErr),
							)
						}
						remainingSubdirs++
					} else {
						removedCount++
					}
					continue
				}
			}

			// Also check individual files if any were left outside date directories
			info, statErr := dateEntry.Info()
			if statErr == nil && !dateEntry.IsDir() {
				if info.ModTime().Before(cutoffDate) {
					_ = os.Remove(subPath)
					continue
				}
			}

			remainingSubdirs++
		}

		// Clean up empty router directory if no backups remain
		if remainingSubdirs == 0 {
			if err := os.Remove(routerPath); err == nil && logger != nil {
				logger.Debug("Removed empty router directory", slog.String("path", routerPath))
			}
		}
	}

	return removedCount, nil
}
