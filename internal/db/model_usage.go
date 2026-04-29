package db

import (
	"context"
	"fmt"
	"time"
)

type ModelUsage struct {
	CustomerID      string
	AgentInstanceID string
	RunID           string
	ModelCallID     string
	Provider        string
	Model           string
	InputTokens     int
	OutputTokens    int
	TotalTokens     int
	CostMicros      int64
}

func (s *Store) EnforceModelUsageQuota(ctx context.Context, customerID, agentInstanceID string) error {
	limits, err := s.EffectiveRuntimeLimits(ctx, customerID, agentInstanceID)
	if err != nil {
		return err
	}
	checks := []struct {
		kind   string
		limit  int64
		column string
		start  time.Time
	}{
		{kind: "daily_tokens", limit: int64(limits.MaxDailyTokens), column: "total_tokens", start: quotaPeriodStart("daily")},
		{kind: "weekly_tokens", limit: int64(limits.MaxWeeklyTokens), column: "total_tokens", start: quotaPeriodStart("weekly")},
		{kind: "monthly_tokens", limit: int64(limits.MaxMonthlyTokens), column: "total_tokens", start: quotaPeriodStart("monthly")},
		{kind: "daily_model_cost_micros", limit: limits.MaxDailyModelCostMicros, column: "cost_micros", start: quotaPeriodStart("daily")},
		{kind: "weekly_model_cost_micros", limit: limits.MaxWeeklyModelCostMicros, column: "cost_micros", start: quotaPeriodStart("weekly")},
		{kind: "monthly_model_cost_micros", limit: limits.MaxMonthlyModelCostMicros, column: "cost_micros", start: quotaPeriodStart("monthly")},
	}
	for _, check := range checks {
		if check.limit <= 0 {
			continue
		}
		used, err := s.modelUsageSum(ctx, customerID, agentInstanceID, check.column, check.start)
		if err != nil {
			return err
		}
		if used >= check.limit {
			return QuotaExceededError{Kind: check.kind, Limit: check.limit, Count: used + 1}
		}
	}
	return nil
}

func (s *Store) RecordModelUsage(ctx context.Context, usage ModelUsage) error {
	if usage.CustomerID == "" || usage.AgentInstanceID == "" || usage.RunID == "" || usage.ModelCallID == "" {
		return ValidationError{Message: "customer_id, agent_instance_id, run_id, and model_call_id are required"}
	}
	if usage.TotalTokens <= 0 {
		usage.TotalTokens = usage.InputTokens + usage.OutputTokens
	}
	if usage.InputTokens < 0 || usage.OutputTokens < 0 || usage.TotalTokens < 0 || usage.CostMicros < 0 {
		return ValidationError{Message: "model usage values must be non-negative"}
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO model_usage_ledger(customer_id,agent_instance_id,run_id,model_call_id,provider,model,input_tokens,output_tokens,total_tokens,cost_micros)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
		ON CONFLICT (model_call_id) DO UPDATE SET
			input_tokens=EXCLUDED.input_tokens,
			output_tokens=EXCLUDED.output_tokens,
			total_tokens=EXCLUDED.total_tokens,
			cost_micros=EXCLUDED.cost_micros`,
		usage.CustomerID, usage.AgentInstanceID, usage.RunID, usage.ModelCallID, usage.Provider, usage.Model, usage.InputTokens, usage.OutputTokens, usage.TotalTokens, usage.CostMicros)
	return err
}

func (s *Store) modelUsageSum(ctx context.Context, customerID, agentInstanceID, column string, start time.Time) (int64, error) {
	switch column {
	case "total_tokens", "cost_micros":
	default:
		return 0, fmt.Errorf("unsupported model usage column %q", column)
	}
	var used int64
	query := fmt.Sprintf(`
		SELECT COALESCE(sum(%s),0)
		FROM model_usage_ledger
		WHERE customer_id=$1
		AND agent_instance_id=$2
		AND created_at >= $3`, column)
	err := s.pool.QueryRow(ctx, query, customerID, agentInstanceID, start).Scan(&used)
	return used, err
}

func quotaPeriodStart(period string) time.Time {
	now := time.Now().UTC()
	switch period {
	case "daily":
		return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	case "weekly":
		daysSinceMonday := (int(now.Weekday()) + 6) % 7
		day := now.AddDate(0, 0, -daysSinceMonday)
		return time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, time.UTC)
	case "monthly":
		return time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	default:
		return now
	}
}
