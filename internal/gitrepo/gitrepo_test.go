package gitrepo

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AndreyOkh/ros-fleet/internal/config"
)

func TestNormalizeRSC(t *testing.T) {
	input := []byte(`# 2026-11-17 14:30:15 by RouterOS 7.16.1
# software id = 1234
/interface bridge add name=bridge1
`)

	normalized := string(NormalizeRSC(input))
	if strings.Contains(normalized, "2026-11-17") {
		t.Errorf("expected date/time to be stripped, got:\n%s", normalized)
	}
	if !strings.Contains(normalized, "# RouterOS 7.16.1") {
		t.Errorf("expected version to be preserved, got:\n%s", normalized)
	}
}

func TestGitCommitAndPushCycle(t *testing.T) {
	tempDir := t.TempDir()
	exportDir := filepath.Join(tempDir, "exports")

	cfg := config.GitConfig{
		Enabled:     true,
		Branch:      "main",
		AuthorName:  "Test Bot",
		AuthorEmail: "test@local",
		AutoPush:    false,
	}

	repo := New(exportDir, cfg, slog.Default())
	ctx := context.Background()

	// 1. Initial write of Router1.rsc (normalized)
	rsc1 := NormalizeRSC([]byte("# 2026-11-17 01:00:00 by RouterOS 7.16.1\n/system identity set name=Router1\n"))
	_ = os.MkdirAll(exportDir, 0o755)
	if err := os.WriteFile(filepath.Join(exportDir, "Router1.rsc"), rsc1, 0o644); err != nil {
		t.Fatal(err)
	}

	committed, err := repo.CommitAndPush(ctx)
	if err != nil {
		t.Fatalf("first CommitAndPush failed: %v", err)
	}
	if !committed {
		t.Error("expected first cycle to commit")
	}

	status := repo.GetStatus()
	if status.LastCommitHash == "" {
		t.Error("expected non-empty commit hash")
	}
	if !strings.Contains(status.LastCommitMsg, "Router1") {
		t.Errorf("expected commit message to mention Router1, got %q", status.LastCommitMsg)
	}

	// 2. Second cycle: write identical config with different date/time in header
	rsc2 := NormalizeRSC([]byte("# 2026-11-17 02:00:00 by RouterOS 7.16.1\n/system identity set name=Router1\n"))
	if err := os.WriteFile(filepath.Join(exportDir, "Router1.rsc"), rsc2, 0o644); err != nil {
		t.Fatal(err)
	}

	committed, err = repo.CommitAndPush(ctx)
	if err != nil {
		t.Fatalf("second CommitAndPush failed: %v", err)
	}
	if committed {
		t.Error("expected no commit when normalized content is unchanged")
	}

	// 3. Third cycle: genuine configuration change
	rsc3 := NormalizeRSC([]byte("# 2026-11-17 03:00:00 by RouterOS 7.16.1\n/system identity set name=Router1-Renamed\n"))
	if err := os.WriteFile(filepath.Join(exportDir, "Router1.rsc"), rsc3, 0o644); err != nil {
		t.Fatal(err)
	}

	committed, err = repo.CommitAndPush(ctx)
	if err != nil {
		t.Fatalf("third CommitAndPush failed: %v", err)
	}
	if !committed {
		t.Error("expected commit on configuration change")
	}
}

func TestBuildEffectiveRemoteURL(t *testing.T) {
	tests := []struct {
		name     string
		rawURL   string
		token    string
		expected string
	}{
		{
			name:     "GitHub HTTPS without user",
			rawURL:   "https://github.com/myorg/configs.git",
			token:    "ghp_SecretToken123",
			expected: "https://x-access-token:ghp_SecretToken123@github.com/myorg/configs.git",
		},
		{
			name:     "GitLab HTTPS without user",
			rawURL:   "https://gitlab.com/myorg/configs.git",
			token:    "glpat-SecretToken456",
			expected: "https://oauth2:glpat-SecretToken456@gitlab.com/myorg/configs.git",
		},
		{
			name:     "Bitbucket HTTPS without user",
			rawURL:   "https://bitbucket.org/myorg/configs.git",
			token:    "bb_token_789",
			expected: "https://x-token-auth:bb_token_789@bitbucket.org/myorg/configs.git",
		},
		{
			name:     "Custom/self-hosted Git HTTPS",
			rawURL:   "https://git.company.local/network/mikrotik.git",
			token:    "pat_custom_111",
			expected: "https://oauth2:pat_custom_111@git.company.local/network/mikrotik.git",
		},
		{
			name:     "Explicit username in URL",
			rawURL:   "https://deploy-bot@gitlab.com/myorg/configs.git",
			token:    "glpat-SecretToken456",
			expected: "https://deploy-bot:glpat-SecretToken456@gitlab.com/myorg/configs.git",
		},
		{
			name:     "SSH URL converted to HTTPS with token",
			rawURL:   "git@github.com:myorg/configs.git",
			token:    "ghp_SecretToken123",
			expected: "https://x-access-token:ghp_SecretToken123@github.com/myorg/configs.git",
		},
		{
			name:     "Empty token returns unchanged URL",
			rawURL:   "https://github.com/myorg/configs.git",
			token:    "",
			expected: "https://github.com/myorg/configs.git",
		},
		{
			name:     "Empty rawURL returns empty",
			rawURL:   "",
			token:    "ghp_token",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := BuildEffectiveRemoteURL(tt.rawURL, tt.token)
			if got != tt.expected {
				t.Errorf("BuildEffectiveRemoteURL() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestSanitizeRemoteURL(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{
			input:    "https://x-access-token:ghp_SecretToken123@github.com/myorg/configs.git",
			expected: "https://github.com/myorg/configs.git",
		},
		{
			input:    "https://oauth2:glpat-xyz@gitlab.com/myorg/configs.git",
			expected: "https://gitlab.com/myorg/configs.git",
		},
		{
			input:    "git@github.com:myorg/configs.git",
			expected: "git@github.com:myorg/configs.git",
		},
		{
			input:    "https://github.com/myorg/configs.git",
			expected: "https://github.com/myorg/configs.git",
		},
		{
			input:    "",
			expected: "",
		},
	}

	for _, tt := range tests {
		got := SanitizeRemoteURL(tt.input)
		if got != tt.expected {
			t.Errorf("SanitizeRemoteURL(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestSanitizeOutput(t *testing.T) {
	repo := &GitRepository{
		cfg: config.GitConfig{
			AccessToken: "ghp_VerySecretString123",
		},
	}

	rawOutput := "fatal: unable to access 'https://x-access-token:ghp_VerySecretString123@github.com/org/repo.git': 403\nToken was ghp_VerySecretString123"
	sanitized := repo.SanitizeOutput(rawOutput)

	if strings.Contains(sanitized, "ghp_VerySecretString123") {
		t.Errorf("SanitizeOutput leaked raw token: %s", sanitized)
	}
	if !strings.Contains(sanitized, "[REDACTED]") {
		t.Errorf("SanitizeOutput expected [REDACTED], got: %s", sanitized)
	}
}

func TestGitPushWithLocalRemote(t *testing.T) {
	tempDir := t.TempDir()
	exportDir := filepath.Join(tempDir, "exports")
	remoteBareDir := filepath.Join(tempDir, "remote.git")

	// 1. Initialize bare remote
	repoInit := New(remoteBareDir, config.GitConfig{}, slog.Default())
	_, err := repoInit.runGit(context.Background(), tempDir, "init", "--bare", "-b", "main", remoteBareDir)
	if err != nil {
		t.Fatalf("failed to init bare remote: %v", err)
	}

	// 2. Setup export repository with AutoPush enabled to the bare remote
	cfg := config.GitConfig{
		Enabled:     true,
		Branch:      "main",
		RemoteURL:   remoteBareDir,
		AuthorName:  "Push Tester",
		AuthorEmail: "push@local",
		AutoPush:    true,
	}

	repo := New(exportDir, cfg, slog.Default())
	ctx := context.Background()

	_ = os.MkdirAll(exportDir, 0o755)
	if err := os.WriteFile(filepath.Join(exportDir, "Router1.rsc"), []byte("/system identity set name=R1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	committed, err := repo.CommitAndPush(ctx)
	if err != nil {
		t.Fatalf("CommitAndPush with AutoPush failed: %v", err)
	}
	if !committed {
		t.Fatal("expected commit to be created")
	}

	status := repo.GetStatus()
	if status.LastPushAt.IsZero() {
		t.Error("expected LastPushAt to be populated after push")
	}
	if status.LastError != "" {
		t.Errorf("expected no error in status, got %q", status.LastError)
	}
	if status.NeedsPush {
		t.Errorf("expected NeedsPush to be false after successful push, got true")
	}
	if status.Ahead != 0 {
		t.Errorf("expected Ahead to be 0 after successful push, got %d", status.Ahead)
	}

	// 3. Test unpushed commit with AutoPush disabled
	repo.cfg.AutoPush = false
	if err := os.WriteFile(filepath.Join(exportDir, "Router1.rsc"), []byte("/system identity set name=R1-Updated\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	committed2, err := repo.CommitAndPush(ctx)
	if err != nil || !committed2 {
		t.Fatalf("second CommitAndPush failed: %v", err)
	}

	statusUnpushed := repo.GetStatus()
	if !statusUnpushed.NeedsPush {
		t.Errorf("expected NeedsPush to be true when local has unpushed commit")
	}
	if statusUnpushed.Ahead != 1 {
		t.Errorf("expected Ahead to be 1, got %d", statusUnpushed.Ahead)
	}

	// 4. Test explicit Push()
	if err := repo.Push(ctx); err != nil {
		t.Fatalf("explicit Push() failed: %v", err)
	}

	statusPushed := repo.GetStatus()
	if statusPushed.NeedsPush {
		t.Errorf("expected NeedsPush to be false after Push(), got true")
	}
	if statusPushed.Ahead != 0 {
		t.Errorf("expected Ahead to be 0 after Push(), got %d", statusPushed.Ahead)
	}
}
