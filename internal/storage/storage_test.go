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

	// 1. Success case
	content := []byte("/system identity set name=MyRouter\n")
	n, err := s.SaveStream(bytes.NewReader(content), targetFile)
	if err != nil {
		t.Fatalf("SaveStream failed: %v", err)
	}
	if n != int64(len(content)) {
		t.Errorf("SaveStream written bytes = %d, want %d", n, len(content))
	}

	// Verify file
	size, err := s.VerifyFile(targetFile)
	if err != nil {
		t.Fatalf("VerifyFile failed: %v", err)
	}
	if size != int64(len(content)) {
		t.Errorf("VerifyFile size = %d, want %d", size, len(content))
	}

	// 2. Empty stream case
	emptyTarget := filepath.Join(tempDir, "empty.rsc")
	_, err = s.SaveStream(bytes.NewReader([]byte{}), emptyTarget)
	if err == nil {
		t.Error("expected SaveStream to fail on empty stream")
	}

	// Ensure .tmp file is removed on error
	if _, statErr := os.Stat(emptyTarget + ".tmp"); !os.IsNotExist(statErr) {
		t.Error("expected temp file to be cleaned up after error")
	}
}

func TestRetentionRotate(t *testing.T) {
	tempDir := t.TempDir()
	s := New(tempDir)

	now := time.Now()
	recentDate := now.AddDate(0, 0, -5).Format("2006-01-02")
	oldDate := now.AddDate(0, 0, -45).Format("2006-01-02")

	routerPath := filepath.Join(tempDir, "Router1")
	recentDir := filepath.Join(routerPath, recentDate)
	oldDir := filepath.Join(routerPath, oldDate)

	if err := os.MkdirAll(recentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(oldDir, 0o755); err != nil {
		t.Fatal(err)
	}

	_ = os.WriteFile(filepath.Join(recentDir, "test.rsc"), []byte("data"), 0o644)
	_ = os.WriteFile(filepath.Join(oldDir, "test.rsc"), []byte("data"), 0o644)

	// Rotate with 30 days retention
	removed, err := s.Rotate(30, nil)
	if err != nil {
		t.Fatalf("Rotate failed: %v", err)
	}
	if removed != 1 {
		t.Errorf("Rotate removed = %d, want 1", removed)
	}

	// Check oldDir is gone
	if _, err := os.Stat(oldDir); !os.IsNotExist(err) {
		t.Errorf("expected oldDir %q to be deleted", oldDir)
	}

	// Check recentDir still exists
	if _, err := os.Stat(recentDir); err != nil {
		t.Errorf("expected recentDir %q to still exist", recentDir)
	}
}
