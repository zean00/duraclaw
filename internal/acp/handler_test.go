package acp

import (
	"context"
	"duraclaw/internal/db"
	"duraclaw/internal/mcp"
	"duraclaw/internal/observability"

	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type adminMCPClient struct{}

func (adminMCPClient) CallTool(context.Context, mcp.ExecutionContext, string, string, map[string]any) (map[string]any, error) {
	return map[string]any{}, nil
}

func (adminMCPClient) ListTools(context.Context, mcp.ExecutionContext, string) ([]mcp.ToolInfo, error) {
	return []mcp.ToolInfo{{Name: "lookup"}}, nil
}

func (adminMCPClient) ListResources(context.Context, mcp.ExecutionContext, string) ([]mcp.ResourceInfo, error) {
	return []mcp.ResourceInfo{{URI: "file://doc"}}, nil
}

func (adminMCPClient) ReadResource(context.Context, mcp.ExecutionContext, string, string) (*mcp.ResourceContent, error) {
	return &mcp.ResourceContent{URI: "file://doc", Text: "hello"}, nil
}

func (adminMCPClient) ListPrompts(context.Context, mcp.ExecutionContext, string) ([]mcp.PromptInfo, error) {
	return []mcp.PromptInfo{{Name: "summarize"}}, nil
}

func (adminMCPClient) GetPrompt(context.Context, mcp.ExecutionContext, string, string, map[string]any) (*mcp.PromptContent, error) {
	return &mcp.PromptContent{Name: "summarize", Messages: []mcp.PromptMessage{{Role: "user", Content: map[string]any{"type": "text", "text": "Summarize"}}}}, nil
}

func TestAgentsDiscovery(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/acp/agents", nil)
	rec := httptest.NewRecorder()
	NewHandler(nil).Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"duraclaw"`) {
		t.Fatalf("missing agent manifest: %s", rec.Body.String())
	}
}

func TestHealthz(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	NewHandler(nil).Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"ok"`) {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestReadyzWithoutStore(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rec := httptest.NewRecorder()
	NewHandler(nil).Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "not_configured") {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestMetrics(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rec := httptest.NewRecorder()
	NewHandler(nil).Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestStatusForErrorMapsQuotaToTooManyRequests(t *testing.T) {
	if got := statusForError(db.QuotaExceededError{Kind: "queued_runs", Limit: 1, Count: 2}); got != http.StatusTooManyRequests {
		t.Fatalf("status=%d", got)
	}
}

func TestStatusForErrorMapsValidationToBadRequest(t *testing.T) {
	if got := statusForError(db.ValidationError{Message: "max_active_runs must be non-negative"}); got != http.StatusBadRequest {
		t.Fatalf("status=%d", got)
	}
}

func TestAccessLogStatusCounter(t *testing.T) {
	counters := observability.NewCounters()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	NewHandler(nil).WithCounters(counters).Routes().ServeHTTP(rec, req)
	if counters.Snapshot()["http_status_200"] != 1 {
		t.Fatalf("counters=%#v", counters.Snapshot())
	}
}

func TestAgentInstanceVersionRoutesValidateBeforeStore(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/admin/agent-instances/a1/versions", strings.NewReader(`{"version":1}`))
	rec := httptest.NewRecorder()
	NewHandler(nil).Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "customer_id") {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/admin/agent-instances/a1/versions", nil)
	rec = httptest.NewRecorder()
	NewHandler(nil).Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "customer_id") {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodPost, "/admin/agent-instances/a1/versions/v1/activate", strings.NewReader(`{}`))
	rec = httptest.NewRecorder()
	NewHandler(nil).Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "version_id") {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestCreateSchedulerJobValidatesSchedule(t *testing.T) {
	body := `{"customer_id":"c1","user_id":"u1","agent_instance_id":"a1","session_id":"s1","schedule":"not cron"}`
	req := httptest.NewRequest(http.MethodPost, "/admin/scheduler/jobs", strings.NewReader(body))
	rec := httptest.NewRecorder()
	NewHandler(nil).Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "expected exactly 5 fields") {
		t.Fatalf("body=%s", rec.Body.String())
	}
}

func TestListSchedulerJobsRequiresCustomer(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/admin/scheduler/jobs", nil)
	rec := httptest.NewRecorder()
	NewHandler(nil).Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "customer_id") {
		t.Fatalf("body=%s", rec.Body.String())
	}
}

func TestListBackgroundRunsRequiresCustomer(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/admin/background-runs", nil)
	rec := httptest.NewRecorder()
	NewHandler(nil).Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "customer_id") {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestAdminMCPRoutes(t *testing.T) {
	manager := mcp.NewManager()
	manager.Register("srv", adminMCPClient{})
	handler := NewHandler(nil).WithMCPManager(manager).Routes()
	for _, tc := range []struct {
		method string
		path   string
		body   string
		want   string
	}{
		{http.MethodGet, "/admin/mcp/servers", "", `"srv"`},
		{http.MethodGet, "/admin/mcp/servers/srv/tools", "", `"lookup"`},
		{http.MethodGet, "/admin/mcp/servers/srv/resources", "", `"file://doc"`},
		{http.MethodGet, "/admin/mcp/servers/srv/resources/read?uri=file://doc", "", `"hello"`},
		{http.MethodGet, "/admin/mcp/servers/srv/prompts", "", `"summarize"`},
		{http.MethodPost, "/admin/mcp/servers/srv/prompts/summarize/get", `{}`, `"Summarize"`},
	} {
		req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), tc.want) {
			t.Fatalf("path=%s status=%d body=%s", tc.path, rec.Code, rec.Body.String())
		}
	}
}

func TestMCPNotificationRequiresContext(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/admin/mcp/notifications", strings.NewReader(`{"server_name":"srv"}`))
	rec := httptest.NewRecorder()
	NewHandler(nil).Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "customer_id") {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestUpdateSchedulerJobRequiresEnabled(t *testing.T) {
	req := httptest.NewRequest(http.MethodPatch, "/admin/scheduler/jobs/job-1", strings.NewReader(`{"customer_id":"c"}`))
	rec := httptest.NewRecorder()
	NewHandler(nil).Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "enabled") {
		t.Fatalf("body=%s", rec.Body.String())
	}
}

func TestCreateReminderSubscriptionValidatesBeforeStore(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/admin/reminders/subscriptions", strings.NewReader(`{"customer_id":"c"}`))
	rec := httptest.NewRecorder()
	NewHandler(nil).Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "schedule") {
		t.Fatalf("body=%s", rec.Body.String())
	}
}

func TestUpdateReminderSubscriptionRequiresEnabled(t *testing.T) {
	req := httptest.NewRequest(http.MethodPatch, "/admin/reminders/subscriptions/sub-1", strings.NewReader(`{"customer_id":"c"}`))
	rec := httptest.NewRecorder()
	NewHandler(nil).Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "enabled") {
		t.Fatalf("body=%s", rec.Body.String())
	}
}

func TestUpsertWorkflowNodeRequiresNodeType(t *testing.T) {
	req := httptest.NewRequest(http.MethodPut, "/admin/workflows/wf-1/nodes/start", strings.NewReader(`{}`))
	rec := httptest.NewRecorder()
	NewHandler(nil).Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "node_type") {
		t.Fatalf("body=%s", rec.Body.String())
	}
}

func TestUpsertWorkflowEdgeRequiresEndpoints(t *testing.T) {
	req := httptest.NewRequest(http.MethodPut, "/admin/workflows/wf-1/edges", strings.NewReader(`{}`))
	rec := httptest.NewRecorder()
	NewHandler(nil).Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "from_node_key") {
		t.Fatalf("body=%s", rec.Body.String())
	}
}

func TestStartRunRequiresContextHeadersBeforeBody(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/acp/runs", strings.NewReader(`{}`))
	rec := httptest.NewRecorder()
	NewHandler(nil).Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "missing required header") {
		t.Fatalf("body=%s", rec.Body.String())
	}
}

func TestValidateRunInputRejectsUnsupportedPartType(t *testing.T) {
	payload := map[string]any{"parts": []any{map[string]any{"type": "unknown"}}}
	if err := validateRunInput(payload); err == nil || !strings.Contains(err.Error(), "unsupported type") {
		t.Fatalf("err=%v", err)
	}
}

func TestValidateRunInputAllowsProviderMultimodalPartTypes(t *testing.T) {
	for _, typ := range []string{"image_url", "file", "input_audio", "video_url"} {
		payload := map[string]any{"parts": []any{map[string]any{"type": typ, "data": map[string]any{"url": "https://example.test/file"}}}}
		if err := validateRunInput(payload); err != nil {
			t.Fatalf("type=%s err=%v", typ, err)
		}
	}
}

func TestValidateRunInputRequiresArtifactID(t *testing.T) {
	payload := map[string]any{"parts": []any{map[string]any{"type": "artifact_ref", "data": map[string]any{}}}}
	if err := validateRunInput(payload); err == nil || !strings.Contains(err.Error(), "artifact_id") {
		t.Fatalf("err=%v", err)
	}
}

func TestRequiredContextHeaderOrderIsStable(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/acp/runs", strings.NewReader(`{}`))
	rec := httptest.NewRecorder()
	_, ok := requiredContext(rec, req, true)
	if ok {
		t.Fatalf("expected missing headers")
	}
	if !strings.Contains(rec.Body.String(), "X-Customer-ID") {
		t.Fatalf("body=%s", rec.Body.String())
	}
}

func TestDecodeJSONRejectsUnknownFields(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(`{"name":"wf","version":1,"unknown":true}`))
	rec := httptest.NewRecorder()
	var payload struct {
		Name    string `json:"name"`
		Version int    `json:"version"`
	}
	err := decodeJSON(rec, req, &payload)
	if err == nil {
		t.Fatalf("expected unknown field error")
	}
}

func TestCreateWorkflowValidatesPayloadBeforeStore(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/admin/workflows", strings.NewReader(`{"version":0}`))
	rec := httptest.NewRecorder()
	NewHandler(nil).Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestKnowledgeIngestValidatesBeforeStore(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/admin/knowledge/text", strings.NewReader(`{"customer_id":"c"}`))
	rec := httptest.NewRecorder()
	NewHandler(nil).Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestGetAgentPolicyRequiresScope(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/admin/agent-policies?customer_id=c", nil)
	rec := httptest.NewRecorder()
	NewHandler(nil).Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "agent_instance_id") {
		t.Fatalf("body=%s", rec.Body.String())
	}
}

func TestCreatePolicyPackValidatesPayloadBeforeStore(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/admin/policy-packs", strings.NewReader(`{"version":1}`))
	rec := httptest.NewRecorder()
	NewHandler(nil).Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "name") {
		t.Fatalf("body=%s", rec.Body.String())
	}
}

func TestUpsertPolicyRuleValidatesPayloadBeforeStore(t *testing.T) {
	req := httptest.NewRequest(http.MethodPut, "/admin/policy-packs/pack-1/rules/rule-1", strings.NewReader(`{"action":"deny"}`))
	rec := httptest.NewRecorder()
	NewHandler(nil).Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "rule_type") {
		t.Fatalf("body=%s", rec.Body.String())
	}
}

func TestListKnowledgeDocumentsRequiresCustomer(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/admin/knowledge/documents", nil)
	rec := httptest.NewRecorder()
	NewHandler(nil).Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "customer_id") {
		t.Fatalf("body=%s", rec.Body.String())
	}
}

func TestDeleteKnowledgeDocumentRequiresCustomer(t *testing.T) {
	req := httptest.NewRequest(http.MethodDelete, "/admin/knowledge/documents/doc-1", nil)
	rec := httptest.NewRecorder()
	NewHandler(nil).Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "customer_id") {
		t.Fatalf("body=%s", rec.Body.String())
	}
}

func TestCreateMemoryValidatesBeforeStore(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/admin/memories", strings.NewReader(`{"customer_id":"c","user_id":"u"}`))
	rec := httptest.NewRecorder()
	NewHandler(nil).Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "content") {
		t.Fatalf("body=%s", rec.Body.String())
	}
}

func TestCreatePreferenceValidatesBeforeStore(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/admin/preferences", strings.NewReader(`{"customer_id":"c","user_id":"u"}`))
	rec := httptest.NewRecorder()
	NewHandler(nil).Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "content") {
		t.Fatalf("body=%s", rec.Body.String())
	}
}

func TestListMemoriesRequiresScope(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/admin/memories?customer_id=c", nil)
	rec := httptest.NewRecorder()
	NewHandler(nil).Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "user_id") {
		t.Fatalf("body=%s", rec.Body.String())
	}
}

func TestListPreferencesRequiresScope(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/admin/preferences?customer_id=c", nil)
	rec := httptest.NewRecorder()
	NewHandler(nil).Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "user_id") {
		t.Fatalf("body=%s", rec.Body.String())
	}
}

func TestUpdateMemoryValidatesBeforeStore(t *testing.T) {
	req := httptest.NewRequest(http.MethodPut, "/admin/memories/mem-1", strings.NewReader(`{"customer_id":"c","user_id":"u"}`))
	rec := httptest.NewRecorder()
	NewHandler(nil).Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "content") {
		t.Fatalf("body=%s", rec.Body.String())
	}
}

func TestUpdatePreferenceValidatesBeforeStore(t *testing.T) {
	req := httptest.NewRequest(http.MethodPut, "/admin/preferences/pref-1", strings.NewReader(`{"customer_id":"c","user_id":"u"}`))
	rec := httptest.NewRecorder()
	NewHandler(nil).Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "content") {
		t.Fatalf("body=%s", rec.Body.String())
	}
}

func TestDeleteMemoryRequiresScope(t *testing.T) {
	req := httptest.NewRequest(http.MethodDelete, "/admin/memories/mem-1?customer_id=c", nil)
	rec := httptest.NewRecorder()
	NewHandler(nil).Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "user_id") {
		t.Fatalf("body=%s", rec.Body.String())
	}
}

func TestDeletePreferenceRequiresScope(t *testing.T) {
	req := httptest.NewRequest(http.MethodDelete, "/admin/preferences/pref-1?customer_id=c", nil)
	rec := httptest.NewRecorder()
	NewHandler(nil).Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "user_id") {
		t.Fatalf("body=%s", rec.Body.String())
	}
}

func TestListObservabilityEventsRequiresCustomer(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/admin/observability/events", nil)
	rec := httptest.NewRecorder()
	NewHandler(nil).Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "customer_id") {
		t.Fatalf("body=%s", rec.Body.String())
	}
}

func TestListOutboundIntentsRequiresCustomer(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/admin/outbound-intents", nil)
	rec := httptest.NewRecorder()
	NewHandler(nil).Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "customer_id") {
		t.Fatalf("body=%s", rec.Body.String())
	}
}

func TestCreateBroadcastValidatesBeforeStore(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/admin/broadcasts", strings.NewReader(`{"customer_id":"c","title":"t"}`))
	rec := httptest.NewRecorder()
	NewHandler(nil).Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "targets") {
		t.Fatalf("body=%s", rec.Body.String())
	}
}

func TestListBroadcastsRequiresCustomer(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/admin/broadcasts", nil)
	rec := httptest.NewRecorder()
	NewHandler(nil).Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "customer_id") {
		t.Fatalf("body=%s", rec.Body.String())
	}
}

func TestListBroadcastTargetsRequiresCustomer(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/admin/broadcasts/b-1/targets", nil)
	rec := httptest.NewRecorder()
	NewHandler(nil).Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "customer_id") {
		t.Fatalf("body=%s", rec.Body.String())
	}
}

func TestCancelBroadcastRequiresCustomer(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/admin/broadcasts/b-1/cancel", strings.NewReader(`{}`))
	rec := httptest.NewRecorder()
	NewHandler(nil).Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "customer_id") {
		t.Fatalf("body=%s", rec.Body.String())
	}
}

func TestRetentionValidatesJSONBeforeStore(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/admin/retention/run", strings.NewReader(`{"unknown":true}`))
	rec := httptest.NewRecorder()
	NewHandler(nil).Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestAdminTokenRequiredWhenConfigured(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/admin/workflows", strings.NewReader(`{"name":"wf","version":1}`))
	rec := httptest.NewRecorder()
	NewHandler(nil).WithAdminToken("secret").Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	req = httptest.NewRequest(http.MethodPost, "/admin/workflows", strings.NewReader(`{"version":0}`))
	req.Header.Set("Authorization", "Bearer secret")
	rec = httptest.NewRecorder()
	NewHandler(nil).WithAdminToken("secret").Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestACPTokenRequiredWhenConfigured(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/acp/agents", nil)
	rec := httptest.NewRecorder()
	NewHandler(nil).WithACPToken("secret").Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	req = httptest.NewRequest(http.MethodGet, "/acp/agents", nil)
	req.Header.Set("Authorization", "Bearer secret")
	rec = httptest.NewRecorder()
	NewHandler(nil).WithACPToken("secret").Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestRequireAuthRejectsUnconfiguredTokens(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/acp/agents", nil)
	rec := httptest.NewRecorder()
	NewHandler(nil).WithRequireAuth(true).Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestRequestIDResponseHeader(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	req.Header.Set("X-Request-ID", "req-1")
	rec := httptest.NewRecorder()
	NewHandler(nil).Routes().ServeHTTP(rec, req)
	if rec.Header().Get("X-Request-ID") != "req-1" {
		t.Fatalf("missing response request id")
	}
}

func TestSecurityHeaders(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	NewHandler(nil).Routes().ServeHTTP(rec, req)
	if rec.Header().Get("X-Content-Type-Options") != "nosniff" || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("headers=%#v", rec.Header())
	}
}

func TestReassignSessionValidatesRouteAndBody(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/acp/sessions/route-session/reassign", strings.NewReader(`{"agent_instance_id":"agent-2"}`))
	req.Header.Set("X-Customer-ID", "c")
	req.Header.Set("X-User-ID", "u")
	req.Header.Set("X-Agent-Instance-ID", "agent-1")
	req.Header.Set("X-Session-ID", "other-session")
	req.Header.Set("X-Request-ID", "r")
	rec := httptest.NewRecorder()
	NewHandler(nil).Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "does not match") {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodPost, "/acp/sessions/route-session/reassign", strings.NewReader(`{"reason":"handoff"}`))
	req.Header.Set("X-Customer-ID", "c")
	req.Header.Set("X-User-ID", "u")
	req.Header.Set("X-Agent-Instance-ID", "agent-1")
	req.Header.Set("X-Session-ID", "route-session")
	req.Header.Set("X-Request-ID", "r")
	rec = httptest.NewRecorder()
	NewHandler(nil).Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "agent_instance_id is required") {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestRequiredContextAcceptsOptionalChannelHeaders(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/acp/runs", nil)
	req.Header.Set("X-Customer-ID", "c")
	req.Header.Set("X-User-ID", "u")
	req.Header.Set("X-Agent-Instance-ID", "a")
	req.Header.Set("X-Session-ID", "s")
	req.Header.Set("X-Request-ID", "r")
	req.Header.Set("X-Idempotency-Key", "i")
	req.Header.Set("X-Channel-Type", "web")
	req.Header.Set("X-Channel-Conversation-ID", "conv")
	rec := httptest.NewRecorder()
	ctx, ok := requiredContext(rec, req, true)
	if !ok {
		t.Fatalf("unexpected failure: %s", rec.Body.String())
	}
	if ctx.ChannelType != "web" || ctx.ChannelConvID != "conv" {
		t.Fatalf("ctx=%#v", ctx)
	}
}

func TestExistingRunContextRequiresMatchingRunID(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/acp/runs/route-run/cancel", nil)
	req.SetPathValue("run_id", "route-run")
	req.Header.Set("X-Customer-ID", "c")
	req.Header.Set("X-User-ID", "u")
	req.Header.Set("X-Agent-Instance-ID", "a")
	req.Header.Set("X-Session-ID", "s")
	req.Header.Set("X-Request-ID", "r")
	req.Header.Set("X-Run-ID", "other-run")
	rec := httptest.NewRecorder()
	_, ok := existingRunContext(rec, req)
	if ok {
		t.Fatalf("expected mismatch to fail")
	}
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "does not match") {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestListRunArtifactsRequiresRunContext(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/acp/runs/run-1/artifacts", nil)
	rec := httptest.NewRecorder()
	NewHandler(nil).Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "X-Customer-ID") {
		t.Fatalf("body=%s", rec.Body.String())
	}
}

func TestRunStatusRequiresCustomer(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/acp/runs/run-1", nil)
	rec := httptest.NewRecorder()
	NewHandler(nil).Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "X-Customer-ID") {
		t.Fatalf("body=%s", rec.Body.String())
	}
}

func TestEventsRequireCustomer(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/acp/runs/run-1/events", nil)
	rec := httptest.NewRecorder()
	NewHandler(nil).Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "X-Customer-ID") {
		t.Fatalf("body=%s", rec.Body.String())
	}
}

func TestRunTraceRequiresRunContext(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/acp/runs/run-1/trace", nil)
	rec := httptest.NewRecorder()
	NewHandler(nil).Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "X-Customer-ID") {
		t.Fatalf("body=%s", rec.Body.String())
	}
}

func TestListArtifactRepresentationsRequiresCustomer(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/acp/artifacts/art-1/representations", nil)
	rec := httptest.NewRecorder()
	NewHandler(nil).Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "X-Customer-ID") {
		t.Fatalf("body=%s", rec.Body.String())
	}
}

func TestOutboundIntentStatusValidatesJSONBeforeStore(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/acp/outbound-intents/out-1/status", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer secret")
	req.Header.Set("X-Customer-ID", "c")
	rec := httptest.NewRecorder()
	NewHandler(nil).WithACPToken("secret").Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "status") {
		t.Fatalf("body=%s", rec.Body.String())
	}
}

func TestOutboundIntentStatusRequiresCustomer(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/acp/outbound-intents/out-1/status", strings.NewReader(`{"status":"sent"}`))
	rec := httptest.NewRecorder()
	NewHandler(nil).Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "X-Customer-ID") {
		t.Fatalf("body=%s", rec.Body.String())
	}
}

func TestOutboundIntentStatusRejectsInvalidCallbackStatus(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/acp/outbound-intents/out-1/status", strings.NewReader(`{"status":"queued"}`))
	req.Header.Set("X-Customer-ID", "c")
	rec := httptest.NewRecorder()
	NewHandler(nil).Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "invalid outbound intent status") {
		t.Fatalf("body=%s", rec.Body.String())
	}
}
