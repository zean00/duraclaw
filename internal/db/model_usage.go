package db

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

type ModelUsage struct {
	CustomerID      string
	UserID          string
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

type ModelUsageSummary struct {
	CustomerID      string    `json:"customer_id"`
	UserID          string    `json:"user_id,omitempty"`
	AgentInstanceID string    `json:"agent_instance_id,omitempty"`
	Period          string    `json:"period"`
	PeriodStart     time.Time `json:"period_start"`
	InputTokens     int64     `json:"input_tokens"`
	OutputTokens    int64     `json:"output_tokens"`
	TotalTokens     int64     `json:"total_tokens"`
	CostMicros      int64     `json:"cost_micros"`
}

func (s *Store) EnforceModelUsageQuota(ctx context.Context, customerID, agentInstanceID, userID string) error {
	if customer, err := s.CustomerRuntimeLimits(ctx, customerID); err == nil {
		if err := s.enforceModelUsageQuotaForLimits(ctx, customerID, "", "", customer); err != nil {
			return err
		}
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if agentInstanceID != "" {
		if agent, err := s.AgentInstanceRuntimeLimits(ctx, customerID, agentInstanceID); err == nil {
			if err := s.enforceModelUsageQuotaForLimits(ctx, customerID, agentInstanceID, "", agent); err != nil {
				return err
			}
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
	}
	if userID != "" {
		if user, err := s.UserRuntimeLimits(ctx, customerID, userID); err == nil {
			if err := s.enforceModelUsageQuotaForLimits(ctx, customerID, "", userID, user); err != nil {
				return err
			}
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
	}
	return nil
}

func (s *Store) enforceModelUsageQuotaForLimits(ctx context.Context, customerID, agentInstanceID, userID string, configured *RuntimeLimits) error {
	limits := DefaultRuntimeLimits()
	applyRuntimeLimits(&limits, configured)
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
		used, err := s.modelUsageSum(ctx, customerID, agentInstanceID, userID, check.column, check.start)
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
	if usage.CustomerID == "" || usage.RunID == "" || usage.ModelCallID == "" {
		return ValidationError{Message: "customer_id, run_id, and model_call_id are required"}
	}
	if usage.TotalTokens <= 0 {
		usage.TotalTokens = usage.InputTokens + usage.OutputTokens
	}
	if usage.InputTokens < 0 || usage.OutputTokens < 0 || usage.TotalTokens < 0 || usage.CostMicros < 0 {
		return ValidationError{Message: "model usage values must be non-negative"}
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO model_usage_ledger(customer_id,user_id,agent_instance_id,run_id,model_call_id,provider,model,input_tokens,output_tokens,total_tokens,cost_micros)
		SELECT $1,user_id,agent_instance_id,$2,$3,$4,$5,$6,$7,$8,$9
		FROM runs
		WHERE id=$2
		AND customer_id=$1
		AND EXISTS (
			SELECT 1
			FROM model_calls
			WHERE id=$3
			AND run_id=$2
			AND state='succeeded'
		)
		ON CONFLICT (model_call_id) DO UPDATE SET
			user_id=EXCLUDED.user_id,
			input_tokens=EXCLUDED.input_tokens,
			output_tokens=EXCLUDED.output_tokens,
			total_tokens=EXCLUDED.total_tokens,
			cost_micros=EXCLUDED.cost_micros`,
		usage.CustomerID, usage.RunID, usage.ModelCallID, usage.Provider, usage.Model, usage.InputTokens, usage.OutputTokens, usage.TotalTokens, usage.CostMicros)
	return err
}

func (s *Store) modelUsageSum(ctx context.Context, customerID, agentInstanceID, userID, column string, start time.Time) (int64, error) {
	switch column {
	case "total_tokens", "cost_micros":
	default:
		return 0, fmt.Errorf("unsupported model usage column %q", column)
	}
	var used int64
	if agentInstanceID == "" && userID == "" {
		query := fmt.Sprintf(`
			SELECT COALESCE(sum(%s),0)
			FROM model_usage_ledger
			WHERE customer_id=$1
			AND created_at >= $2`, column)
		err := s.pool.QueryRow(ctx, query, customerID, start).Scan(&used)
		return used, err
	}
	if userID != "" {
		query := fmt.Sprintf(`
			SELECT COALESCE(sum(%s),0)
			FROM model_usage_ledger
			WHERE customer_id=$1
			AND user_id=$2
			AND created_at >= $3`, column)
		err := s.pool.QueryRow(ctx, query, customerID, userID, start).Scan(&used)
		return used, err
	}
	query := fmt.Sprintf(`
		SELECT COALESCE(sum(%s),0)
		FROM model_usage_ledger
		WHERE customer_id=$1
		AND agent_instance_id=$2
		AND created_at >= $3`, column)
	err := s.pool.QueryRow(ctx, query, customerID, agentInstanceID, start).Scan(&used)
	return used, err
}

func (s *Store) ModelUsageSummary(ctx context.Context, customerID, agentInstanceID, userID, period string) (ModelUsageSummary, error) {
	customerID = strings.TrimSpace(customerID)
	agentInstanceID = strings.TrimSpace(agentInstanceID)
	userID = strings.TrimSpace(userID)
	period = strings.TrimSpace(period)
	if period == "" {
		period = "daily"
	}
	if customerID == "" {
		return ModelUsageSummary{}, ValidationError{Message: "customer_id is required"}
	}
	switch period {
	case "daily", "weekly", "monthly":
	default:
		return ModelUsageSummary{}, ValidationError{Message: "period must be daily, weekly, or monthly"}
	}
	start := quotaPeriodStart(period)
	query := `
		SELECT COALESCE(sum(input_tokens),0), COALESCE(sum(output_tokens),0), COALESCE(sum(total_tokens),0), COALESCE(sum(cost_micros),0)
		FROM model_usage_ledger
		WHERE customer_id=$1
		AND created_at >= $2`
	args := []any{customerID, start}
	if agentInstanceID != "" {
		args = append(args, agentInstanceID)
		query += fmt.Sprintf(" AND agent_instance_id=$%d", len(args))
	}
	if userID != "" {
		args = append(args, userID)
		query += fmt.Sprintf(" AND user_id=$%d", len(args))
	}
	out := ModelUsageSummary{CustomerID: customerID, AgentInstanceID: agentInstanceID, UserID: userID, Period: period, PeriodStart: start}
	err := s.pool.QueryRow(ctx, query, args...).Scan(&out.InputTokens, &out.OutputTokens, &out.TotalTokens, &out.CostMicros)
	return out, err
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
