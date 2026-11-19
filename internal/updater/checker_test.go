package updater

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestCheckerCheckNowSuccess(t *testing.T) {
	mockRelease := Release{
		TagName:     "v2.5.0",
		Name:        "Release 2.5.0",
		Body:        "## Release Notes\n* Fixed update issue\n* Added new features",
		HTMLURL:     "https://github.com/AndreyOkh/ros-fleet/releases/tag/v2.5.0",
		PublishedAt: time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC),
		Assets: []ReleaseAsset{
			{
				Name:               "ros-fleet_v2.5.0_linux_amd64.tar.gz",
				BrowserDownloadURL: "https://example.com/ros-fleet_v2.5.0_linux_amd64.tar.gz",
				Size:               1234567,
			},
			{
				Name:               "sha256sums.txt",
				BrowserDownloadURL: "https://example.com/sha256sums.txt",
				Size:               256,
			},
		},
	}

	var receivedAccept string
	var receivedUserAgent string
	var requestedPath string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAccept = r.Header.Get("Accept")
		receivedUserAgent = r.Header.Get("User-Agent")
		requestedPath = r.URL.Path

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(mockRelease)
	}))
	defer server.Close()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	checker := NewChecker("AndreyOkh/ros-fleet", "v2.1.0", logger)
	checker.SetBaseURL(server.URL)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	result, err := checker.CheckNow(ctx)
	if err != nil {
		t.Fatalf("CheckNow failed: %v", err)
	}

	if requestedPath != "/repos/AndreyOkh/ros-fleet/releases/latest" {
		t.Errorf("expected path /repos/AndreyOkh/ros-fleet/releases/latest, got %s", requestedPath)
	}
	if receivedAccept != "application/vnd.github+json" {
		t.Errorf("expected Accept application/vnd.github+json, got %s", receivedAccept)
	}
	if receivedUserAgent != "ros-fleet" {
		t.Errorf("expected User-Agent ros-fleet, got %s", receivedUserAgent)
	}

	if !result.UpdateAvailable {
		t.Errorf("expected UpdateAvailable to be true")
	}
	if result.CurrentVersion != "v2.1.0" {
		t.Errorf("expected CurrentVersion v2.1.0, got %s", result.CurrentVersion)
	}
	if result.LatestVersion != "v2.5.0" {
		t.Errorf("expected LatestVersion v2.5.0, got %s", result.LatestVersion)
	}
	if result.ReleaseNotes != mockRelease.Body {
		t.Errorf("expected ReleaseNotes %q, got %q", mockRelease.Body, result.ReleaseNotes)
	}
	if len(result.Release.Assets) != 2 {
		t.Errorf("expected 2 assets, got %d", len(result.Release.Assets))
	}

	// Verify GetStatus
	status := checker.GetStatus()
	if status == nil || !status.UpdateAvailable || status.LatestVersion != "v2.5.0" {
		t.Errorf("GetStatus() mismatch: %+v", status)
	}
}

func TestCheckerUpToDateOrDev(t *testing.T) {
	mockRelease := Release{
		TagName: "v2.1.0",
		Body:    "Current release",
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(mockRelease)
	}))
	defer server.Close()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	// Case 1: Same version -> false
	c1 := NewChecker("AndreyOkh/ros-fleet", "v2.1.0", logger)
	c1.SetBaseURL(server.URL)
	res1, err := c1.CheckNow(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res1.UpdateAvailable {
		t.Errorf("expected UpdateAvailable to be false for identical versions")
	}

	// Case 2: Dev version -> false
	c2 := NewChecker("AndreyOkh/ros-fleet", "dev", logger)
	c2.SetBaseURL(server.URL)
	res2, err := c2.CheckNow(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res2.UpdateAvailable {
		t.Errorf("expected UpdateAvailable to be false for dev current version")
	}
}

func TestCheckerErrors(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	// Subtest 1: Server returns 404
	t.Run("404 not found", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "Not found", http.StatusNotFound)
		}))
		defer server.Close()

		checker := NewChecker("AndreyOkh/ros-fleet", "v2.1.0", logger)
		checker.SetBaseURL(server.URL)

		_, err := checker.CheckNow(context.Background())
		if err == nil {
			t.Fatalf("expected error on 404, got nil")
		}

		status := checker.GetStatus()
		if status.Error == "" {
			t.Fatalf("expected status.Error to be recorded")
		}
	})

	// Subtest 2: Server returns invalid JSON
	t.Run("invalid JSON", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("{invalid-json"))
		}))
		defer server.Close()

		checker := NewChecker("AndreyOkh/ros-fleet", "v2.1.0", logger)
		checker.SetBaseURL(server.URL)

		_, err := checker.CheckNow(context.Background())
		if err == nil {
			t.Fatalf("expected error on invalid JSON, got nil")
		}
	})
}

func TestCheckerStartTicker(t *testing.T) {
	var requestCount atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount.Add(1)
		_ = json.NewEncoder(w).Encode(Release{TagName: "v2.2.0"})
	}))
	defer server.Close()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	checker := NewChecker("AndreyOkh/ros-fleet", "v2.0.0", logger)
	checker.SetBaseURL(server.URL)
	checker.SetCheckInterval(50 * time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())

	checker.Start(ctx)

	// Wait for at least initial check + 1 ticker trigger
	time.Sleep(130 * time.Millisecond)
	cancel()

	// Wait briefly for goroutine to exit
	time.Sleep(30 * time.Millisecond)

	count := requestCount.Load()
	if count < 2 {
		t.Fatalf("expected at least 2 requests (startup + ticker), got %d", count)
	}

	status := checker.GetStatus()
	if !status.UpdateAvailable {
		t.Fatalf("expected UpdateAvailable to be true")
	}
}
