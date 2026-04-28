package main

import (
	"context"
	"log"
	"net/http"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"duraclaw/internal/acp"
	"duraclaw/internal/asyncwrite"
	"duraclaw/internal/db"
	"duraclaw/internal/observability"
	"duraclaw/internal/outbound"
	"duraclaw/internal/providers"
	"duraclaw/internal/runtime"
	"duraclaw/internal/scheduler"
)

func main() {
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
	outboundService := outbound.NewService(store)
	asyncWriter := asyncwrite.NewWriter(store, cfg.Hostname, db.DefaultRuntimeLimits().AsyncBufferSize).WithCounters(counters)
	worker := runtime.NewWorkerWithProviders(store, buildProviderRegistry(cfg), buildModelConfig(cfg), cfg.Hostname).
		WithCounters(counters).
		WithOutbound(outboundService).
		WithAsyncWriter(asyncWriter)
	go func() {
		if err := worker.Loop(ctx, cfg.WorkerInterval); err != nil && err != context.Canceled {
			log.Printf("worker stopped: %v", err)
		}
	}()
	schedulerService := scheduler.NewService(store, cfg.Hostname)
	go func() {
		ticker := time.NewTicker(cfg.SchedulerInterval)
		defer ticker.Stop()
		for {
			if _, err := schedulerService.RunOnce(ctx, time.Now().UTC()); err != nil && ctx.Err() == nil {
				log.Printf("scheduler tick failed: %v", err)
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
			log.Printf("outbox worker stopped: %v", err)
		}
	}()
	go func() {
		if err := asyncWriter.Loop(ctx, cfg.OutboxInterval); err != nil && err != context.Canceled {
			log.Printf("async writer stopped: %v", err)
		}
	}()
	server := &http.Server{
		Addr:              cfg.Addr,
		Handler:           acp.NewHandler(store).WithAdminToken(cfg.AdminToken).WithACPToken(cfg.ACPToken).WithCounters(counters).Routes(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			log.Printf("http shutdown failed: %v", err)
		}
	}()
	log.Printf("duraclaw listening on %s", cfg.Addr)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
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
