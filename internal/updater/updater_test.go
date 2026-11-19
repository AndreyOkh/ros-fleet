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
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func createMockTarGz(binaryName string, content []byte) ([]byte, error) {
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)

	hdr := &tar.Header{
		Name:     binaryName,
		Mode:     0755,
		Size:     int64(len(content)),
		Typeflag: tar.TypeReg,
	}
	if err := tw.WriteHeader(hdr); err != nil {
		return nil, err
	}
	if _, err := tw.Write(content); err != nil {
		return nil, err
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	if err := gw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func TestRunSelfUpdateNonLinux(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	opts := selfUpdateOptions{
		os: "darwin",
	}

	err := runSelfUpdateWithOptions(context.Background(), "repo", "v1.0.0", logger, opts)
	if err == nil {
		t.Fatalf("expected error on non-Linux OS, got nil")
	}
	if !strings.Contains(err.Error(), "only supported on Linux") {
		t.Fatalf("unexpected error message: %v", err)
	}
}

func TestRunSelfUpdateDocker(t *testing.T) {
	origLookup := envLookupFunc
	origEnvPath := dockerEnvPath
	t.Cleanup(func() {
		envLookupFunc = origLookup
		dockerEnvPath = origEnvPath
	})

	dockerEnvPath = filepath.Join(t.TempDir(), "none")
	envLookupFunc = func(key string) string {
		if key == "DOCKER_CONTAINER" {
			return "1"
		}
		return ""
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	opts := selfUpdateOptions{
		os: "linux",
	}

	err := runSelfUpdateWithOptions(context.Background(), "repo", "v1.0.0", logger, opts)
	if err != nil {
		t.Fatalf("expected nil error when running in Docker, got: %v", err)
	}
}

func TestRunSelfUpdateAlreadyUpToDate(t *testing.T) {
	origLookup := envLookupFunc
	origEnvPath := dockerEnvPath
	t.Cleanup(func() {
		envLookupFunc = origLookup
		dockerEnvPath = origEnvPath
	})
	dockerEnvPath = filepath.Join(t.TempDir(), "none")
	envLookupFunc = func(key string) string { return "" }

	mockRelease := Release{
		TagName: "v1.5.0",
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(mockRelease)
	}))
	defer server.Close()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	opts := selfUpdateOptions{
		os:         "linux",
		arch:       "amd64",
		apiBaseURL: server.URL,
		httpClient: server.Client(),
	}

	// 1. Same version
	err := runSelfUpdateWithOptions(context.Background(), "test/repo", "v1.5.0", logger, opts)
	if err != nil {
		t.Fatalf("expected nil on up-to-date check, got: %v", err)
	}

	// 2. Dev version -> already up to date / no-op
	err = runSelfUpdateWithOptions(context.Background(), "test/repo", "dev", logger, opts)
	if err != nil {
		t.Fatalf("expected nil on dev version check, got: %v", err)
	}
}

func TestRunSelfUpdateMissingAssets(t *testing.T) {
	origLookup := envLookupFunc
	origEnvPath := dockerEnvPath
	t.Cleanup(func() {
		envLookupFunc = origLookup
		dockerEnvPath = origEnvPath
	})
	dockerEnvPath = filepath.Join(t.TempDir(), "none")
	envLookupFunc = func(key string) string { return "" }

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	// Subtest 1: missing binary archive
	t.Run("missing archive asset", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode(Release{
				TagName: "v2.0.0",
				Assets: []ReleaseAsset{
					{Name: "sha256sums.txt", BrowserDownloadURL: "http://example.com/sha256sums.txt"},
				},
			})
		}))
		defer server.Close()

		opts := selfUpdateOptions{
			os:         "linux",
			arch:       "amd64",
			apiBaseURL: server.URL,
			httpClient: server.Client(),
		}

		err := runSelfUpdateWithOptions(context.Background(), "test/repo", "v1.0.0", logger, opts)
		if err == nil || !strings.Contains(err.Error(), "does not contain matching binary archive") {
			t.Fatalf("expected missing archive error, got %v", err)
		}
	})

	// Subtest 2: missing checksums file
	t.Run("missing checksums asset", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode(Release{
				TagName: "v2.0.0",
				Assets: []ReleaseAsset{
					{Name: "ros-fleet_v2.0.0_linux_amd64.tar.gz", BrowserDownloadURL: "http://example.com/ros-fleet.tar.gz"},
				},
			})
		}))
		defer server.Close()

		opts := selfUpdateOptions{
			os:         "linux",
			arch:       "amd64",
			apiBaseURL: server.URL,
			httpClient: server.Client(),
		}

		err := runSelfUpdateWithOptions(context.Background(), "test/repo", "v1.0.0", logger, opts)
		if err == nil || !strings.Contains(err.Error(), "does not contain checksums file") {
			t.Fatalf("expected missing checksums error, got %v", err)
		}
	})
}

func TestRunSelfUpdateChecksumMismatch(t *testing.T) {
	origLookup := envLookupFunc
	origEnvPath := dockerEnvPath
	t.Cleanup(func() {
		envLookupFunc = origLookup
		dockerEnvPath = origEnvPath
	})
	dockerEnvPath = filepath.Join(t.TempDir(), "none")
	envLookupFunc = func(key string) string { return "" }

	archiveData := []byte("fake-archive-bytes")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/test/repo/releases/latest":
			_ = json.NewEncoder(w).Encode(Release{
				TagName: "v2.0.0",
				Assets: []ReleaseAsset{
					{Name: "ros-fleet_v2.0.0_linux_amd64.tar.gz", BrowserDownloadURL: "http://" + r.Host + "/archive.tar.gz"},
					{Name: "sha256sums.txt", BrowserDownloadURL: "http://" + r.Host + "/sha256sums.txt"},
				},
			})
		case "/archive.tar.gz":
			_, _ = w.Write(archiveData)
		case "/sha256sums.txt":
			_, _ = w.Write([]byte("deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef  ros-fleet_v2.0.0_linux_amd64.tar.gz\n"))
		}
	}))
	defer server.Close()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	opts := selfUpdateOptions{
		os:         "linux",
		arch:       "amd64",
		apiBaseURL: server.URL,
		httpClient: server.Client(),
	}

	err := runSelfUpdateWithOptions(context.Background(), "test/repo", "v1.0.0", logger, opts)
	if err == nil || !strings.Contains(err.Error(), "checksum verification failed") {
		t.Fatalf("expected checksum verification failed error, got: %v", err)
	}
}

func TestRunSelfUpdateSuccessAndRollback(t *testing.T) {
	origLookup := envLookupFunc
	origEnvPath := dockerEnvPath
	t.Cleanup(func() {
		envLookupFunc = origLookup
		dockerEnvPath = origEnvPath
	})
	dockerEnvPath = filepath.Join(t.TempDir(), "none")
	envLookupFunc = func(key string) string { return "" }

	// Prepare mock target directory and binary
	tmpDir := t.TempDir()
	execPath := filepath.Join(tmpDir, "ros-fleet")
	oldContent := []byte("#!/bin/sh\necho v1.0.0\n")
	if err := os.WriteFile(execPath, oldContent, 0755); err != nil {
		t.Fatalf("failed to write original binary: %v", err)
	}

	// Prepare new binary inside tar.gz
	newContent := []byte("#!/bin/sh\necho v2.0.0\n")
	archiveBytes, err := createMockTarGz("ros-fleet", newContent)
	if err != nil {
		t.Fatalf("failed to create mock tar.gz: %v", err)
	}

	h := sha256.Sum256(archiveBytes)
	hashHex := hex.EncodeToString(h[:])
	archiveName := "ros-fleet_v2.0.0_linux_amd64.tar.gz"
	checksumContent := fmt.Sprintf("%s  %s\n", hashHex, archiveName)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/test/repo/releases/latest":
			_ = json.NewEncoder(w).Encode(Release{
				TagName: "v2.0.0",
				Assets: []ReleaseAsset{
					{Name: archiveName, BrowserDownloadURL: "http://" + r.Host + "/download/archive"},
					{Name: "sha256sums.txt", BrowserDownloadURL: "http://" + r.Host + "/download/sums"},
				},
			})
		case "/download/archive":
			_, _ = w.Write(archiveBytes)
		case "/download/sums":
			_, _ = w.Write([]byte(checksumContent))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	opts := selfUpdateOptions{
		os:         "linux",
		arch:       "amd64",
		apiBaseURL: server.URL,
		httpClient: server.Client(),
		executableFn: func() (string, error) {
			return execPath, nil
		},
	}

	// Run update
	err = runSelfUpdateWithOptions(context.Background(), "test/repo", "v1.0.0", logger, opts)
	if err != nil {
		t.Fatalf("RunSelfUpdate failed: %v", err)
	}

	// Verify replacement
	updatedBytes, err := os.ReadFile(execPath)
	if err != nil {
		t.Fatalf("failed to read updated executable: %v", err)
	}
	if !bytes.Equal(updatedBytes, newContent) {
		t.Fatalf("updated binary content mismatch, got %q, expected %q", string(updatedBytes), string(newContent))
	}

	// Verify file mode
	stat, err := os.Stat(execPath)
	if err != nil {
		t.Fatalf("failed to stat updated executable: %v", err)
	}
	if stat.Mode().Perm() != 0755 {
		t.Fatalf("expected mode 0755, got %v", stat.Mode().Perm())
	}

	// Verify .old file was cleaned up
	oldPath := execPath + ".old"
	if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
		t.Fatalf("expected .old backup file to be removed after successful swap")
	}

	// Verify no stray staging files exist
	files, err := os.ReadDir(tmpDir)
	if err != nil {
		t.Fatalf("failed to readdir: %v", err)
	}
	if len(files) != 1 || files[0].Name() != "ros-fleet" {
		t.Fatalf("expected only ros-fleet in directory, got %+v", files)
	}
}

func TestParseChecksums(t *testing.T) {
	data := []byte(`
# SHA256 checksums
e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855  ros-fleet_v1.0.0_linux_amd64.tar.gz
1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef *ros-fleet_v1.0.0_linux_arm64.tar.gz
abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890  dist/ros-fleet_v1.0.0_darwin_amd64.tar.gz

`)

	m := parseChecksums(data)
	if m["ros-fleet_v1.0.0_linux_amd64.tar.gz"] != "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855" {
		t.Errorf("failed to parse standard checksum line")
	}
	if m["ros-fleet_v1.0.0_linux_arm64.tar.gz"] != "1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef" {
		t.Errorf("failed to parse asterisk checksum line")
	}
	if m["ros-fleet_v1.0.0_darwin_amd64.tar.gz"] != "abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890" {
		t.Errorf("failed to parse path checksum line")
	}
}

func TestRunSelfUpdateRollback(t *testing.T) {
	origLookup := envLookupFunc
	origEnvPath := dockerEnvPath
	origRename := osRename
	t.Cleanup(func() {
		envLookupFunc = origLookup
		dockerEnvPath = origEnvPath
		osRename = origRename
	})
	dockerEnvPath = filepath.Join(t.TempDir(), "none")
	envLookupFunc = func(key string) string { return "" }

	tmpDir := t.TempDir()
	execPath := filepath.Join(tmpDir, "ros-fleet")
	originalContent := []byte("original-v1-binary")
	if err := os.WriteFile(execPath, originalContent, 0755); err != nil {
		t.Fatalf("failed to write original binary: %v", err)
	}

	newContent := []byte("new-v2-binary")
	archiveBytes, err := createMockTarGz("ros-fleet", newContent)
	if err != nil {
		t.Fatalf("failed to create mock tar.gz: %v", err)
	}

	h := sha256.Sum256(archiveBytes)
	hashHex := hex.EncodeToString(h[:])
	archiveName := "ros-fleet_v2.0.0_linux_amd64.tar.gz"
	checksumContent := fmt.Sprintf("%s  %s\n", hashHex, archiveName)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/test/repo/releases/latest":
			_ = json.NewEncoder(w).Encode(Release{
				TagName: "v2.0.0",
				Assets: []ReleaseAsset{
					{Name: archiveName, BrowserDownloadURL: "http://" + r.Host + "/archive.tar.gz"},
					{Name: "sha256sums.txt", BrowserDownloadURL: "http://" + r.Host + "/sha256sums.txt"},
				},
			})
		case "/archive.tar.gz":
			_, _ = w.Write(archiveBytes)
		case "/sha256sums.txt":
			_, _ = w.Write([]byte(checksumContent))
		}
	}))
	defer server.Close()

	// Intercept osRename: allow execPath -> oldPath, but fail tmpPath -> execPath
	osRename = func(oldpath, newpath string) error {
		if strings.Contains(oldpath, ".ros-fleet-update-") && newpath == execPath {
			return fmt.Errorf("injected rename failure for testing rollback")
		}
		return os.Rename(oldpath, newpath)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	opts := selfUpdateOptions{
		os:         "linux",
		arch:       "amd64",
		apiBaseURL: server.URL,
		httpClient: server.Client(),
		executableFn: func() (string, error) {
			return execPath, nil
		},
	}

	err = runSelfUpdateWithOptions(context.Background(), "test/repo", "v1.0.0", logger, opts)
	if err == nil || !strings.Contains(err.Error(), "injected rename failure") {
		t.Fatalf("expected injected rename failure error, got: %v", err)
	}

	// Verify rollback restored original binary
	restoredContent, err := os.ReadFile(execPath)
	if err != nil {
		t.Fatalf("failed to read restored executable: %v", err)
	}
	if !bytes.Equal(restoredContent, originalContent) {
		t.Fatalf("expected binary restored to original content, got %q", string(restoredContent))
	}

	// Verify .old file was cleaned up by rollback rename
	oldPath := execPath + ".old"
	if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
		t.Fatalf("expected .old file to be removed / rolled back")
	}

	// Verify staging temp files cleaned up
	files, err := os.ReadDir(tmpDir)
	if err != nil {
		t.Fatalf("failed to read tmpDir: %v", err)
	}
	if len(files) != 1 || files[0].Name() != "ros-fleet" {
		t.Fatalf("expected only original ros-fleet in dir after rollback, got %+v", files)
	}
}
