package main

import "testing"

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

func TestBuildOutboxSinkDefaultsToLog(t *testing.T) {
	if buildOutboxSink(config{} /* default */) == nil {
		t.Fatalf("expected sink")
	}
}

func TestBuildModelConfigPrefixesProvider(t *testing.T) {
	cfg := buildModelConfig(config{Provider: "openai", ProviderModel: "gpt-x", ProviderFallbacks: []string{"mock/duraclaw"}})
	if cfg.Primary != "openai/gpt-x" || len(cfg.Fallbacks) != 1 {
		t.Fatalf("cfg=%#v", cfg)
	}
	cfg = buildModelConfig(config{})
	if cfg.Primary != "mock/duraclaw" {
		t.Fatalf("cfg=%#v", cfg)
	}
}
