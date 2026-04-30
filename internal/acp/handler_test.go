package acp

import (
	"context"
	"duraclaw/internal/db"
	"duraclaw/internal/mcp"
	"duraclaw/internal/observability"
	"os"

	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/pashagolub/pgxmock/v4"
)

type adminMCPClient struct{}

func readHandlerSources(t *testing.T, names ...string) string {
	t.Helper()
	var out strings.Builder
	for _, name := range names {
		raw, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		out.Write(raw)
		out.WriteByte('\n')
	}
	return out.String()
}

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

func TestGeneratedArtifactRouteIsRegistered(t *testing.T) {
	src := readHandlerSources(t, "routes.go", "handler.go")
	for _, want := range []string{
		`POST /acp/runs/{run_id}/artifacts/generate`,
		`POST /admin/media/generate`,
		"GenerateMediaArtifact",
		`AddEvent(r.Context(), run.ID, "artifact.generated"`,
	} {
		if !strings.Contains(src, want) {
			t.Fatalf("handler missing generated artifact hook %q", want)
		}
	}
}

func TestEventsRouteSupportsFollowSSE(t *testing.T) {
	raw, err := os.ReadFile("handler.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(raw)
	for _, want := range []string{
		`r.URL.Query().Get("follow")`,
		"writeSSEEvent",
		"keepalive",
		"EventsPage(r.Context(), run.ID, after, limit)",
	} {
		if !strings.Contains(src, want) {
			t.Fatalf("handler missing follow SSE hook %q", want)
		}
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

func TestAgentInstanceVersionImportExportRoutesExist(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/admin/agent-instances/a1/versions/import", strings.NewReader(`agent_instance_id: other`))
	req.Header.Set("Content-Type", "application/yaml")
	rec := httptest.NewRecorder()
	NewHandler(nil).Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "does not match route") {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/admin/agent-instances/a1/versions/v1/export", nil)
	rec = httptest.NewRecorder()
	NewHandler(nil).Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestAgentInstanceVersionImportRejectsOversizedBody(t *testing.T) {
	body := strings.Repeat("x", maxJSONBodyBytes+1)
	req := httptest.NewRequest(http.MethodPost, "/admin/agent-instances/a1/versions/import", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/yaml")
	rec := httptest.NewRecorder()
	NewHandler(nil).Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
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

func TestListUserSchedulerJobsRequiresUserScope(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/acp/scheduler/jobs?customer_id=c", nil)
	rec := httptest.NewRecorder()
	NewHandler(nil).Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "user_id") {
		t.Fatalf("body=%s", rec.Body.String())
	}
}

func TestUpdateUserSchedulerJobValidatesInput(t *testing.T) {
	req := httptest.NewRequest(http.MethodPatch, "/acp/scheduler/jobs/job-1", strings.NewReader(`{"customer_id":"c","user_id":"u","input":{"parts":"bad"}}`))
	req.Header.Set("X-Customer-ID", "c")
	req.Header.Set("X-User-ID", "u")
	rec := httptest.NewRecorder()
	NewHandler(nil).Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "parts") {
		t.Fatalf("body=%s", rec.Body.String())
	}
}

func TestUpdateUserSchedulerJobValidatesScheduleWithExplicitNextRun(t *testing.T) {
	req := httptest.NewRequest(http.MethodPatch, "/acp/scheduler/jobs/job-1", strings.NewReader(`{"customer_id":"c","user_id":"u","schedule":"not cron","next_run_at":"2030-01-01T00:00:00Z"}`))
	req.Header.Set("X-Customer-ID", "c")
	req.Header.Set("X-User-ID", "u")
	rec := httptest.NewRecorder()
	NewHandler(nil).Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "expected exactly 5 fields") {
		t.Fatalf("body=%s", rec.Body.String())
	}
}

func TestUserSchedulerJobRejectsHeaderMismatch(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/acp/scheduler/jobs?customer_id=c&user_id=other", nil)
	req.Header.Set("X-Customer-ID", "c")
	req.Header.Set("X-User-ID", "u")
	rec := httptest.NewRecorder()
	NewHandler(nil).Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestDeleteUserSchedulerJobRequiresUserScope(t *testing.T) {
	req := httptest.NewRequest(http.MethodDelete, "/acp/scheduler/jobs/job-1?customer_id=c", nil)
	rec := httptest.NewRecorder()
	NewHandler(nil).Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "user_id") {
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

func TestListUserBackgroundRunsRequiresUserScope(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/acp/background-runs?customer_id=c", nil)
	rec := httptest.NewRecorder()
	NewHandler(nil).Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "user_id") {
		t.Fatalf("body=%s", rec.Body.String())
	}
}

func TestCancelUserBackgroundRunRequiresUserScope(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/acp/background-runs/run-1/cancel", strings.NewReader(`{"customer_id":"c"}`))
	rec := httptest.NewRecorder()
	NewHandler(nil).Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "user_id") {
		t.Fatalf("body=%s", rec.Body.String())
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

func TestAdminMCPToolsCanBeFilteredByAccessRule(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()
	now := time.Now().UTC()
	mock.ExpectQuery("FROM mcp_tool_access_rules").
		WithArgs("c1", "a1", "u1", "srv").
		WillReturnRows(pgxmock.NewRows([]string{"customer_id", "agent_instance_id", "user_id", "server_name", "allowed_tools", "denied_tools", "metadata", "updated_at"}).
			AddRow("c1", "a1", "u1", "srv", []byte(`["other"]`), []byte(`["blocked"]`), []byte(`{}`), now))

	manager := mcp.NewManager()
	manager.Register("srv", adminMCPClient{})
	handler := NewHandler(db.NewStore(mock)).WithMCPManager(manager).Routes()
	req := httptest.NewRequest(http.MethodGet, "/admin/mcp/servers/srv/tools?customer_id=c1&agent_instance_id=a1&user_id=u1", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), `"lookup"`) {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
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

func TestListUserReminderSubscriptionsRequiresUserScope(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/acp/reminders?customer_id=c", nil)
	rec := httptest.NewRecorder()
	NewHandler(nil).Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "user_id") {
		t.Fatalf("body=%s", rec.Body.String())
	}
}

func TestUpdateUserReminderSubscriptionValidatesSchedule(t *testing.T) {
	req := httptest.NewRequest(http.MethodPatch, "/acp/reminders/sub-1", strings.NewReader(`{"customer_id":"c","user_id":"u","schedule":" "}`))
	req.Header.Set("X-Customer-ID", "c")
	req.Header.Set("X-User-ID", "u")
	rec := httptest.NewRecorder()
	NewHandler(nil).Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "schedule") {
		t.Fatalf("body=%s", rec.Body.String())
	}
}

func TestUpdateUserReminderSubscriptionValidatesScheduleWithExplicitNextRun(t *testing.T) {
	req := httptest.NewRequest(http.MethodPatch, "/acp/reminders/sub-1", strings.NewReader(`{"customer_id":"c","user_id":"u","schedule":"not cron","next_run_at":"2030-01-01T00:00:00Z"}`))
	req.Header.Set("X-Customer-ID", "c")
	req.Header.Set("X-User-ID", "u")
	rec := httptest.NewRecorder()
	NewHandler(nil).Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "expected exactly 5 fields") {
		t.Fatalf("body=%s", rec.Body.String())
	}
}

func TestUserReminderRejectsHeaderMismatch(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/acp/reminders?customer_id=c&user_id=other", nil)
	req.Header.Set("X-Customer-ID", "c")
	req.Header.Set("X-User-ID", "u")
	rec := httptest.NewRecorder()
	NewHandler(nil).Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestDeleteUserReminderSubscriptionRequiresUserScope(t *testing.T) {
	req := httptest.NewRequest(http.MethodDelete, "/acp/reminders/sub-1?customer_id=c", nil)
	rec := httptest.NewRecorder()
	NewHandler(nil).Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "user_id") {
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

func TestStartRunChecksIdempotencyBeforeProfileRefresh(t *testing.T) {
	raw, err := os.ReadFile("handler.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(raw)
	start := strings.Index(src, "func (h *Handler) startRun")
	if start < 0 {
		t.Fatal("startRun not found")
	}
	body := src[start:]
	idempotency := strings.Index(body, "RunByIdempotencyKey")
	refresh := strings.Index(body, "refreshUserProfile")
	create := strings.Index(body, "CreateRun")
	if idempotency < 0 || refresh < 0 || create < 0 {
		t.Fatal("startRun missing idempotency, profile refresh, or create call")
	}
	if !(idempotency < refresh && refresh < create) {
		t.Fatalf("unexpected startRun order: idempotency=%d refresh=%d create=%d", idempotency, refresh, create)
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

func TestDecodeJSONRejectsMultipleAndOversizedBodies(t *testing.T) {
	var payload map[string]any
	req := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(`{} {}`))
	rec := httptest.NewRecorder()
	if err := decodeJSON(rec, req, &payload); err == nil || !strings.Contains(err.Error(), "single JSON") {
		t.Fatalf("expected multiple value error, got %v", err)
	}

	req = httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(`{"x":"`+strings.Repeat("a", maxJSONBodyBytes)+`"}`))
	rec = httptest.NewRecorder()
	if err := decodeJSON(rec, req, &payload); err == nil {
		t.Fatalf("expected oversized body error")
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

func TestCreatePolicyPackVersionValidatesPayloadBeforeStore(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/admin/policy-packs/pack-1/versions", strings.NewReader(`{"version":0}`))
	rec := httptest.NewRecorder()
	NewHandler(nil).Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "version") {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodPost, "/admin/policy-packs/pack-1/versions", strings.NewReader(`{"version":2,"status":"archived"}`))
	rec = httptest.NewRecorder()
	NewHandler(nil).Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "status") {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestSetPolicyPackStatusValidatesPayloadBeforeStore(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/admin/policy-packs/pack-1/status", strings.NewReader(`{"status":"archived"}`))
	rec := httptest.NewRecorder()
	NewHandler(nil).Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "status") {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestPolicyPackHistoryRoutesAreRegistered(t *testing.T) {
	src := readHandlerSources(t, "routes.go", "handler.go")
	for _, want := range []string{
		`GET /admin/policy-packs/{pack_id}/versions`,
		`GET /admin/policy-packs/{pack_id}/diff`,
		"PolicyPackVersions",
		"PolicyPackDiff",
	} {
		if !strings.Contains(src, want) {
			t.Fatalf("handler missing policy history hook %q", want)
		}
	}
}

func TestPolicyPackDiffRequiresTarget(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/admin/policy-packs/pack-1/diff", nil)
	rec := httptest.NewRecorder()
	NewHandler(nil).Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "to_policy_pack_id") {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
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

func TestRetentionRunsAgainstStore(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()
	mock.ExpectExec("UPDATE artifacts").WithArgs(pgxmock.AnyArg()).WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	mock.ExpectExec("DELETE FROM run_events").WithArgs(pgxmock.AnyArg()).WillReturnResult(pgxmock.NewResult("DELETE", 2))
	mock.ExpectExec("DELETE FROM async_outbox").WithArgs(pgxmock.AnyArg()).WillReturnResult(pgxmock.NewResult("DELETE", 3))
	mock.ExpectExec("DELETE FROM async_write_jobs").WithArgs(pgxmock.AnyArg()).WillReturnResult(pgxmock.NewResult("DELETE", 4))
	mock.ExpectExec("DELETE FROM observability_events").WithArgs(pgxmock.AnyArg()).WillReturnResult(pgxmock.NewResult("DELETE", 5))
	mock.ExpectExec("DELETE FROM broadcasts").WithArgs(pgxmock.AnyArg()).WillReturnResult(pgxmock.NewResult("DELETE", 6))

	req := httptest.NewRequest(http.MethodPost, "/admin/retention/run", strings.NewReader(`{"artifact_days":1,"event_days":1,"outbox_days":1,"async_write_days":1,"observability_days":1,"broadcast_days":1}`))
	rec := httptest.NewRecorder()
	NewHandler(db.NewStore(mock)).Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"broadcasts_deleted":6`) {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestRuntimeLimitRoutesUseStore(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()
	now := time.Now().UTC()
	active, queued, buffer := 2, 3, 25
	mock.ExpectExec("INSERT INTO customers").WithArgs("c1").WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectQuery("INSERT INTO customer_runtime_limits").
		WithArgs("c1", pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), []byte(`{}`)).
		WillReturnRows(pgxmock.NewRows([]string{
			"customer_id", "max_active_runs", "max_queued_runs", "max_workflow_runs", "max_background_runs",
			"async_buffer_size", "max_async_payload_bytes", "async_degrade_threshold_bytes",
			"max_daily_tokens", "max_weekly_tokens", "max_monthly_tokens", "max_daily_model_cost_micros", "max_weekly_model_cost_micros", "max_monthly_model_cost_micros",
			"metadata", "updated_at",
		}).AddRow("c1", &active, &queued, nil, nil, &buffer, nil, nil, nil, nil, nil, nil, nil, nil, []byte(`{}`), now))
	req := httptest.NewRequest(http.MethodPut, "/admin/runtime-limits/customer/c1", strings.NewReader(`{"max_active_runs":2,"max_queued_runs":3,"async_buffer_size":25}`))
	rec := httptest.NewRecorder()
	NewHandler(db.NewStore(mock)).Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"customer_id":"c1"`) {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}

	mock.ExpectQuery("SELECT customer_id").WithArgs("c1").
		WillReturnRows(pgxmock.NewRows([]string{
			"customer_id", "max_active_runs", "max_queued_runs", "max_workflow_runs", "max_background_runs",
			"async_buffer_size", "max_async_payload_bytes", "async_degrade_threshold_bytes",
			"max_daily_tokens", "max_weekly_tokens", "max_monthly_tokens", "max_daily_model_cost_micros", "max_weekly_model_cost_micros", "max_monthly_model_cost_micros",
			"metadata", "updated_at",
		}).AddRow("c1", &active, &queued, nil, nil, &buffer, nil, nil, nil, nil, nil, nil, nil, nil, []byte(`{}`), now))
	mock.ExpectQuery("SELECT customer_id").WithArgs("c1").
		WillReturnRows(pgxmock.NewRows([]string{
			"customer_id", "max_active_runs", "max_queued_runs", "max_workflow_runs", "max_background_runs",
			"async_buffer_size", "max_async_payload_bytes", "async_degrade_threshold_bytes",
			"max_daily_tokens", "max_weekly_tokens", "max_monthly_tokens", "max_daily_model_cost_micros", "max_weekly_model_cost_micros", "max_monthly_model_cost_micros",
			"metadata", "updated_at",
		}).AddRow("c1", &active, &queued, nil, nil, &buffer, nil, nil, nil, nil, nil, nil, nil, nil, []byte(`{}`), now))
	req = httptest.NewRequest(http.MethodGet, "/admin/runtime-limits/customer/c1", nil)
	rec = httptest.NewRecorder()
	NewHandler(db.NewStore(mock)).Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"effective"`) {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestUserRuntimeLimitAndUsageRoutesUseStore(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()
	now := time.Now().UTC()
	dailyTokens := 20
	mock.ExpectExec("INSERT INTO customers").WithArgs("c1").WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectExec("INSERT INTO users").WithArgs("c1", "u1").WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectQuery("INSERT INTO user_runtime_limits").
		WithArgs("c1", "u1", &dailyTokens, pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), []byte(`{}`)).
		WillReturnRows(pgxmock.NewRows([]string{
			"customer_id", "user_id", "max_daily_tokens", "max_weekly_tokens", "max_monthly_tokens", "max_daily_model_cost_micros", "max_weekly_model_cost_micros", "max_monthly_model_cost_micros", "metadata", "updated_at",
		}).AddRow("c1", "u1", &dailyTokens, nil, nil, nil, nil, nil, []byte(`{}`), now))
	req := httptest.NewRequest(http.MethodPut, "/admin/runtime-limits/customer/c1/users/u1", strings.NewReader(`{"max_daily_tokens":20}`))
	rec := httptest.NewRecorder()
	NewHandler(db.NewStore(mock)).Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"user_id":"u1"`) {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}

	mock.ExpectQuery("SELECT COALESCE\\(sum\\(input_tokens\\),0\\)").
		WithArgs("c1", pgxmock.AnyArg(), "u1").
		WillReturnRows(pgxmock.NewRows([]string{"input_tokens", "output_tokens", "total_tokens", "cost_micros"}).AddRow(int64(1), int64(2), int64(3), int64(4)))
	req = httptest.NewRequest(http.MethodGet, "/admin/usage/model?customer_id=c1&user_id=u1&period=daily", nil)
	rec = httptest.NewRecorder()
	NewHandler(db.NewStore(mock)).Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"total_tokens":3`) {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestListOutboundIntentsUsesStore(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()
	now := time.Now().UTC()
	outboxID := int64(7)
	mock.ExpectQuery("SELECT id").WithArgs("c1", 100, "sent_to_nexus").
		WillReturnRows(pgxmock.NewRows([]string{"id", "customer_id", "user_id", "session_id", "run_id", "intent_type", "payload", "status", "outbox_id", "created_at", "updated_at"}).
			AddRow("intent-1", "c1", "u1", "s1", nil, "email", []byte(`{"ok":true}`), "sent_to_nexus", &outboxID, now, now))
	req := httptest.NewRequest(http.MethodGet, "/admin/outbound-intents?customer_id=c1&status=sent", nil)
	rec := httptest.NewRecorder()
	NewHandler(db.NewStore(mock)).Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"intent-1"`) {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
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

func TestQueryLimitBounds(t *testing.T) {
	cases := []struct {
		raw      string
		fallback int
		want     int
	}{
		{raw: "", fallback: 25, want: 25},
		{raw: "10", fallback: 25, want: 10},
		{raw: "0", fallback: 25, want: 25},
		{raw: "501", fallback: 25, want: 25},
		{raw: "bad", fallback: 25, want: 25},
	}
	for _, tc := range cases {
		req := httptest.NewRequest(http.MethodGet, "/?limit="+tc.raw, nil)
		if got := queryLimit(req, tc.fallback); got != tc.want {
			t.Fatalf("queryLimit(%q)=%d want %d", tc.raw, got, tc.want)
		}
	}
}

func TestMustJSONString(t *testing.T) {
	if got := mustJSONString(map[string]any{"ok": true}); got != `{"ok":true}` {
		t.Fatalf("got %q", got)
	}
}
