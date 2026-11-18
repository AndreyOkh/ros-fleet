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

func TestListAndResolveBackups(t *testing.T) {
	tempDir := t.TempDir()
	binDir := filepath.Join(tempDir, "binary")
	expDir := filepath.Join(tempDir, "exports")
	s := New(binDir, expDir)

	routerName := "Router-Alpha"
	safeName := SanitizeName(routerName)

	// 1. Initially empty
	list, err := s.ListRouterBackups(routerName)
	if err != nil {
		t.Fatalf("ListRouterBackups error on empty dirs: %v", err)
	}
	if len(list) != 0 {
		t.Errorf("expected 0 backups for new router, got %d", len(list))
	}

	// 2. Create .rsc file in exportDir
	_ = os.MkdirAll(expDir, 0o755)
	rscPath := filepath.Join(expDir, safeName+".rsc")
	if err := os.WriteFile(rscPath, []byte("# RouterOS export"), 0o644); err != nil {
		t.Fatal(err)
	}

	// 3. Create two .backup files in binaryDir/Router-Alpha/
	routerBinDir := filepath.Join(binDir, safeName)
	_ = os.MkdirAll(routerBinDir, 0o755)

	tOld := time.Now().Add(-2 * time.Hour)
	tNew := time.Now().Add(-10 * time.Minute)

	oldBin := filepath.Join(routerBinDir, safeName+"_2026-11-18_08-00-00.backup")
	_ = os.WriteFile(oldBin, []byte("old backup binary"), 0o644)
	_ = os.Chtimes(oldBin, tOld, tOld)

	newBin := filepath.Join(routerBinDir, safeName+"_2026-11-18_10-00-00.backup")
	_ = os.WriteFile(newBin, []byte("new backup binary"), 0o644)
	_ = os.Chtimes(newBin, tNew, tNew)

	// List backups
	list, err = s.ListRouterBackups(routerName)
	if err != nil {
		t.Fatalf("ListRouterBackups failed: %v", err)
	}
	if len(list) != 3 {
		t.Fatalf("expected 3 items (1 rsc + 2 backups), got %d", len(list))
	}

	// First item is RSC
	if list[0].Type != "rsc" || list[0].FileName != safeName+".rsc" {
		t.Errorf("expected first item to be RSC %s.rsc, got %+v", safeName, list[0])
	}
	// Second item is newest backup
	if list[1].Type != "backup" || list[1].FileName != filepath.Base(newBin) {
		t.Errorf("expected second item to be newest backup, got %+v", list[1])
	}
	// Third item is older backup
	if list[2].Type != "backup" || list[2].FileName != filepath.Base(oldBin) {
		t.Errorf("expected third item to be older backup, got %+v", list[2])
	}

	// 4. Test ResolveBackupFile
	// (a) Valid export
	resolvedPath, dlName, err := s.ResolveBackupFile("export", routerName, safeName+".rsc")
	if err != nil {
		t.Errorf("ResolveBackupFile export failed: %v", err)
	}
	if resolvedPath != rscPath || dlName != safeName+".rsc" {
		t.Errorf("ResolveBackupFile export returned %q, %q; want %q, %q", resolvedPath, dlName, rscPath, safeName+".rsc")
	}

	// (b) Valid binary
	relBinary := filepath.Join(safeName, filepath.Base(newBin))
	resolvedBin, dlBinName, err := s.ResolveBackupFile("binary", routerName, relBinary)
	if err != nil {
		t.Errorf("ResolveBackupFile binary failed: %v", err)
	}
	if resolvedBin != newBin || dlBinName != filepath.Base(newBin) {
		t.Errorf("ResolveBackupFile binary returned %q, %q; want %q, %q", resolvedBin, dlBinName, newBin, filepath.Base(newBin))
	}

	// (c) Security test: Path Traversal attempts
	badPaths := []string{
		"../other.rsc",
		"../../etc/passwd",
		"/etc/passwd",
		"sub/../../export.rsc",
		safeName + "/../../other.backup",
	}
	for _, bad := range badPaths {
		if _, _, err := s.ResolveBackupFile("export", routerName, bad); err == nil {
			t.Errorf("expected path traversal error for export %q, got nil", bad)
		}
		if _, _, err := s.ResolveBackupFile("binary", routerName, bad); err == nil {
			t.Errorf("expected path traversal error for binary %q, got nil", bad)
		}
	}

	// (d) Router mismatch attempt
	_ = os.WriteFile(filepath.Join(routerBinDir, "OtherRouter_test.backup"), []byte("data"), 0o644)
	if _, _, err := s.ResolveBackupFile("binary", routerName, filepath.Join(safeName, "OtherRouter_test.backup")); err == nil {
		t.Errorf("expected error resolving file belonging to different router")
	}

	// (e) Invalid kind
	if _, _, err := s.ResolveBackupFile("invalid_kind", routerName, safeName+".rsc"); err == nil {
		t.Errorf("expected error for invalid kind")
	}

	// 5. Test LatestRouterBackup
	latestBin, err := s.LatestRouterBackup(routerName, "binary")
	if err != nil {
		t.Fatalf("LatestRouterBackup binary failed: %v", err)
	}
	if latestBin.FileName != filepath.Base(newBin) {
		t.Errorf("expected latest binary to be %q, got %q", filepath.Base(newBin), latestBin.FileName)
	}

	latestRsc, err := s.LatestRouterBackup(routerName, "export")
	if err != nil {
		t.Fatalf("LatestRouterBackup export failed: %v", err)
	}
	if latestRsc.FileName != safeName+".rsc" {
		t.Errorf("expected latest export to be %q, got %q", safeName+".rsc", latestRsc.FileName)
	}
}

