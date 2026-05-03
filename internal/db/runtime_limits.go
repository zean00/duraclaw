package db

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

type RuntimeLimits struct {
	CustomerID                 string          `json:"customer_id"`
	AgentInstanceID            string          `json:"agent_instance_id,omitempty"`
	UserID                     string          `json:"user_id,omitempty"`
	MaxActiveRuns              *int            `json:"max_active_runs,omitempty"`
	MaxQueuedRuns              *int            `json:"max_queued_runs,omitempty"`
	MaxWorkflowRuns            *int            `json:"max_workflow_runs,omitempty"`
	MaxBackgroundRuns          *int            `json:"max_background_runs,omitempty"`
	AsyncBufferSize            *int            `json:"async_buffer_size,omitempty"`
	MaxAsyncPayloadBytes       *int            `json:"max_async_payload_bytes,omitempty"`
	AsyncDegradeThresholdBytes *int            `json:"async_degrade_threshold_bytes,omitempty"`
	MaxDailyTokens             *int            `json:"max_daily_tokens,omitempty"`
	MaxWeeklyTokens            *int            `json:"max_weekly_tokens,omitempty"`
	MaxMonthlyTokens           *int            `json:"max_monthly_tokens,omitempty"`
	MaxDailyModelCostMicros    *int64          `json:"max_daily_model_cost_micros,omitempty"`
	MaxWeeklyModelCostMicros   *int64          `json:"max_weekly_model_cost_micros,omitempty"`
	MaxMonthlyModelCostMicros  *int64          `json:"max_monthly_model_cost_micros,omitempty"`
	Metadata                   json.RawMessage `json:"metadata,omitempty"`
	UpdatedAt                  time.Time       `json:"updated_at"`
}

type EffectiveRuntimeLimits struct {
	MaxActiveRuns              int   `json:"max_active_runs"`
	MaxQueuedRuns              int   `json:"max_queued_runs"`
	MaxWorkflowRuns            int   `json:"max_workflow_runs"`
	MaxBackgroundRuns          int   `json:"max_background_runs"`
	AsyncBufferSize            int   `json:"async_buffer_size"`
	MaxAsyncPayloadBytes       int   `json:"max_async_payload_bytes"`
	AsyncDegradeThresholdBytes int   `json:"async_degrade_threshold_bytes"`
	MaxDailyTokens             int   `json:"max_daily_tokens"`
	MaxWeeklyTokens            int   `json:"max_weekly_tokens"`
	MaxMonthlyTokens           int   `json:"max_monthly_tokens"`
	MaxDailyModelCostMicros    int64 `json:"max_daily_model_cost_micros"`
	MaxWeeklyModelCostMicros   int64 `json:"max_weekly_model_cost_micros"`
	MaxMonthlyModelCostMicros  int64 `json:"max_monthly_model_cost_micros"`
}

func DefaultRuntimeLimits() EffectiveRuntimeLimits {
	return EffectiveRuntimeLimits{
		MaxActiveRuns:              0,
		MaxQueuedRuns:              0,
		MaxWorkflowRuns:            0,
		MaxBackgroundRuns:          0,
		AsyncBufferSize:            100,
		MaxAsyncPayloadBytes:       1 << 20,
		AsyncDegradeThresholdBytes: 64 << 10,
	}
}

type QuotaExceededError struct {
	Kind  string
	Limit int64
	Count int64
}

func (e QuotaExceededError) Error() string {
	return fmt.Sprintf("quota exceeded: %s count %d exceeds limit %d", e.Kind, e.Count, e.Limit)
}

func IsQuotaExceeded(err error) bool {
	var q QuotaExceededError
	return errors.As(err, &q)
}

type ValidationError struct {
	Message string
}

func (e ValidationError) Error() string { return e.Message }

func IsValidationError(err error) bool {
	var v ValidationError
	return errors.As(err, &v)
}

type ConflictError struct {
	Message string
}

func (e ConflictError) Error() string { return e.Message }

func IsConflictError(err error) bool {
	var c ConflictError
	return errors.As(err, &c)
}

func (s *Store) UpsertCustomerRuntimeLimits(ctx context.Context, limits RuntimeLimits) (*RuntimeLimits, error) {
	if limits.CustomerID == "" {
		return nil, ValidationError{Message: "customer_id is required"}
	}
	if err := validateRuntimeLimits(limits); err != nil {
		return nil, err
	}
	if err := s.ensureCustomer(ctx, limits.CustomerID); err != nil {
		return nil, err
	}
	metadata := jsonObject(limits.Metadata)
	var out RuntimeLimits
	err := s.pool.QueryRow(ctx, `
		INSERT INTO customer_runtime_limits(customer_id,max_active_runs,max_queued_runs,max_workflow_runs,max_background_runs,async_buffer_size,max_async_payload_bytes,async_degrade_threshold_bytes,max_daily_tokens,max_weekly_tokens,max_monthly_tokens,max_daily_model_cost_micros,max_weekly_model_cost_micros,max_monthly_model_cost_micros,metadata,updated_at)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,now())
		ON CONFLICT (customer_id) DO UPDATE SET
			max_active_runs=EXCLUDED.max_active_runs,
			max_queued_runs=EXCLUDED.max_queued_runs,
			max_workflow_runs=EXCLUDED.max_workflow_runs,
			max_background_runs=EXCLUDED.max_background_runs,
			async_buffer_size=EXCLUDED.async_buffer_size,
			max_async_payload_bytes=EXCLUDED.max_async_payload_bytes,
			async_degrade_threshold_bytes=EXCLUDED.async_degrade_threshold_bytes,
			max_daily_tokens=EXCLUDED.max_daily_tokens,
			max_weekly_tokens=EXCLUDED.max_weekly_tokens,
			max_monthly_tokens=EXCLUDED.max_monthly_tokens,
			max_daily_model_cost_micros=EXCLUDED.max_daily_model_cost_micros,
			max_weekly_model_cost_micros=EXCLUDED.max_weekly_model_cost_micros,
			max_monthly_model_cost_micros=EXCLUDED.max_monthly_model_cost_micros,
			metadata=EXCLUDED.metadata,
			updated_at=now()
		RETURNING customer_id, max_active_runs, max_queued_runs, max_workflow_runs, max_background_runs, async_buffer_size, max_async_payload_bytes, async_degrade_threshold_bytes, max_daily_tokens, max_weekly_tokens, max_monthly_tokens, max_daily_model_cost_micros, max_weekly_model_cost_micros, max_monthly_model_cost_micros, metadata, updated_at`,
		limits.CustomerID, limits.MaxActiveRuns, limits.MaxQueuedRuns, limits.MaxWorkflowRuns, limits.MaxBackgroundRuns, limits.AsyncBufferSize, limits.MaxAsyncPayloadBytes, limits.AsyncDegradeThresholdBytes, limits.MaxDailyTokens, limits.MaxWeeklyTokens, limits.MaxMonthlyTokens, limits.MaxDailyModelCostMicros, limits.MaxWeeklyModelCostMicros, limits.MaxMonthlyModelCostMicros, metadata).
		Scan(&out.CustomerID, &out.MaxActiveRuns, &out.MaxQueuedRuns, &out.MaxWorkflowRuns, &out.MaxBackgroundRuns, &out.AsyncBufferSize, &out.MaxAsyncPayloadBytes, &out.AsyncDegradeThresholdBytes, &out.MaxDailyTokens, &out.MaxWeeklyTokens, &out.MaxMonthlyTokens, &out.MaxDailyModelCostMicros, &out.MaxWeeklyModelCostMicros, &out.MaxMonthlyModelCostMicros, &out.Metadata, &out.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *Store) CustomerRuntimeLimits(ctx context.Context, customerID string) (*RuntimeLimits, error) {
	var out RuntimeLimits
	err := s.pool.QueryRow(ctx, `
		SELECT customer_id, max_active_runs, max_queued_runs, max_workflow_runs, max_background_runs, async_buffer_size, max_async_payload_bytes, async_degrade_threshold_bytes, max_daily_tokens, max_weekly_tokens, max_monthly_tokens, max_daily_model_cost_micros, max_weekly_model_cost_micros, max_monthly_model_cost_micros, metadata, updated_at
		FROM customer_runtime_limits
		WHERE customer_id=$1`, customerID).
		Scan(&out.CustomerID, &out.MaxActiveRuns, &out.MaxQueuedRuns, &out.MaxWorkflowRuns, &out.MaxBackgroundRuns, &out.AsyncBufferSize, &out.MaxAsyncPayloadBytes, &out.AsyncDegradeThresholdBytes, &out.MaxDailyTokens, &out.MaxWeeklyTokens, &out.MaxMonthlyTokens, &out.MaxDailyModelCostMicros, &out.MaxWeeklyModelCostMicros, &out.MaxMonthlyModelCostMicros, &out.Metadata, &out.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *Store) UpsertAgentInstanceRuntimeLimits(ctx context.Context, limits RuntimeLimits) (*RuntimeLimits, error) {
	if limits.CustomerID == "" || limits.AgentInstanceID == "" {
		return nil, ValidationError{Message: "customer_id and agent_instance_id are required"}
	}
	if err := validateRuntimeLimits(limits); err != nil {
		return nil, err
	}
	if err := s.ensureCustomer(ctx, limits.CustomerID); err != nil {
		return nil, err
	}
	if _, err := s.pool.Exec(ctx, `INSERT INTO agent_instances(customer_id,id) VALUES($1,$2) ON CONFLICT DO NOTHING`, limits.CustomerID, limits.AgentInstanceID); err != nil {
		return nil, err
	}
	metadata := jsonObject(limits.Metadata)
	var out RuntimeLimits
	err := s.pool.QueryRow(ctx, `
		INSERT INTO agent_instance_runtime_limits(customer_id,agent_instance_id,max_active_runs,max_queued_runs,max_workflow_runs,max_background_runs,async_buffer_size,max_async_payload_bytes,async_degrade_threshold_bytes,max_daily_tokens,max_weekly_tokens,max_monthly_tokens,max_daily_model_cost_micros,max_weekly_model_cost_micros,max_monthly_model_cost_micros,metadata,updated_at)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,now())
		ON CONFLICT (customer_id,agent_instance_id) DO UPDATE SET
			max_active_runs=EXCLUDED.max_active_runs,
			max_queued_runs=EXCLUDED.max_queued_runs,
			max_workflow_runs=EXCLUDED.max_workflow_runs,
			max_background_runs=EXCLUDED.max_background_runs,
			async_buffer_size=EXCLUDED.async_buffer_size,
			max_async_payload_bytes=EXCLUDED.max_async_payload_bytes,
			async_degrade_threshold_bytes=EXCLUDED.async_degrade_threshold_bytes,
			max_daily_tokens=EXCLUDED.max_daily_tokens,
			max_weekly_tokens=EXCLUDED.max_weekly_tokens,
			max_monthly_tokens=EXCLUDED.max_monthly_tokens,
			max_daily_model_cost_micros=EXCLUDED.max_daily_model_cost_micros,
			max_weekly_model_cost_micros=EXCLUDED.max_weekly_model_cost_micros,
			max_monthly_model_cost_micros=EXCLUDED.max_monthly_model_cost_micros,
			metadata=EXCLUDED.metadata,
			updated_at=now()
		RETURNING customer_id, agent_instance_id, max_active_runs, max_queued_runs, max_workflow_runs, max_background_runs, async_buffer_size, max_async_payload_bytes, async_degrade_threshold_bytes, max_daily_tokens, max_weekly_tokens, max_monthly_tokens, max_daily_model_cost_micros, max_weekly_model_cost_micros, max_monthly_model_cost_micros, metadata, updated_at`,
		limits.CustomerID, limits.AgentInstanceID, limits.MaxActiveRuns, limits.MaxQueuedRuns, limits.MaxWorkflowRuns, limits.MaxBackgroundRuns, limits.AsyncBufferSize, limits.MaxAsyncPayloadBytes, limits.AsyncDegradeThresholdBytes, limits.MaxDailyTokens, limits.MaxWeeklyTokens, limits.MaxMonthlyTokens, limits.MaxDailyModelCostMicros, limits.MaxWeeklyModelCostMicros, limits.MaxMonthlyModelCostMicros, metadata).
		Scan(&out.CustomerID, &out.AgentInstanceID, &out.MaxActiveRuns, &out.MaxQueuedRuns, &out.MaxWorkflowRuns, &out.MaxBackgroundRuns, &out.AsyncBufferSize, &out.MaxAsyncPayloadBytes, &out.AsyncDegradeThresholdBytes, &out.MaxDailyTokens, &out.MaxWeeklyTokens, &out.MaxMonthlyTokens, &out.MaxDailyModelCostMicros, &out.MaxWeeklyModelCostMicros, &out.MaxMonthlyModelCostMicros, &out.Metadata, &out.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *Store) AgentInstanceRuntimeLimits(ctx context.Context, customerID, agentInstanceID string) (*RuntimeLimits, error) {
	var out RuntimeLimits
	err := s.pool.QueryRow(ctx, `
		SELECT customer_id, agent_instance_id, max_active_runs, max_queued_runs, max_workflow_runs, max_background_runs, async_buffer_size, max_async_payload_bytes, async_degrade_threshold_bytes, max_daily_tokens, max_weekly_tokens, max_monthly_tokens, max_daily_model_cost_micros, max_weekly_model_cost_micros, max_monthly_model_cost_micros, metadata, updated_at
		FROM agent_instance_runtime_limits
		WHERE customer_id=$1 AND agent_instance_id=$2`, customerID, agentInstanceID).
		Scan(&out.CustomerID, &out.AgentInstanceID, &out.MaxActiveRuns, &out.MaxQueuedRuns, &out.MaxWorkflowRuns, &out.MaxBackgroundRuns, &out.AsyncBufferSize, &out.MaxAsyncPayloadBytes, &out.AsyncDegradeThresholdBytes, &out.MaxDailyTokens, &out.MaxWeeklyTokens, &out.MaxMonthlyTokens, &out.MaxDailyModelCostMicros, &out.MaxWeeklyModelCostMicros, &out.MaxMonthlyModelCostMicros, &out.Metadata, &out.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *Store) UpsertUserRuntimeLimits(ctx context.Context, limits RuntimeLimits) (*RuntimeLimits, error) {
	if limits.CustomerID == "" || limits.UserID == "" {
		return nil, ValidationError{Message: "customer_id and user_id are required"}
	}
	if limits.AgentInstanceID != "" || limits.MaxActiveRuns != nil || limits.MaxQueuedRuns != nil || limits.MaxWorkflowRuns != nil ||
		limits.MaxBackgroundRuns != nil || limits.AsyncBufferSize != nil || limits.MaxAsyncPayloadBytes != nil || limits.AsyncDegradeThresholdBytes != nil {
		return nil, ValidationError{Message: "user runtime limits only support model token and model cost quotas"}
	}
	if err := validateRuntimeLimits(limits); err != nil {
		return nil, err
	}
	if err := s.ensureCustomer(ctx, limits.CustomerID); err != nil {
		return nil, err
	}
	if _, err := s.pool.Exec(ctx, `INSERT INTO users(customer_id,id) VALUES($1,$2) ON CONFLICT DO NOTHING`, limits.CustomerID, limits.UserID); err != nil {
		return nil, err
	}
	metadata := jsonObject(limits.Metadata)
	var out RuntimeLimits
	err := s.pool.QueryRow(ctx, `
		INSERT INTO user_runtime_limits(customer_id,user_id,max_daily_tokens,max_weekly_tokens,max_monthly_tokens,max_daily_model_cost_micros,max_weekly_model_cost_micros,max_monthly_model_cost_micros,metadata,updated_at)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,now())
		ON CONFLICT (customer_id,user_id) DO UPDATE SET
			max_daily_tokens=EXCLUDED.max_daily_tokens,
			max_weekly_tokens=EXCLUDED.max_weekly_tokens,
			max_monthly_tokens=EXCLUDED.max_monthly_tokens,
			max_daily_model_cost_micros=EXCLUDED.max_daily_model_cost_micros,
			max_weekly_model_cost_micros=EXCLUDED.max_weekly_model_cost_micros,
			max_monthly_model_cost_micros=EXCLUDED.max_monthly_model_cost_micros,
			metadata=EXCLUDED.metadata,
			updated_at=now()
		RETURNING customer_id, user_id, max_daily_tokens, max_weekly_tokens, max_monthly_tokens, max_daily_model_cost_micros, max_weekly_model_cost_micros, max_monthly_model_cost_micros, metadata, updated_at`,
		limits.CustomerID, limits.UserID, limits.MaxDailyTokens, limits.MaxWeeklyTokens, limits.MaxMonthlyTokens, limits.MaxDailyModelCostMicros, limits.MaxWeeklyModelCostMicros, limits.MaxMonthlyModelCostMicros, metadata).
		Scan(&out.CustomerID, &out.UserID, &out.MaxDailyTokens, &out.MaxWeeklyTokens, &out.MaxMonthlyTokens, &out.MaxDailyModelCostMicros, &out.MaxWeeklyModelCostMicros, &out.MaxMonthlyModelCostMicros, &out.Metadata, &out.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *Store) UserRuntimeLimits(ctx context.Context, customerID, userID string) (*RuntimeLimits, error) {
	var out RuntimeLimits
	err := s.pool.QueryRow(ctx, `
		SELECT customer_id, user_id, max_daily_tokens, max_weekly_tokens, max_monthly_tokens, max_daily_model_cost_micros, max_weekly_model_cost_micros, max_monthly_model_cost_micros, metadata, updated_at
		FROM user_runtime_limits
		WHERE customer_id=$1 AND user_id=$2`, customerID, userID).
		Scan(&out.CustomerID, &out.UserID, &out.MaxDailyTokens, &out.MaxWeeklyTokens, &out.MaxMonthlyTokens, &out.MaxDailyModelCostMicros, &out.MaxWeeklyModelCostMicros, &out.MaxMonthlyModelCostMicros, &out.Metadata, &out.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *Store) EffectiveRuntimeLimits(ctx context.Context, customerID, agentInstanceID string) (EffectiveRuntimeLimits, error) {
	limits := DefaultRuntimeLimits()
	if customer, err := s.CustomerRuntimeLimits(ctx, customerID); err == nil {
		applyRuntimeLimits(&limits, customer)
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return limits, err
	}
	if agentInstanceID != "" {
		if agent, err := s.AgentInstanceRuntimeLimits(ctx, customerID, agentInstanceID); err == nil {
			applyRuntimeLimits(&limits, agent)
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return limits, err
		}
	}
	return limits, nil
}

func (s *Store) EnforceRunQuota(ctx context.Context, customerID, agentInstanceID string) error {
	limits, err := s.EffectiveRuntimeLimits(ctx, customerID, agentInstanceID)
	if err != nil {
		return err
	}
	if limits.MaxQueuedRuns > 0 {
		count, err := s.runCount(ctx, customerID, agentInstanceID, []string{"queued"})
		if err != nil {
			return err
		}
		if count >= limits.MaxQueuedRuns {
			return QuotaExceededError{Kind: "queued_runs", Limit: int64(limits.MaxQueuedRuns), Count: int64(count + 1)}
		}
	}
	if limits.MaxActiveRuns > 0 {
		count, err := s.runCount(ctx, customerID, agentInstanceID, []string{"leased", "running", "running_workflow", "awaiting_user"})
		if err != nil {
			return err
		}
		if count >= limits.MaxActiveRuns {
			return QuotaExceededError{Kind: "active_runs", Limit: int64(limits.MaxActiveRuns), Count: int64(count + 1)}
		}
	}
	return nil
}

func (s *Store) EnforceWorkflowQuota(ctx context.Context, customerID, agentInstanceID string) error {
	limits, err := s.EffectiveRuntimeLimits(ctx, customerID, agentInstanceID)
	if err != nil {
		return err
	}
	if limits.MaxWorkflowRuns <= 0 {
		return nil
	}
	count, err := s.workflowRunCount(ctx, customerID, agentInstanceID)
	if err != nil {
		return err
	}
	if count >= limits.MaxWorkflowRuns {
		return QuotaExceededError{Kind: "workflow_runs", Limit: int64(limits.MaxWorkflowRuns), Count: int64(count + 1)}
	}
	return nil
}

func (s *Store) EnforceRunStartQuota(ctx context.Context, runID, customerID, agentInstanceID string) error {
	limits, err := s.EffectiveRuntimeLimits(ctx, customerID, agentInstanceID)
	if err != nil {
		return err
	}
	if limits.MaxActiveRuns <= 0 {
		return nil
	}
	var count int
	err = s.pool.QueryRow(ctx, `
		SELECT count(*)
		FROM runs
		WHERE customer_id=$1
		AND agent_instance_id=$2
		AND id<>$3
		AND state IN ('leased','running','running_workflow','awaiting_user')`, customerID, agentInstanceID, runID).Scan(&count)
	if err != nil {
		return err
	}
	if count+1 > limits.MaxActiveRuns {
		return QuotaExceededError{Kind: "active_runs", Limit: int64(limits.MaxActiveRuns), Count: int64(count + 1)}
	}
	return nil
}

func (s *Store) EnforceBackgroundQuota(ctx context.Context, customerID, agentInstanceID string) error {
	limits, err := s.EffectiveRuntimeLimits(ctx, customerID, agentInstanceID)
	if err != nil {
		return err
	}
	if limits.MaxBackgroundRuns <= 0 {
		return nil
	}
	var count int
	err = s.pool.QueryRow(ctx, `
		SELECT count(*)
		FROM runs
		WHERE customer_id=$1
		AND agent_instance_id=$2
		AND run_mode='background'
		AND state IN ('queued','leased','running','running_workflow','awaiting_user')`, customerID, agentInstanceID).Scan(&count)
	if err != nil {
		return err
	}
	if count >= limits.MaxBackgroundRuns {
		return QuotaExceededError{Kind: "background_runs", Limit: int64(limits.MaxBackgroundRuns), Count: int64(count + 1)}
	}
	return nil
}

func applyRuntimeLimits(out *EffectiveRuntimeLimits, in *RuntimeLimits) {
	if in.MaxActiveRuns != nil {
		out.MaxActiveRuns = *in.MaxActiveRuns
	}
	if in.MaxQueuedRuns != nil {
		out.MaxQueuedRuns = *in.MaxQueuedRuns
	}
	if in.MaxWorkflowRuns != nil {
		out.MaxWorkflowRuns = *in.MaxWorkflowRuns
	}
	if in.MaxBackgroundRuns != nil {
		out.MaxBackgroundRuns = *in.MaxBackgroundRuns
	}
	if in.AsyncBufferSize != nil {
		out.AsyncBufferSize = *in.AsyncBufferSize
	}
	if in.MaxAsyncPayloadBytes != nil {
		out.MaxAsyncPayloadBytes = *in.MaxAsyncPayloadBytes
	}
	if in.AsyncDegradeThresholdBytes != nil {
		out.AsyncDegradeThresholdBytes = *in.AsyncDegradeThresholdBytes
	}
	if in.MaxDailyTokens != nil {
		out.MaxDailyTokens = *in.MaxDailyTokens
	}
	if in.MaxWeeklyTokens != nil {
		out.MaxWeeklyTokens = *in.MaxWeeklyTokens
	}
	if in.MaxMonthlyTokens != nil {
		out.MaxMonthlyTokens = *in.MaxMonthlyTokens
	}
	if in.MaxDailyModelCostMicros != nil {
		out.MaxDailyModelCostMicros = *in.MaxDailyModelCostMicros
	}
	if in.MaxWeeklyModelCostMicros != nil {
		out.MaxWeeklyModelCostMicros = *in.MaxWeeklyModelCostMicros
	}
	if in.MaxMonthlyModelCostMicros != nil {
		out.MaxMonthlyModelCostMicros = *in.MaxMonthlyModelCostMicros
	}
}

func validateRuntimeLimits(limits RuntimeLimits) error {
	for name, value := range map[string]*int{
		"max_active_runs":               limits.MaxActiveRuns,
		"max_queued_runs":               limits.MaxQueuedRuns,
		"max_workflow_runs":             limits.MaxWorkflowRuns,
		"max_background_runs":           limits.MaxBackgroundRuns,
		"async_buffer_size":             limits.AsyncBufferSize,
		"max_async_payload_bytes":       limits.MaxAsyncPayloadBytes,
		"async_degrade_threshold_bytes": limits.AsyncDegradeThresholdBytes,
		"max_daily_tokens":              limits.MaxDailyTokens,
		"max_weekly_tokens":             limits.MaxWeeklyTokens,
		"max_monthly_tokens":            limits.MaxMonthlyTokens,
	} {
		if value != nil && *value < 0 {
			return ValidationError{Message: fmt.Sprintf("%s must be non-negative", name)}
		}
	}
	for name, value := range map[string]*int64{
		"max_daily_model_cost_micros":   limits.MaxDailyModelCostMicros,
		"max_weekly_model_cost_micros":  limits.MaxWeeklyModelCostMicros,
		"max_monthly_model_cost_micros": limits.MaxMonthlyModelCostMicros,
	} {
		if value != nil && *value < 0 {
			return ValidationError{Message: fmt.Sprintf("%s must be non-negative", name)}
		}
	}
	return nil
}

func (s *Store) runCount(ctx context.Context, customerID, agentInstanceID string, states []string) (int, error) {
	var count int
	err := s.pool.QueryRow(ctx, `
		SELECT count(*)
		FROM runs
		WHERE customer_id=$1
		AND agent_instance_id=$2
		AND state = ANY($3)`, customerID, agentInstanceID, states).Scan(&count)
	return count, err
}

func (s *Store) workflowRunCount(ctx context.Context, customerID, agentInstanceID string) (int, error) {
	var count int
	err := s.pool.QueryRow(ctx, `
		SELECT count(*)
		FROM workflow_runs wr
		JOIN runs r ON r.id=wr.run_id
		WHERE r.customer_id=$1
		AND r.agent_instance_id=$2
		AND wr.status IN ('queued','running','awaiting_user')`, customerID, agentInstanceID).Scan(&count)
	return count, err
}
