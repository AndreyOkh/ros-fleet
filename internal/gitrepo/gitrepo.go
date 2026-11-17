package gitrepo

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/AndreyOkh/ros-fleet/internal/config"
	"github.com/AndreyOkh/ros-fleet/internal/storage"
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

// GitRepository manages local Git versioning of configuration files.
type GitRepository struct {
	cfg    config.GitConfig
	logger *slog.Logger
	mu     sync.Mutex
}

// New creates a new GitRepository manager.
func New(cfg config.GitConfig, logger *slog.Logger) *GitRepository {
	return &GitRepository{
		cfg:    cfg,
		logger: logger.With(slog.String("component", "gitrepo")),
	}
}

// SyncConfig writes the normalized .rsc file and commits changes if detected.
func (g *GitRepository) SyncConfig(ctx context.Context, routerName string, rscBytes []byte) (bool, error) {
	if !g.cfg.Enabled {
		return false, nil
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	repoDir := filepath.Clean(g.cfg.RepoDir)
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		return false, fmt.Errorf("failed to create git repo dir %q: %w", repoDir, err)
	}

	// Ensure repository is initialized
	gitDir := filepath.Join(repoDir, ".git")
	if _, err := os.Stat(gitDir); os.IsNotExist(err) {
		branch := g.cfg.Branch
		if branch == "" {
			branch = "main"
		}
		if _, err := g.runGit(ctx, repoDir, "init", "-b", branch); err != nil {
			return false, fmt.Errorf("git init failed: %w", err)
		}

		authorName := g.cfg.AuthorName
		if authorName == "" {
			authorName = "ros-fleet"
		}
		authorEmail := g.cfg.AuthorEmail
		if authorEmail == "" {
			authorEmail = "ros-fleet@local"
		}

		_, _ = g.runGit(ctx, repoDir, "config", "user.name", authorName)
		_, _ = g.runGit(ctx, repoDir, "config", "user.email", authorEmail)
		g.logger.Info("Initialized new Git repository for router configs",
			slog.String("dir", repoDir),
			slog.String("branch", branch),
		)
	}

	fileName := storage.SanitizeName(routerName) + ".rsc"
	targetPath := filepath.Join(repoDir, fileName)

	// Normalize and write content
	normalized := NormalizeRSC(rscBytes)
	existing, err := os.ReadFile(targetPath)
	if err == nil && bytes.Equal(existing, normalized) {
		// File has identical content, no git operation needed
		return false, nil
	}

	if err := os.WriteFile(targetPath, normalized, 0o644); err != nil {
		return false, fmt.Errorf("failed to write normalized config to %q: %w", targetPath, err)
	}

	// Check git status
	statusOut, err := g.runGit(ctx, repoDir, "status", "--porcelain", fileName)
	if err != nil {
		return false, fmt.Errorf("git status failed: %w", err)
	}

	if strings.TrimSpace(statusOut) == "" {
		return false, nil
	}

	// Stage file
	if _, err := g.runGit(ctx, repoDir, "add", fileName); err != nil {
		return false, fmt.Errorf("git add %q failed: %w", fileName, err)
	}

	// Commit
	commitMsg := fmt.Sprintf("chore(backup): update config for %s", routerName)
	if _, err := g.runGit(ctx, repoDir, "commit", "-m", commitMsg); err != nil {
		return false, fmt.Errorf("git commit failed: %w", err)
	}

	g.logger.Info("Committed updated configuration to Git",
		slog.String("router", routerName),
		slog.String("file", fileName),
	)

	// Optional auto push
	if g.cfg.AutoPush {
		branch := g.cfg.Branch
		if branch == "" {
			branch = "main"
		}
		if pushOut, pushErr := g.runGit(ctx, repoDir, "push", "origin", branch); pushErr != nil {
			g.logger.Warn("git push failed",
				slog.String("output", pushOut),
				slog.Any("error", pushErr),
			)
		} else {
			g.logger.Info("Pushed config changes to remote repository", slog.String("branch", branch))
		}
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
