package storage

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
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
	s := New(tempDir)

	fixedTime := time.Date(2026, 11, 17, 10, 30, 0, 0, time.UTC)
	paths, err := s.PrepareDestination("Core-Router/1", fixedTime)
	if err != nil {
		t.Fatalf("PrepareDestination failed: %v", err)
	}

	expectedDir := filepath.Join(tempDir, "Core-Router_1", "2026-11-17")
	if paths.DateDir != expectedDir {
		t.Errorf("DateDir = %q, want %q", paths.DateDir, expectedDir)
	}

	info, err := os.Stat(paths.DateDir)
	if err != nil || !info.IsDir() {
		t.Fatalf("expected date directory to exist as a folder: %v", err)
	}

	if !strings.HasSuffix(paths.LocalRscPath, "Core-Router_1_2026-11-17_10-30-00.rsc") {
		t.Errorf("unexpected LocalRscPath: %q", paths.LocalRscPath)
	}
	if !strings.HasSuffix(paths.LocalBackupPath, "Core-Router_1_2026-11-17_10-30-00.backup") {
		t.Errorf("unexpected LocalBackupPath: %q", paths.LocalBackupPath)
	}
}

func TestSaveStreamAndVerify(t *testing.T) {
	tempDir := t.TempDir()
	s := New(tempDir)
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
	s := New(tempDir)

	routerPath := filepath.Join(tempDir, "Router1", "2026-01-01")
	if err := os.MkdirAll(routerPath, 0o755); err != nil {
		t.Fatal(err)
	}

	oldFile := filepath.Join(routerPath, "old.backup")
	_ = os.WriteFile(oldFile, []byte("dummy backup data"), 0o644)

	// Set mod time to 40 days ago
	oldTime := time.Now().AddDate(0, 0, -40)
	_ = os.Chtimes(oldFile, oldTime, oldTime)

	recentPath := filepath.Join(tempDir, "Router1", "2026-11-17")
	_ = os.MkdirAll(recentPath, 0o755)
	recentFile := filepath.Join(recentPath, "recent.backup")
	_ = os.WriteFile(recentFile, []byte("recent backup data"), 0o644)

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
}
