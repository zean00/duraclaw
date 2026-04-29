package outbound

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"duraclaw/internal/db"
)

type HTTPSink struct {
	URL        string
	BatchURL   string
	TopicURLs  map[string]string
	BatchURLs  map[string]string
	Token      string
	HTTPClient *http.Client
}

func (s HTTPSink) Handle(ctx context.Context, item db.OutboxItem) error {
	url := s.URLForTopic(item.Topic)
	if strings.TrimSpace(url) == "" {
		return fmt.Errorf("http sink URL is required")
	}
	client := s.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(item.Payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Duraclaw-Outbox-ID", fmt.Sprintf("%d", item.ID))
	req.Header.Set("X-Duraclaw-Outbox-Topic", item.Topic)
	if s.Token != "" {
		req.Header.Set("Authorization", "Bearer "+s.Token)
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("http sink status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

func (s HTTPSink) HandleBatch(ctx context.Context, topic string, items []db.OutboxItem) error {
	if len(items) == 0 {
		return nil
	}
	url := s.BatchURLForTopic(topic)
	if strings.TrimSpace(url) == "" {
		return fmt.Errorf("http batch sink URL is required")
	}
	payload := map[string]any{
		"topic": topic,
		"items": batchItems(items),
	}
	body, _ := json.Marshal(payload)
	client := s.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Duraclaw-Outbox-Topic", topic)
	req.Header.Set("X-Duraclaw-Outbox-Batch-Size", fmt.Sprintf("%d", len(items)))
	if s.Token != "" {
		req.Header.Set("Authorization", "Bearer "+s.Token)
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("http batch sink status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

func (s HTTPSink) SupportsBatch(topic string) bool {
	return strings.TrimSpace(s.BatchURLForTopic(topic)) != ""
}

func batchItems(items []db.OutboxItem) []map[string]any {
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		var payload any
		if len(item.Payload) > 0 {
			payload = json.RawMessage(item.Payload)
		} else {
			payload = map[string]any{}
		}
		out = append(out, map[string]any{
			"outbox_id": item.ID,
			"topic":     item.Topic,
			"payload":   payload,
		})
	}
	return out
}

func (s HTTPSink) URLForTopic(topic string) string {
	if s.TopicURLs != nil {
		if url := strings.TrimSpace(s.TopicURLs[topic]); url != "" {
			return url
		}
	}
	return s.URL
}

func (s HTTPSink) BatchURLForTopic(topic string) string {
	if s.BatchURLs != nil {
		if url := strings.TrimSpace(s.BatchURLs[topic]); url != "" {
			return url
		}
	}
	return s.BatchURL
}
