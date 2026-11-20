package gitrepo

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/AndreyOkh/ros-fleet/internal/config"
)

var (
	// rscHeaderRegex matches the volatile RouterOS export header containing current date/time.
	// E.g.: "# 2026-11-17 03:00:05 by RouterOS 7.16.1" or "# nov/17/2026 03:00:05 by RouterOS 6.49.10"
	rscHeaderRegex = regexp.MustCompile(`(?m)^#\s+.*\s+by\s+RouterOS\s+(.*)$`)

	// credRegex matches credentials embedded in HTTP/HTTPS URLs (e.g. https://token@ or https://user:pass@)
	credRegex = regexp.MustCompile(`(https?://)[^/\s@]+@`)
)

// NormalizeRSC strips the dynamic date/time comment from RouterOS exports,
// replacing it with a stable header so that git only detects genuine configuration changes.
func NormalizeRSC(content []byte) []byte {
	return rscHeaderRegex.ReplaceAll(content, []byte("# RouterOS $1"))
}

// BuildEffectiveRemoteURL constructs an authenticated Git remote URL incorporating the access token if provided.
// It supports:
// - standard HTTPS URLs: https://github.com/org/repo.git -> https://x-access-token:<token>@github.com/org/repo.git
// - GitLab HTTPS URLs: https://gitlab.com/org/repo.git -> https://oauth2:<token>@gitlab.com/org/repo.git
// - Bitbucket HTTPS URLs: https://bitbucket.org/org/repo.git -> https://x-token-auth:<token>@bitbucket.org/org/repo.git
// - generic HTTP/HTTPS URLs: https://git.local/org/repo.git -> https://oauth2:<token>@git.local/org/repo.git
// - URLs with explicit username: https://myuser@github.com/org/repo.git -> https://myuser:<token>@github.com/org/repo.git
// - SSH URLs with token provided: git@github.com:org/repo.git -> converted to https://x-access-token:<token>@github.com/org/repo.git
func BuildEffectiveRemoteURL(rawURL string, token string) string {
	rawURL = strings.TrimSpace(rawURL)
	token = strings.TrimSpace(token)
	if rawURL == "" {
		return ""
	}
	if token == "" {
		return rawURL
	}

	// Support converting SSH syntax git@host:owner/repo.git into HTTPS when token is provided
	if strings.HasPrefix(rawURL, "git@") {
		afterAt := rawURL[4:]
		parts := strings.SplitN(afterAt, ":", 2)
		if len(parts) == 2 {
			rawURL = fmt.Sprintf("https://%s/%s", parts[0], parts[1])
		}
	}

	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return rawURL
	}

	hostLower := strings.ToLower(u.Host)
	var username string

	if u.User != nil && u.User.Username() != "" {
		username = u.User.Username()
	} else {
		switch {
		case strings.Contains(hostLower, "github.com"):
			username = "x-access-token"
		case strings.Contains(hostLower, "gitlab"):
			username = "oauth2"
		case strings.Contains(hostLower, "bitbucket"):
			username = "x-token-auth"
		default:
			username = "oauth2"
		}
	}

	u.User = url.UserPassword(username, token)
	return u.String()
}

// SanitizeRemoteURL strips credentials (tokens/passwords) from an HTTP/HTTPS remote URL for safe display and logging.
func SanitizeRemoteURL(rawURL string) string {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return ""
	}

	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return rawURL
	}

	if u.User != nil {
		u.User = nil
	}
	return u.String()
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
	HasRemote      bool      `json:"has_remote"`
	Ahead          int       `json:"ahead"`
	NeedsPush      bool      `json:"needs_push"`
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
	mu        sync.Mutex
	status    GitStatus
}

// New creates a new GitRepository manager for the export directory.
func New(exportDir string, cfg config.GitConfig, logger *slog.Logger) *GitRepository {
	cleanDir := filepath.Clean(exportDir)
	hasRemote := strings.TrimSpace(cfg.RemoteURL) != ""
	g := &GitRepository{
		exportDir: cleanDir,
		cfg:       cfg,
		logger:    logger.With(slog.String("component", "gitrepo")),
		status: GitStatus{
			Enabled:   cfg.Enabled,
			Branch:    cfg.Branch,
			RemoteURL: SanitizeRemoteURL(cfg.RemoteURL),
			AutoPush:  cfg.AutoPush,
			HasRemote: hasRemote,
		},
	}

	if cfg.Enabled {
		g.refreshStatusFromDiskLocked()
	}

	return g
}

// SanitizeOutput replaces sensitive tokens and embedded HTTP credentials with [REDACTED].
func (g *GitRepository) SanitizeOutput(input string) string {
	if input == "" {
		return ""
	}

	result := input
	if token := strings.TrimSpace(g.cfg.AccessToken); token != "" {
		result = strings.ReplaceAll(result, token, "[REDACTED]")
	}
	result = credRegex.ReplaceAllString(result, "${1}[REDACTED]@")
	return result
}

// GetStatus returns a snapshot of current Git status.
func (g *GitRepository) GetStatus() GitStatus {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.cfg.Enabled {
		g.refreshStatusFromDiskLocked()
	}
	return g.status
}

func (g *GitRepository) refreshStatusFromDiskLocked() {
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

	g.status.HasRemote = strings.TrimSpace(g.cfg.RemoteURL) != ""
	if g.status.HasRemote && g.status.LastCommitHash != "" {
		branch := g.cfg.Branch
		if branch == "" {
			branch = "main"
		}
		remoteRef := "origin/" + branch
		if _, verifyErr := g.runGit(ctx, g.exportDir, "rev-parse", "--verify", remoteRef); verifyErr == nil {
			aheadOut, countErr := g.runGit(ctx, g.exportDir, "rev-list", "--count", remoteRef+"..HEAD")
			if countErr == nil {
				aheadCount, _ := strconv.Atoi(strings.TrimSpace(aheadOut))
				g.status.Ahead = aheadCount
			}
		} else {
			// Remote tracking branch not found yet, but local has commits
			g.status.Ahead = 1
		}
	} else {
		g.status.Ahead = 0
	}

	g.status.NeedsPush = g.status.HasRemote && (g.status.Ahead > 0 || g.status.LastError != "")
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

		effectiveURL := BuildEffectiveRemoteURL(g.cfg.RemoteURL, g.cfg.AccessToken)
		if hasOrigin {
			_, _ = g.runGit(ctx, g.exportDir, "remote", "set-url", "origin", effectiveURL)
		} else {
			_, _ = g.runGit(ctx, g.exportDir, "remote", "add", "origin", effectiveURL)
		}
	}

	return nil
}

// Push explicitly pushes the export branch to the remote origin.
func (g *GitRepository) Push(ctx context.Context) error {
	if !g.cfg.Enabled {
		return errors.New("git repository versioning is disabled")
	}
	if strings.TrimSpace(g.cfg.RemoteURL) == "" {
		return errors.New("remote_url is not configured")
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	return g.pushLocked(ctx)
}

// FetchAndMerge fetches remote changes from origin and merges them into the current branch.
func (g *GitRepository) FetchAndMerge(ctx context.Context) error {
	if !g.cfg.Enabled {
		return errors.New("git repository versioning is disabled")
	}
	if strings.TrimSpace(g.cfg.RemoteURL) == "" {
		return errors.New("remote_url is not configured")
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	branch := g.cfg.Branch
	if branch == "" {
		branch = "main"
	}

	return g.fetchAndMergeLocked(ctx, branch)
}

func (g *GitRepository) fetchAndMergeLocked(ctx context.Context, branch string) error {
	if err := g.ensureRepoLocked(ctx); err != nil {
		return err
	}

	// 1. Fetch from remote
	fetchOut, fetchErr := g.runGit(ctx, g.exportDir, "fetch", "origin", branch)
	if fetchErr != nil {
		return fmt.Errorf("git fetch origin %s failed: %w (output: %s)", branch, fetchErr, fetchOut)
	}

	// 2. Merge remote branch favoring local live router configs on conflict (-X ours)
	remoteRef := "origin/" + branch
	mergeMsg := fmt.Sprintf("backup: merge remote changes from %s", remoteRef)
	mergeOut, mergeErr := g.runGit(ctx, g.exportDir, "merge", "--no-edit", "--allow-unrelated-histories", "-X", "ours", "-m", mergeMsg, remoteRef)
	if mergeErr != nil {
		// Clean up any incomplete merge state
		_, _ = g.runGit(ctx, g.exportDir, "merge", "--abort")
		return fmt.Errorf("git merge %s failed: %w (output: %s)", remoteRef, mergeErr, mergeOut)
	}

	// Update telemetry
	hashOut, _ := g.runGit(ctx, g.exportDir, "rev-parse", "--short", "HEAD")
	if strings.TrimSpace(hashOut) != "" {
		g.status.LastCommitHash = strings.TrimSpace(hashOut)
		g.status.LastCommitAt = time.Now()
		g.status.LastCommitMsg = mergeMsg
	}

	g.logger.Info("Successfully merged remote changes into local export branch",
		slog.String("branch", branch),
		slog.String("remote_ref", remoteRef),
	)
	return nil
}

func (g *GitRepository) pushLocked(ctx context.Context) error {
	if err := g.ensureRepoLocked(ctx); err != nil {
		g.status.LastError = g.SanitizeOutput(err.Error())
		return err
	}

	branch := g.cfg.Branch
	if branch == "" {
		branch = "main"
	}

	pushOut, pushErr := g.runGit(ctx, g.exportDir, "push", "-u", "origin", branch)
	if pushErr != nil {
		// If initial push failed, remote may contain commits that we do not have locally (diverged / non-fast-forward).
		// Attempt auto-recovery: fetch from remote, merge into local branch, and retry push.
		g.logger.Warn("Initial git push failed, attempting to fetch and merge remote changes...",
			slog.String("branch", branch),
			slog.String("output", g.SanitizeOutput(pushOut)),
		)

		if mergeErr := g.fetchAndMergeLocked(ctx, branch); mergeErr == nil {
			g.logger.Info("Auto-merge of remote changes succeeded, retrying push...", slog.String("branch", branch))
			retryOut, retryErr := g.runGit(ctx, g.exportDir, "push", "-u", "origin", branch)
			if retryErr == nil {
				g.status.LastPushAt = time.Now()
				g.status.LastError = ""
				g.status.Ahead = 0
				g.status.NeedsPush = false
				g.logger.Info("Successfully pushed configuration changes after auto-merging remote", slog.String("branch", branch))
				return nil
			}
			pushOut = retryOut
			pushErr = retryErr
		} else {
			g.logger.Warn("Failed to auto-merge remote changes during push recovery", slog.Any("error", mergeErr))
		}

		sanitizedErr := g.SanitizeOutput(fmt.Sprintf("push error: %s", pushOut))
		g.status.LastError = sanitizedErr
		g.status.NeedsPush = true
		g.logger.Error("git push failed",
			slog.String("output", g.SanitizeOutput(pushOut)),
			slog.Any("error", pushErr),
		)
		return fmt.Errorf("git push failed: %w (output: %s)", pushErr, g.SanitizeOutput(pushOut))
	}

	g.status.LastPushAt = time.Now()
	g.status.LastError = ""
	g.status.Ahead = 0
	g.status.NeedsPush = false
	g.logger.Info("Pushed configuration changes to remote Git repository", slog.String("branch", branch))
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
		g.status.LastError = g.SanitizeOutput(err.Error())
		return false, err
	}

	// 1. Stage all changes
	if _, err := g.runGit(ctx, g.exportDir, "add", "-A"); err != nil {
		g.status.LastError = g.SanitizeOutput(fmt.Sprintf("git add error: %v", err))
		return false, fmt.Errorf("git add failed: %w", err)
	}

	// 2. Check porcelain status
	statusOut, err := g.runGit(ctx, g.exportDir, "status", "--porcelain")
	if err != nil {
		g.status.LastError = g.SanitizeOutput(fmt.Sprintf("git status error: %v", err))
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
		g.status.LastError = g.SanitizeOutput(fmt.Sprintf("git commit error: %v", err))
		return false, fmt.Errorf("git commit failed: %w", err)
	}

	hashOut, _ := g.runGit(ctx, g.exportDir, "rev-parse", "--short", "HEAD")
	g.status.LastCommitAt = time.Now()
	g.status.LastCommitMsg = commitMsg
	g.status.LastCommitHash = strings.TrimSpace(hashOut)
	g.status.LastError = ""
	g.status.Ahead++
	if g.status.HasRemote {
		g.status.NeedsPush = true
	}

	g.logger.Info("Committed updated configurations to Git",
		slog.String("commit", g.status.LastCommitHash),
		slog.String("message", commitMsg),
		slog.Int("changed_devices", len(changedRouters)),
	)

	// 5. Auto push if enabled
	if g.cfg.AutoPush {
		if pushErr := g.pushLocked(ctx); pushErr != nil {
			return true, pushErr
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
		sanitizedOut := g.SanitizeOutput(outStr)
		sanitizedArgs := g.SanitizeOutput(strings.Join(args, " "))
		return sanitizedOut, fmt.Errorf("git %s error: %w (output: %s)", sanitizedArgs, err, sanitizedOut)
	}
	return g.SanitizeOutput(outStr), nil
}
