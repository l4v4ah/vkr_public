package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"go.uber.org/zap"
)

type alerter struct {
	token  string
	chatID string
	log    *zap.Logger
}

func newAlerter(token, chatID string, log *zap.Logger) *alerter {
	return &alerter{token: token, chatID: chatID, log: log}
}

func (a *alerter) enabled() bool {
	return a.token != "" && a.chatID != ""
}

// send posts a message to Telegram. Silently does nothing if not configured.
func (a *alerter) send(ctx context.Context, level, message string) {
	if !a.enabled() {
		return
	}

	icon := "⚠️"
	if level == "error" {
		icon = "🔴"
	}

	text := fmt.Sprintf("%s *[%s]* `%s`\n🖥 Host: `%s`\n🕐 %s",
		icon, level, message, hostname(),
		time.Now().Format("2006-01-02 15:04:05"),
	)

	body, _ := json.Marshal(map[string]string{
		"chat_id":    a.chatID,
		"text":       text,
		"parse_mode": "Markdown",
	})

	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", a.token)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		a.log.Warn("telegram alert failed", zap.Error(err))
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		a.log.Warn("telegram alert rejected", zap.Int("status", resp.StatusCode), zap.String("body", string(raw)))
		return
	}
	a.log.Info("telegram alert sent", zap.String("level", level))
}
