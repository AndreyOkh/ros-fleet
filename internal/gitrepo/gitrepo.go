package gitrepo

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/AndreyOkh/ros-fleet/internal/config"
)

var (
	// rscHeaderRegex matches the volatile RouterOS export header containing current date/time.
	// E.g.: "# 2026-11-17 03:00:05 by RouterOS 7.16.1" or "# nov/17/2026 03:00:05 by RouterOS 6.49.10"
	rscHeaderRegex = regexp.MustCompile(`(?m)^#\s+.*\s+by\s+RouterOS\s+(.*)$`)
)

// NormalizeRSC strips the dynamic date/time comment from RouterOS exports,
// replacing it with a stable header so that git only detects genuine configuration changes.
func NormalizeRSC(content []byte) []byte {
	return rscHeaderRegex.ReplaceAll(content, []byte("# RouterOS $1"))
}

// GitStatus holds telemetry and runtime state of the Git export repository.
type GitStatus struct {
	Enabled        bool      `json:"enabled"`
	Branch         string    `json:"branch"`
	RemoteURL      string    `json:"remote_url"`
	AutoPush       bool      `json:"auto_push"`
	LastCommitAt   time.Time `json:"last_commit_at"`
	LastCommitMsg  string    `json:"last_commit_msg"`
	LastCommitHash string    `json:"last_commit_hash"`
	LastPushAt     time.Time `json:"last_push_at"`
	LastError      string    `json:"last_error"`
}

// LastCommitFormatted returns human-readable commit time.
func (s GitStatus) LastCommitFormatted() string {
	if s.LastCommitAt.IsZero() {
		return "нет коммитов"
	}
	return s.LastCommitAt.Format("15:04:05 (02.01)")
}

// LastPushFormatted returns human-readable push time.
func (s GitStatus) LastPushFormatted() string {
	if s.LastPushAt.IsZero() {
		return "-"
	}
	return s.LastPushAt.Format("15:04:05 (02.01)")
}

// GitRepository manages local Git versioning and optional remote push of configuration files.
type GitRepository struct {
	exportDir string
	cfg       config.GitConfig
	logger    *slog.Logger
	mu        sync.RWMutex
	status    GitStatus
}

// New creates a new GitRepository manager for the export directory.
func New(exportDir string, cfg config.GitConfig, logger *slog.Logger) *GitRepository {
	cleanDir := filepath.Clean(exportDir)
	g := &GitRepository{
		exportDir: cleanDir,
		cfg:       cfg,
		logger:    logger.With(slog.String("component", "gitrepo")),
		status: GitStatus{
			Enabled:   cfg.Enabled,
			Branch:    cfg.Branch,
			RemoteURL: cfg.RemoteURL,
			AutoPush:  cfg.AutoPush,
		},
	}

	if cfg.Enabled {
		g.refreshStatusFromDisk()
	}

	return g
}

// GetStatus returns a snapshot of current Git status.
func (g *GitRepository) GetStatus() GitStatus {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.status
}

func (g *GitRepository) refreshStatusFromDisk() {
	gitDir := filepath.Join(g.exportDir, ".git")
	if _, err := os.Stat(gitDir); os.IsNotExist(err) {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	out, err := g.runGit(ctx, g.exportDir, "log", "-1", "--format=%h|%cI|%s")
	if err == nil && strings.TrimSpace(out) != "" {
		parts := strings.SplitN(strings.TrimSpace(out), "|", 3)
		if len(parts) == 3 {
			g.status.LastCommitHash = parts[0]
			if t, parseErr := time.Parse(time.RFC3339, parts[1]); parseErr == nil {
				g.status.LastCommitAt = t
			}
			g.status.LastCommitMsg = parts[2]
		}
	}
}

// EnsureRepo initializes the repository if .git is missing and updates user/remotes.
func (g *GitRepository) EnsureRepo(ctx context.Context) error {
	if !g.cfg.Enabled {
		return nil
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	return g.ensureRepoLocked(ctx)
}

func (g *GitRepository) ensureRepoLocked(ctx context.Context) error {
	if err := os.MkdirAll(g.exportDir, 0o755); err != nil {
		return fmt.Errorf("failed to create export directory %q: %w", g.exportDir, err)
	}

	gitDir := filepath.Join(g.exportDir, ".git")
	if _, err := os.Stat(gitDir); os.IsNotExist(err) {
		branch := g.cfg.Branch
		if branch == "" {
			branch = "main"
		}
		if _, err := g.runGit(ctx, g.exportDir, "init", "-b", branch); err != nil {
			return fmt.Errorf("git init failed: %w", err)
		}

		authorName := g.cfg.AuthorName
		if authorName == "" {
			authorName = "ros-fleet"
		}
		authorEmail := g.cfg.AuthorEmail
		if authorEmail == "" {
			authorEmail = "ros-fleet@local"
		}

		_, _ = g.runGit(ctx, g.exportDir, "config", "user.name", authorName)
		_, _ = g.runGit(ctx, g.exportDir, "config", "user.email", authorEmail)

		g.logger.Info("Initialized new Git repository for router exports",
			slog.String("dir", g.exportDir),
			slog.String("branch", branch),
		)
	}

	// Configure remote origin if remote_url is provided
	if strings.TrimSpace(g.cfg.RemoteURL) != "" {
		remotes, _ := g.runGit(ctx, g.exportDir, "remote")
		hasOrigin := false
		for _, r := range strings.Fields(remotes) {
			if r == "origin" {
				hasOrigin = true
				break
			}
		}

		if hasOrigin {
			_, _ = g.runGit(ctx, g.exportDir, "remote", "set-url", "origin", g.cfg.RemoteURL)
		} else {
			_, _ = g.runGit(ctx, g.exportDir, "remote", "add", "origin", g.cfg.RemoteURL)
		}
	}

	return nil
}

// CommitAndPush stages all updated files in exportDir, commits if changes are detected,
// and optionally pushes to the remote origin.
func (g *GitRepository) CommitAndPush(ctx context.Context) (bool, error) {
	if !g.cfg.Enabled {
		return false, nil
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	if err := g.ensureRepoLocked(ctx); err != nil {
		g.status.LastError = err.Error()
		return false, err
	}

	// 1. Stage all changes
	if _, err := g.runGit(ctx, g.exportDir, "add", "-A"); err != nil {
		g.status.LastError = fmt.Sprintf("git add error: %v", err)
		return false, fmt.Errorf("git add failed: %w", err)
	}

	// 2. Check porcelain status
	statusOut, err := g.runGit(ctx, g.exportDir, "status", "--porcelain")
	if err != nil {
		g.status.LastError = fmt.Sprintf("git status error: %v", err)
		return false, fmt.Errorf("git status failed: %w", err)
	}

	if strings.TrimSpace(statusOut) == "" {
		g.logger.Info("No configuration changes detected in export directory")
		return false, nil
	}

	// 3. Extract changed router names for a descriptive commit message
	var changedRouters []string
	seen := make(map[string]bool)
	for _, line := range strings.Split(statusOut, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) >= 2 {
			fileName := parts[len(parts)-1]
			if strings.HasSuffix(fileName, ".rsc") {
				routerName := strings.TrimSuffix(filepath.Base(fileName), ".rsc")
				if !seen[routerName] {
					seen[routerName] = true
					changedRouters = append(changedRouters, routerName)
				}
			}
		}
	}

	var commitMsg string
	if len(changedRouters) > 0 {
		commitMsg = fmt.Sprintf("backup: update configs (%s)", strings.Join(changedRouters, ", "))
	} else {
		commitMsg = "backup: update configs"
	}

	// 4. Commit
	if _, err := g.runGit(ctx, g.exportDir, "commit", "-m", commitMsg); err != nil {
		g.status.LastError = fmt.Sprintf("git commit error: %v", err)
		return false, fmt.Errorf("git commit failed: %w", err)
	}

	hashOut, _ := g.runGit(ctx, g.exportDir, "rev-parse", "--short", "HEAD")
	g.status.LastCommitAt = time.Now()
	g.status.LastCommitMsg = commitMsg
	g.status.LastCommitHash = strings.TrimSpace(hashOut)
	g.status.LastError = ""

	g.logger.Info("Committed updated configurations to Git",
		slog.String("commit", g.status.LastCommitHash),
		slog.String("message", commitMsg),
		slog.Int("changed_devices", len(changedRouters)),
	)

	// 5. Auto push if enabled
	if g.cfg.AutoPush {
		branch := g.cfg.Branch
		if branch == "" {
			branch = "main"
		}
		if pushOut, pushErr := g.runGit(ctx, g.exportDir, "push", "-u", "origin", branch); pushErr != nil {
			g.status.LastError = fmt.Sprintf("push error: %s", pushOut)
			g.logger.Error("git push failed",
				slog.String("output", pushOut),
				slog.Any("error", pushErr),
			)
			return true, fmt.Errorf("git push failed: %w (output: %s)", pushErr, pushOut)
		}

		g.status.LastPushAt = time.Now()
		g.status.LastError = ""
		g.logger.Info("Pushed configuration changes to remote Git repository", slog.String("branch", branch))
	}

	return true, nil
}

func (g *GitRepository) runGit(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	outStr := strings.TrimSpace(string(out))
	if err != nil {
		return outStr, fmt.Errorf("git %s error: %w (output: %s)", strings.Join(args, " "), err, outStr)
	}
	return outStr, nil
}
