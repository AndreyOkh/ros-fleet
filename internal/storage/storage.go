package storage

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var (
	illegalChars = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)
)

// DestinationPaths holds generated local file paths for a backup run.
type DestinationPaths struct {
	DateDir         string
	BaseFilename    string
	LocalRscPath    string
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

// Rotate removes .backup and .rsc files older than retentionDays and cleans up empty folders.
// It never touches .git or hidden directories.
func (s *Storage) Rotate(retentionDays int, logger *slog.Logger) (int, int64, error) {
	if retentionDays <= 0 {
		if logger != nil {
			logger.Debug("Retention policy is disabled (retention_days <= 0)")
		}
		return 0, 0, nil
	}

	if _, err := os.Stat(s.baseDir); os.IsNotExist(err) {
		return 0, 0, nil
	}

	cutoff := time.Now().AddDate(0, 0, -retentionDays)
	var deletedFiles int
	var freedBytes int64

	// 1. Walk and remove old backup files (.backup, .rsc)
	err := filepath.WalkDir(s.baseDir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}

		// Skip .git or hidden directories
		if d.IsDir() {
			name := d.Name()
			if strings.HasPrefix(name, ".") || name == ".git" {
				return filepath.SkipDir
			}
			return nil
		}

		// Only check backup files
		ext := filepath.Ext(path)
		if ext != ".backup" && ext != ".rsc" {
			return nil
		}

		info, err := d.Info()
		if err != nil {
			return nil
		}

		if info.ModTime().Before(cutoff) {
			size := info.Size()
			if removeErr := os.Remove(path); removeErr == nil {
				deletedFiles++
				freedBytes += size
				if logger != nil {
					logger.Debug("Deleted expired backup file",
						slog.String("path", path),
						slog.Int64("size", size),
						slog.Time("mod_time", info.ModTime()),
					)
				}
			}
		}

		return nil
	})

	if err != nil && logger != nil {
		logger.Warn("Error during retention file walk", slog.Any("error", err))
	}

	// 2. Clean up empty subdirectories
	s.cleanEmptyDirectories(s.baseDir)

	if deletedFiles > 0 && logger != nil {
		logger.Info("Backup retention rotation completed",
			slog.Int("retention_days", retentionDays),
			slog.Int("deleted_files", deletedFiles),
			slog.String("freed_space", formatBytes(freedBytes)),
		)
	}

	return deletedFiles, freedBytes, nil
}

// cleanEmptyDirectories traverses bottom-up and removes empty directories (except baseDir and .git).
func (s *Storage) cleanEmptyDirectories(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}

	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		subDir := filepath.Join(dir, entry.Name())
		s.cleanEmptyDirectories(subDir)
	}

	if dir != s.baseDir {
		rem, err := os.ReadDir(dir)
		if err == nil && len(rem) == 0 {
			_ = os.Remove(dir)
		}
	}
}

func formatBytes(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(bytes)/float64(div), "KMGTPE"[exp])
}
