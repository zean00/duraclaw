package outbound

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"duraclaw/internal/db"
)

type HTTPSink struct {
	URL        string
	TopicURLs  map[string]string
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

func (s HTTPSink) URLForTopic(topic string) string {
	if s.TopicURLs != nil {
		if url := strings.TrimSpace(s.TopicURLs[topic]); url != "" {
			return url
		}
	}
	return s.URL
}
