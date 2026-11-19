package updater

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// ReleaseAsset represents a single downloadable asset attached to a GitHub release.
type ReleaseAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Size               int64  `json:"size"`
}

// Release represents GitHub release metadata.
type Release struct {
	TagName     string         `json:"tag_name"`
	Name        string         `json:"name"`
	Body        string         `json:"body"`
	HTMLURL     string         `json:"html_url"`
	PublishedAt time.Time      `json:"published_at"`
	Assets      []ReleaseAsset `json:"assets"`
}

// CheckResult contains the evaluation of the latest release against the current version.
type CheckResult struct {
	UpdateAvailable bool      `json:"update_available"`
	CurrentVersion  string    `json:"current_version"`
	LatestVersion   string    `json:"latest_version"`
	ReleaseNotes    string    `json:"release_notes"`
	PublishedAt     time.Time `json:"published_at"`
	HTMLURL         string    `json:"html_url"`
	Release         *Release  `json:"release,omitempty"`
	Error           string    `json:"error,omitempty"`
}

// Checker manages periodic and on-demand checks against the GitHub releases API.
type Checker struct {
	mu             sync.RWMutex
	repo           string
	currentVersion string
	logger         *slog.Logger
	httpClient     *http.Client
	apiBaseURL     string
	checkInterval  time.Duration
	lastResult     *CheckResult
}

// NewChecker creates a new Checker instance.
func NewChecker(repo string, currentVersion string, logger *slog.Logger) *Checker {
	if logger == nil {
		logger = slog.Default()
	}
	if repo == "" {
		repo = "AndreyOkh/ros-fleet"
	}
	apiURL := "https://api.github.com"
	if env := os.Getenv("GITHUB_API_URL"); env != "" {
		apiURL = strings.TrimRight(env, "/")
	}
	return &Checker{
		repo:           repo,
		currentVersion: currentVersion,
		logger:         logger,
		httpClient: &http.Client{
			Timeout: 15 * time.Second,
		},
		apiBaseURL:    apiURL,
		checkInterval: 12 * time.Hour,
		lastResult: &CheckResult{
			CurrentVersion: currentVersion,
		},
	}
}

// SetBaseURL overrides the GitHub API base URL (useful for testing with httptest).
func (c *Checker) SetBaseURL(url string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.apiBaseURL = strings.TrimRight(url, "/")
}

// SetHTTPClient sets a custom HTTP client (useful for unit testing).
func (c *Checker) SetHTTPClient(client *http.Client) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.httpClient = client
}

// SetCheckInterval overrides the ticker duration (useful for unit testing).
func (c *Checker) SetCheckInterval(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.checkInterval = d
}

// CheckNow performs an immediate check against the GitHub releases API.
func (c *Checker) CheckNow(ctx context.Context) (*CheckResult, error) {
	c.mu.RLock()
	baseURL := c.apiBaseURL
	repo := c.repo
	client := c.httpClient
	currVer := c.currentVersion
	c.mu.RUnlock()

	apiURL := fmt.Sprintf("%s/repos/%s/releases/latest", baseURL, repo)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		c.recordError(currVer, err)
		return nil, fmt.Errorf("failed to create update check request: %w", err)
	}

	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "ros-fleet")

	resp, err := client.Do(req)
	if err != nil {
		c.recordError(currVer, err)
		return nil, fmt.Errorf("update check request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		err := fmt.Errorf("github api returned unexpected status %d: %s", resp.StatusCode, resp.Status)
		c.recordError(currVer, err)
		return nil, err
	}

	// Limit response reading to 10MB to avoid excessive memory usage
	bodyBytes, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	if err != nil {
		c.recordError(currVer, err)
		return nil, fmt.Errorf("failed to read github api response: %w", err)
	}

	var release Release
	if err := json.Unmarshal(bodyBytes, &release); err != nil {
		c.recordError(currVer, err)
		return nil, fmt.Errorf("failed to parse github release json: %w", err)
	}

	updateAvailable := IsUpdateAvailable(currVer, release.TagName)

	result := &CheckResult{
		UpdateAvailable: updateAvailable,
		CurrentVersion:  currVer,
		LatestVersion:   release.TagName,
		ReleaseNotes:    release.Body,
		PublishedAt:     release.PublishedAt,
		HTMLURL:         release.HTMLURL,
		Release:         &release,
	}

	c.mu.Lock()
	c.lastResult = result
	c.mu.Unlock()

	return result, nil
}

func (c *Checker) recordError(currentVer string, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.lastResult == nil {
		c.lastResult = &CheckResult{
			CurrentVersion: currentVer,
			Error:          err.Error(),
		}
	} else {
		// Preserve previous version info if present, but record latest error
		c.lastResult.Error = err.Error()
	}
}

// Start launches a non-blocking background goroutine that runs an initial check
// (logged without failing application startup) and continues checking on a periodic ticker.
func (c *Checker) Start(ctx context.Context) {
	go func() {
		// Run initial check at startup (non-fatal)
		if _, err := c.CheckNow(ctx); err != nil {
			c.logger.Warn("initial update check failed", "err", err)
		} else {
			status := c.GetStatus()
			if status != nil && status.UpdateAvailable {
				c.logger.Info("new ros-fleet update available", "latest", status.LatestVersion, "current", status.CurrentVersion)
			}
		}

		c.mu.RLock()
		interval := c.checkInterval
		c.mu.RUnlock()

		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if _, err := c.CheckNow(ctx); err != nil {
					c.logger.Warn("periodic update check failed", "err", err)
				}
			}
		}
	}()
}

// GetStatus returns the most recent CheckResult in a thread-safe manner.
func (c *Checker) GetStatus() *CheckResult {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if c.lastResult == nil {
		return &CheckResult{
			CurrentVersion: c.currentVersion,
		}
	}

	// Return a shallow copy of the result struct
	res := *c.lastResult
	return &res
}
