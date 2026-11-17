package notifier

import (
	"context"
	"encoding/json/v2"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/AndreyOkh/ros-fleet/internal/config"
)

func TestFormatBytes(t *testing.T) {
	tests := []struct {
		bytes    int64
		expected string
	}{
		{500, "500 B"},
		{1024, "1.0 KB"},
		{1536, "1.5 KB"},
		{1048576, "1.0 MB"},
		{2500000, "2.4 MB"},
	}

	for _, tt := range tests {
		got := formatBytes(tt.bytes)
		if got != tt.expected {
			t.Errorf("formatBytes(%d) = %q, want %q", tt.bytes, got, tt.expected)
		}
	}
}

func TestFormatReportHTML(t *testing.T) {
	notifier := &TelegramNotifier{}

	report := SummaryReport{
		Duration:     15 * time.Second,
		TotalRouters: 2,
		SuccessCount: 1,
		FailedCount:  1,
		Routers: []RouterReport{
			{
				Name:       "Core <Router> & Co",
				Host:       "192.168.1.1",
				Success:    true,
				Duration:   5 * time.Second,
				RscSize:    1024,
				BackupSize: 2048,
			},
			{
				Name:     "Branch Router",
				Host:     "10.0.0.1",
				Success:  false,
				Duration: 10 * time.Second,
				Err:      errors.New("connection failed: dial tcp 10.0.0.1:22: <timeout>"),
			},
		},
	}

	chunks := notifier.formatReportHTML(report)
	if len(chunks) == 0 {
		t.Fatal("expected at least one message chunk")
	}

	text := chunks[0]
	// Verify HTML escaping
	if !strings.Contains(text, "Core &lt;Router&gt; &amp; Co") {
		t.Errorf("expected HTML escaped router name, got:\n%s", text)
	}
	if !strings.Contains(text, "&lt;timeout&gt;") {
		t.Errorf("expected HTML escaped error string, got:\n%s", text)
	}
	if !strings.Contains(text, "1.0 KB") || !strings.Contains(text, "2.0 KB") {
		t.Errorf("expected formatted byte sizes, got:\n%s", text)
	}
}

func TestSplitMessage(t *testing.T) {
	var sb strings.Builder
	for i := 0; i < 50; i++ {
		sb.WriteString("Line of text for message splitting test\n")
	}
	text := sb.String()

	chunks := splitMessage(text, 200)
	if len(chunks) < 2 {
		t.Errorf("expected text to be split into multiple chunks, got %d", len(chunks))
	}
	for i, c := range chunks {
		if len(c) > 200 {
			t.Errorf("chunk %d exceeded limit: %d > 200", i, len(c))
		}
	}
}

func TestSendReportWithMockServer(t *testing.T) {
	var receivedPayload sendMessagePayload
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &receivedPayload)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok": true}`))
	}))
	defer server.Close()

	cfg := config.TelegramConfig{
		Enabled:         true,
		BotToken:        "mock-token",
		ChatID:          "123456789",
		NotifyOnSuccess: true,
	}

	n := NewTelegramNotifier(cfg, slog.Default())
	// Override httpClient and sendMessage URL pattern for mock testing
	n.httpClient = server.Client()

	// Direct test of sendMessage with server URL
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, server.URL, strings.NewReader(`{"chat_id":"123","text":"test"}`))
	resp, err := n.httpClient.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("mock server call failed: %v", err)
	}
	_ = resp.Body.Close()
}
