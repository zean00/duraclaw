package main

import (
	"context"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"duraclaw/internal/acp"
	"duraclaw/internal/artifacts"
	"duraclaw/internal/asyncwrite"
	"duraclaw/internal/db"
	"duraclaw/internal/embeddings"
	"duraclaw/internal/mcp"
	"duraclaw/internal/observability"
	"duraclaw/internal/outbound"
	"duraclaw/internal/providers"
	"duraclaw/internal/runtime"
	"duraclaw/internal/scheduler"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	cfg, err := loadConfig()
	if err != nil {
		log.Fatal(err)
	}
	pool, err := db.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatal(err)
	}
	defer pool.Close()
	if err := db.Migrate(ctx, pool); err != nil {
		log.Fatal(err)
	}
	store := db.NewStore(pool)
	counters := observability.NewCounters()
	otelRuntime, err := observability.InitOTel(ctx, observability.OTelConfig{
		Endpoint: cfg.OTLPEndpoint, Headers: cfg.OTLPHeaders, ServiceName: cfg.OTelServiceName, ExportInterval: cfg.OTelExportInterval, Insecure: cfg.OTelInsecure,
	})
	if err != nil {
		log.Fatal(err)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := otelRuntime.Shutdown(shutdownCtx); err != nil {
			logger.ErrorContext(shutdownCtx, "otel shutdown failed", "error", err)
		}
	}()
	otlp := observability.OTLPExporter{}
	embedder := buildEmbeddingProvider(cfg)
	outboundService := outbound.NewService(store)
	mcpManager := buildMCPManager(cfg)
	asyncWriter := asyncwrite.NewWriter(store, cfg.Hostname, db.DefaultRuntimeLimits().AsyncBufferSize).WithCounters(counters).WithOTLPExporter(otlp)
	worker := runtime.NewWorkerWithProviders(store, buildProviderRegistry(cfg), buildModelConfig(cfg), cfg.Hostname).
		WithCounters(counters).
		WithOTLPExporter(otlp).
		WithOutbound(outboundService).
		WithAsyncWriter(asyncWriter).
		WithProcessors(buildArtifactRegistry(cfg)).
		WithEmbedder(embedder)
	worker.SetMCPManager(mcpManager)
	go func() {
		if err := worker.Loop(ctx, cfg.WorkerInterval); err != nil && err != context.Canceled {
			logger.ErrorContext(ctx, "worker stopped", "error", err)
		}
	}()
	schedulerService := scheduler.NewService(store, cfg.Hostname)
	go func() {
		ticker := time.NewTicker(cfg.SchedulerInterval)
		defer ticker.Stop()
		for {
			if _, err := schedulerService.RunOnce(ctx, time.Now().UTC()); err != nil && ctx.Err() == nil {
				logger.ErrorContext(ctx, "scheduler tick failed", "error", err)
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	outboxWorker := outbound.NewOutboxWorker(store, buildOutboxSink(cfg), cfg.Hostname).WithCounters(counters)
	go func() {
		if err := outboxWorker.Loop(ctx, cfg.OutboxInterval); err != nil && err != context.Canceled {
			logger.ErrorContext(ctx, "outbox worker stopped", "error", err)
		}
	}()
	go func() {
		if err := asyncWriter.Loop(ctx, cfg.OutboxInterval); err != nil && err != context.Canceled {
			logger.ErrorContext(ctx, "async writer stopped", "error", err)
		}
	}()
	server := &http.Server{
		Addr:              cfg.Addr,
		Handler:           observability.InstrumentHTTP(acp.NewHandler(store).WithAdminToken(cfg.AdminToken).WithACPToken(cfg.ACPToken).WithRequireAuth(cfg.RequireAuth).WithCounters(counters).WithEmbedder(embedder).WithMCPManager(mcpManager).WithLogger(logger).Routes()),
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			logger.ErrorContext(shutdownCtx, "http shutdown failed", "error", err)
		}
	}()
	logger.InfoContext(ctx, "duraclaw listening", "addr", cfg.Addr)
	_, startedSpan := observability.StartSpan(ctx, "duraclaw.started")
	startedSpan.End()
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}

func exportOTLPMetrics(ctx context.Context, exporter observability.OTLPExporter, counters *observability.Counters, every time.Duration, logger *slog.Logger) {
	if !exporter.Enabled() {
		return
	}
	if every <= 0 {
		every = 10 * time.Second
	}
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := exporter.ExportCounters(ctx, counters); err != nil && logger != nil && ctx.Err() == nil {
				logger.ErrorContext(ctx, "otlp metrics export failed", "error", err)
			}
		}
	}
}

func buildOutboxSink(cfg config) outbound.Sink {
	switch cfg.OutboxSink {
	case "http", "nexus":
		return outbound.HTTPSink{URL: cfg.NexusOutboundURL, Token: cfg.NexusToken}
	default:
		return outbound.LogSink{}
	}
}

func buildProvider(cfg config) providers.LLMProvider {
	switch cfg.Provider {
	case "openai-compatible", "openai":
		return providers.OpenAICompatibleProvider{
			BaseURL:      cfg.ProviderBaseURL,
			APIKey:       cfg.ProviderAPIKey,
			DefaultModel: cfg.ProviderModel,
		}
	default:
		return providers.MockProvider{}
	}
}

func buildMCPManager(cfg config) *mcp.Manager {
	manager := mcp.NewManager()
	if len(cfg.MCPConfig) == 0 {
		return manager
	}
	next, err := manager.WithConfig(cfg.MCPConfig)
	if err != nil {
		slog.Default().Error("mcp config ignored", "error", err)
		return manager
	}
	return next
}

func buildArtifactRegistry(cfg config) *artifacts.Registry {
	registry := artifacts.NewRegistry()
	if strings.TrimSpace(cfg.ArtifactProcessorURL) != "" {
		registry.Register(artifacts.HTTPProcessor{
			NameValue:          cfg.ArtifactProcessorName,
			BaseURL:            cfg.ArtifactProcessorURL,
			Token:              cfg.ArtifactProcessorToken,
			Modalities:         stringSet(cfg.ArtifactProcessorModalities),
			MediaTypes:         stringSet(cfg.ArtifactProcessorMediaTypes),
			Timeout:            cfg.ArtifactProcessorTimeout,
			MaxResponseBytes:   cfg.ArtifactProcessorMaxBytes,
			MaxRepresentations: cfg.ArtifactProcessorMaxReps,
			RawMediaAllowed:    cfg.ArtifactProcessorRawMedia,
			DegradeOnOversize:  true,
			MaxRetries:         cfg.ArtifactProcessorRetries,
		})
	}
	registry.Register(artifacts.MockProcessor{})
	return registry
}

func buildEmbeddingProvider(cfg config) embeddings.Provider {
	switch cfg.EmbeddingProvider {
	case "openai-compatible", "openai":
		return embeddings.OpenAICompatibleProvider{
			BaseURL:    cfg.EmbeddingBaseURL,
			APIKey:     cfg.EmbeddingAPIKey,
			Model:      cfg.EmbeddingModel,
			Dimensions: cfg.EmbeddingDimensions,
		}
	default:
		return embeddings.NewHashProvider(cfg.EmbeddingDimensions)
	}
}

func stringSet(values []string) map[string]bool {
	if len(values) == 0 {
		return nil
	}
	out := map[string]bool{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			out[value] = true
		}
	}
	return out
}

func buildProviderRegistry(cfg config) *providers.Registry {
	defaultProvider := "mock"
	if cfg.Provider == "openai-compatible" || cfg.Provider == "openai" {
		defaultProvider = "openai"
	}
	registry := providers.NewRegistry(defaultProvider)
	registry.Register("mock", providers.MockProvider{})
	registry.Register(defaultProvider, buildProvider(cfg))
	return registry
}

func buildModelConfig(cfg config) providers.ModelConfig {
	primary := cfg.ProviderModel
	if primary == "" {
		switch cfg.Provider {
		case "openai-compatible", "openai":
			primary = "openai/" + providers.OpenAICompatibleProvider{DefaultModel: cfg.ProviderModel}.GetDefaultModel()
		default:
			primary = "mock/duraclaw"
		}
	} else if providers.ParseModelRef(primary, "") == nil || !strings.Contains(primary, "/") {
		switch cfg.Provider {
		case "openai-compatible", "openai":
			primary = "openai/" + primary
		default:
			primary = "mock/" + primary
		}
	}
	return providers.ModelConfig{Primary: primary, Fallbacks: cfg.ProviderFallbacks}
}
