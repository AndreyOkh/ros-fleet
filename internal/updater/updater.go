package updater

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

var (
	updaterOS           = runtime.GOOS
	updaterArch         = runtime.GOARCH
	updaterAPIBaseURL   = "https://api.github.com"
	updaterExecutableFn = os.Executable
	updaterHTTPClient   = &http.Client{Timeout: 60 * time.Second}
	osRename            = os.Rename
)

type selfUpdateOptions struct {
	os           string
	arch         string
	apiBaseURL   string
	executableFn func() (string, error)
	httpClient   *http.Client
}

// RunSelfUpdate executes the safe in-place self-update of the ros-fleet Linux binary:
// 1. Verifies the OS is Linux.
// 2. Checks if running inside Docker (logs compose guidance and exits cleanly).
// 3. Fetches latest release and checks if an update is available.
// 4. Matches release assets (tar.gz and sha256sums.txt).
// 5. Downloads and verifies SHA-256 checksum.
// 6. Stages replacement binary in filepath.Dir(execPath) preventing EXDEV cross-device errors.
// 7. Copies executable file mode/permissions.
// 8. Performs atomic swap via rename with .old rollback backup.
// 9. Prints restart instructions.
func RunSelfUpdate(ctx context.Context, repo, currentVersion string, logger *slog.Logger) error {
	apiURL := updaterAPIBaseURL
	if env := os.Getenv("GITHUB_API_URL"); env != "" {
		apiURL = strings.TrimRight(env, "/")
	}
	return runSelfUpdateWithOptions(ctx, repo, currentVersion, logger, selfUpdateOptions{
		os:           updaterOS,
		arch:         updaterArch,
		apiBaseURL:   apiURL,
		executableFn: updaterExecutableFn,
		httpClient:   updaterHTTPClient,
	})
}

func runSelfUpdateWithOptions(ctx context.Context, repo, currentVersion string, logger *slog.Logger, opts selfUpdateOptions) error {
	if logger == nil {
		logger = slog.Default()
	}

	// 1. Verify OS is Linux
	if opts.os != "linux" {
		return fmt.Errorf("self-update is only supported on Linux (current OS: %s)", opts.os)
	}

	// 2. Check if running inside Docker
	if IsRunningInDocker() {
		msg := "ros-fleet is running inside a Docker container.\n" +
			"Self-update of binary is disabled. Please pull the latest container image:\n\n" +
			"    docker compose pull ros-fleet\n" +
			"    docker compose up -d ros-fleet\n"
		logger.Info("Self-update skipped: running inside Docker container")
		fmt.Print(msg)
		return nil
	}

	if repo == "" {
		repo = "AndreyOkh/ros-fleet"
	}

	// 3. Query latest release from GitHub
	release, err := fetchLatestRelease(ctx, opts.httpClient, opts.apiBaseURL, repo)
	if err != nil {
		return fmt.Errorf("failed to fetch latest release: %w", err)
	}

	// Check if update is available
	if !IsUpdateAvailable(currentVersion, release.TagName) {
		logger.Info("ros-fleet is already up to date", "current", currentVersion, "latest", release.TagName)
		fmt.Printf("ros-fleet is already up to date (version %s)\n", currentVersion)
		return nil
	}

	logger.Info("Update available, starting download", "current", currentVersion, "target", release.TagName)

	// 4. Match assets strictly
	// Pattern: ros-fleet_${VERSION}_linux_${ARCH}.tar.gz and sha256sums.txt
	expectedArchiveName := fmt.Sprintf("ros-fleet_%s_linux_%s.tar.gz", release.TagName, opts.arch)
	fallbackArchiveName := ""
	if !strings.HasPrefix(release.TagName, "v") && !strings.HasPrefix(release.TagName, "V") {
		fallbackArchiveName = fmt.Sprintf("ros-fleet_v%s_linux_%s.tar.gz", release.TagName, opts.arch)
	}

	var archiveAsset *ReleaseAsset
	var checksumAsset *ReleaseAsset

	for i := range release.Assets {
		asset := &release.Assets[i]
		if asset.Name == expectedArchiveName || (fallbackArchiveName != "" && asset.Name == fallbackArchiveName) {
			archiveAsset = asset
		} else if asset.Name == "sha256sums.txt" {
			checksumAsset = asset
		}
	}

	if archiveAsset == nil {
		return fmt.Errorf("release %s does not contain matching binary archive %q", release.TagName, expectedArchiveName)
	}
	if checksumAsset == nil {
		return fmt.Errorf("release %s does not contain checksums file %q", release.TagName, "sha256sums.txt")
	}

	// 5. Download sha256sums.txt
	logger.Info("Downloading checksums file", "url", checksumAsset.BrowserDownloadURL)
	checksumData, err := downloadAsset(ctx, opts.httpClient, checksumAsset.BrowserDownloadURL)
	if err != nil {
		return fmt.Errorf("failed to download checksums file: %w", err)
	}

	checksumMap := parseChecksums(checksumData)
	expectedHash, found := checksumMap[archiveAsset.Name]
	if !found {
		expectedHash, found = checksumMap[filepath.Base(archiveAsset.Name)]
	}
	if !found {
		return fmt.Errorf("checksum for %q not found in sha256sums.txt", archiveAsset.Name)
	}

	// Download binary archive
	logger.Info("Downloading update archive", "asset", archiveAsset.Name, "url", archiveAsset.BrowserDownloadURL)
	archiveData, err := downloadAsset(ctx, opts.httpClient, archiveAsset.BrowserDownloadURL)
	if err != nil {
		return fmt.Errorf("failed to download update archive: %w", err)
	}

	// Verify SHA-256
	h := sha256.Sum256(archiveData)
	actualHash := hex.EncodeToString(h[:])
	if !strings.EqualFold(actualHash, expectedHash) {
		return fmt.Errorf("checksum verification failed for %s: expected %s, got %s", archiveAsset.Name, expectedHash, actualHash)
	}
	logger.Info("Checksum verified successfully", "hash", actualHash)

	// 6. Resolve executable path & permissions
	rawExecPath, err := opts.executableFn()
	if err != nil {
		return fmt.Errorf("failed to get executable path: %w", err)
	}
	execPath, err := filepath.EvalSymlinks(rawExecPath)
	if err != nil {
		return fmt.Errorf("failed to resolve executable symlinks: %w", err)
	}

	execStat, err := os.Stat(execPath)
	if err != nil {
		return fmt.Errorf("failed to stat current executable: %w", err)
	}
	execMode := execStat.Mode()

	// 7. Staging: unpack into filepath.Dir(execPath) to avoid EXDEV cross-device errors
	execDir := filepath.Dir(execPath)
	tmpFile, err := os.CreateTemp(execDir, ".ros-fleet-update-*")
	if err != nil {
		return fmt.Errorf("failed to create staging file in %s: %w", execDir, err)
	}
	tmpPath := tmpFile.Name()

	// Ensure cleanup of tmpPath if swap is not completed
	swapSuccess := false
	defer func() {
		if !swapSuccess {
			_ = os.Remove(tmpPath)
		}
	}()

	// Extract binary from tar.gz into tmpFile
	if err := extractBinaryFromTarGz(archiveData, "ros-fleet", tmpFile); err != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("failed to extract binary from archive: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("failed to flush staging file: %w", err)
	}

	// Copy file mode / permissions
	if err := os.Chmod(tmpPath, execMode); err != nil {
		return fmt.Errorf("failed to set permissions on staged binary: %w", err)
	}

	// 8. Atomic swap: rename execPath -> execPath.old, rename tmpPath -> execPath
	oldPath := execPath + ".old"
	_ = os.Remove(oldPath) // remove any previous stale backup

	if err := osRename(execPath, oldPath); err != nil {
		return fmt.Errorf("failed to backup current executable: %w", err)
	}

	if err := osRename(tmpPath, execPath); err != nil {
		// Rollback: restore oldPath back to execPath
		rollbackErr := osRename(oldPath, execPath)
		return fmt.Errorf("failed to replace executable with new binary (rollback: %v): %w", rollbackErr, err)
	}

	// Swap succeeded, remove .old backup
	swapSuccess = true
	_ = os.Remove(oldPath)

	// 9. Display restart instructions
	msg := fmt.Sprintf(
		"Successfully updated ros-fleet to version %s!\n"+
			"To apply changes, please restart the service:\n\n"+
			"    sudo systemctl restart ros-fleet\n",
		release.TagName,
	)
	logger.Info("Self-update completed successfully", "version", release.TagName)
	fmt.Print(msg)

	return nil
}

func fetchLatestRelease(ctx context.Context, client *http.Client, baseURL, repo string) (*Release, error) {
	apiURL := fmt.Sprintf("%s/repos/%s/releases/latest", strings.TrimRight(baseURL, "/"), repo)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "ros-fleet")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("github api returned status %d: %s", resp.StatusCode, resp.Status)
	}

	bodyBytes, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	if err != nil {
		return nil, err
	}

	var release Release
	if err := json.Unmarshal(bodyBytes, &release); err != nil {
		return nil, err
	}

	return &release, nil
}

func downloadAsset(ctx context.Context, client *http.Client, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "ros-fleet")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download returned HTTP %d: %s", resp.StatusCode, resp.Status)
	}

	// 100MB limit for binary archive download
	return io.ReadAll(io.LimitReader(resp.Body, 100<<20))
}

func parseChecksums(data []byte) map[string]string {
	result := make(map[string]string)
	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) >= 2 {
			hash := strings.TrimSpace(fields[0])
			filename := strings.TrimPrefix(strings.TrimSpace(fields[1]), "*")
			result[filename] = hash
			result[filepath.Base(filename)] = hash
		}
	}
	return result
}

func extractBinaryFromTarGz(archiveData []byte, binaryName string, dst io.Writer) error {
	gz, err := gzip.NewReader(bytes.NewReader(archiveData))
	if err != nil {
		return fmt.Errorf("gzip reader failed: %w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("tar reading failed: %w", err)
		}

		// Look for target binary file
		baseName := filepath.Base(hdr.Name)
		if (baseName == binaryName || baseName == binaryName+".exe") &&
			(hdr.Typeflag == tar.TypeReg || hdr.Typeflag == tar.TypeRegA || hdr.Typeflag == 0) {
			_, err = io.Copy(dst, tr)
			return err
		}
	}

	return fmt.Errorf("binary %q not found in archive", binaryName)
}
