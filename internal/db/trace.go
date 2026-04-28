package db

import (
	"context"
	"encoding/json"
	"time"
)

type ModelCallRecord struct {
	ID              string          `json:"id"`
	Provider        string          `json:"provider"`
	Model           string          `json:"model"`
	State           string          `json:"state"`
	RequestSummary  json.RawMessage `json:"request_summary"`
	ResponseSummary json.RawMessage `json:"response_summary"`
	Error           *string         `json:"error,omitempty"`
	CreatedAt       time.Time       `json:"created_at"`
	CompletedAt     *time.Time      `json:"completed_at,omitempty"`
}

type ProcessorCallRecord struct {
	ID              string          `json:"id"`
	ArtifactID      string          `json:"artifact_id"`
	Processor       string          `json:"processor"`
	State           string          `json:"state"`
	RequestSummary  json.RawMessage `json:"request_summary"`
	ResponseSummary json.RawMessage `json:"response_summary"`
	Error           *string         `json:"error,omitempty"`
	CreatedAt       time.Time       `json:"created_at"`
	CompletedAt     *time.Time      `json:"completed_at,omitempty"`
}

type MCPCallRecord struct {
	ID              string          `json:"id"`
	ServerName      string          `json:"server_name"`
	ToolName        string          `json:"tool_name"`
	State           string          `json:"state"`
	RequestSummary  json.RawMessage `json:"request_summary"`
	ResponseSummary json.RawMessage `json:"response_summary"`
	Error           *string         `json:"error,omitempty"`
	CreatedAt       time.Time       `json:"created_at"`
	CompletedAt     *time.Time      `json:"completed_at,omitempty"`
}

type RunTrace struct {
	Steps          []RunStep             `json:"steps"`
	ModelCalls     []ModelCallRecord     `json:"model_calls"`
	ToolCalls      []ToolCallRecord      `json:"tool_calls"`
	ProcessorCalls []ProcessorCallRecord `json:"processor_calls"`
	MCPCalls       []MCPCallRecord       `json:"mcp_calls"`
}

func (s *Store) RunTrace(ctx context.Context, runID string) (*RunTrace, error) {
	steps, err := s.runSteps(ctx, runID)
	if err != nil {
		return nil, err
	}
	models, err := s.modelCalls(ctx, runID)
	if err != nil {
		return nil, err
	}
	tools, err := s.toolCalls(ctx, runID)
	if err != nil {
		return nil, err
	}
	processors, err := s.processorCalls(ctx, runID)
	if err != nil {
		return nil, err
	}
	mcp, err := s.mcpCalls(ctx, runID)
	if err != nil {
		return nil, err
	}
	return &RunTrace{Steps: steps, ModelCalls: models, ToolCalls: tools, ProcessorCalls: processors, MCPCalls: mcp}, nil
}

func (s *Store) runSteps(ctx context.Context, runID string) ([]RunStep, error) {
	rows, err := s.pool.Query(ctx, `SELECT id::text, run_id::text, kind, state, input, output, error, started_at, completed_at, created_at FROM run_steps WHERE run_id=$1 ORDER BY created_at`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RunStep
	for rows.Next() {
		var rec RunStep
		if err := rows.Scan(&rec.ID, &rec.RunID, &rec.Kind, &rec.State, &rec.Input, &rec.Output, &rec.Error, &rec.StartedAt, &rec.CompletedAt, &rec.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

func (s *Store) modelCalls(ctx context.Context, runID string) ([]ModelCallRecord, error) {
	rows, err := s.pool.Query(ctx, `SELECT id::text, provider, model, state, request_summary, response_summary, error, created_at, completed_at FROM model_calls WHERE run_id=$1 ORDER BY created_at`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ModelCallRecord
	for rows.Next() {
		var rec ModelCallRecord
		if err := rows.Scan(&rec.ID, &rec.Provider, &rec.Model, &rec.State, &rec.RequestSummary, &rec.ResponseSummary, &rec.Error, &rec.CreatedAt, &rec.CompletedAt); err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

func (s *Store) toolCalls(ctx context.Context, runID string) ([]ToolCallRecord, error) {
	rows, err := s.pool.Query(ctx, `SELECT id::text, run_id::text, tool_name, state, arguments, result, retryable, error FROM tool_calls WHERE run_id=$1 ORDER BY created_at`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ToolCallRecord
	for rows.Next() {
		var rec ToolCallRecord
		if err := rows.Scan(&rec.ID, &rec.RunID, &rec.ToolName, &rec.State, &rec.Arguments, &rec.Result, &rec.Retryable, &rec.Error); err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

func (s *Store) processorCalls(ctx context.Context, runID string) ([]ProcessorCallRecord, error) {
	rows, err := s.pool.Query(ctx, `SELECT id::text, artifact_id, processor, state, request_summary, response_summary, error, created_at, completed_at FROM processor_calls WHERE run_id=$1 ORDER BY created_at`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ProcessorCallRecord
	for rows.Next() {
		var rec ProcessorCallRecord
		if err := rows.Scan(&rec.ID, &rec.ArtifactID, &rec.Processor, &rec.State, &rec.RequestSummary, &rec.ResponseSummary, &rec.Error, &rec.CreatedAt, &rec.CompletedAt); err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

func (s *Store) mcpCalls(ctx context.Context, runID string) ([]MCPCallRecord, error) {
	rows, err := s.pool.Query(ctx, `SELECT id::text, server_name, tool_name, state, request_summary, response_summary, error, created_at, completed_at FROM mcp_calls WHERE run_id=$1 ORDER BY created_at`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MCPCallRecord
	for rows.Next() {
		var rec MCPCallRecord
		if err := rows.Scan(&rec.ID, &rec.ServerName, &rec.ToolName, &rec.State, &rec.RequestSummary, &rec.ResponseSummary, &rec.Error, &rec.CreatedAt, &rec.CompletedAt); err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}
