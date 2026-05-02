package db

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

type BroadcastTargetSpec struct {
	UserID    string `json:"user_id"`
	SessionID string `json:"session_id"`
}

type BroadcastGenerationSpec struct {
	Mode            string         `json:"mode,omitempty"`
	AgentInstanceID string         `json:"agent_instance_id,omitempty"`
	Guidelines      string         `json:"guidelines,omitempty"`
	Context         map[string]any `json:"context,omitempty"`
	Details         string         `json:"details,omitempty"`
}

type BroadcastSpec struct {
	CustomerID string                  `json:"customer_id"`
	Title      string                  `json:"title"`
	Payload    any                     `json:"payload"`
	Targets    []BroadcastTargetSpec   `json:"targets"`
	Generation BroadcastGenerationSpec `json:"generation,omitempty"`
}

type BroadcastTargetSelection struct {
	AllUsers               bool           `json:"all_users,omitempty"`
	UserIDs                []string       `json:"user_ids,omitempty"`
	AgentInstanceID        string         `json:"agent_instance_id,omitempty"`
	ReminderSubscriptionID string         `json:"reminder_subscription_id,omitempty"`
	Segment                map[string]any `json:"segment,omitempty"`
	Limit                  int            `json:"limit,omitempty"`
}

type Broadcast struct {
	ID                string          `json:"id"`
	CustomerID        string          `json:"customer_id"`
	Title             string          `json:"title"`
	Payload           json.RawMessage `json:"payload"`
	GenerationMode    string          `json:"generation_mode"`
	AgentInstanceID   *string         `json:"agent_instance_id,omitempty"`
	GenerationRequest json.RawMessage `json:"generation_request,omitempty"`
	Status            string          `json:"status"`
	CreatedAt         time.Time       `json:"created_at"`
	UpdatedAt         time.Time       `json:"updated_at"`
}

type BroadcastTarget struct {
	ID               string    `json:"id"`
	BroadcastID      string    `json:"broadcast_id"`
	CustomerID       string    `json:"customer_id"`
	UserID           string    `json:"user_id"`
	SessionID        string    `json:"session_id"`
	Status           string    `json:"status"`
	OutboundIntentID *string   `json:"outbound_intent_id,omitempty"`
	GenerationRunID  *string   `json:"generation_run_id,omitempty"`
	LastError        *string   `json:"last_error,omitempty"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

func (s *Store) CreateBroadcast(ctx context.Context, customerID, title string, payload any, targets []BroadcastTargetSpec) (string, int, error) {
	return s.createDirectBroadcast(ctx, customerID, title, payload, targets)
}

func (s *Store) CreateBroadcastFromSpec(ctx context.Context, spec BroadcastSpec) (string, int, int, error) {
	mode := normalizeBroadcastGenerationMode(spec.Generation.Mode)
	if mode == "direct" {
		id, count, err := s.createDirectBroadcast(ctx, spec.CustomerID, spec.Title, spec.Payload, spec.Targets)
		return id, count, 0, err
	}
	if spec.CustomerID == "" || spec.Title == "" || len(spec.Targets) == 0 {
		return "", 0, 0, fmt.Errorf("customer_id, title, and targets are required")
	}
	if strings.TrimSpace(spec.Generation.AgentInstanceID) == "" {
		return "", 0, 0, fmt.Errorf("generation.agent_instance_id is required")
	}
	if mode != "agent_per_instance" && mode != "per_user" {
		return "", 0, 0, fmt.Errorf("generation.mode must be direct, agent_per_instance, or per_user")
	}
	if err := s.ensureCustomer(ctx, spec.CustomerID); err != nil {
		return "", 0, 0, err
	}
	for _, target := range spec.Targets {
		if target.UserID == "" || target.SessionID == "" {
			return "", 0, 0, fmt.Errorf("broadcast targets require user_id and session_id")
		}
	}
	payloadJSON, _ := json.Marshal(spec.Payload)
	if len(payloadJSON) == 0 {
		payloadJSON = []byte(`{}`)
	}
	generationJSON, _ := json.Marshal(spec.Generation)
	if len(generationJSON) == 0 {
		generationJSON = []byte(`{}`)
	}
	var broadcastID string
	if err := s.pool.QueryRow(ctx, `
		INSERT INTO broadcasts(customer_id,title,payload,generation_mode,agent_instance_id,generation_request,status)
		VALUES($1,$2,$3,$4,$5,$6,'queued')
		RETURNING id::text`, spec.CustomerID, spec.Title, payloadJSON, mode, spec.Generation.AgentInstanceID, generationJSON).Scan(&broadcastID); err != nil {
		return "", 0, 0, err
	}
	targetIDs := make([]string, 0, len(spec.Targets))
	for _, target := range spec.Targets {
		var targetID string
		if err := s.pool.QueryRow(ctx, `
			INSERT INTO broadcast_targets(broadcast_id,customer_id,user_id,session_id,status)
			VALUES($1,$2,$3,$4,'generating')
			RETURNING id::text`, broadcastID, spec.CustomerID, target.UserID, target.SessionID).Scan(&targetID); err != nil {
			return broadcastID, len(targetIDs), 0, err
		}
		targetIDs = append(targetIDs, targetID)
	}
	runCount, err := s.createBroadcastGenerationRuns(ctx, broadcastID, spec, mode, targetIDs)
	if err != nil {
		_ = s.failBroadcastGeneration(ctx, broadcastID, spec.CustomerID, err.Error())
	}
	return broadcastID, len(targetIDs), runCount, err
}

func (s *Store) createDirectBroadcast(ctx context.Context, customerID, title string, payload any, targets []BroadcastTargetSpec) (string, int, error) {
	if customerID == "" || title == "" || len(targets) == 0 {
		return "", 0, fmt.Errorf("customer_id, title, and targets are required")
	}
	if err := s.ensureCustomer(ctx, customerID); err != nil {
		return "", 0, err
	}
	for _, target := range targets {
		if target.UserID == "" || target.SessionID == "" {
			return "", 0, fmt.Errorf("broadcast targets require user_id and session_id")
		}
	}
	payloadJSON, _ := json.Marshal(payload)
	if len(payloadJSON) == 0 {
		payloadJSON = []byte(`{}`)
	}
	var broadcastID string
	created := 0
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `
			INSERT INTO broadcasts(customer_id,title,payload,status)
			VALUES($1,$2,$3,'queued')
			RETURNING id::text`, customerID, title, payloadJSON).Scan(&broadcastID); err != nil {
			return err
		}
		for _, target := range targets {
			var targetID string
			if err := tx.QueryRow(ctx, `
				INSERT INTO broadcast_targets(broadcast_id,customer_id,user_id,session_id,status)
				VALUES($1,$2,$3,$4,'pending')
				RETURNING id::text`, broadcastID, customerID, target.UserID, target.SessionID).Scan(&targetID); err != nil {
				return err
			}
			intentID, _, err := createOutboundIntentTx(ctx, tx, OutboundIntent{
				CustomerID: customerID,
				UserID:     target.UserID,
				SessionID:  target.SessionID,
				Type:       "broadcast",
				Payload:    mustJSON(map[string]any{"broadcast_id": broadcastID, "target_id": targetID, "title": title, "payload": json.RawMessage(payloadJSON)}),
			})
			if err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE broadcast_targets SET status='queued', outbound_intent_id=$2, updated_at=now() WHERE id=$1`, targetID, intentID); err != nil {
				return err
			}
			created++
		}
		return nil
	})
	return broadcastID, created, err
}

func (s *Store) createBroadcastGenerationRuns(ctx context.Context, broadcastID string, spec BroadcastSpec, mode string, targetIDs []string) (int, error) {
	switch mode {
	case "agent_per_instance":
		sessionID := "broadcast-" + broadcastID + "-" + spec.Generation.AgentInstanceID
		run, err := s.CreateSystemRun(ctx, ACPContext{
			CustomerID: spec.CustomerID, UserID: "broadcast", AgentInstanceID: spec.Generation.AgentInstanceID, SessionID: sessionID,
			RequestID: "broadcast-" + broadcastID, IdempotencyKey: broadcastID + ":agent:" + spec.Generation.AgentInstanceID,
		}, broadcastGenerationInput(broadcastID, spec, nil, len(spec.Targets)))
		if err != nil {
			return 0, err
		}
		_, err = s.pool.Exec(ctx, `
			UPDATE broadcast_targets
			SET generation_run_id=$1, updated_at=now()
			WHERE broadcast_id=$2 AND customer_id=$3 AND status='generating'`, run.ID, broadcastID, spec.CustomerID)
		if err != nil {
			return 0, err
		}
		return 1, nil
	case "per_user":
		created := 0
		for i, target := range spec.Targets {
			run, err := s.CreateSystemRun(ctx, ACPContext{
				CustomerID: spec.CustomerID, UserID: target.UserID, AgentInstanceID: spec.Generation.AgentInstanceID, SessionID: "broadcast-" + broadcastID + "-" + target.UserID,
				RequestID: "broadcast-" + broadcastID, IdempotencyKey: broadcastID + ":user:" + target.UserID,
			}, broadcastGenerationInput(broadcastID, spec, &target, 1))
			if err != nil {
				return created, err
			}
			if _, err := s.pool.Exec(ctx, `
				UPDATE broadcast_targets
				SET generation_run_id=$1, updated_at=now()
				WHERE id=$2`, run.ID, targetIDs[i]); err != nil {
				return created, err
			}
			created++
		}
		return created, nil
	default:
		return 0, fmt.Errorf("unsupported generation mode %q", mode)
	}
}

func (s *Store) failBroadcastGeneration(ctx context.Context, broadcastID, customerID, errText string) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `
			UPDATE broadcasts
			SET status='generation_failed', updated_at=now()
			WHERE id=$1 AND customer_id=$2`, broadcastID, customerID); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `
			UPDATE broadcast_targets
			SET status='generation_failed', last_error=$3, updated_at=now()
			WHERE broadcast_id=$1 AND customer_id=$2 AND status IN ('generating','processing')`, broadcastID, customerID, nullableBroadcastError(errText))
		return err
	})
}

func broadcastGenerationInput(broadcastID string, spec BroadcastSpec, target *BroadcastTargetSpec, targetCount int) map[string]any {
	input := map[string]any{
		"event_type":   "broadcast_generation",
		"broadcast_id": broadcastID,
		"title":        spec.Title,
		"text":         broadcastGenerationPrompt(broadcastID, spec, target, targetCount),
		"generation": map[string]any{
			"mode":       normalizeBroadcastGenerationMode(spec.Generation.Mode),
			"guidelines": spec.Generation.Guidelines,
			"context":    spec.Generation.Context,
			"details":    spec.Generation.Details,
		},
		"target_count": targetCount,
	}
	if target != nil {
		input["target"] = map[string]any{"user_id": target.UserID, "session_id": target.SessionID}
	}
	return input
}

func broadcastGenerationPrompt(broadcastID string, spec BroadcastSpec, target *BroadcastTargetSpec, targetCount int) string {
	var b strings.Builder
	b.WriteString("Trusted broadcast generation instruction:\n")
	b.WriteString("Write one concise outbound message for this promotion, offer, discount, or feature announcement. Match the configured agent profile personality. Do not mention internal systems or that this was generated by a worker.\n\n")
	b.WriteString("Broadcast ID: " + broadcastID + "\n")
	b.WriteString("Title: " + strings.TrimSpace(spec.Title) + "\n")
	b.WriteString(fmt.Sprintf("Target count: %d\n", targetCount))
	if target != nil {
		b.WriteString("Target user_id: " + target.UserID + "\n")
	}
	if strings.TrimSpace(spec.Generation.Guidelines) != "" {
		b.WriteString("\nDelivery guidelines:\n" + strings.TrimSpace(spec.Generation.Guidelines) + "\n")
	}
	if len(spec.Generation.Context) > 0 {
		contextJSON, _ := json.Marshal(spec.Generation.Context)
		b.WriteString("\nPromotion context JSON:\n" + string(contextJSON) + "\n")
	}
	if strings.TrimSpace(spec.Generation.Details) != "" {
		b.WriteString("\nDetailed information:\n" + strings.TrimSpace(spec.Generation.Details) + "\n")
	}
	b.WriteString("\nReturn only the user-facing message text.")
	return b.String()
}

func normalizeBroadcastGenerationMode(mode string) string {
	mode = strings.TrimSpace(mode)
	if mode == "" {
		return "direct"
	}
	return mode
}

func (s *Store) ResolveBroadcastTargets(ctx context.Context, customerID string, selection BroadcastTargetSelection) ([]BroadcastTargetSpec, error) {
	if customerID == "" {
		return nil, fmt.Errorf("customer_id is required")
	}
	limit := selection.Limit
	if limit <= 0 || limit > 10000 {
		limit = 10000
	}
	seen := map[string]bool{}
	var out []BroadcastTargetSpec
	addRows := func(rows pgx.Rows) error {
		defer rows.Close()
		for rows.Next() {
			var target BroadcastTargetSpec
			if err := rows.Scan(&target.UserID, &target.SessionID); err != nil {
				return err
			}
			key := target.UserID + "\x00" + target.SessionID
			if target.UserID == "" || target.SessionID == "" || seen[key] {
				continue
			}
			out = append(out, target)
			seen[key] = true
		}
		return rows.Err()
	}
	if selection.AllUsers {
		rows, err := s.pool.Query(ctx, `
			SELECT DISTINCT ON (user_id) user_id, id
			FROM sessions
			WHERE customer_id=$1
			ORDER BY user_id, updated_at DESC
			LIMIT $2`, customerID, limit)
		if err != nil {
			return nil, err
		}
		if err := addRows(rows); err != nil {
			return nil, err
		}
	}
	if selection.AgentInstanceID != "" && len(out) < limit {
		rows, err := s.pool.Query(ctx, `
			SELECT DISTINCT ON (user_id) user_id, id
			FROM sessions
			WHERE customer_id=$1 AND agent_instance_id=$2
			ORDER BY user_id, updated_at DESC
			LIMIT $3`, customerID, selection.AgentInstanceID, limit-len(out))
		if err != nil {
			return nil, err
		}
		if err := addRows(rows); err != nil {
			return nil, err
		}
	}
	if selection.ReminderSubscriptionID != "" && len(out) < limit {
		rows, err := s.pool.Query(ctx, `
			SELECT user_id, session_id
			FROM reminder_subscriptions
			WHERE customer_id=$1 AND id=$2 AND enabled=true
			LIMIT $3`, customerID, selection.ReminderSubscriptionID, limit-len(out))
		if err != nil {
			return nil, err
		}
		if err := addRows(rows); err != nil {
			return nil, err
		}
	}
	for _, userID := range selection.UserIDs {
		if len(out) >= limit {
			break
		}
		var target BroadcastTargetSpec
		err := s.pool.QueryRow(ctx, `
			SELECT user_id, id
			FROM sessions
			WHERE customer_id=$1 AND user_id=$2
			ORDER BY updated_at DESC
			LIMIT 1`, customerID, userID).Scan(&target.UserID, &target.SessionID)
		if err != nil {
			continue
		}
		key := target.UserID + "\x00" + target.SessionID
		if !seen[key] {
			out = append(out, target)
			seen[key] = true
		}
	}
	if len(selection.Segment) > 0 && len(out) < limit {
		segmentTargets, err := s.resolveBroadcastSegment(ctx, customerID, selection.Segment, limit-len(out))
		if err != nil {
			return nil, err
		}
		for _, target := range segmentTargets {
			key := target.UserID + "\x00" + target.SessionID
			if !seen[key] {
				out = append(out, target)
				seen[key] = true
			}
		}
	}
	return out, nil
}

func (s *Store) resolveBroadcastSegment(ctx context.Context, customerID string, segment map[string]any, limit int) ([]BroadcastTargetSpec, error) {
	if limit <= 0 {
		return nil, nil
	}
	clauses := []string{"customer_id=$1"}
	args := []any{customerID}
	nextArg := func(v any) string {
		args = append(args, v)
		return fmt.Sprintf("$%d", len(args))
	}
	if prefix, _ := segment["user_id_prefix"].(string); strings.TrimSpace(prefix) != "" {
		clauses = append(clauses, "user_id LIKE "+nextArg(prefix+"%"))
	}
	if prefix, _ := segment["session_id_prefix"].(string); strings.TrimSpace(prefix) != "" {
		clauses = append(clauses, "id LIKE "+nextArg(prefix+"%"))
	}
	if agentInstanceID, _ := segment["agent_instance_id"].(string); strings.TrimSpace(agentInstanceID) != "" {
		clauses = append(clauses, "agent_instance_id="+nextArg(agentInstanceID))
	}
	if updatedSince, _ := segment["updated_since"].(string); strings.TrimSpace(updatedSince) != "" {
		t, err := time.Parse(time.RFC3339, updatedSince)
		if err != nil {
			return nil, fmt.Errorf("segment.updated_since must be RFC3339")
		}
		clauses = append(clauses, "updated_at >= "+nextArg(t))
	}
	if len(clauses) == 1 {
		return nil, fmt.Errorf("segment requires at least one supported condition")
	}
	args = append(args, limit)
	rows, err := s.pool.Query(ctx, `
		SELECT DISTINCT ON (user_id) user_id, id
		FROM sessions
		WHERE `+strings.Join(clauses, " AND ")+`
		ORDER BY user_id, updated_at DESC
		LIMIT `+fmt.Sprintf("$%d", len(args)), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []BroadcastTargetSpec
	for rows.Next() {
		var target BroadcastTargetSpec
		if err := rows.Scan(&target.UserID, &target.SessionID); err != nil {
			return nil, err
		}
		out = append(out, target)
	}
	return out, rows.Err()
}

func (s *Store) ListBroadcasts(ctx context.Context, customerID string, limit int) ([]Broadcast, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id::text, customer_id, title, payload, generation_mode, agent_instance_id, generation_request, status, created_at, updated_at
		FROM broadcasts
		WHERE customer_id=$1
		ORDER BY created_at DESC
		LIMIT $2`, customerID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var broadcasts []Broadcast
	for rows.Next() {
		var broadcast Broadcast
		if err := rows.Scan(&broadcast.ID, &broadcast.CustomerID, &broadcast.Title, &broadcast.Payload, &broadcast.GenerationMode, &broadcast.AgentInstanceID, &broadcast.GenerationRequest, &broadcast.Status, &broadcast.CreatedAt, &broadcast.UpdatedAt); err != nil {
			return nil, err
		}
		broadcasts = append(broadcasts, broadcast)
	}
	return broadcasts, rows.Err()
}

func (s *Store) ListBroadcastTargets(ctx context.Context, customerID, broadcastID string, limit int) ([]BroadcastTarget, error) {
	if limit <= 0 || limit > 1000 {
		limit = 250
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id::text, broadcast_id::text, customer_id, user_id, session_id, status, outbound_intent_id::text, generation_run_id::text, last_error, created_at, updated_at
		FROM broadcast_targets
		WHERE customer_id=$1 AND broadcast_id=$2
		ORDER BY created_at ASC
		LIMIT $3`, customerID, broadcastID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var targets []BroadcastTarget
	for rows.Next() {
		var target BroadcastTarget
		if err := rows.Scan(&target.ID, &target.BroadcastID, &target.CustomerID, &target.UserID, &target.SessionID, &target.Status, &target.OutboundIntentID, &target.GenerationRunID, &target.LastError, &target.CreatedAt, &target.UpdatedAt); err != nil {
			return nil, err
		}
		targets = append(targets, target)
	}
	return targets, rows.Err()
}

func (s *Store) CancelBroadcast(ctx context.Context, customerID, broadcastID string) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE broadcasts
		SET status='cancelled', updated_at=now()
		WHERE id=$1 AND customer_id=$2`, broadcastID, customerID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("broadcast not found")
	}
	if _, err := s.pool.Exec(ctx, `
		UPDATE broadcast_targets
		SET status='cancelled', updated_at=now()
		WHERE broadcast_id=$1 AND customer_id=$2 AND status IN ('pending','generating','processing','queued')`, broadcastID, customerID); err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `
		UPDATE outbound_intents
		SET status='cancelled', updated_at=now()
		WHERE id IN (
			SELECT outbound_intent_id FROM broadcast_targets
			WHERE broadcast_id=$1 AND customer_id=$2 AND outbound_intent_id IS NOT NULL
		)
		AND status IN ('pending','queued')`, broadcastID, customerID)
	return err
}

type BroadcastGenerationDelivery struct {
	TargetID    string          `json:"target_id"`
	BroadcastID string          `json:"broadcast_id"`
	CustomerID  string          `json:"customer_id"`
	UserID      string          `json:"user_id"`
	SessionID   string          `json:"session_id"`
	RunID       string          `json:"run_id"`
	Title       string          `json:"title"`
	Payload     json.RawMessage `json:"payload"`
	FinalText   string          `json:"final_text"`
}

func (s *Store) ClaimCompletedBroadcastGenerationDeliveries(ctx context.Context, limit int) ([]BroadcastGenerationDelivery, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.pool.Query(ctx, `
		WITH candidate AS (
			SELECT bt.id
			FROM broadcast_targets bt
			JOIN runs r ON r.id=bt.generation_run_id
			WHERE bt.generation_run_id IS NOT NULL
			AND r.state='completed'
			AND (bt.status='generating' OR (bt.status='processing' AND bt.updated_at < now() - interval '5 minutes'))
			ORDER BY bt.created_at
			FOR UPDATE SKIP LOCKED
			LIMIT $1
		)
		UPDATE broadcast_targets bt
		SET status='processing', updated_at=now()
		FROM candidate
		WHERE bt.id=candidate.id
		RETURNING bt.id::text, bt.broadcast_id::text, bt.customer_id, bt.user_id, bt.session_id, bt.generation_run_id::text,
			(SELECT title FROM broadcasts WHERE id=bt.broadcast_id),
			(SELECT payload FROM broadcasts WHERE id=bt.broadcast_id),
			COALESCE((SELECT suppressed_response FROM runs WHERE id=bt.generation_run_id),'')`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []BroadcastGenerationDelivery
	for rows.Next() {
		var delivery BroadcastGenerationDelivery
		if err := rows.Scan(&delivery.TargetID, &delivery.BroadcastID, &delivery.CustomerID, &delivery.UserID, &delivery.SessionID, &delivery.RunID, &delivery.Title, &delivery.Payload, &delivery.FinalText); err != nil {
			return nil, err
		}
		out = append(out, delivery)
	}
	return out, rows.Err()
}

func (s *Store) CreateBroadcastGenerationOutbound(ctx context.Context, delivery BroadcastGenerationDelivery) (bool, error) {
	if delivery.TargetID == "" || delivery.UserID == "" || delivery.SessionID == "" {
		return false, fmt.Errorf("target_id, user_id, and session_id are required")
	}
	created := false
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var status string
		if err := tx.QueryRow(ctx, `
			SELECT status
			FROM broadcast_targets
			WHERE id=$1
			FOR UPDATE`, delivery.TargetID).Scan(&status); err != nil {
			return err
		}
		if status != "processing" {
			return nil
		}
		payload := map[string]any{
			"broadcast_id": delivery.BroadcastID,
			"target_id":    delivery.TargetID,
			"title":        delivery.Title,
			"payload":      json.RawMessage(delivery.Payload),
			"text":         strings.TrimSpace(delivery.FinalText),
			"parts":        []map[string]any{{"type": "text", "text": strings.TrimSpace(delivery.FinalText)}},
			"run_id":       delivery.RunID,
		}
		intentID, _, err := createOutboundIntentTx(ctx, tx, OutboundIntent{CustomerID: delivery.CustomerID, UserID: delivery.UserID, SessionID: delivery.SessionID, RunID: &delivery.RunID, Type: "broadcast", Payload: mustJSON(payload)})
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `
			UPDATE broadcast_targets
			SET status='queued', outbound_intent_id=$2, last_error=NULL, updated_at=now()
			WHERE id=$1 AND status='processing'`, delivery.TargetID, intentID)
		if err != nil {
			return err
		}
		created = true
		return nil
	})
	return created, err
}

func (s *Store) CompleteBroadcastGenerationDelivery(ctx context.Context, targetID string, status string, errText string) error {
	if status == "" {
		status = "generation_failed"
	}
	_, err := s.pool.Exec(ctx, `
		UPDATE broadcast_targets
		SET status=$2, last_error=$3, updated_at=now()
		WHERE id=$1`, targetID, status, nullableBroadcastError(errText))
	return err
}

func nullableBroadcastError(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

func mustJSON(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	if len(b) == 0 {
		return json.RawMessage(`{}`)
	}
	return b
}
