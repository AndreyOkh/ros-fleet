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
	"sort"
	"strings"
	"time"
)

var (
	illegalChars = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)
)

// DestinationPaths holds generated local file paths for a backup run.
type DestinationPaths struct {
	BaseFilename    string
	LocalRscPath    string
	LocalBackupPath string
}

// BackupFileInfo represents metadata for a stored backup file (.backup or .rsc).
type BackupFileInfo struct {
	Type      string    `json:"type"`       // "rsc" or "backup"
	FileName  string    `json:"file_name"`  // Base filename (e.g. "Router-1.rsc" or "Router-1_2026-11-18_10-00-00.backup")
	RelPath   string    `json:"rel_path"`   // Relative path from base directory for download
	Size      string    `json:"size"`       // Formatted size e.g. "1.4 MB"
	SizeBytes int64     `json:"size_bytes"` // Raw byte size
	ModTime   time.Time `json:"mod_time"`   // Creation/modification timestamp
}

// Storage manages local binary backups and export paths.
type Storage struct {
	binaryDir string
	exportDir string
}

// New creates a new Storage manager with separated binary and export directories.
func New(binaryDir, exportDir string) *Storage {
	return &Storage{
		binaryDir: filepath.Clean(binaryDir),
		exportDir: filepath.Clean(exportDir),
	}
}

// BinaryDir returns the configured binary backups root directory.
func (s *Storage) BinaryDir() string {
	return s.binaryDir
}

// ExportDir returns the configured RSC export directory.
func (s *Storage) ExportDir() string {
	return s.exportDir
}

// SanitizeName cleans a router name to make it safe for filesystem usage,
// preventing path traversal attacks and illegal filesystem characters.
func SanitizeName(name string) string {
	cleaned := illegalChars.ReplaceAllString(name, "_")
	cleaned = strings.Trim(cleaned, "._-")
	if cleaned == "" || cleaned == "." || cleaned == ".." {
		cleaned = "unnamed_router"
	}
	return cleaned
}

// PrepareDestination prepares paths for the timestamped binary backup and latest RSC export.
func (s *Storage) PrepareDestination(routerName string, t time.Time) (DestinationPaths, error) {
	safeRouterName := SanitizeName(routerName)
	timeStr := t.Format("2006-01-02_15-04-05")
	baseFilename := fmt.Sprintf("%s_%s", safeRouterName, timeStr)

	// Binary backup goes to binaryDir (with timestamp in router subfolder)
	routerBinaryDir := filepath.Join(s.binaryDir, safeRouterName)
	relBinary, err := filepath.Rel(s.binaryDir, routerBinaryDir)
	if err != nil || strings.HasPrefix(relBinary, "..") {
		return DestinationPaths{}, fmt.Errorf("path traversal attempt detected for binary backup directory: %q", routerName)
	}

	if err := os.MkdirAll(routerBinaryDir, 0o755); err != nil {
		return DestinationPaths{}, fmt.Errorf("failed to create router binary directory %q: %w", routerBinaryDir, err)
	}
	localBackupPath := filepath.Join(routerBinaryDir, baseFilename+".backup")

	// RSC export goes to exportDir (latest version with fixed name: <router_name>.rsc)
	localRscPath := filepath.Join(s.exportDir, safeRouterName+".rsc")
	relRsc, err := filepath.Rel(s.exportDir, localRscPath)
	if err != nil || strings.HasPrefix(relRsc, "..") {
		return DestinationPaths{}, fmt.Errorf("path traversal attempt detected for export path: %q", routerName)
	}

	if err := os.MkdirAll(s.exportDir, 0o755); err != nil {
		return DestinationPaths{}, fmt.Errorf("failed to create export directory %q: %w", s.exportDir, err)
	}

	return DestinationPaths{
		BaseFilename:    baseFilename,
		LocalRscPath:    localRscPath,
		LocalBackupPath: localBackupPath,
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
		if cpErr := copyFile(tmpPath, targetPath); cpErr != nil {
			_ = os.Remove(tmpPath)
			return 0, fmt.Errorf("failed to rename or copy temp file to target %q: %w (rename error: %v)", targetPath, cpErr, err)
		}
		_ = os.Remove(tmpPath)
	}

	return written, nil
}

// SaveBytes atomically writes a byte slice to targetPath via a temporary file.
func (s *Storage) SaveBytes(data []byte, targetPath string) (int64, error) {
	if len(data) == 0 {
		return 0, errors.New("data is empty (0 bytes)")
	}

	tmpPath := targetPath + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0o644); err != nil {
		return 0, fmt.Errorf("failed to write temp file %q: %w", tmpPath, err)
	}

	if err := os.Rename(tmpPath, targetPath); err != nil {
		_ = os.Remove(tmpPath)
		if writeErr := writeDirectFile(targetPath, data); writeErr != nil {
			return 0, fmt.Errorf("failed to write target %q: %w (rename error: %v)", targetPath, writeErr, err)
		}
		return int64(len(data)), nil
	}

	return int64(len(data)), nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_TRUNC|os.O_CREATE, 0o644)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Sync()
}

func writeDirectFile(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_TRUNC|os.O_CREATE, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()

	if _, err := f.Write(data); err != nil {
		return err
	}
	return f.Sync()
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

// Rotate removes binary backup (.backup) files older than retentionDays in binaryDir.
// It never touches exportDir or .git directories.
func (s *Storage) Rotate(retentionDays int, logger *slog.Logger) (int, int64, error) {
	if retentionDays <= 0 {
		if logger != nil {
			logger.Debug("Retention policy is disabled (retention_days <= 0)")
		}
		return 0, 0, nil
	}

	if _, err := os.Stat(s.binaryDir); os.IsNotExist(err) {
		return 0, 0, nil
	}

	cutoff := time.Now().AddDate(0, 0, -retentionDays)
	var deletedFiles int
	var freedBytes int64

	// Walk and remove old .backup files
	err := filepath.WalkDir(s.binaryDir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}

		// Skip hidden directories or .git
		if d.IsDir() {
			name := d.Name()
			if strings.HasPrefix(name, ".") || name == ".git" {
				return filepath.SkipDir
			}
			return nil
		}

		// Only check .backup files
		if filepath.Ext(path) != ".backup" {
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
					logger.Debug("Deleted expired binary backup file",
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

	// Clean up empty subdirectories in binaryDir
	s.cleanEmptyDirectories(s.binaryDir)

	if deletedFiles > 0 && logger != nil {
		logger.Info("Binary backup retention rotation completed",
			slog.Int("retention_days", retentionDays),
			slog.Int("deleted_files", deletedFiles),
			slog.String("freed_space", formatBytes(freedBytes)),
		)
	}

	return deletedFiles, freedBytes, nil
}

// cleanEmptyDirectories traverses bottom-up and removes empty directories (except binaryDir).
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

	if dir != s.binaryDir {
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

// ListRouterBackups scans exportDir and binaryDir for all backup files belonging to the router.
func (s *Storage) ListRouterBackups(routerName string) ([]BackupFileInfo, error) {
	safeName := SanitizeName(routerName)
	var results []BackupFileInfo

	// 1. Check for latest .rsc export in exportDir
	rscPath := filepath.Join(s.exportDir, safeName+".rsc")
	if info, err := os.Stat(rscPath); err == nil && !info.IsDir() {
		results = append(results, BackupFileInfo{
			Type:      "rsc",
			FileName:  info.Name(),
			RelPath:   info.Name(),
			Size:      formatBytes(info.Size()),
			SizeBytes: info.Size(),
			ModTime:   info.ModTime(),
		})
	}

	// 2. Check for binary backups in binaryDir
	var binaries []BackupFileInfo

	// Check router subfolder: binaryDir/<safeName>/
	routerSubDir := filepath.Join(s.binaryDir, safeName)
	if dirInfo, err := os.Stat(routerSubDir); err == nil && dirInfo.IsDir() {
		_ = filepath.WalkDir(routerSubDir, func(path string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil || d.IsDir() {
				return nil
			}
			if filepath.Ext(d.Name()) == ".backup" && strings.HasPrefix(d.Name(), safeName) {
				if fi, err := d.Info(); err == nil && fi.Size() > 0 {
					rel, relErr := filepath.Rel(s.binaryDir, path)
					if relErr == nil {
						binaries = append(binaries, BackupFileInfo{
							Type:      "backup",
							FileName:  d.Name(),
							RelPath:   rel,
							Size:      formatBytes(fi.Size()),
							SizeBytes: fi.Size(),
							ModTime:   fi.ModTime(),
						})
					}
				}
			}
			return nil
		})
	}

	// Also check directly inside binaryDir for flat legacy files: binaryDir/<safeName>_*.backup
	if entries, err := os.ReadDir(s.binaryDir); err == nil {
		for _, entry := range entries {
			if !entry.IsDir() && filepath.Ext(entry.Name()) == ".backup" && strings.HasPrefix(entry.Name(), safeName) {
				alreadyAdded := false
				for _, b := range binaries {
					if b.FileName == entry.Name() {
						alreadyAdded = true
						break
					}
				}
				if !alreadyAdded {
					if fi, err := entry.Info(); err == nil && fi.Size() > 0 {
						binaries = append(binaries, BackupFileInfo{
							Type:      "backup",
							FileName:  entry.Name(),
							RelPath:   entry.Name(),
							Size:      formatBytes(fi.Size()),
							SizeBytes: fi.Size(),
							ModTime:   fi.ModTime(),
						})
					}
				}
			}
		}
	}

	// Sort binary backups from newest to oldest
	sort.Slice(binaries, func(i, j int) bool {
		return binaries[i].ModTime.After(binaries[j].ModTime)
	})

	results = append(results, binaries...)
	return results, nil
}

// ResolveBackupFile safely resolves and validates the path to a requested backup file.
// It strictly prevents path traversal and verifies that the file belongs to the given router.
func (s *Storage) ResolveBackupFile(kind string, routerName string, relPath string) (string, string, error) {
	safeName := SanitizeName(routerName)
	kind = strings.ToLower(strings.TrimSpace(kind))
	if kind != "binary" && kind != "export" {
		return "", "", fmt.Errorf("invalid backup kind %q: must be 'binary' or 'export'", kind)
	}

	cleanRel := filepath.Clean(strings.TrimSpace(relPath))
	if cleanRel == "" || cleanRel == "." || strings.HasPrefix(cleanRel, "..") || filepath.IsAbs(cleanRel) {
		return "", "", errors.New("invalid backup path: path traversal detected")
	}

	var baseDir string
	var expectedExt string
	if kind == "export" {
		baseDir = s.exportDir
		expectedExt = ".rsc"
	} else {
		baseDir = s.binaryDir
		expectedExt = ".backup"
	}

	targetPath := filepath.Join(baseDir, cleanRel)
	targetPath = filepath.Clean(targetPath)

	// Strict boundary check: targetPath must be within baseDir
	rel, err := filepath.Rel(baseDir, targetPath)
	if err != nil || strings.HasPrefix(rel, "..") || rel == "." {
		return "", "", errors.New("security error: path traversal outside backup root")
	}

	baseFileName := filepath.Base(targetPath)
	if filepath.Ext(baseFileName) != expectedExt {
		return "", "", fmt.Errorf("security error: file extension must be %s", expectedExt)
	}

	// Verify file belongs to the specified router
	if kind == "export" {
		if baseFileName != safeName+".rsc" {
			return "", "", fmt.Errorf("security error: export file %q does not match router %q", baseFileName, routerName)
		}
	} else {
		if !strings.HasPrefix(baseFileName, safeName) {
			return "", "", fmt.Errorf("security error: binary backup %q does not match router %q", baseFileName, routerName)
		}
	}

	info, err := os.Stat(targetPath)
	if err != nil {
		return "", "", fmt.Errorf("backup file not found: %w", err)
	}
	if info.IsDir() {
		return "", "", errors.New("security error: target path is a directory")
	}

	return targetPath, baseFileName, nil
}

// LatestRouterBackup finds the most recent backup of the specified kind for a router.
func (s *Storage) LatestRouterBackup(routerName string, kind string) (BackupFileInfo, error) {
	backups, err := s.ListRouterBackups(routerName)
	if err != nil {
		return BackupFileInfo{}, err
	}

	targetType := "backup"
	if strings.ToLower(strings.TrimSpace(kind)) == "export" {
		targetType = "rsc"
	}

	for _, b := range backups {
		if b.Type == targetType {
			return b, nil
		}
	}

	return BackupFileInfo{}, fmt.Errorf("no %s backup found for router %q", kind, routerName)
}
