package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
)

type config struct {
	DatabaseURL                 string
	Addr                        string
	Hostname                    string
	Provider                    string
	ProviderBaseURL             string
	ProviderAPIKey              string
	ProviderModel               string
	ProviderFallbacks           []string
	ProviderReferer             string
	ProviderTitle               string
	WorkerInterval              time.Duration
	SchedulerInterval           time.Duration
	OutboxInterval              time.Duration
	OutboxSink                  string
	NexusOutboundURL            string
	NexusToken                  string
	AdminToken                  string
	ACPToken                    string
	RequireAuth                 bool
	OTLPEndpoint                string
	OTLPHeaders                 map[string]string
	OTelServiceName             string
	OTelExportInterval          time.Duration
	OTelInsecure                bool
	MCPConfig                   json.RawMessage
	ArtifactProcessorProvider   string
	ArtifactProcessorModel      string
	ArtifactProcessorBaseURL    string
	ArtifactProcessorAPIKey     string
	ArtifactProcessorURL        string
	ArtifactProcessorToken      string
	ArtifactProcessorName       string
	ArtifactProcessorModalities []string
	ArtifactProcessorMediaTypes []string
	ArtifactProcessorTimeout    time.Duration
	ArtifactProcessorMaxBytes   int
	ArtifactProcessorMaxReps    int
	ArtifactProcessorRawMedia   bool
	ArtifactProcessorRetries    int
	EmbeddingProvider           string
	EmbeddingBaseURL            string
	EmbeddingAPIKey             string
	EmbeddingModel              string
	EmbeddingDimensions         int
	GeneratedMediaDir           string
	GeneratedMediaRefPrefix     string
}

func loadConfig() (config, error) {
	cfg := config{
		DatabaseURL:                 os.Getenv("DATABASE_URL"),
		Addr:                        envDefault("ADDR", ":8080"),
		Hostname:                    os.Getenv("HOSTNAME"),
		Provider:                    envDefault("DURACLAW_PROVIDER", "mock"),
		ProviderBaseURL:             os.Getenv("DURACLAW_PROVIDER_BASE_URL"),
		ProviderAPIKey:              os.Getenv("DURACLAW_PROVIDER_API_KEY"),
		ProviderModel:               os.Getenv("DURACLAW_PROVIDER_MODEL"),
		ProviderFallbacks:           splitCSV(os.Getenv("DURACLAW_PROVIDER_FALLBACKS")),
		ProviderReferer:             os.Getenv("DURACLAW_PROVIDER_REFERER"),
		ProviderTitle:               os.Getenv("DURACLAW_PROVIDER_TITLE"),
		WorkerInterval:              time.Second,
		SchedulerInterval:           5 * time.Second,
		OutboxInterval:              2 * time.Second,
		OutboxSink:                  envDefault("DURACLAW_OUTBOX_SINK", "log"),
		NexusOutboundURL:            os.Getenv("NEXUS_OUTBOUND_URL"),
		NexusToken:                  os.Getenv("NEXUS_TOKEN"),
		AdminToken:                  os.Getenv("DURACLAW_ADMIN_TOKEN"),
		ACPToken:                    os.Getenv("DURACLAW_ACP_TOKEN"),
		RequireAuth:                 envBool("DURACLAW_REQUIRE_AUTH", false),
		OTLPEndpoint:                os.Getenv("DURACLAW_OTLP_ENDPOINT"),
		OTLPHeaders:                 parseHeaders(os.Getenv("DURACLAW_OTLP_HEADERS")),
		OTelServiceName:             envDefault("DURACLAW_OTEL_SERVICE_NAME", "duraclaw"),
		OTelExportInterval:          time.Duration(envInt("DURACLAW_OTEL_EXPORT_INTERVAL_SECONDS", 10)) * time.Second,
		OTelInsecure:                envBool("DURACLAW_OTEL_INSECURE", false),
		MCPConfig:                   json.RawMessage(os.Getenv("DURACLAW_MCP_CONFIG")),
		ArtifactProcessorProvider:   os.Getenv("DURACLAW_ARTIFACT_PROCESSOR_PROVIDER"),
		ArtifactProcessorModel:      os.Getenv("DURACLAW_ARTIFACT_PROCESSOR_MODEL"),
		ArtifactProcessorBaseURL:    os.Getenv("DURACLAW_ARTIFACT_PROCESSOR_BASE_URL"),
		ArtifactProcessorAPIKey:     os.Getenv("DURACLAW_ARTIFACT_PROCESSOR_API_KEY"),
		ArtifactProcessorURL:        os.Getenv("DURACLAW_ARTIFACT_PROCESSOR_URL"),
		ArtifactProcessorToken:      os.Getenv("DURACLAW_ARTIFACT_PROCESSOR_TOKEN"),
		ArtifactProcessorName:       envDefault("DURACLAW_ARTIFACT_PROCESSOR_NAME", "http_processor"),
		ArtifactProcessorModalities: splitCSV(os.Getenv("DURACLAW_ARTIFACT_PROCESSOR_MODALITIES")),
		ArtifactProcessorMediaTypes: splitCSV(os.Getenv("DURACLAW_ARTIFACT_PROCESSOR_MEDIA_TYPES")),
		ArtifactProcessorTimeout:    time.Duration(envInt("DURACLAW_ARTIFACT_PROCESSOR_TIMEOUT_SECONDS", 60)) * time.Second,
		ArtifactProcessorMaxBytes:   envInt("DURACLAW_ARTIFACT_PROCESSOR_MAX_RESPONSE_BYTES", 1<<20),
		ArtifactProcessorMaxReps:    envInt("DURACLAW_ARTIFACT_PROCESSOR_MAX_REPRESENTATIONS", 16),
		ArtifactProcessorRawMedia:   envBool("DURACLAW_ARTIFACT_PROCESSOR_RAW_MEDIA_ALLOWED", false),
		ArtifactProcessorRetries:    envInt("DURACLAW_ARTIFACT_PROCESSOR_MAX_RETRIES", 0),
		EmbeddingProvider:           envDefault("DURACLAW_EMBEDDING_PROVIDER", "hash"),
		EmbeddingBaseURL:            os.Getenv("DURACLAW_EMBEDDING_BASE_URL"),
		EmbeddingAPIKey:             os.Getenv("DURACLAW_EMBEDDING_API_KEY"),
		EmbeddingModel:              os.Getenv("DURACLAW_EMBEDDING_MODEL"),
		EmbeddingDimensions:         envInt("DURACLAW_EMBEDDING_DIMENSIONS", 768),
		GeneratedMediaDir:           os.Getenv("DURACLAW_GENERATED_MEDIA_DIR"),
		GeneratedMediaRefPrefix:     os.Getenv("DURACLAW_GENERATED_MEDIA_REF_PREFIX"),
	}
	if cfg.DatabaseURL == "" {
		return cfg, fmt.Errorf("DATABASE_URL is required")
	}
	if cfg.RequireAuth && (strings.TrimSpace(cfg.AdminToken) == "" || strings.TrimSpace(cfg.ACPToken) == "") {
		return cfg, fmt.Errorf("DURACLAW_ADMIN_TOKEN and DURACLAW_ACP_TOKEN are required when DURACLAW_REQUIRE_AUTH is true")
	}
	if len(cfg.MCPConfig) > 0 && !json.Valid(cfg.MCPConfig) {
		return cfg, fmt.Errorf("DURACLAW_MCP_CONFIG must be valid JSON")
	}
	if cfg.ArtifactProcessorRetries < 0 || cfg.ArtifactProcessorMaxBytes <= 0 || cfg.ArtifactProcessorMaxReps <= 0 || cfg.ArtifactProcessorTimeout <= 0 {
		return cfg, fmt.Errorf("artifact processor timeout, max bytes, and max representations must be positive")
	}
	return cfg, nil
}

func envBool(key string, fallback bool) bool {
	raw := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	switch raw {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return fallback
	}
}

func envInt(key string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	var out int
	if _, err := fmt.Sscanf(raw, "%d", &out); err != nil || out <= 0 {
		return fallback
	}
	return out
}

func parseHeaders(raw string) map[string]string {
	out := map[string]string{}
	for _, part := range strings.Split(raw, ",") {
		key, value, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key != "" && value != "" {
			out[key] = value
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
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
