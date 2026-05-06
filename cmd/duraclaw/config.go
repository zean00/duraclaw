package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"duraclaw/internal/providers"
)

type config struct {
	Environment                 string
	DatabaseURL                 string
	Addr                        string
	Hostname                    string
	Provider                    string
	Providers                   json.RawMessage
	AllowMockInProduction       bool
	ProviderBaseURL             string
	ProviderAPIKey              string
	ProviderModel               string
	ProviderFallbacks           []string
	ProviderReferer             string
	ProviderTitle               string
	WorkerInterval              time.Duration
	SchedulerInterval           time.Duration
	OutboxInterval              time.Duration
	SessionMonitorInterval      time.Duration
	SessionMonitorIdleFor       time.Duration
	SessionMonitorLimit         int
	SessionMonitorMessageLimit  int
	SessionCompactionThreshold  int
	RunInterruptWindow          time.Duration
	RunMaxRefinementDepth       int
	AgentActivityEnabled        bool
	AgentActivityInclude        []string
	AgentActivityOmit           []string
	OutboxSink                  string
	NexusOutboundURL            string
	NexusOutboundBulkURL        string
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
	GeneratedMediaHTTPPutURL    string
	GeneratedMediaHTTPBaseURL   string
	GeneratedMediaHTTPHeaders   map[string]string
	CustomerProfileURL          string
	CustomerProfileToken        string
	CustomerProfileHeaders      map[string]string
	CustomerProfileTimeout      time.Duration
	CustomerProfilePromptFields []string
}

func loadConfig() (config, error) {
	cfg := config{
		Environment:                 envDefault("DURACLAW_ENV", "development"),
		DatabaseURL:                 os.Getenv("DATABASE_URL"),
		Addr:                        envDefault("ADDR", ":8080"),
		Hostname:                    os.Getenv("HOSTNAME"),
		Provider:                    envDefault("DURACLAW_PROVIDER", "mock"),
		Providers:                   json.RawMessage(os.Getenv("DURACLAW_PROVIDERS")),
		AllowMockInProduction:       envBool("DURACLAW_ALLOW_MOCK_IN_PRODUCTION", false),
		ProviderBaseURL:             os.Getenv("DURACLAW_PROVIDER_BASE_URL"),
		ProviderAPIKey:              os.Getenv("DURACLAW_PROVIDER_API_KEY"),
		ProviderModel:               os.Getenv("DURACLAW_PROVIDER_MODEL"),
		ProviderFallbacks:           splitCSV(os.Getenv("DURACLAW_PROVIDER_FALLBACKS")),
		ProviderReferer:             os.Getenv("DURACLAW_PROVIDER_REFERER"),
		ProviderTitle:               os.Getenv("DURACLAW_PROVIDER_TITLE"),
		WorkerInterval:              time.Second,
		SchedulerInterval:           5 * time.Second,
		OutboxInterval:              2 * time.Second,
		SessionMonitorInterval:      time.Duration(envInt("DURACLAW_SESSION_MONITOR_INTERVAL_SECONDS", 60)) * time.Second,
		SessionMonitorIdleFor:       time.Duration(envInt("DURACLAW_SESSION_MONITOR_IDLE_SECONDS", 1800)) * time.Second,
		SessionMonitorLimit:         envInt("DURACLAW_SESSION_MONITOR_LIMIT", 25),
		SessionMonitorMessageLimit:  envInt("DURACLAW_SESSION_MONITOR_MESSAGE_LIMIT", 40),
		SessionCompactionThreshold:  envInt("DURACLAW_SESSION_COMPACTION_THRESHOLD_CHARS", 12000),
		RunInterruptWindow:          time.Duration(envInt("DURACLAW_RUN_INTERRUPT_WINDOW_MS", 2000)) * time.Millisecond,
		RunMaxRefinementDepth:       envInt("DURACLAW_RUN_MAX_REFINEMENT_DEPTH", 2),
		AgentActivityEnabled:        envBool("DURACLAW_AGENT_ACTIVITY_ENABLED", false),
		AgentActivityInclude:        splitCSV(os.Getenv("DURACLAW_AGENT_ACTIVITY_INCLUDE")),
		AgentActivityOmit:           splitCSV(os.Getenv("DURACLAW_AGENT_ACTIVITY_OMIT")),
		OutboxSink:                  envDefault("DURACLAW_OUTBOX_SINK", "log"),
		NexusOutboundURL:            os.Getenv("NEXUS_OUTBOUND_URL"),
		NexusOutboundBulkURL:        os.Getenv("NEXUS_OUTBOUND_BULK_URL"),
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
		GeneratedMediaHTTPPutURL:    os.Getenv("DURACLAW_GENERATED_MEDIA_HTTP_PUT_URL"),
		GeneratedMediaHTTPBaseURL:   os.Getenv("DURACLAW_GENERATED_MEDIA_HTTP_BASE_URL"),
		GeneratedMediaHTTPHeaders:   parseHeaders(os.Getenv("DURACLAW_GENERATED_MEDIA_HTTP_HEADERS")),
		CustomerProfileURL:          os.Getenv("DURACLAW_CUSTOMER_PROFILE_URL"),
		CustomerProfileToken:        os.Getenv("DURACLAW_CUSTOMER_PROFILE_TOKEN"),
		CustomerProfileHeaders:      parseHeaders(os.Getenv("DURACLAW_CUSTOMER_PROFILE_HEADERS")),
		CustomerProfileTimeout:      time.Duration(envInt("DURACLAW_CUSTOMER_PROFILE_TIMEOUT_SECONDS", 5)) * time.Second,
		CustomerProfilePromptFields: splitCSV(os.Getenv("DURACLAW_CUSTOMER_PROFILE_PROMPT_FIELDS")),
	}
	if cfg.DatabaseURL == "" {
		return cfg, fmt.Errorf("DATABASE_URL is required")
	}
	if cfg.isProduction() && !cfg.RequireAuth {
		return cfg, fmt.Errorf("DURACLAW_REQUIRE_AUTH must be true when DURACLAW_ENV is production")
	}
	if cfg.RequireAuth && (strings.TrimSpace(cfg.AdminToken) == "" || strings.TrimSpace(cfg.ACPToken) == "") {
		return cfg, fmt.Errorf("DURACLAW_ADMIN_TOKEN and DURACLAW_ACP_TOKEN are required when DURACLAW_REQUIRE_AUTH is true")
	}
	providerName := providers.NormalizeProvider(cfg.Provider)
	if cfg.isProduction() && !isProductionProvider(providerName) && !cfg.AllowMockInProduction {
		return cfg, fmt.Errorf("provider %q is not allowed when DURACLAW_ENV is production unless DURACLAW_ALLOW_MOCK_IN_PRODUCTION is true", cfg.Provider)
	}
	if len(cfg.Providers) > 0 {
		configured, err := parseProviderConfigs(cfg.Providers)
		if err != nil {
			return cfg, err
		}
		for name := range configured {
			if !isKnownProvider(name) {
				return cfg, fmt.Errorf("DURACLAW_PROVIDERS contains unsupported provider %q", name)
			}
			if cfg.isProduction() && !isProductionProvider(name) && !cfg.AllowMockInProduction {
				return cfg, fmt.Errorf("provider %q in DURACLAW_PROVIDERS is not allowed when DURACLAW_ENV is production unless DURACLAW_ALLOW_MOCK_IN_PRODUCTION is true", name)
			}
		}
	}
	if len(cfg.MCPConfig) > 0 && !json.Valid(cfg.MCPConfig) {
		return cfg, fmt.Errorf("DURACLAW_MCP_CONFIG must be valid JSON")
	}
	if cfg.ArtifactProcessorRetries < 0 || cfg.ArtifactProcessorMaxBytes <= 0 || cfg.ArtifactProcessorMaxReps <= 0 || cfg.ArtifactProcessorTimeout <= 0 {
		return cfg, fmt.Errorf("artifact processor timeout, max bytes, and max representations must be positive")
	}
	return cfg, nil
}

func (cfg config) isProduction() bool {
	env := strings.ToLower(strings.TrimSpace(cfg.Environment))
	return env == "production" || env == "prod"
}

func isKnownProvider(provider string) bool {
	switch providers.NormalizeProvider(provider) {
	case "mock", "openai", "openrouter", "openai-compatible", "together", "deepseek":
		return true
	default:
		return false
	}
}

func isProductionProvider(provider string) bool {
	switch providers.NormalizeProvider(provider) {
	case "openai", "openrouter", "openai-compatible", "together", "deepseek":
		return true
	default:
		return false
	}
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
