package main

import (
	"fmt"
	"os"
	"strings"
	"time"
)

type config struct {
	DatabaseURL       string
	Addr              string
	Hostname          string
	Provider          string
	ProviderBaseURL   string
	ProviderAPIKey    string
	ProviderModel     string
	ProviderFallbacks []string
	WorkerInterval    time.Duration
	SchedulerInterval time.Duration
	OutboxInterval    time.Duration
	OutboxSink        string
	NexusOutboundURL  string
	NexusToken        string
	AdminToken        string
	ACPToken          string
}

func loadConfig() (config, error) {
	cfg := config{
		DatabaseURL:       os.Getenv("DATABASE_URL"),
		Addr:              envDefault("ADDR", ":8080"),
		Hostname:          os.Getenv("HOSTNAME"),
		Provider:          envDefault("DURACLAW_PROVIDER", "mock"),
		ProviderBaseURL:   os.Getenv("DURACLAW_PROVIDER_BASE_URL"),
		ProviderAPIKey:    os.Getenv("DURACLAW_PROVIDER_API_KEY"),
		ProviderModel:     os.Getenv("DURACLAW_PROVIDER_MODEL"),
		ProviderFallbacks: splitCSV(os.Getenv("DURACLAW_PROVIDER_FALLBACKS")),
		WorkerInterval:    time.Second,
		SchedulerInterval: 5 * time.Second,
		OutboxInterval:    2 * time.Second,
		OutboxSink:        envDefault("DURACLAW_OUTBOX_SINK", "log"),
		NexusOutboundURL:  os.Getenv("NEXUS_OUTBOUND_URL"),
		NexusToken:        os.Getenv("NEXUS_TOKEN"),
		AdminToken:        os.Getenv("DURACLAW_ADMIN_TOKEN"),
		ACPToken:          os.Getenv("DURACLAW_ACP_TOKEN"),
	}
	if cfg.DatabaseURL == "" {
		return cfg, fmt.Errorf("DATABASE_URL is required")
	}
	return cfg, nil
}

func splitCSV(raw string) []string {
	var out []string
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func envDefault(key, fallback string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return fallback
}
