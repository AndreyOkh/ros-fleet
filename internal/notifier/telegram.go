package notifier

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/username/real-mikrotik-backup/internal/config"
)

const (
	telegramMaxMessageLen = 4000 // Telegram hard limit is 4096; keep a safe buffer
	telegramAPIBaseURL    = "https://api.telegram.org"
)

// RouterReport contains the backup result for a single router.
type RouterReport struct {
	Name       string
	Host       string
	Success    bool
	Duration   time.Duration
	RscSize    int64
	BackupSize int64
	Err        error
}

// SummaryReport contains aggregated backup results for an entire run.
type SummaryReport struct {
	StartTime    time.Time
	EndTime      time.Time
	Duration     time.Duration
	TotalRouters int
	SuccessCount int
	FailedCount  int
	Routers      []RouterReport
}

// TelegramNotifier handles sending backup status reports to Telegram.
type TelegramNotifier struct {
	cfg        config.TelegramConfig
	httpClient *http.Client
	logger     *slog.Logger
}

// NewTelegramNotifier creates a new TelegramNotifier.
func NewTelegramNotifier(cfg config.TelegramConfig, logger *slog.Logger) *TelegramNotifier {
	timeout := time.Duration(cfg.TimeoutSec) * time.Second
	if timeout <= 0 {
		timeout = 15 * time.Second
	}

	return &TelegramNotifier{
		cfg: cfg,
		httpClient: &http.Client{
			Timeout: timeout,
		},
		logger: logger.With(slog.String("component", "telegram_notifier")),
	}
}

type sendMessagePayload struct {
	ChatID                string `json:"chat_id"`
	Text                  string `json:"text"`
	ParseMode             string `json:"parse_mode"`
	DisableWebPagePreview bool   `json:"disable_web_page_preview"`
}

type telegramResponse struct {
	OK          bool   `json:"ok"`
	Description string `json:"description,omitempty"`
	ErrorCode   int    `json:"error_code,omitempty"`
}

// SendReport formats and delivers the summary report to the configured Telegram chat.
func (n *TelegramNotifier) SendReport(ctx context.Context, report SummaryReport) error {
	if !n.cfg.Enabled {
		n.logger.Debug("Telegram notifications are disabled")
		return nil
	}

	if !n.cfg.NotifyOnSuccess && report.FailedCount == 0 {
		n.logger.Info("All backups succeeded and notify_on_success is false; skipping Telegram notification")
		return nil
	}

	messages := n.formatReportHTML(report)
	for i, msg := range messages {
		if err := n.sendMessage(ctx, msg); err != nil {
			return fmt.Errorf("failed to send telegram report chunk %d/%d: %w", i+1, len(messages), err)
		}
	}

	n.logger.Info("Telegram report successfully sent",
		slog.Int("total_routers", report.TotalRouters),
		slog.Int("failed_count", report.FailedCount),
	)
	return nil
}

// sendMessage sends a single message chunk to Telegram API.
func (n *TelegramNotifier) sendMessage(ctx context.Context, text string) error {
	url := fmt.Sprintf("%s/bot%s/sendMessage", telegramAPIBaseURL, n.cfg.BotToken)

	payload := sendMessagePayload{
		ChatID:                n.cfg.ChatID,
		Text:                  text,
		ParseMode:             "HTML",
		DisableWebPagePreview: true,
	}

	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal telegram payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return fmt.Errorf("failed to create telegram HTTP request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := n.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("http request to telegram API failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)

	var tgResp telegramResponse
	if jsonErr := json.Unmarshal(respBody, &tgResp); jsonErr == nil && !tgResp.OK {
		return fmt.Errorf("telegram API error (code %d): %s", tgResp.ErrorCode, tgResp.Description)
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("telegram API returned non-200 status %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}

	return nil
}

// formatReportHTML generates HTML-formatted message chunks.
func (n *TelegramNotifier) formatReportHTML(report SummaryReport) []string {
	var icon string
	if report.FailedCount == 0 {
		icon = "✅"
	} else if report.SuccessCount == 0 {
		icon = "🚨"
	} else {
		icon = "⚠️"
	}

	durationStr := report.Duration.Round(time.Millisecond * 100).String()

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("%s <b>MikroTik Backup Report</b>\n\n", icon))
	sb.WriteString(fmt.Sprintf("⏱ <b>Duration:</b> %s\n", durationStr))
	sb.WriteString(fmt.Sprintf("📊 <b>Summary:</b> %d total | ✅ %d success | ❌ %d failed\n\n",
		report.TotalRouters, report.SuccessCount, report.FailedCount))

	var failedReports []RouterReport
	var successReports []RouterReport
	for _, r := range report.Routers {
		if r.Success {
			successReports = append(successReports, r)
		} else {
			failedReports = append(failedReports, r)
		}
	}

	if len(failedReports) > 0 {
		sb.WriteString("❌ <b>Failed Routers:</b>\n")
		for _, r := range failedReports {
			errText := "unknown error"
			if r.Err != nil {
				errText = r.Err.Error()
			}
			sb.WriteString(fmt.Sprintf("• <b>%s</b> (%s, %s)\n  └ ⚠️ <code>%s</code>\n",
				html.EscapeString(r.Name),
				html.EscapeString(r.Host),
				r.Duration.Round(time.Millisecond*100),
				html.EscapeString(errText),
			))
		}
		sb.WriteString("\n")
	}

	if len(successReports) > 0 {
		sb.WriteString("✅ <b>Successful Routers:</b>\n")
		for _, r := range successReports {
			sb.WriteString(fmt.Sprintf("• <b>%s</b> (%s) — %s (RSC: %s, Backup: %s)\n",
				html.EscapeString(r.Name),
				html.EscapeString(r.Host),
				r.Duration.Round(time.Millisecond*100),
				formatBytes(r.RscSize),
				formatBytes(r.BackupSize),
			))
		}
	}

	fullText := sb.String()
	if len(fullText) <= telegramMaxMessageLen {
		return []string{fullText}
	}

	return splitMessage(fullText, telegramMaxMessageLen)
}

// formatBytes returns human-readable file size string.
func formatBytes(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(bytes)/float64(div), "KMGTPE"[exp])
}

// splitMessage splits text into chunks if it exceeds Telegram's size limit.
func splitMessage(text string, maxLen int) []string {
	var chunks []string
	lines := strings.Split(text, "\n")
	var current strings.Builder

	for _, line := range lines {
		if current.Len()+len(line)+1 > maxLen {
			if current.Len() > 0 {
				chunks = append(chunks, current.String())
				current.Reset()
			}
		}
		if current.Len() > 0 {
			current.WriteString("\n")
		}
		current.WriteString(line)
	}

	if current.Len() > 0 {
		chunks = append(chunks, current.String())
	}

	return chunks
}
