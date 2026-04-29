package main

import (
	"os"
	"reflect"
	"strings"
	"testing"

	"duraclaw/internal/artifacts"
	"duraclaw/internal/embeddings"
	"duraclaw/internal/providers"
)

func TestEnvDefault(t *testing.T) {
	t.Setenv("DURACLAW_TEST_VALUE", "set")
	if got := envDefault("DURACLAW_TEST_VALUE", "fallback"); got != "set" {
		t.Fatalf("got %q", got)
	}
	if got := envDefault("DURACLAW_MISSING_VALUE", "fallback"); got != "fallback" {
		t.Fatalf("got %q", got)
	}
}

func TestLoadConfigRequiresDatabaseURL(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	if _, err := loadConfig(); err == nil {
		t.Fatalf("expected DATABASE_URL error")
	}
}

func TestLoadConfigRequiresTokensWhenAuthRequired(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://example")
	t.Setenv("DURACLAW_REQUIRE_AUTH", "true")
	if _, err := loadConfig(); err == nil {
		t.Fatalf("expected auth token error")
	}
	t.Setenv("DURACLAW_ADMIN_TOKEN", "admin")
	t.Setenv("DURACLAW_ACP_TOKEN", "acp")
	if _, err := loadConfig(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoadConfigRejectsInvalidMCPConfig(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://example")
	t.Setenv("DURACLAW_MCP_CONFIG", "{bad")
	if _, err := loadConfig(); err == nil {
		t.Fatalf("expected mcp config error")
	}
}

func TestSplitCSV(t *testing.T) {
	got := splitCSV("a, b ,,c")
	if len(got) != 3 || got[0] != "a" || got[1] != "b" || got[2] != "c" {
		t.Fatalf("got %#v", got)
	}
}

func TestBuildProviderDefaultsToMock(t *testing.T) {
	if _, ok := buildProvider(config{}).(interface{ GetDefaultModel() string }); !ok {
		t.Fatalf("provider does not implement expected interface")
	}
}

func TestBuildProviderSupportsConcreteProviderTypes(t *testing.T) {
	cases := []struct {
		name     string
		cfg      config
		wantType any
	}{
		{name: "openai", cfg: config{Provider: "openai", ProviderAPIKey: "key"}, wantType: providers.OpenAIProvider{}},
		{name: "openrouter", cfg: config{Provider: "openrouter", ProviderAPIKey: "key", ProviderReferer: "https://duraclaw.test", ProviderTitle: "Duraclaw"}, wantType: providers.OpenRouterProvider{}},
		{name: "openai-compatible", cfg: config{Provider: "openai-compatible", ProviderBaseURL: "http://localhost:11434/v1"}, wantType: providers.OpenAICompatibleProvider{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := buildProvider(tc.cfg)
			if reflect.TypeOf(got) != reflect.TypeOf(tc.wantType) {
				t.Fatalf("got %T want %T", got, tc.wantType)
			}
		})
	}
}

func TestBuildOutboxSinkDefaultsToLog(t *testing.T) {
	if buildOutboxSink(config{} /* default */) == nil {
		t.Fatalf("expected sink")
	}
}

func TestBuildArtifactRegistryAddsHTTPProcessor(t *testing.T) {
	registry := buildArtifactRegistry(config{
		ArtifactProcessorURL:        "http://processor.test",
		ArtifactProcessorName:       "ocr",
		ArtifactProcessorModalities: []string{"image"},
	})
	processor, ok := registry.ProcessorFor(artifacts.Artifact{Modality: "image", MediaType: "image/png"})
	if !ok || processor.Name() != "ocr" {
		t.Fatalf("processor=%#v ok=%v", processor, ok)
	}
}

func TestBuildArtifactRegistryAddsProviderProcessor(t *testing.T) {
	registry := buildArtifactRegistry(config{
		ArtifactProcessorProvider:   "openrouter",
		ArtifactProcessorModel:      "openai/gpt-4.1-mini",
		ArtifactProcessorAPIKey:     "key",
		ArtifactProcessorModalities: []string{"image"},
	})
	processor, ok := registry.ProcessorFor(artifacts.Artifact{Modality: "image", MediaType: "image/png", StorageRef: "https://example.test/image.png"})
	if !ok || !strings.Contains(processor.Name(), "openrouter") {
		t.Fatalf("processor=%#v ok=%v", processor, ok)
	}
}

func TestMainWiresProviderRegistryIntoACPHandler(t *testing.T) {
	raw, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(raw)
	for _, want := range []string{
		"providerRegistry := buildProviderRegistry(cfg)",
		"modelConfig := buildModelConfig(cfg)",
		"WithProviders(providerRegistry, modelConfig)",
		"runtime.NewWorkerWithProviders(store, providerRegistry, modelConfig",
	} {
		if !strings.Contains(src, want) {
			t.Fatalf("main missing provider wiring %q", want)
		}
	}
}

func TestBuildMediaBlobStore(t *testing.T) {
	if got := buildMediaBlobStore(config{}); got != nil {
		t.Fatalf("expected nil store")
	}
	got := buildMediaBlobStore(config{GeneratedMediaDir: "/tmp/generated", GeneratedMediaRefPrefix: "object://generated"})
	if got == nil {
		t.Fatalf("expected media blob store")
	}
}

func TestBuildModelConfigPrefixesProvider(t *testing.T) {
	cfg := buildModelConfig(config{Provider: "openai", ProviderModel: "gpt-x", ProviderFallbacks: []string{"mock/duraclaw"}})
	if cfg.Primary != "openai/gpt-x" || len(cfg.Fallbacks) != 1 {
		t.Fatalf("cfg=%#v", cfg)
	}
	cfg = buildModelConfig(config{Provider: "openrouter", ProviderModel: "openai/gpt-4.1-mini"})
	if cfg.Primary != "openrouter/openai/gpt-4.1-mini" {
		t.Fatalf("cfg=%#v", cfg)
	}
	cfg = buildModelConfig(config{Provider: "openai-compatible", ProviderModel: "llama3.1"})
	if cfg.Primary != "openai-compatible/llama3.1" {
		t.Fatalf("cfg=%#v", cfg)
	}
	cfg = buildModelConfig(config{Provider: "openai-compatible", ProviderModel: "meta-llama/llama-3.1"})
	if cfg.Primary != "openai-compatible/meta-llama/llama-3.1" {
		t.Fatalf("cfg=%#v", cfg)
	}
	cfg = buildModelConfig(config{})
	if cfg.Primary != "mock/duraclaw" {
		t.Fatalf("cfg=%#v", cfg)
	}
}

func TestBuildProviderRegistryUsesProviderIdentity(t *testing.T) {
	if got := buildProviderRegistry(config{Provider: "openrouter"}).DefaultProvider(); got != "openrouter" {
		t.Fatalf("default=%s", got)
	}
	if got := buildProviderRegistry(config{Provider: "local"}).DefaultProvider(); got != "openai-compatible" {
		t.Fatalf("default=%s", got)
	}
}

func TestBuildEmbeddingProvider(t *testing.T) {
	if _, ok := buildEmbeddingProvider(config{EmbeddingProvider: "hash", EmbeddingDimensions: 8}).(embeddings.HashProvider); !ok {
		t.Fatalf("expected hash provider")
	}
	if got := buildEmbeddingProvider(config{EmbeddingProvider: "openai", EmbeddingBaseURL: "http://example.test", EmbeddingModel: "embed", EmbeddingDimensions: 768}); got.Dimension() != 768 {
		t.Fatalf("dimension=%d", got.Dimension())
	}
	if _, ok := buildEmbeddingProvider(config{EmbeddingProvider: "openrouter", EmbeddingModel: "openai/text-embedding-3-small"}).(embeddings.OpenRouterProvider); !ok {
		t.Fatalf("expected openrouter embedding provider")
	}
}

func TestBuildMCPManagerFromConfig(t *testing.T) {
	manager := buildMCPManager(config{MCPConfig: []byte(`{"servers":[{"name":"srv","transport":"http","base_url":"http://example.test"}]}`)})
	statuses := manager.Statuses()
	if len(statuses) != 1 || statuses[0].Name != "srv" || statuses[0].Transport != "http" {
		t.Fatalf("statuses=%#v", statuses)
	}
}

func TestEnvInt(t *testing.T) {
	t.Setenv("DURACLAW_TEST_INT", "42")
	if got := envInt("DURACLAW_TEST_INT", 7); got != 42 {
		t.Fatalf("got=%d", got)
	}
	t.Setenv("DURACLAW_TEST_INT", "bad")
	if got := envInt("DURACLAW_TEST_INT", 7); got != 7 {
		t.Fatalf("got=%d", got)
	}
}

func TestParseHeadersAndEnvBool(t *testing.T) {
	got := parseHeaders("Authorization=Bearer abc, X-Test = ok, invalid")
	if got["Authorization"] != "Bearer abc" || got["X-Test"] != "ok" {
		t.Fatalf("headers=%#v", got)
	}
	t.Setenv("DURACLAW_TEST_BOOL", "true")
	if !envBool("DURACLAW_TEST_BOOL", false) {
		t.Fatal("expected true")
	}
	t.Setenv("DURACLAW_TEST_BOOL", "false")
	if envBool("DURACLAW_TEST_BOOL", true) {
		t.Fatal("expected false")
	}
}
