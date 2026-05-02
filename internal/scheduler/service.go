package scheduler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"duraclaw/internal/db"
)

type Store interface {
	ClaimDueSchedulerJobs(ctx context.Context, owner string, limit int, leaseFor time.Duration) ([]db.SchedulerJob, error)
	ClaimDueReminderSubscriptions(ctx context.Context, owner string, limit int, leaseFor time.Duration) ([]db.ReminderSubscription, error)
	ClaimDueSharedSchedulerJobs(ctx context.Context, owner string, limit int, leaseFor time.Duration) ([]db.SharedSchedulerJob, error)
	CompleteSchedulerJob(ctx context.Context, jobID string, firedAt, nextRunAt time.Time) error
	CompleteReminderSubscription(ctx context.Context, id string, firedAt, nextRunAt time.Time) error
	CompleteSharedSchedulerJob(ctx context.Context, jobID string, firedAt, nextRunAt time.Time) error
	ListSharedSchedulerSubscriptions(ctx context.Context, customerID, userID, sharedJobID string, limit int) ([]db.SharedSchedulerSubscription, error)
	RecordSharedSchedulerFire(ctx context.Context, jobID, customerID string, fireAt time.Time, status string, selected, created int, errText string, req, resp any) error
	CreateSharedSchedulerOutbound(ctx context.Context, job db.SharedSchedulerJob, sub db.SharedSchedulerSubscription, fireAt time.Time, payload map[string]any) (bool, error)
	CreateSharedSchedulerRunDelivery(ctx context.Context, job db.SharedSchedulerJob, fireAt time.Time, agentInstanceID string, runID string, subscribers any) (bool, error)
	ClaimCompletedSharedSchedulerRunDeliveries(ctx context.Context, limit int) ([]db.SharedSchedulerRunDelivery, error)
	CreateSharedSchedulerDeliveryOutbound(ctx context.Context, delivery db.SharedSchedulerRunDelivery, userID, sessionID string, payload map[string]any) (bool, error)
	CompleteSharedSchedulerRunDelivery(ctx context.Context, deliveryID string, status string, errText string) error
	CreateRun(ctx context.Context, c db.ACPContext, input any) (*db.Run, error)
	ResumeWorkflowTimer(ctx context.Context, runID, workflowRunID, nodeKey string, response map[string]any) error
}

type Service struct {
	store    Store
	owner    string
	limit    int
	leaseFor time.Duration
	client   *http.Client
}

func NewService(store Store, owner string) *Service {
	if owner == "" {
		owner = "duraclaw-scheduler"
	}
	return &Service{store: store, owner: owner, limit: 10, leaseFor: time.Minute, client: &http.Client{Timeout: 10 * time.Second}}
}

func (s *Service) RunOnce(ctx context.Context, now time.Time) (int, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	created, err := s.runCompletedSharedSchedulerRunDeliveries(ctx)
	if err != nil {
		return created, err
	}
	n, err := s.runSharedSchedulerJobs(ctx, now)
	if err != nil {
		return created, err
	}
	created += n
	n, err = s.runReminderSubscriptions(ctx, now)
	if err != nil {
		return created, err
	}
	created += n
	jobs, err := s.store.ClaimDueSchedulerJobs(ctx, s.owner, s.limit, s.leaseFor)
	if err != nil {
		return created, err
	}
	for _, job := range jobs {
		spec, err := parsePayload(job.Payload)
		if err != nil {
			return created, err
		}
		if spec.WorkflowWake == nil && len(job.Metadata) > 0 {
			var metadata struct {
				WorkflowWake *workflowWake `json:"workflow_wake"`
			}
			_ = json.Unmarshal(job.Metadata, &metadata)
			spec.WorkflowWake = metadata.WorkflowWake
		}
		fireAt := job.NextRunAt
		if spec.WorkflowWake != nil {
			if err := s.store.ResumeWorkflowTimer(ctx, spec.WorkflowWake.RunID, spec.WorkflowWake.WorkflowRunID, spec.WorkflowWake.NodeKey, map[string]any{"scheduler_job_id": job.ID, "fired_at": fireAt}); err != nil {
				return created, err
			}
		} else {
			key := IdempotencyKey(job.ID, fireAt)
			_, err = s.store.CreateRun(ctx, db.ACPContext{
				CustomerID:      job.CustomerID,
				UserID:          spec.UserID,
				AgentInstanceID: spec.AgentInstanceID,
				SessionID:       spec.SessionID,
				RequestID:       "scheduler-" + job.ID,
				IdempotencyKey:  key,
			}, spec.Input)
			if err != nil {
				return created, err
			}
		}
		next, err := Next(job.Schedule, maxTime(now, fireAt))
		if err != nil {
			return created, err
		}
		if err := s.store.CompleteSchedulerJob(ctx, job.ID, fireAt, next); err != nil {
			return created, err
		}
		created++
	}
	return created, nil
}

type externalServiceConfig struct {
	URL                string                `json:"url"`
	Method             string                `json:"method"`
	Headers            map[string]string     `json:"headers"`
	IncludeSubscribers bool                  `json:"include_subscribers"`
	ResponseMapping    responseMappingConfig `json:"response_mapping"`
}

type responseMappingConfig struct {
	TargetPath           string `json:"target_path"`
	TargetShape          string `json:"target_shape"`
	IDPath               string `json:"id_path"`
	ObjectIDSource       string `json:"object_id_source"`
	EmptyTargetBehavior  string `json:"empty_target_behavior"`
	MessageContextSource string `json:"message_context_source"`
}

type selectedSubscriber struct {
	sub     db.SharedSchedulerSubscription
	context map[string]any
}

func (s *Service) runSharedSchedulerJobs(ctx context.Context, now time.Time) (int, error) {
	jobs, err := s.store.ClaimDueSharedSchedulerJobs(ctx, s.owner, s.limit, s.leaseFor)
	if err != nil {
		return 0, err
	}
	total := 0
	for _, job := range jobs {
		created, err := s.runSharedSchedulerJob(ctx, job, now)
		if err != nil {
			return total, err
		}
		total += created
	}
	return total, nil
}

type durableRunSubscriberPayload struct {
	UserID    string         `json:"user_id"`
	SessionID string         `json:"session_id"`
	Context   map[string]any `json:"context"`
}

func (s *Service) runCompletedSharedSchedulerRunDeliveries(ctx context.Context) (int, error) {
	deliveries, err := s.store.ClaimCompletedSharedSchedulerRunDeliveries(ctx, s.limit)
	if err != nil {
		return 0, err
	}
	created := 0
	for _, delivery := range deliveries {
		var subscribers []durableRunSubscriberPayload
		_ = json.Unmarshal(delivery.SubscriberPayloads, &subscribers)
		text := textFromMessageContent(delivery.FinalMessage)
		if strings.TrimSpace(text) == "" {
			text = "Reminder"
		}
		for _, subscriber := range subscribers {
			payload := map[string]any{
				"run_id": delivery.RunID,
				"parts":  []map[string]any{{"type": "text", "text": text}},
			}
			ok, err := s.store.CreateSharedSchedulerDeliveryOutbound(ctx, delivery, subscriber.UserID, subscriber.SessionID, payload)
			if err != nil {
				_ = s.store.CompleteSharedSchedulerRunDelivery(ctx, delivery.ID, "failed", err.Error())
				return created, err
			}
			if ok {
				created++
			}
		}
		if err := s.store.CompleteSharedSchedulerRunDelivery(ctx, delivery.ID, "completed", ""); err != nil {
			return created, err
		}
	}
	return created, nil
}

func (s *Service) runSharedSchedulerJob(ctx context.Context, job db.SharedSchedulerJob, now time.Time) (int, error) {
	fireAt := job.NextRunAt
	subs, err := s.store.ListSharedSchedulerSubscriptions(ctx, job.CustomerID, "", job.ID, 0)
	if err != nil {
		return 0, err
	}
	active := activeSharedSubscriptions(subs)
	selected, reqSummary, respSummary, evalErr := s.selectSharedSubscribers(ctx, job, fireAt, active)
	if evalErr != nil {
		_ = s.store.RecordSharedSchedulerFire(ctx, job.ID, job.CustomerID, fireAt, "failed", 0, 0, evalErr.Error(), reqSummary, respSummary)
		next, nextErr := Next(job.Schedule, maxTime(now, fireAt))
		if nextErr != nil {
			return 0, nextErr
		}
		if err := s.store.CompleteSharedSchedulerJob(ctx, job.ID, fireAt, next); err != nil {
			return 0, err
		}
		return 0, nil
	}
	created := 0
	if sharedFanoutAction(job) == "durable_run" {
		created, err = s.createSharedDurableRuns(ctx, job, selected, fireAt)
	} else {
		created, err = s.createSharedOutboundDeliveries(ctx, job, selected, fireAt)
	}
	if err != nil {
		return created, err
	}
	if err := s.store.RecordSharedSchedulerFire(ctx, job.ID, job.CustomerID, fireAt, "succeeded", len(selected), created, "", reqSummary, respSummary); err != nil {
		return created, err
	}
	next, err := Next(job.Schedule, maxTime(now, fireAt))
	if err != nil {
		return created, err
	}
	if err := s.store.CompleteSharedSchedulerJob(ctx, job.ID, fireAt, next); err != nil {
		return created, err
	}
	return created, nil
}

func activeSharedSubscriptions(subs []db.SharedSchedulerSubscription) []db.SharedSchedulerSubscription {
	out := make([]db.SharedSchedulerSubscription, 0, len(subs))
	for _, sub := range subs {
		if sub.Enabled {
			out = append(out, sub)
		}
	}
	return out
}

func (s *Service) selectSharedSubscribers(ctx context.Context, job db.SharedSchedulerJob, fireAt time.Time, subs []db.SharedSchedulerSubscription) ([]selectedSubscriber, map[string]any, map[string]any, error) {
	var cfg externalServiceConfig
	_ = json.Unmarshal(job.ExternalService, &cfg)
	reqSummary := map[string]any{"subscriber_count": len(subs)}
	if strings.TrimSpace(cfg.URL) == "" {
		return allSharedSubscribers(subs, nil), reqSummary, map[string]any{"targeting": "all_subscribers"}, nil
	}
	if cfg.Method == "" {
		cfg.Method = http.MethodPost
	}
	body := map[string]any{
		"customer_id":       job.CustomerID,
		"job_id":            job.ID,
		"job_key":           job.JobKey,
		"job_type":          job.JobType,
		"scheduled_fire_at": fireAt,
		"timezone":          job.Timezone,
		"metadata":          rawMap(job.Metadata),
	}
	if cfg.IncludeSubscribers {
		body["subscribers"] = sharedSubscriberPayloads(subs)
	}
	rawBody, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, cfg.Method, cfg.URL, bytes.NewReader(rawBody))
	if err != nil {
		return nil, reqSummary, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	for key, value := range cfg.Headers {
		req.Header.Set(key, value)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, reqSummary, nil, err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, reqSummary, map[string]any{"status_code": resp.StatusCode}, fmt.Errorf("shared scheduler external service returned %d", resp.StatusCode)
	}
	var decoded any
	if err := json.Unmarshal(respBody, &decoded); err != nil {
		return nil, reqSummary, map[string]any{"status_code": resp.StatusCode}, err
	}
	selected, summary, err := mapSharedTargets(decoded, cfg.ResponseMapping, subs)
	return selected, reqSummary, summary, err
}

func sharedFanoutAction(job db.SharedSchedulerJob) string {
	action := strings.TrimSpace(job.FanoutAction)
	if action == "" {
		return "outbound_intent"
	}
	return action
}

func (s *Service) createSharedOutboundDeliveries(ctx context.Context, job db.SharedSchedulerJob, selected []selectedSubscriber, fireAt time.Time) (int, error) {
	created := 0
	for _, target := range selected {
		payload := map[string]any{"text": renderTemplate(job.MessageTemplate, sharedTemplateContext(job, target))}
		if payload["text"] == "" {
			payload["text"] = job.Title
		}
		ok, err := s.store.CreateSharedSchedulerOutbound(ctx, job, target.sub, fireAt, payload)
		if err != nil {
			return created, err
		}
		if ok {
			created++
		}
	}
	return created, nil
}

func (s *Service) createSharedDurableRuns(ctx context.Context, job db.SharedSchedulerJob, selected []selectedSubscriber, fireAt time.Time) (int, error) {
	groups := map[string][]selectedSubscriber{}
	for _, target := range selected {
		groups[target.sub.AgentInstanceID] = append(groups[target.sub.AgentInstanceID], target)
	}
	created := 0
	for agentInstanceID, targets := range groups {
		input := renderMapTemplate(rawMap(job.RunInputTemplate), sharedGroupTemplateContext(job, agentInstanceID, targets))
		if len(input) == 0 {
			input = map[string]any{
				"text":          renderTemplate(job.MessageTemplate, sharedGroupTemplateContext(job, agentInstanceID, targets)),
				"subscribers":   sharedSelectedSubscriberPayloads(targets),
				"shared_job_id": job.ID,
				"job_key":       job.JobKey,
			}
		}
		sessionID := "shared-scheduler-" + job.ID + "-" + agentInstanceID
		run, err := s.store.CreateRun(ctx, db.ACPContext{
			CustomerID: job.CustomerID, UserID: "shared-scheduler", AgentInstanceID: agentInstanceID, SessionID: sessionID,
			RequestID: "shared-scheduler-" + job.ID, IdempotencyKey: IdempotencyKey(job.ID+":"+agentInstanceID+":durable_run", fireAt),
		}, input)
		if err != nil {
			return created, err
		}
		ok, err := s.store.CreateSharedSchedulerRunDelivery(ctx, job, fireAt, agentInstanceID, run.ID, sharedSelectedSubscriberPayloads(targets))
		if err != nil {
			return created, err
		}
		if ok {
			created++
		}
	}
	return created, nil
}

func (s *Service) runReminderSubscriptions(ctx context.Context, now time.Time) (int, error) {
	subs, err := s.store.ClaimDueReminderSubscriptions(ctx, s.owner, s.limit, s.leaseFor)
	if err != nil {
		return 0, err
	}
	created := 0
	for _, sub := range subs {
		fireAt := sub.NextRunAt
		input := map[string]any{}
		_ = json.Unmarshal(sub.Payload, &input)
		if len(input) == 0 {
			input = map[string]any{"text": sub.Title}
		}
		reminderText, _ := input["text"].(string)
		if strings.TrimSpace(reminderText) == "" {
			reminderText = sub.Title
		}
		instruction := "This reminder is due now. Send a direct reminder message to the user. Do not say the reminder was created or scheduled."
		input["event_type"] = "reminder_due"
		input["reminder"] = map[string]any{
			"subscription_id": sub.ID,
			"title":           sub.Title,
			"scheduled_for":   fireAt.UTC().Format(time.RFC3339),
			"schedule":        sub.Schedule,
			"timezone":        sub.Timezone,
		}
		input["instruction"] = instruction
		input["text"] = dueReminderPromptText(instruction, reminderText, sub, fireAt)
		if _, err := s.store.CreateRun(ctx, db.ACPContext{
			CustomerID: sub.CustomerID, UserID: sub.UserID, AgentInstanceID: sub.AgentInstanceID, SessionID: sub.SessionID,
			RequestID: "reminder-" + sub.ID, IdempotencyKey: IdempotencyKey(sub.ID, fireAt),
		}, input); err != nil {
			return created, err
		}
		next, err := Next(sub.Schedule, maxTime(now, fireAt))
		if err != nil {
			return created, err
		}
		if err := s.store.CompleteReminderSubscription(ctx, sub.ID, fireAt, next); err != nil {
			return created, err
		}
		created++
	}
	return created, nil
}

func dueReminderPromptText(instruction, reminderText string, sub db.ReminderSubscription, fireAt time.Time) string {
	var b strings.Builder
	b.WriteString("Trusted reminder runtime instruction:\n")
	b.WriteString(strings.TrimSpace(instruction))
	b.WriteString("\n\nReminder due context:\n")
	b.WriteString("subscription_id: ")
	b.WriteString(sub.ID)
	b.WriteString("\n")
	b.WriteString("title: ")
	b.WriteString(strings.TrimSpace(sub.Title))
	b.WriteString("\n")
	b.WriteString("scheduled_for: ")
	b.WriteString(fireAt.UTC().Format(time.RFC3339))
	b.WriteString("\n")
	b.WriteString("schedule: ")
	b.WriteString(strings.TrimSpace(sub.Schedule))
	b.WriteString("\n")
	b.WriteString("timezone: ")
	b.WriteString(strings.TrimSpace(sub.Timezone))
	b.WriteString("\n\nReminder message text:\n")
	b.WriteString(strings.TrimSpace(reminderText))
	return b.String()
}

type jobPayload struct {
	UserID          string         `json:"user_id"`
	AgentInstanceID string         `json:"agent_instance_id"`
	SessionID       string         `json:"session_id"`
	Input           map[string]any `json:"input"`
	WorkflowWake    *workflowWake  `json:"workflow_wake,omitempty"`
}

type workflowWake struct {
	RunID         string `json:"run_id"`
	WorkflowRunID string `json:"workflow_run_id"`
	NodeKey       string `json:"node_key"`
}

func parsePayload(raw json.RawMessage) (jobPayload, error) {
	var p jobPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return p, err
	}
	if p.UserID == "" || p.AgentInstanceID == "" || p.SessionID == "" {
		return p, fmt.Errorf("scheduler payload requires user_id, agent_instance_id, and session_id")
	}
	if p.Input == nil {
		p.Input = map[string]any{"text": "Scheduled job fired."}
	}
	return p, nil
}

func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

func allSharedSubscribers(subs []db.SharedSchedulerSubscription, item map[string]any) []selectedSubscriber {
	out := make([]selectedSubscriber, 0, len(subs))
	for _, sub := range subs {
		out = append(out, selectedSubscriber{sub: sub, context: item})
	}
	return out
}

func sharedSubscriberPayloads(subs []db.SharedSchedulerSubscription) []map[string]any {
	out := make([]map[string]any, 0, len(subs))
	for _, sub := range subs {
		out = append(out, map[string]any{
			"user_id":             sub.UserID,
			"session_id":          sub.SessionID,
			"agent_instance_id":   sub.AgentInstanceID,
			"subscriber_metadata": rawMap(sub.SubscriberMetadata),
		})
	}
	return out
}

func sharedSelectedSubscriberPayloads(targets []selectedSubscriber) []map[string]any {
	out := make([]map[string]any, 0, len(targets))
	for _, target := range targets {
		out = append(out, map[string]any{
			"user_id":             target.sub.UserID,
			"session_id":          target.sub.SessionID,
			"subscriber_metadata": rawMap(target.sub.SubscriberMetadata),
			"context":             target.context,
		})
	}
	return out
}

func mapSharedTargets(decoded any, cfg responseMappingConfig, subs []db.SharedSchedulerSubscription) ([]selectedSubscriber, map[string]any, error) {
	target := valueAtAny(decoded, cfg.TargetPath)
	if target == nil {
		return allSharedSubscribers(subs, nil), map[string]any{"target_path": cfg.TargetPath, "selected_count": len(subs), "missing": true, "fallback": "all_subscribers"}, nil
	}
	byUser := map[string]db.SharedSchedulerSubscription{}
	for _, sub := range subs {
		byUser[sub.UserID] = sub
	}
	var out []selectedSubscriber
	add := func(userID string, ctx map[string]any) {
		if sub, ok := byUser[userID]; ok && sub.Enabled {
			out = append(out, selectedSubscriber{sub: sub, context: ctx})
		}
	}
	switch v := target.(type) {
	case []any:
		for _, item := range v {
			obj, _ := item.(map[string]any)
			id := stringAtAny(item, cfg.IDPath)
			if id != "" {
				add(id, obj)
			}
		}
	case map[string]any:
		if cfg.ObjectIDSource == "key" {
			for key, item := range v {
				obj, _ := item.(map[string]any)
				add(key, obj)
			}
		} else {
			for _, item := range v {
				obj, _ := item.(map[string]any)
				id := stringAtAny(item, cfg.IDPath)
				if id != "" {
					add(id, obj)
				}
			}
		}
	default:
		return nil, map[string]any{"target_path": cfg.TargetPath}, fmt.Errorf("mapped target must be list or object")
	}
	return out, map[string]any{"target_path": cfg.TargetPath, "selected_count": len(out)}, nil
}

func valueAtAny(value any, path string) any {
	if path == "" {
		return value
	}
	cur := value
	for _, part := range strings.Split(path, ".") {
		obj, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur = obj[part]
	}
	return cur
}

func stringAtAny(value any, path string) string {
	if path == "" {
		if s, ok := value.(string); ok {
			return s
		}
		return ""
	}
	v := valueAtAny(value, path)
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func rawMap(raw json.RawMessage) map[string]any {
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	if out == nil {
		return map[string]any{}
	}
	return out
}

func sharedTemplateContext(job db.SharedSchedulerJob, target selectedSubscriber) map[string]any {
	ctx := map[string]any{
		"job_key":             job.JobKey,
		"title":               job.Title,
		"user_id":             target.sub.UserID,
		"session_id":          target.sub.SessionID,
		"agent_instance_id":   target.sub.AgentInstanceID,
		"subscriber_metadata": rawMap(target.sub.SubscriberMetadata),
		"metadata":            rawMap(job.Metadata),
	}
	for key, value := range target.context {
		ctx[key] = value
	}
	return ctx
}

func sharedGroupTemplateContext(job db.SharedSchedulerJob, agentInstanceID string, targets []selectedSubscriber) map[string]any {
	ctx := map[string]any{
		"job_key":           job.JobKey,
		"title":             job.Title,
		"agent_instance_id": agentInstanceID,
		"subscriber_count":  len(targets),
		"subscribers":       sharedSelectedSubscriberPayloads(targets),
		"metadata":          rawMap(job.Metadata),
	}
	if len(targets) == 1 {
		for key, value := range sharedTemplateContext(job, targets[0]) {
			ctx[key] = value
		}
	}
	return ctx
}

func renderMapTemplate(template map[string]any, ctx map[string]any) map[string]any {
	out := map[string]any{}
	for key, value := range template {
		if s, ok := value.(string); ok {
			out[key] = renderTemplate(s, ctx)
		} else {
			out[key] = value
		}
	}
	return out
}

func renderTemplate(template string, ctx map[string]any) string {
	out := template
	for {
		start := strings.Index(out, "{{")
		if start < 0 {
			return out
		}
		end := strings.Index(out[start+2:], "}}")
		if end < 0 {
			return out
		}
		end += start + 2
		key := strings.TrimSpace(out[start+2 : end])
		repl := fmt.Sprint(valueAtAny(ctx, key))
		if repl == "<nil>" {
			repl = ""
		}
		out = out[:start] + repl + out[end+2:]
	}
}

func textFromMessageContent(raw json.RawMessage) string {
	var payload struct {
		Text  string `json:"text"`
		Parts []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"parts"`
	}
	_ = json.Unmarshal(raw, &payload)
	if strings.TrimSpace(payload.Text) != "" {
		return strings.TrimSpace(payload.Text)
	}
	for _, part := range payload.Parts {
		if part.Type == "text" && strings.TrimSpace(part.Text) != "" {
			return strings.TrimSpace(part.Text)
		}
	}
	return ""
}
