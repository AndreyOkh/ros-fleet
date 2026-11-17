package storage

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSanitizeName(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"Mikrotik-Core", "Mikrotik-Core"},
		{"Router / Branch..1", "Router_Branch..1"},
		{"../../etc/passwd", "etc_passwd"},
		{"office:gate:01", "office_gate_01"},
		{"___", "unnamed_router"},
	}

	for _, tt := range tests {
		got := SanitizeName(tt.input)
		if got != tt.expected {
			t.Errorf("SanitizeName(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestPrepareDestination(t *testing.T) {
	tempDir := t.TempDir()
	binDir := filepath.Join(tempDir, "binary")
	expDir := filepath.Join(tempDir, "exports")
	s := New(binDir, expDir)

	fixedTime := time.Date(2026, 11, 17, 10, 30, 0, 0, time.UTC)
	paths, err := s.PrepareDestination("Core-Router/1", fixedTime)
	if err != nil {
		t.Fatalf("PrepareDestination failed: %v", err)
	}

	expectedBackupPath := filepath.Join(binDir, "Core-Router_1", "Core-Router_1_2026-11-17_10-30-00.backup")
	if paths.LocalBackupPath != expectedBackupPath {
		t.Errorf("LocalBackupPath = %q, want %q", paths.LocalBackupPath, expectedBackupPath)
	}

	expectedRscPath := filepath.Join(expDir, "Core-Router_1.rsc")
	if paths.LocalRscPath != expectedRscPath {
		t.Errorf("LocalRscPath = %q, want %q", paths.LocalRscPath, expectedRscPath)
	}

	info, err := os.Stat(filepath.Dir(paths.LocalBackupPath))
	if err != nil || !info.IsDir() {
		t.Fatalf("expected binary router directory to exist as a folder: %v", err)
	}

	infoExp, err := os.Stat(expDir)
	if err != nil || !infoExp.IsDir() {
		t.Fatalf("expected export directory to exist as a folder: %v", err)
	}
}

func TestSaveStreamAndVerify(t *testing.T) {
	tempDir := t.TempDir()
	s := New(filepath.Join(tempDir, "bin"), filepath.Join(tempDir, "exp"))
	targetFile := filepath.Join(tempDir, "test.rsc")

	content := []byte("/system identity set name=MyRouter\n")
	n, err := s.SaveStream(bytes.NewReader(content), targetFile)
	if err != nil {
		t.Fatalf("SaveStream failed: %v", err)
	}
	if n != int64(len(content)) {
		t.Errorf("SaveStream written bytes = %d, want %d", n, len(content))
	}

	size, err := s.VerifyFile(targetFile)
	if err != nil {
		t.Fatalf("VerifyFile failed: %v", err)
	}
	if size != int64(len(content)) {
		t.Errorf("VerifyFile size = %d, want %d", size, len(content))
	}
}

func TestRetentionRotate(t *testing.T) {
	tempDir := t.TempDir()
	binDir := filepath.Join(tempDir, "binary")
	expDir := filepath.Join(tempDir, "exports")
	s := New(binDir, expDir)

	routerPath := filepath.Join(binDir, "Router1")
	if err := os.MkdirAll(routerPath, 0o755); err != nil {
		t.Fatal(err)
	}

	oldFile := filepath.Join(routerPath, "Router1_old.backup")
	_ = os.WriteFile(oldFile, []byte("dummy backup data"), 0o644)

	// Set mod time to 40 days ago
	oldTime := time.Now().AddDate(0, 0, -40)
	_ = os.Chtimes(oldFile, oldTime, oldTime)

	recentFile := filepath.Join(routerPath, "Router1_recent.backup")
	_ = os.WriteFile(recentFile, []byte("recent backup data"), 0o644)

	// In exportDir, place an old .rsc file. It should NOT be rotated!
	_ = os.MkdirAll(expDir, 0o755)
	rscFile := filepath.Join(expDir, "Router1.rsc")
	_ = os.WriteFile(rscFile, []byte("# RouterOS export"), 0o644)
	_ = os.Chtimes(rscFile, oldTime, oldTime)

	// Rotate with 30 days retention
	deleted, freed, err := s.Rotate(30, nil)
	if err != nil {
		t.Fatalf("Rotate failed: %v", err)
	}
	if deleted != 1 {
		t.Errorf("Rotate deleted = %d, want 1", deleted)
	}
	if freed == 0 {
		t.Errorf("expected freed bytes > 0")
	}

	if _, err := os.Stat(oldFile); !os.IsNotExist(err) {
		t.Errorf("expected oldFile to be deleted")
	}
	if _, err := os.Stat(recentFile); err != nil {
		t.Errorf("expected recentFile to still exist")
	}
	// Export .rsc must not be touched
	if _, err := os.Stat(rscFile); err != nil {
		t.Errorf("expected rscFile in exportDir to remain untouched")
	}
}
