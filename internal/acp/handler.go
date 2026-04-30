package acp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"duraclaw/internal/agentconfig"
	"duraclaw/internal/db"
	"duraclaw/internal/embeddings"
	"duraclaw/internal/knowledge"
	"duraclaw/internal/mcp"
	"duraclaw/internal/observability"
	"duraclaw/internal/policy"
	"duraclaw/internal/profiles"
	"duraclaw/internal/providers"
	"duraclaw/internal/scheduler"
	"duraclaw/internal/tools"
	"duraclaw/internal/workflow"

	"github.com/jackc/pgx/v5"
)

const maxJSONBodyBytes = 2 << 20
const defaultInterruptWindow = 2 * time.Second
const defaultMaxRefinementDepth = 2

type Handler struct {
	store            *db.Store
	workflow         *workflow.Executor
	artifactPolicy   policy.ArtifactRule
	adminToken       string
	acpToken         string
	requireAuth      bool
	counters         *observability.Counters
	embedder         embeddings.Provider
	mcpManager       *mcp.Manager
	providers        *providers.Registry
	modelConfig      providers.ModelConfig
	mediaBlobStore   tools.MediaBlobStore
	profileRetriever profiles.Retriever
	logger           *slog.Logger
	interruptWindow  time.Duration
	maxRefineDepth   int
}

func NewHandler(store *db.Store) *Handler {
	return &Handler{
		store:           store,
		workflow:        workflow.NewExecutor(store),
		counters:        observability.NewCounters(),
		logger:          observability.Logger(),
		interruptWindow: defaultInterruptWindow,
		maxRefineDepth:  defaultMaxRefinementDepth,
		artifactPolicy: policy.ArtifactRule{
			MaxSizeBytes: 100 << 20,
			MediaTypes: map[string]bool{
				"audio/mpeg": true, "audio/ogg": true, "audio/wav": true,
				"image/jpeg": true, "image/png": true, "image/webp": true,
				"application/pdf": true, "text/plain": true,
			},
		},
	}
}

func (h *Handler) WithRunRefinement(window time.Duration, maxDepth int) *Handler {
	if window > 0 {
		h.interruptWindow = window
	}
	if maxDepth >= 0 {
		h.maxRefineDepth = maxDepth
	}
	return h
}

func (h *Handler) WithAdminToken(token string) *Handler {
	h.adminToken = token
	return h
}

func (h *Handler) WithACPToken(token string) *Handler {
	h.acpToken = token
	return h
}

func (h *Handler) WithRequireAuth(required bool) *Handler {
	h.requireAuth = required
	return h
}

func (h *Handler) WithCounters(counters *observability.Counters) *Handler {
	h.counters = counters
	return h
}

func (h *Handler) WithEmbedder(embedder embeddings.Provider) *Handler {
	h.embedder = embedder
	if h.workflow != nil {
		h.workflow.WithEmbedder(embedder)
	}
	return h
}

func (h *Handler) WithLogger(logger *slog.Logger) *Handler {
	if logger != nil {
		h.logger = logger
	}
	return h
}

func (h *Handler) WithMCPManager(manager *mcp.Manager) *Handler {
	h.mcpManager = manager
	return h
}

func (h *Handler) WithProviders(registry *providers.Registry, cfg providers.ModelConfig) *Handler {
	h.providers = registry
	h.modelConfig = cfg
	if h.workflow != nil {
		h.workflow.WithProviders(registry, cfg)
	}
	return h
}

func (h *Handler) WithMediaBlobStore(store tools.MediaBlobStore) *Handler {
	h.mediaBlobStore = store
	return h
}

func (h *Handler) WithProfileRetriever(retriever profiles.Retriever) *Handler {
	h.profileRetriever = retriever
	return h
}

func (h *Handler) createAgentInstanceVersion(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		CustomerID          string         `json:"customer_id"`
		Version             int            `json:"version"`
		Name                string         `json:"name"`
		ModelConfig         map[string]any `json:"model_config"`
		SystemInstructions  string         `json:"system_instructions"`
		ToolConfig          map[string]any `json:"tool_config"`
		MCPConfig           map[string]any `json:"mcp_config"`
		WorkflowConfig      map[string]any `json:"workflow_config"`
		PolicyConfig        map[string]any `json:"policy_config"`
		ProfileConfig       map[string]any `json:"profile_config"`
		Metadata            map[string]any `json:"metadata"`
		ActivateImmediately bool           `json:"activate_immediately"`
	}
	if err := decodeJSON(w, r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if payload.CustomerID == "" || r.PathValue("agent_instance_id") == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("customer_id and agent_instance_id are required"))
		return
	}
	version, err := h.store.CreateAgentInstanceVersion(r.Context(), db.AgentInstanceVersionSpec{
		CustomerID:          payload.CustomerID,
		AgentInstanceID:     r.PathValue("agent_instance_id"),
		Version:             payload.Version,
		Name:                payload.Name,
		ModelConfig:         payload.ModelConfig,
		SystemInstructions:  payload.SystemInstructions,
		ToolConfig:          payload.ToolConfig,
		MCPConfig:           payload.MCPConfig,
		WorkflowConfig:      payload.WorkflowConfig,
		PolicyConfig:        payload.PolicyConfig,
		ProfileConfig:       payload.ProfileConfig,
		Metadata:            payload.Metadata,
		ActivateImmediately: payload.ActivateImmediately,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusCreated, version)
}

func (h *Handler) listAgentInstanceVersions(w http.ResponseWriter, r *http.Request) {
	customerID := r.URL.Query().Get("customer_id")
	if customerID == "" || r.PathValue("agent_instance_id") == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("customer_id and agent_instance_id are required"))
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	versions, err := h.store.ListAgentInstanceVersions(r.Context(), customerID, r.PathValue("agent_instance_id"), limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"versions": versions})
}

func (h *Handler) importAgentInstanceVersion(w http.ResponseWriter, r *http.Request) {
	format := r.URL.Query().Get("format")
	if format == "" {
		format = contentFormat(r.Header.Get("Content-Type"))
	}
	limited := http.MaxBytesReader(w, r.Body, maxJSONBodyBytes)
	doc, err := agentconfig.Decode(limited, format)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if doc.AgentInstanceID == "" {
		doc.AgentInstanceID = r.PathValue("agent_instance_id")
	}
	if doc.AgentInstanceID != r.PathValue("agent_instance_id") {
		writeError(w, http.StatusBadRequest, fmt.Errorf("agent_instance_id does not match route"))
		return
	}
	if doc.CustomerID == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("customer_id is required"))
		return
	}
	if h.store == nil {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("store is unavailable"))
		return
	}
	version, err := agentconfig.Import(r.Context(), h.store, doc)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusCreated, version)
}

func (h *Handler) exportAgentInstanceVersion(w http.ResponseWriter, r *http.Request) {
	if h.store == nil {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("store is unavailable"))
		return
	}
	doc, err := agentconfig.Export(r.Context(), h.store, r.PathValue("version_id"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if doc.AgentInstanceID != r.PathValue("agent_instance_id") {
		writeError(w, http.StatusNotFound, fmt.Errorf("agent instance version not found"))
		return
	}
	format := r.URL.Query().Get("format")
	if format == "" {
		format = "json"
	}
	raw, err := agentconfig.Encode(doc, format)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if strings.EqualFold(format, "yaml") || strings.EqualFold(format, "yml") {
		w.Header().Set("Content-Type", "application/yaml")
	} else {
		w.Header().Set("Content-Type", "application/json")
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(append(raw, '\n'))
}

func (h *Handler) activateAgentInstanceVersion(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		CustomerID string `json:"customer_id"`
	}
	if err := decodeJSON(w, r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if payload.CustomerID == "" || r.PathValue("agent_instance_id") == "" || r.PathValue("version_id") == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("customer_id, agent_instance_id, and version_id are required"))
		return
	}
	version, err := h.store.ActivateAgentInstanceVersion(r.Context(), payload.CustomerID, r.PathValue("agent_instance_id"), r.PathValue("version_id"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, version)
}

func (h *Handler) createWorkflow(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		Name         string         `json:"name"`
		Version      int            `json:"version"`
		Description  string         `json:"description"`
		WhenToUse    string         `json:"when_to_use"`
		InputSchema  map[string]any `json:"input_schema"`
		OutputSchema map[string]any `json:"output_schema"`
	}
	if err := decodeJSON(w, r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if payload.Name == "" || payload.Version <= 0 {
		writeError(w, http.StatusBadRequest, fmt.Errorf("name and positive version are required"))
		return
	}
	id, err := h.store.CreateWorkflowDefinition(r.Context(), payload.Name, payload.Version, payload.Description, payload.WhenToUse, payload.InputSchema, payload.OutputSchema)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"workflow_id": id})
}

func (h *Handler) listWorkflows(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	defs, err := h.store.ListWorkflowDefinitions(r.Context(), limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"workflows": defs})
}

func (h *Handler) assignWorkflow(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		CustomerID      string `json:"customer_id"`
		AgentInstanceID string `json:"agent_instance_id"`
		Enabled         *bool  `json:"enabled"`
	}
	if err := decodeJSON(w, r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if payload.CustomerID == "" || payload.AgentInstanceID == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("customer_id and agent_instance_id are required"))
		return
	}
	enabled := true
	if payload.Enabled != nil {
		enabled = *payload.Enabled
	}
	if err := h.store.AssignWorkflow(r.Context(), r.PathValue("workflow_id"), payload.CustomerID, payload.AgentInstanceID, enabled); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"workflow_id": r.PathValue("workflow_id"), "enabled": enabled})
}

func (h *Handler) upsertWorkflowNode(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		NodeType      string         `json:"node_type"`
		Config        map[string]any `json:"config"`
		RetryPolicy   map[string]any `json:"retry_policy"`
		TimeoutPolicy map[string]any `json:"timeout_policy"`
	}
	if err := decodeJSON(w, r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if r.PathValue("workflow_id") == "" || r.PathValue("node_key") == "" || payload.NodeType == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("workflow_id, node_key, and node_type are required"))
		return
	}
	if payload.Config == nil {
		payload.Config = map[string]any{}
	}
	if payload.RetryPolicy == nil {
		payload.RetryPolicy = map[string]any{}
	}
	if payload.TimeoutPolicy == nil {
		payload.TimeoutPolicy = map[string]any{}
	}
	if err := h.store.UpsertWorkflowNode(r.Context(), r.PathValue("workflow_id"), r.PathValue("node_key"), payload.NodeType, payload.Config, payload.RetryPolicy, payload.TimeoutPolicy); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"workflow_id": r.PathValue("workflow_id"), "node_key": r.PathValue("node_key")})
}

func (h *Handler) listWorkflowNodes(w http.ResponseWriter, r *http.Request) {
	if r.PathValue("workflow_id") == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("workflow_id is required"))
		return
	}
	nodes, err := h.store.WorkflowNodes(r.Context(), r.PathValue("workflow_id"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"nodes": nodes})
}

func (h *Handler) upsertWorkflowEdge(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		FromNodeKey string         `json:"from_node_key"`
		ToNodeKey   string         `json:"to_node_key"`
		Condition   map[string]any `json:"condition"`
	}
	if err := decodeJSON(w, r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if r.PathValue("workflow_id") == "" || payload.FromNodeKey == "" || payload.ToNodeKey == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("workflow_id, from_node_key, and to_node_key are required"))
		return
	}
	if payload.Condition == nil {
		payload.Condition = map[string]any{}
	}
	if err := h.store.UpsertWorkflowEdge(r.Context(), r.PathValue("workflow_id"), payload.FromNodeKey, payload.ToNodeKey, payload.Condition); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"workflow_id": r.PathValue("workflow_id"), "from_node_key": payload.FromNodeKey, "to_node_key": payload.ToNodeKey})
}

func (h *Handler) listWorkflowEdges(w http.ResponseWriter, r *http.Request) {
	if r.PathValue("workflow_id") == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("workflow_id is required"))
		return
	}
	edges, err := h.store.WorkflowEdges(r.Context(), r.PathValue("workflow_id"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"edges": edges})
}

func (h *Handler) upsertAgentPolicy(w http.ResponseWriter, r *http.Request) {
	var payload db.AgentPolicy
	if err := decodeJSON(w, r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if payload.CustomerID == "" || payload.AgentInstanceID == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("customer_id and agent_instance_id are required"))
		return
	}
	if payload.ArtifactMaxSizeBytes <= 0 {
		payload.ArtifactMaxSizeBytes = h.artifactPolicy.MaxSizeBytes
	}
	if err := h.store.UpsertAgentPolicy(r.Context(), payload); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, payload)
}

func (h *Handler) getAgentPolicy(w http.ResponseWriter, r *http.Request) {
	customerID := r.URL.Query().Get("customer_id")
	agentInstanceID := r.URL.Query().Get("agent_instance_id")
	if customerID == "" || agentInstanceID == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("customer_id and agent_instance_id are required"))
		return
	}
	policy, err := h.store.AgentPolicy(r.Context(), customerID, agentInstanceID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if policy == nil {
		writeError(w, http.StatusNotFound, fmt.Errorf("agent policy not found"))
		return
	}
	writeJSON(w, http.StatusOK, policy)
}

func (h *Handler) createPolicyPack(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		Name       string `json:"name"`
		Version    int    `json:"version"`
		OwnerScope string `json:"owner_scope"`
	}
	if err := decodeJSON(w, r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if strings.TrimSpace(payload.Name) == "" || payload.Version <= 0 {
		writeError(w, http.StatusBadRequest, fmt.Errorf("name and positive version are required"))
		return
	}
	id, err := h.store.CreatePolicyPack(r.Context(), payload.Name, payload.Version, payload.OwnerScope)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"policy_pack_id": id})
}

func (h *Handler) listPolicyPacks(w http.ResponseWriter, r *http.Request) {
	packs, err := h.store.ListPolicyPacks(r.Context(), queryLimit(r, 100))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"policy_packs": packs})
}

func (h *Handler) createPolicyPackVersion(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		Version int    `json:"version"`
		Status  string `json:"status"`
	}
	if err := decodeJSON(w, r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if payload.Version <= 0 {
		writeError(w, http.StatusBadRequest, fmt.Errorf("positive version is required"))
		return
	}
	if payload.Status != "" && !validPolicyPackStatus(payload.Status) {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid policy pack status %q", payload.Status))
		return
	}
	id, err := h.store.CreatePolicyPackVersion(r.Context(), r.PathValue("pack_id"), payload.Version, payload.Status)
	if err != nil {
		writeError(w, statusForError(err), err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"policy_pack_id": id})
}

func (h *Handler) setPolicyPackStatus(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		Status string `json:"status"`
	}
	if err := decodeJSON(w, r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if !validPolicyPackStatus(payload.Status) {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid policy pack status %q", payload.Status))
		return
	}
	if err := h.store.SetPolicyPackStatus(r.Context(), r.PathValue("pack_id"), payload.Status); err != nil {
		writeError(w, statusForError(err), err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"policy_pack_id": r.PathValue("pack_id"), "status": payload.Status})
}

func (h *Handler) listPolicyPackVersions(w http.ResponseWriter, r *http.Request) {
	packs, err := h.store.PolicyPackVersions(r.Context(), r.PathValue("pack_id"))
	if err != nil {
		writeError(w, statusForError(err), err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"policy_packs": packs})
}

func (h *Handler) policyPackDiff(w http.ResponseWriter, r *http.Request) {
	to := r.URL.Query().Get("to_policy_pack_id")
	if to == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("to_policy_pack_id is required"))
		return
	}
	diff, err := h.store.PolicyPackDiff(r.Context(), r.PathValue("pack_id"), to)
	if err != nil {
		writeError(w, statusForError(err), err)
		return
	}
	writeJSON(w, http.StatusOK, diff)
}

func validPolicyPackStatus(status string) bool {
	return status == "active" || status == "disabled" || status == "draft"
}

func (h *Handler) upsertPolicyRule(w http.ResponseWriter, r *http.Request) {
	var payload db.PolicyRule
	if err := decodeJSON(w, r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	payload.PolicyPackID = r.PathValue("pack_id")
	payload.ID = r.PathValue("rule_id")
	if payload.RuleType == "" || payload.EnforcementMode == "" || payload.Action == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("rule_type, enforcement_mode, and action are required"))
		return
	}
	id, err := h.store.UpsertPolicyRule(r.Context(), payload)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"policy_rule_id": id})
}

func (h *Handler) listPolicyRules(w http.ResponseWriter, r *http.Request) {
	rules, err := h.store.ListPolicyRules(r.Context(), r.PathValue("pack_id"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"policy_rules": rules})
}

func (h *Handler) assignPolicyPack(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		CustomerID      string `json:"customer_id"`
		AgentInstanceID string `json:"agent_instance_id"`
		Enabled         *bool  `json:"enabled"`
	}
	if err := decodeJSON(w, r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	enabled := true
	if payload.Enabled != nil {
		enabled = *payload.Enabled
	}
	id, err := h.store.AssignPolicyPack(r.Context(), r.PathValue("pack_id"), payload.CustomerID, payload.AgentInstanceID, enabled)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"policy_assignment_id": id})
}

func (h *Handler) listPolicyEvaluations(w http.ResponseWriter, r *http.Request) {
	evals, err := h.store.ListPolicyEvaluations(r.Context(), r.URL.Query().Get("run_id"), queryLimit(r, 100))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"policy_evaluations": evals})
}

func (h *Handler) upsertCustomerRuntimeLimits(w http.ResponseWriter, r *http.Request) {
	var payload db.RuntimeLimits
	if err := decodeJSON(w, r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	payload.CustomerID = r.PathValue("customer_id")
	limits, err := h.store.UpsertCustomerRuntimeLimits(r.Context(), payload)
	if err != nil {
		writeError(w, statusForError(err), err)
		return
	}
	writeJSON(w, http.StatusOK, limits)
}

func (h *Handler) getCustomerRuntimeLimits(w http.ResponseWriter, r *http.Request) {
	limits, err := h.store.CustomerRuntimeLimits(r.Context(), r.PathValue("customer_id"))
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	effective, _ := h.store.EffectiveRuntimeLimits(r.Context(), r.PathValue("customer_id"), "")
	writeJSON(w, http.StatusOK, map[string]any{"limits": limits, "effective": effective})
}

func (h *Handler) upsertAgentInstanceRuntimeLimits(w http.ResponseWriter, r *http.Request) {
	var payload db.RuntimeLimits
	if err := decodeJSON(w, r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	payload.CustomerID = r.PathValue("customer_id")
	payload.AgentInstanceID = r.PathValue("agent_instance_id")
	limits, err := h.store.UpsertAgentInstanceRuntimeLimits(r.Context(), payload)
	if err != nil {
		writeError(w, statusForError(err), err)
		return
	}
	writeJSON(w, http.StatusOK, limits)
}

func (h *Handler) getAgentInstanceRuntimeLimits(w http.ResponseWriter, r *http.Request) {
	limits, err := h.store.AgentInstanceRuntimeLimits(r.Context(), r.PathValue("customer_id"), r.PathValue("agent_instance_id"))
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	effective, _ := h.store.EffectiveRuntimeLimits(r.Context(), r.PathValue("customer_id"), r.PathValue("agent_instance_id"))
	writeJSON(w, http.StatusOK, map[string]any{"limits": limits, "effective": effective})
}

func (h *Handler) upsertUserRuntimeLimits(w http.ResponseWriter, r *http.Request) {
	var payload db.RuntimeLimits
	if err := decodeJSON(w, r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	payload.CustomerID = r.PathValue("customer_id")
	payload.UserID = r.PathValue("user_id")
	limits, err := h.store.UpsertUserRuntimeLimits(r.Context(), payload)
	if err != nil {
		writeError(w, statusForError(err), err)
		return
	}
	writeJSON(w, http.StatusOK, limits)
}

func (h *Handler) getUserRuntimeLimits(w http.ResponseWriter, r *http.Request) {
	limits, err := h.store.UserRuntimeLimits(r.Context(), r.PathValue("customer_id"), r.PathValue("user_id"))
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"limits": limits})
}

func (h *Handler) getModelUsageSummary(w http.ResponseWriter, r *http.Request) {
	customerID := strings.TrimSpace(r.URL.Query().Get("customer_id"))
	if customerID == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("customer_id is required"))
		return
	}
	summary, err := h.store.ModelUsageSummary(
		r.Context(),
		customerID,
		r.URL.Query().Get("agent_instance_id"),
		r.URL.Query().Get("user_id"),
		r.URL.Query().Get("period"),
	)
	if err != nil {
		writeError(w, statusForError(err), err)
		return
	}
	writeJSON(w, http.StatusOK, summary)
}

func (h *Handler) ingestKnowledgeText(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		CustomerID string         `json:"customer_id"`
		Title      string         `json:"title"`
		Scope      string         `json:"scope"`
		SourceRef  string         `json:"source_ref"`
		Text       string         `json:"text"`
		Metadata   map[string]any `json:"metadata"`
	}
	if err := decodeJSON(w, r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if payload.CustomerID == "" || strings.TrimSpace(payload.Text) == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("customer_id and text are required"))
		return
	}
	ingester := knowledge.NewIngester(h.store).WithEmbedder(h.embedder)
	docID, chunks, err := ingester.IngestTextWithScope(r.Context(), payload.CustomerID, payload.Scope, payload.Title, payload.SourceRef, payload.Text, payload.Metadata)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"document_id": docID, "chunks": chunks})
}

func (h *Handler) listKnowledgeDocuments(w http.ResponseWriter, r *http.Request) {
	customerID := r.URL.Query().Get("customer_id")
	if customerID == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("customer_id is required"))
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	documents, err := h.store.ListKnowledgeDocumentsByScope(r.Context(), customerID, r.URL.Query().Get("scope"), limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"documents": documents})
}

func (h *Handler) listKnowledgeChunks(w http.ResponseWriter, r *http.Request) {
	if r.PathValue("document_id") == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("document_id is required"))
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	chunks, err := h.store.ListKnowledgeChunks(r.Context(), r.PathValue("document_id"), limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"chunks": chunks})
}

func (h *Handler) deleteKnowledgeDocument(w http.ResponseWriter, r *http.Request) {
	customerID := r.URL.Query().Get("customer_id")
	if r.PathValue("document_id") == "" || customerID == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("document_id and customer_id are required"))
		return
	}
	if err := h.store.DeleteKnowledgeDocument(r.Context(), r.PathValue("document_id"), customerID); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"document_id": r.PathValue("document_id"), "deleted": true})
}

func (h *Handler) createMemory(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		CustomerID string         `json:"customer_id"`
		UserID     string         `json:"user_id"`
		SessionID  string         `json:"session_id"`
		Type       string         `json:"memory_type"`
		Content    string         `json:"content"`
		Metadata   map[string]any `json:"metadata"`
	}
	if err := decodeJSON(w, r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if payload.CustomerID == "" || payload.UserID == "" || strings.TrimSpace(payload.Content) == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("customer_id, user_id, and content are required"))
		return
	}
	if payload.Type == "" {
		payload.Type = "fact"
	}
	id, err := h.store.AddMemory(r.Context(), payload.CustomerID, payload.UserID, payload.SessionID, payload.Type, payload.Content, payload.Metadata)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"memory_id": id})
}

func (h *Handler) listMemories(w http.ResponseWriter, r *http.Request) {
	customerID := r.URL.Query().Get("customer_id")
	userID := r.URL.Query().Get("user_id")
	if customerID == "" || userID == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("customer_id and user_id are required"))
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	memories, err := h.store.ListMemories(r.Context(), customerID, userID, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"memories": memories})
}

func (h *Handler) updateMemory(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		CustomerID string         `json:"customer_id"`
		UserID     string         `json:"user_id"`
		Type       string         `json:"memory_type"`
		Content    string         `json:"content"`
		Metadata   map[string]any `json:"metadata"`
	}
	if err := decodeJSON(w, r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if r.PathValue("memory_id") == "" || payload.CustomerID == "" || payload.UserID == "" || strings.TrimSpace(payload.Content) == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("memory_id, customer_id, user_id, and content are required"))
		return
	}
	if payload.Type == "" {
		payload.Type = "fact"
	}
	if err := h.store.UpdateMemory(r.Context(), r.PathValue("memory_id"), payload.CustomerID, payload.UserID, payload.Type, payload.Content, payload.Metadata); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"memory_id": r.PathValue("memory_id"), "updated": true})
}

func (h *Handler) deleteMemory(w http.ResponseWriter, r *http.Request) {
	customerID := r.URL.Query().Get("customer_id")
	userID := r.URL.Query().Get("user_id")
	if r.PathValue("memory_id") == "" || customerID == "" || userID == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("memory_id, customer_id, and user_id are required"))
		return
	}
	if err := h.store.DeleteMemory(r.Context(), r.PathValue("memory_id"), customerID, userID); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"memory_id": r.PathValue("memory_id"), "deleted": true})
}

func (h *Handler) createPreference(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		CustomerID string         `json:"customer_id"`
		UserID     string         `json:"user_id"`
		SessionID  string         `json:"session_id"`
		Category   string         `json:"category"`
		Content    string         `json:"content"`
		Condition  map[string]any `json:"condition"`
		Metadata   map[string]any `json:"metadata"`
	}
	if err := decodeJSON(w, r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if payload.CustomerID == "" || payload.UserID == "" || strings.TrimSpace(payload.Content) == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("customer_id, user_id, and content are required"))
		return
	}
	id, err := h.store.AddPreference(r.Context(), payload.CustomerID, payload.UserID, payload.SessionID, payload.Category, payload.Content, payload.Condition, payload.Metadata)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"preference_id": id})
}

func (h *Handler) listPreferences(w http.ResponseWriter, r *http.Request) {
	customerID := r.URL.Query().Get("customer_id")
	userID := r.URL.Query().Get("user_id")
	if customerID == "" || userID == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("customer_id and user_id are required"))
		return
	}
	preferences, err := h.store.ListPreferences(r.Context(), customerID, userID, queryLimit(r, 20))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"preferences": preferences})
}

func (h *Handler) updatePreference(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		CustomerID string         `json:"customer_id"`
		UserID     string         `json:"user_id"`
		Category   string         `json:"category"`
		Content    string         `json:"content"`
		Condition  map[string]any `json:"condition"`
		Metadata   map[string]any `json:"metadata"`
	}
	if err := decodeJSON(w, r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if r.PathValue("preference_id") == "" || payload.CustomerID == "" || payload.UserID == "" || strings.TrimSpace(payload.Content) == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("preference_id, customer_id, user_id, and content are required"))
		return
	}
	if err := h.store.UpdatePreference(r.Context(), r.PathValue("preference_id"), payload.CustomerID, payload.UserID, payload.Category, payload.Content, payload.Condition, payload.Metadata); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"preference_id": r.PathValue("preference_id"), "updated": true})
}

func (h *Handler) deletePreference(w http.ResponseWriter, r *http.Request) {
	customerID := r.URL.Query().Get("customer_id")
	userID := r.URL.Query().Get("user_id")
	if r.PathValue("preference_id") == "" || customerID == "" || userID == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("preference_id, customer_id, and user_id are required"))
		return
	}
	if err := h.store.DeletePreference(r.Context(), r.PathValue("preference_id"), customerID, userID); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"preference_id": r.PathValue("preference_id"), "deleted": true})
}

func (h *Handler) createReminderSubscription(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		CustomerID      string         `json:"customer_id"`
		UserID          string         `json:"user_id"`
		SessionID       string         `json:"session_id"`
		AgentInstanceID string         `json:"agent_instance_id"`
		Title           string         `json:"title"`
		Schedule        string         `json:"schedule"`
		Timezone        string         `json:"timezone"`
		Payload         map[string]any `json:"payload"`
		NextRunAt       time.Time      `json:"next_run_at"`
		Metadata        map[string]any `json:"metadata"`
	}
	if err := decodeJSON(w, r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if payload.CustomerID == "" || payload.UserID == "" || payload.SessionID == "" || payload.AgentInstanceID == "" || payload.Schedule == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("customer_id, user_id, session_id, agent_instance_id, and schedule are required"))
		return
	}
	if strings.HasPrefix(r.URL.Path, "/acp/") && !requireACPIdentityMatch(w, r, payload.CustomerID, payload.UserID) {
		return
	}
	if payload.NextRunAt.IsZero() {
		next, err := scheduler.Next(payload.Schedule, time.Now().UTC())
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		payload.NextRunAt = next
	}
	sub, err := h.store.CreateReminderSubscription(r.Context(), db.ReminderSubscriptionSpec{
		CustomerID: payload.CustomerID, UserID: payload.UserID, SessionID: payload.SessionID, AgentInstanceID: payload.AgentInstanceID,
		Title: payload.Title, Schedule: payload.Schedule, Timezone: payload.Timezone, Payload: payload.Payload, NextRunAt: payload.NextRunAt, Metadata: payload.Metadata,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusCreated, sub)
}

func (h *Handler) listReminderSubscriptions(w http.ResponseWriter, r *http.Request) {
	customerID := r.URL.Query().Get("customer_id")
	if customerID == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("customer_id is required"))
		return
	}
	subs, err := h.store.ListReminderSubscriptions(r.Context(), customerID, r.URL.Query().Get("user_id"), queryLimit(r, 100))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"reminder_subscriptions": subs})
}

func (h *Handler) updateReminderSubscription(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		CustomerID string `json:"customer_id"`
		Enabled    *bool  `json:"enabled"`
	}
	if err := decodeJSON(w, r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if payload.CustomerID == "" || payload.Enabled == nil || r.PathValue("subscription_id") == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("subscription_id, customer_id, and enabled are required"))
		return
	}
	if err := h.store.SetReminderSubscriptionEnabled(r.Context(), r.PathValue("subscription_id"), payload.CustomerID, *payload.Enabled); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"subscription_id": r.PathValue("subscription_id"), "enabled": *payload.Enabled})
}

func (h *Handler) listUserReminderSubscriptions(w http.ResponseWriter, r *http.Request) {
	customerID := r.URL.Query().Get("customer_id")
	userID := r.URL.Query().Get("user_id")
	if customerID == "" || userID == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("customer_id and user_id are required"))
		return
	}
	if !requireACPIdentityMatch(w, r, customerID, userID) {
		return
	}
	subs, err := h.store.ListReminderSubscriptions(r.Context(), customerID, userID, queryLimit(r, 100))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"reminders": subs})
}

func (h *Handler) updateUserReminderSubscription(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		CustomerID string         `json:"customer_id"`
		UserID     string         `json:"user_id"`
		Title      *string        `json:"title"`
		Schedule   *string        `json:"schedule"`
		Timezone   *string        `json:"timezone"`
		Payload    map[string]any `json:"payload"`
		NextRunAt  time.Time      `json:"next_run_at"`
		Metadata   map[string]any `json:"metadata"`
		Enabled    *bool          `json:"enabled"`
	}
	if err := decodeJSON(w, r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if r.PathValue("subscription_id") == "" || payload.CustomerID == "" || payload.UserID == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("subscription_id, customer_id, and user_id are required"))
		return
	}
	if !requireACPIdentityMatch(w, r, payload.CustomerID, payload.UserID) {
		return
	}
	if payload.Schedule != nil {
		schedule := strings.TrimSpace(*payload.Schedule)
		if schedule == "" {
			writeError(w, http.StatusBadRequest, fmt.Errorf("schedule cannot be empty"))
			return
		}
		payload.Schedule = &schedule
		if _, err := scheduler.Next(schedule, time.Now().UTC()); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		if payload.NextRunAt.IsZero() {
			next, _ := scheduler.Next(schedule, time.Now().UTC())
			payload.NextRunAt = next
		}
	}
	var nextRunAt *time.Time
	if !payload.NextRunAt.IsZero() {
		nextRunAt = &payload.NextRunAt
	}
	sub, err := h.store.UpdateUserReminderSubscription(r.Context(), r.PathValue("subscription_id"), payload.CustomerID, payload.UserID, db.ReminderSubscriptionUpdate{
		Title: payload.Title, Schedule: payload.Schedule, Timezone: payload.Timezone,
		Payload: payload.Payload, NextRunAt: nextRunAt, Metadata: payload.Metadata, Enabled: payload.Enabled,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, sub)
}

func (h *Handler) deleteUserReminderSubscription(w http.ResponseWriter, r *http.Request) {
	customerID := r.URL.Query().Get("customer_id")
	userID := r.URL.Query().Get("user_id")
	if r.PathValue("subscription_id") == "" || customerID == "" || userID == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("subscription_id, customer_id, and user_id are required"))
		return
	}
	if !requireACPIdentityMatch(w, r, customerID, userID) {
		return
	}
	if err := h.store.DeleteUserReminderSubscription(r.Context(), r.PathValue("subscription_id"), customerID, userID); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"subscription_id": r.PathValue("subscription_id"), "deleted": true})
}

func (h *Handler) createSchedulerJob(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		CustomerID      string         `json:"customer_id"`
		UserID          string         `json:"user_id"`
		AgentInstanceID string         `json:"agent_instance_id"`
		SessionID       string         `json:"session_id"`
		JobType         string         `json:"job_type"`
		Schedule        string         `json:"schedule"`
		NextRunAt       time.Time      `json:"next_run_at"`
		Input           map[string]any `json:"input"`
		Metadata        map[string]any `json:"metadata"`
	}
	if err := decodeJSON(w, r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if payload.CustomerID == "" || payload.UserID == "" || payload.AgentInstanceID == "" || payload.SessionID == "" || payload.Schedule == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("customer_id, user_id, agent_instance_id, session_id, and schedule are required"))
		return
	}
	if strings.HasPrefix(r.URL.Path, "/acp/") && !requireACPIdentityMatch(w, r, payload.CustomerID, payload.UserID) {
		return
	}
	if payload.Input == nil {
		payload.Input = map[string]any{"text": "Scheduled job fired."}
	}
	if err := validateRunInput(payload.Input); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if payload.NextRunAt.IsZero() {
		next, err := scheduler.Next(payload.Schedule, time.Now().UTC())
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		if next.IsZero() {
			writeError(w, http.StatusBadRequest, fmt.Errorf("next_run_at is required for @once schedules"))
			return
		}
		payload.NextRunAt = next
	} else if _, err := scheduler.Next(payload.Schedule, payload.NextRunAt.Add(-time.Second)); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	job, err := h.store.CreateSchedulerJob(r.Context(), db.SchedulerJobSpec{
		CustomerID:      payload.CustomerID,
		UserID:          payload.UserID,
		AgentInstanceID: payload.AgentInstanceID,
		SessionID:       payload.SessionID,
		JobType:         payload.JobType,
		Schedule:        payload.Schedule,
		NextRunAt:       payload.NextRunAt,
		Input:           payload.Input,
		Metadata:        payload.Metadata,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusCreated, job)
}

func (h *Handler) listSchedulerJobs(w http.ResponseWriter, r *http.Request) {
	customerID := r.URL.Query().Get("customer_id")
	if customerID == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("customer_id is required"))
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	jobs, err := h.store.ListSchedulerJobs(r.Context(), customerID, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"scheduler_jobs": jobs})
}

func (h *Handler) updateSchedulerJob(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		CustomerID string `json:"customer_id"`
		Enabled    *bool  `json:"enabled"`
	}
	if err := decodeJSON(w, r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if r.PathValue("job_id") == "" || payload.CustomerID == "" || payload.Enabled == nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("job_id, customer_id, and enabled are required"))
		return
	}
	if err := h.store.SetSchedulerJobEnabled(r.Context(), r.PathValue("job_id"), payload.CustomerID, *payload.Enabled); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"job_id": r.PathValue("job_id"), "enabled": *payload.Enabled})
}

func (h *Handler) listUserSchedulerJobs(w http.ResponseWriter, r *http.Request) {
	customerID := r.URL.Query().Get("customer_id")
	userID := r.URL.Query().Get("user_id")
	if customerID == "" || userID == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("customer_id and user_id are required"))
		return
	}
	if !requireACPIdentityMatch(w, r, customerID, userID) {
		return
	}
	jobs, err := h.store.ListUserSchedulerJobs(r.Context(), customerID, userID, queryLimit(r, 100))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"scheduler_jobs": jobs})
}

func (h *Handler) updateUserSchedulerJob(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		CustomerID string         `json:"customer_id"`
		UserID     string         `json:"user_id"`
		Schedule   *string        `json:"schedule"`
		NextRunAt  time.Time      `json:"next_run_at"`
		Input      map[string]any `json:"input"`
		Metadata   map[string]any `json:"metadata"`
		Enabled    *bool          `json:"enabled"`
	}
	if err := decodeJSON(w, r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if r.PathValue("job_id") == "" || payload.CustomerID == "" || payload.UserID == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("job_id, customer_id, and user_id are required"))
		return
	}
	if !requireACPIdentityMatch(w, r, payload.CustomerID, payload.UserID) {
		return
	}
	if payload.Input != nil {
		if err := validateRunInput(payload.Input); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
	}
	if payload.Schedule != nil {
		schedule := strings.TrimSpace(*payload.Schedule)
		if schedule == "" {
			writeError(w, http.StatusBadRequest, fmt.Errorf("schedule cannot be empty"))
			return
		}
		payload.Schedule = &schedule
		if _, err := scheduler.Next(schedule, time.Now().UTC()); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		if payload.NextRunAt.IsZero() {
			next, _ := scheduler.Next(schedule, time.Now().UTC())
			if next.IsZero() {
				writeError(w, http.StatusBadRequest, fmt.Errorf("next_run_at is required for @once schedules"))
				return
			}
			payload.NextRunAt = next
		}
	}
	var nextRunAt *time.Time
	if !payload.NextRunAt.IsZero() {
		nextRunAt = &payload.NextRunAt
	}
	job, err := h.store.UpdateUserSchedulerJob(r.Context(), r.PathValue("job_id"), payload.CustomerID, payload.UserID, db.SchedulerJobUpdate{
		Schedule: payload.Schedule, NextRunAt: nextRunAt, Input: payload.Input, Metadata: payload.Metadata, Enabled: payload.Enabled,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, job)
}

func (h *Handler) deleteUserSchedulerJob(w http.ResponseWriter, r *http.Request) {
	customerID := r.URL.Query().Get("customer_id")
	userID := r.URL.Query().Get("user_id")
	if r.PathValue("job_id") == "" || customerID == "" || userID == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("job_id, customer_id, and user_id are required"))
		return
	}
	if !requireACPIdentityMatch(w, r, customerID, userID) {
		return
	}
	if err := h.store.DeleteUserSchedulerJob(r.Context(), r.PathValue("job_id"), customerID, userID); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"job_id": r.PathValue("job_id"), "deleted": true})
}

func (h *Handler) createSharedSchedulerJob(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		CustomerID       string         `json:"customer_id"`
		JobKey           string         `json:"job_key"`
		Title            string         `json:"title"`
		JobType          string         `json:"job_type"`
		Schedule         string         `json:"schedule"`
		Timezone         string         `json:"timezone"`
		NextRunAt        time.Time      `json:"next_run_at"`
		ExternalService  map[string]any `json:"external_service"`
		FanoutAction     string         `json:"fanout_action"`
		MessageTemplate  string         `json:"message_template"`
		RunInputTemplate map[string]any `json:"run_input_template"`
		Metadata         map[string]any `json:"metadata"`
	}
	if err := decodeJSON(w, r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if payload.CustomerID == "" || payload.JobKey == "" || payload.Schedule == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("customer_id, job_key, and schedule are required"))
		return
	}
	if !validSharedSchedulerFanoutAction(payload.FanoutAction) {
		writeError(w, http.StatusBadRequest, fmt.Errorf("fanout_action must be outbound_intent or durable_run"))
		return
	}
	if payload.NextRunAt.IsZero() {
		next, err := scheduler.Next(payload.Schedule, time.Now().UTC())
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		if next.IsZero() {
			writeError(w, http.StatusBadRequest, fmt.Errorf("next_run_at is required for @once schedules"))
			return
		}
		payload.NextRunAt = next
	} else if _, err := scheduler.Next(payload.Schedule, payload.NextRunAt.Add(-time.Second)); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	job, err := h.store.CreateSharedSchedulerJob(r.Context(), db.SharedSchedulerJobSpec{
		CustomerID: payload.CustomerID, JobKey: payload.JobKey, Title: payload.Title, JobType: payload.JobType, Schedule: payload.Schedule, Timezone: payload.Timezone,
		NextRunAt: payload.NextRunAt, ExternalService: payload.ExternalService, FanoutAction: payload.FanoutAction, MessageTemplate: payload.MessageTemplate, RunInputTemplate: payload.RunInputTemplate, Metadata: payload.Metadata,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusCreated, job)
}

func (h *Handler) listSharedSchedulerJobs(w http.ResponseWriter, r *http.Request) {
	customerID := r.URL.Query().Get("customer_id")
	if customerID == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("customer_id is required"))
		return
	}
	jobs, err := h.store.ListSharedSchedulerJobs(r.Context(), customerID, queryLimit(r, 100))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"shared_scheduler_jobs": jobs})
}

func (h *Handler) updateSharedSchedulerJob(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		CustomerID       string         `json:"customer_id"`
		Title            *string        `json:"title"`
		JobType          *string        `json:"job_type"`
		Schedule         *string        `json:"schedule"`
		Timezone         *string        `json:"timezone"`
		NextRunAt        time.Time      `json:"next_run_at"`
		ExternalService  map[string]any `json:"external_service"`
		FanoutAction     *string        `json:"fanout_action"`
		MessageTemplate  *string        `json:"message_template"`
		RunInputTemplate map[string]any `json:"run_input_template"`
		Metadata         map[string]any `json:"metadata"`
		Enabled          *bool          `json:"enabled"`
	}
	if err := decodeJSON(w, r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if payload.CustomerID == "" || r.PathValue("job_id") == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("job_id and customer_id are required"))
		return
	}
	if payload.FanoutAction != nil && !validSharedSchedulerFanoutAction(*payload.FanoutAction) {
		writeError(w, http.StatusBadRequest, fmt.Errorf("fanout_action must be outbound_intent or durable_run"))
		return
	}
	if payload.Schedule != nil {
		schedule := strings.TrimSpace(*payload.Schedule)
		if schedule == "" {
			writeError(w, http.StatusBadRequest, fmt.Errorf("schedule cannot be empty"))
			return
		}
		payload.Schedule = &schedule
		if _, err := scheduler.Next(schedule, time.Now().UTC()); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		if payload.NextRunAt.IsZero() {
			next, _ := scheduler.Next(schedule, time.Now().UTC())
			if next.IsZero() {
				writeError(w, http.StatusBadRequest, fmt.Errorf("next_run_at is required for @once schedules"))
				return
			}
			payload.NextRunAt = next
		}
	}
	var nextRunAt *time.Time
	if !payload.NextRunAt.IsZero() {
		nextRunAt = &payload.NextRunAt
	}
	job, err := h.store.UpdateSharedSchedulerJob(r.Context(), r.PathValue("job_id"), payload.CustomerID, db.SharedSchedulerJobUpdate{
		Title: payload.Title, JobType: payload.JobType, Schedule: payload.Schedule, Timezone: payload.Timezone, NextRunAt: nextRunAt,
		ExternalService: payload.ExternalService, FanoutAction: payload.FanoutAction, MessageTemplate: payload.MessageTemplate, RunInputTemplate: payload.RunInputTemplate, Metadata: payload.Metadata, Enabled: payload.Enabled,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, job)
}

func (h *Handler) deleteSharedSchedulerJob(w http.ResponseWriter, r *http.Request) {
	customerID := r.URL.Query().Get("customer_id")
	if customerID == "" || r.PathValue("job_id") == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("job_id and customer_id are required"))
		return
	}
	if err := h.store.DeleteSharedSchedulerJob(r.Context(), r.PathValue("job_id"), customerID); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"job_id": r.PathValue("job_id"), "deleted": true})
}

func (h *Handler) createSharedSchedulerSubscription(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		CustomerID         string         `json:"customer_id"`
		UserID             string         `json:"user_id"`
		SharedJobID        string         `json:"shared_job_id"`
		SessionID          string         `json:"session_id"`
		AgentInstanceID    string         `json:"agent_instance_id"`
		Enabled            *bool          `json:"enabled"`
		SubscriberMetadata map[string]any `json:"subscriber_metadata"`
	}
	if err := decodeJSON(w, r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if payload.CustomerID == "" || payload.UserID == "" || payload.SharedJobID == "" || payload.SessionID == "" || payload.AgentInstanceID == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("customer_id, user_id, shared_job_id, session_id, and agent_instance_id are required"))
		return
	}
	if !requireACPIdentityMatch(w, r, payload.CustomerID, payload.UserID) {
		return
	}
	sub, err := h.store.CreateSharedSchedulerSubscription(r.Context(), db.SharedSchedulerSubscriptionSpec{
		CustomerID: payload.CustomerID, UserID: payload.UserID, SharedJobID: payload.SharedJobID, SessionID: payload.SessionID, AgentInstanceID: payload.AgentInstanceID,
		Enabled: payload.Enabled, SubscriberMetadata: payload.SubscriberMetadata,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusCreated, sub)
}

func (h *Handler) listUserSharedSchedulerSubscriptions(w http.ResponseWriter, r *http.Request) {
	customerID := r.URL.Query().Get("customer_id")
	userID := r.URL.Query().Get("user_id")
	if customerID == "" || userID == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("customer_id and user_id are required"))
		return
	}
	if !requireACPIdentityMatch(w, r, customerID, userID) {
		return
	}
	subs, err := h.store.ListSharedSchedulerSubscriptions(r.Context(), customerID, userID, r.URL.Query().Get("shared_job_id"), queryLimit(r, 100))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"shared_scheduler_subscriptions": subs})
}

func (h *Handler) updateSharedSchedulerSubscription(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		CustomerID         string         `json:"customer_id"`
		UserID             string         `json:"user_id"`
		SessionID          *string        `json:"session_id"`
		AgentInstanceID    *string        `json:"agent_instance_id"`
		SubscriberMetadata map[string]any `json:"subscriber_metadata"`
		Enabled            *bool          `json:"enabled"`
	}
	if err := decodeJSON(w, r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if payload.CustomerID == "" || payload.UserID == "" || r.PathValue("subscription_id") == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("subscription_id, customer_id, and user_id are required"))
		return
	}
	if !requireACPIdentityMatch(w, r, payload.CustomerID, payload.UserID) {
		return
	}
	sub, err := h.store.UpdateSharedSchedulerSubscription(r.Context(), r.PathValue("subscription_id"), payload.CustomerID, payload.UserID, db.SharedSchedulerSubscriptionUpdate{
		SessionID: payload.SessionID, AgentInstanceID: payload.AgentInstanceID, SubscriberMetadata: payload.SubscriberMetadata, Enabled: payload.Enabled,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, sub)
}

func (h *Handler) deleteSharedSchedulerSubscription(w http.ResponseWriter, r *http.Request) {
	customerID := r.URL.Query().Get("customer_id")
	userID := r.URL.Query().Get("user_id")
	if customerID == "" || userID == "" || r.PathValue("subscription_id") == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("subscription_id, customer_id, and user_id are required"))
		return
	}
	if !requireACPIdentityMatch(w, r, customerID, userID) {
		return
	}
	if err := h.store.DeleteSharedSchedulerSubscription(r.Context(), r.PathValue("subscription_id"), customerID, userID); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"subscription_id": r.PathValue("subscription_id"), "deleted": true})
}

func validSharedSchedulerFanoutAction(action string) bool {
	action = strings.TrimSpace(action)
	return action == "" || action == "outbound_intent" || action == "durable_run"
}

func (h *Handler) listObservabilityEvents(w http.ResponseWriter, r *http.Request) {
	customerID := r.URL.Query().Get("customer_id")
	if customerID == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("customer_id is required"))
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	events, err := h.store.ListObservabilityEvents(r.Context(), customerID, r.URL.Query().Get("run_id"), limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": events})
}

func (h *Handler) listOutboundIntents(w http.ResponseWriter, r *http.Request) {
	customerID := r.URL.Query().Get("customer_id")
	if customerID == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("customer_id is required"))
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	intents, err := h.store.ListOutboundIntents(r.Context(), customerID, r.URL.Query().Get("status"), limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"outbound_intents": intents})
}

func (h *Handler) listBackgroundRuns(w http.ResponseWriter, r *http.Request) {
	customerID := r.URL.Query().Get("customer_id")
	if customerID == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("customer_id is required"))
		return
	}
	runs, err := h.store.BackgroundRuns(r.Context(), customerID, r.URL.Query().Get("agent_instance_id"), queryLimit(r, 100))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"background_runs": runs})
}

func (h *Handler) listUserBackgroundRuns(w http.ResponseWriter, r *http.Request) {
	customerID := r.URL.Query().Get("customer_id")
	userID := r.URL.Query().Get("user_id")
	if customerID == "" || userID == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("customer_id and user_id are required"))
		return
	}
	if !requireACPIdentityMatch(w, r, customerID, userID) {
		return
	}
	runs, err := h.store.UserBackgroundRuns(r.Context(), customerID, userID, r.URL.Query().Get("agent_instance_id"), queryLimit(r, 100))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"background_runs": runs})
}

func (h *Handler) cancelUserBackgroundRun(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		CustomerID string `json:"customer_id"`
		UserID     string `json:"user_id"`
	}
	if err := decodeJSON(w, r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if r.PathValue("run_id") == "" || payload.CustomerID == "" || payload.UserID == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("run_id, customer_id, and user_id are required"))
		return
	}
	if !requireACPIdentityMatch(w, r, payload.CustomerID, payload.UserID) {
		return
	}
	if err := h.store.CancelUserBackgroundRun(r.Context(), r.PathValue("run_id"), payload.CustomerID, payload.UserID); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": r.PathValue("run_id"), "state": "cancelled"})
}

func (h *Handler) listMCPServers(w http.ResponseWriter, r *http.Request) {
	if h.mcpManager == nil {
		writeJSON(w, http.StatusOK, map[string]any{"servers": []mcp.ServerStatus{}})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"servers": h.mcpManager.Statuses()})
}

func (h *Handler) listMCPTools(w http.ResponseWriter, r *http.Request) {
	serverName := r.PathValue("server_name")
	if serverName == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("server_name is required"))
		return
	}
	if h.mcpManager == nil {
		writeError(w, http.StatusNotFound, fmt.Errorf("mcp server %q not found", serverName))
		return
	}
	tools, err := h.mcpManager.ListTools(r.Context(), adminMCPExecutionContext(r), serverName)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	customerID := strings.TrimSpace(r.URL.Query().Get("customer_id"))
	agentInstanceID := strings.TrimSpace(r.URL.Query().Get("agent_instance_id"))
	if customerID != "" && agentInstanceID != "" && h.store != nil {
		access, err := h.store.EffectiveMCPToolAccess(r.Context(), customerID, agentInstanceID, strings.TrimSpace(r.URL.Query().Get("user_id")), serverName)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		filtered := tools[:0]
		for _, tool := range tools {
			if db.MCPToolAllowed(access, tool.Name) {
				filtered = append(filtered, tool)
			}
		}
		tools = filtered
	}
	writeJSON(w, http.StatusOK, map[string]any{"server_name": serverName, "tools": tools})
}

func (h *Handler) upsertCustomerMCPToolAccess(w http.ResponseWriter, r *http.Request) {
	h.upsertMCPToolAccess(w, r, "")
}

func (h *Handler) upsertUserMCPToolAccess(w http.ResponseWriter, r *http.Request) {
	h.upsertMCPToolAccess(w, r, r.PathValue("user_id"))
}

func (h *Handler) getCustomerMCPToolAccess(w http.ResponseWriter, r *http.Request) {
	h.getMCPToolAccess(w, r, "")
}

func (h *Handler) getUserMCPToolAccess(w http.ResponseWriter, r *http.Request) {
	h.getMCPToolAccess(w, r, r.PathValue("user_id"))
}

func (h *Handler) deleteCustomerMCPToolAccess(w http.ResponseWriter, r *http.Request) {
	h.deleteMCPToolAccess(w, r, "")
}

func (h *Handler) deleteUserMCPToolAccess(w http.ResponseWriter, r *http.Request) {
	h.deleteMCPToolAccess(w, r, r.PathValue("user_id"))
}

func (h *Handler) upsertMCPToolAccess(w http.ResponseWriter, r *http.Request, userID string) {
	if h.store == nil {
		writeError(w, http.StatusServiceUnavailable, fmt.Errorf("store is not configured"))
		return
	}
	var payload struct {
		AllowedTools []string       `json:"allowed_tools"`
		DeniedTools  []string       `json:"denied_tools"`
		Metadata     map[string]any `json:"metadata"`
	}
	if err := decodeJSON(w, r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	rule, err := h.store.UpsertMCPToolAccessRule(r.Context(), db.MCPToolAccessRule{
		CustomerID: r.PathValue("customer_id"), AgentInstanceID: r.PathValue("agent_instance_id"), UserID: userID, ServerName: r.PathValue("server_name"),
		AllowedTools: payload.AllowedTools, DeniedTools: payload.DeniedTools, Metadata: rawJSON(payload.Metadata),
	})
	if err != nil {
		writeError(w, statusForError(err), err)
		return
	}
	writeJSON(w, http.StatusOK, rule)
}

func (h *Handler) getMCPToolAccess(w http.ResponseWriter, r *http.Request, userID string) {
	if h.store == nil {
		writeError(w, http.StatusServiceUnavailable, fmt.Errorf("store is not configured"))
		return
	}
	rule, err := h.store.MCPToolAccessRule(r.Context(), r.PathValue("customer_id"), r.PathValue("agent_instance_id"), userID, r.PathValue("server_name"))
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	effective, err := h.store.EffectiveMCPToolAccess(r.Context(), r.PathValue("customer_id"), r.PathValue("agent_instance_id"), userID, r.PathValue("server_name"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"rule": rule, "effective": effective})
}

func (h *Handler) deleteMCPToolAccess(w http.ResponseWriter, r *http.Request, userID string) {
	if h.store == nil {
		writeError(w, http.StatusServiceUnavailable, fmt.Errorf("store is not configured"))
		return
	}
	if err := h.store.DeleteMCPToolAccessRule(r.Context(), r.PathValue("customer_id"), r.PathValue("agent_instance_id"), userID, r.PathValue("server_name")); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": true})
}

func (h *Handler) listMCPResources(w http.ResponseWriter, r *http.Request) {
	serverName := r.PathValue("server_name")
	if serverName == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("server_name is required"))
		return
	}
	if h.mcpManager == nil {
		writeError(w, http.StatusNotFound, fmt.Errorf("mcp server %q not found", serverName))
		return
	}
	resources, err := h.mcpManager.ListResources(r.Context(), adminMCPExecutionContext(r), serverName)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"server_name": serverName, "resources": resources})
}

func (h *Handler) readMCPResource(w http.ResponseWriter, r *http.Request) {
	serverName := r.PathValue("server_name")
	uri := r.URL.Query().Get("uri")
	if serverName == "" || uri == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("server_name and uri are required"))
		return
	}
	if h.mcpManager == nil {
		writeError(w, http.StatusNotFound, fmt.Errorf("mcp server %q not found", serverName))
		return
	}
	resource, err := h.mcpManager.ReadResource(r.Context(), adminMCPExecutionContext(r), serverName, uri)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"server_name": serverName, "resource": resource})
}

func (h *Handler) listMCPPrompts(w http.ResponseWriter, r *http.Request) {
	serverName := r.PathValue("server_name")
	if serverName == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("server_name is required"))
		return
	}
	if h.mcpManager == nil {
		writeError(w, http.StatusNotFound, fmt.Errorf("mcp server %q not found", serverName))
		return
	}
	prompts, err := h.mcpManager.ListPrompts(r.Context(), adminMCPExecutionContext(r), serverName)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"server_name": serverName, "prompts": prompts})
}

func (h *Handler) getMCPPrompt(w http.ResponseWriter, r *http.Request) {
	serverName := r.PathValue("server_name")
	promptName := r.PathValue("prompt_name")
	if serverName == "" || promptName == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("server_name and prompt_name are required"))
		return
	}
	if h.mcpManager == nil {
		writeError(w, http.StatusNotFound, fmt.Errorf("mcp server %q not found", serverName))
		return
	}
	var payload struct {
		Arguments map[string]any `json:"arguments"`
	}
	if err := decodeJSON(w, r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	prompt, err := h.mcpManager.GetPrompt(r.Context(), adminMCPExecutionContext(r), serverName, promptName, payload.Arguments)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"server_name": serverName, "prompt": prompt})
}

func (h *Handler) ingestMCPNotification(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		CustomerID  string         `json:"customer_id"`
		RunID       string         `json:"run_id"`
		ServerName  string         `json:"server_name"`
		EventType   string         `json:"event_type"`
		ResourceURI string         `json:"resource_uri"`
		Data        map[string]any `json:"data"`
	}
	if err := decodeJSON(w, r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if payload.CustomerID == "" || payload.ServerName == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("customer_id and server_name are required"))
		return
	}
	if payload.EventType == "" {
		payload.EventType = "mcp.notification"
	}
	if h.store == nil {
		writeError(w, http.StatusServiceUnavailable, fmt.Errorf("store is not configured"))
		return
	}
	body := map[string]any{
		"server_name":  payload.ServerName,
		"resource_uri": payload.ResourceURI,
		"event_type":   payload.EventType,
		"data":         payload.Data,
		"trace_id":     r.Header.Get("X-Trace-ID"),
		"traceparent":  r.Header.Get("traceparent"),
	}
	if err := h.store.AddObservabilityEvent(r.Context(), payload.CustomerID, payload.RunID, "mcp.notification", body); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"accepted": true})
}

func adminMCPExecutionContext(r *http.Request) mcp.ExecutionContext {
	return mcp.ExecutionContext{
		CustomerID:      r.URL.Query().Get("customer_id"),
		UserID:          r.URL.Query().Get("user_id"),
		AgentInstanceID: r.URL.Query().Get("agent_instance_id"),
		SessionID:       r.URL.Query().Get("session_id"),
		RequestID:       r.Header.Get("X-Request-ID"),
		TraceID:         r.Header.Get("X-Trace-ID"),
		TraceParent:     r.Header.Get("traceparent"),
	}
}

func (h *Handler) createBroadcast(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		CustomerID      string                      `json:"customer_id"`
		Title           string                      `json:"title"`
		Payload         map[string]any              `json:"payload"`
		Targets         []db.BroadcastTargetSpec    `json:"targets"`
		TargetSelection db.BroadcastTargetSelection `json:"target_selection"`
	}
	if err := decodeJSON(w, r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if payload.CustomerID == "" || strings.TrimSpace(payload.Title) == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("customer_id and title are required"))
		return
	}
	if len(payload.Targets) == 0 {
		targets, err := h.store.ResolveBroadcastTargets(r.Context(), payload.CustomerID, payload.TargetSelection)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		payload.Targets = targets
	}
	if len(payload.Targets) == 0 {
		writeError(w, http.StatusBadRequest, fmt.Errorf("broadcast targets are required"))
		return
	}
	if _, err := policy.NewEngine(h.store).Enforce(r.Context(), "pre_broadcast", policy.Context{
		CustomerID:       payload.CustomerID,
		Content:          mustJSONString(map[string]any{"title": payload.Title, "payload": payload.Payload}),
		AdditionalFields: map[string]any{"target_count": len(payload.Targets)},
	}); err != nil {
		writeError(w, http.StatusForbidden, err)
		return
	}
	id, count, err := h.store.CreateBroadcast(r.Context(), payload.CustomerID, payload.Title, payload.Payload, payload.Targets)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"broadcast_id": id, "targets": count})
}

func (h *Handler) listBroadcasts(w http.ResponseWriter, r *http.Request) {
	customerID := r.URL.Query().Get("customer_id")
	if customerID == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("customer_id is required"))
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	broadcasts, err := h.store.ListBroadcasts(r.Context(), customerID, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"broadcasts": broadcasts})
}

func (h *Handler) listBroadcastTargets(w http.ResponseWriter, r *http.Request) {
	customerID := r.URL.Query().Get("customer_id")
	if customerID == "" || r.PathValue("broadcast_id") == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("customer_id and broadcast_id are required"))
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	targets, err := h.store.ListBroadcastTargets(r.Context(), customerID, r.PathValue("broadcast_id"), limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"targets": targets})
}

func (h *Handler) cancelBroadcast(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		CustomerID string `json:"customer_id"`
	}
	if err := decodeJSON(w, r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if payload.CustomerID == "" || r.PathValue("broadcast_id") == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("customer_id and broadcast_id are required"))
		return
	}
	if err := h.store.CancelBroadcast(r.Context(), payload.CustomerID, r.PathValue("broadcast_id")); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"broadcast_id": r.PathValue("broadcast_id"), "status": "cancelled"})
}

func (h *Handler) runRetention(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		ArtifactDays      int `json:"artifact_days"`
		EventDays         int `json:"event_days"`
		OutboxDays        int `json:"outbox_days"`
		AsyncWriteDays    int `json:"async_write_days"`
		ObservabilityDays int `json:"observability_days"`
		BroadcastDays     int `json:"broadcast_days"`
	}
	if err := decodeJSON(w, r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	now := time.Now().UTC()
	result := map[string]int64{}
	if payload.ArtifactDays > 0 {
		n, err := h.store.ExpireArtifactsOlderThan(r.Context(), now.AddDate(0, 0, -payload.ArtifactDays))
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		result["artifacts_expired"] = n
	}
	if payload.EventDays > 0 {
		n, err := h.store.DeleteRunEventsOlderThan(r.Context(), now.AddDate(0, 0, -payload.EventDays))
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		result["events_deleted"] = n
	}
	if payload.OutboxDays > 0 {
		n, err := h.store.DeleteCompletedOutboxOlderThan(r.Context(), now.AddDate(0, 0, -payload.OutboxDays))
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		result["outbox_deleted"] = n
	}
	if payload.AsyncWriteDays > 0 {
		n, err := h.store.DeleteTerminalAsyncWriteJobsOlderThan(r.Context(), now.AddDate(0, 0, -payload.AsyncWriteDays))
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		result["async_write_jobs_deleted"] = n
	}
	if payload.ObservabilityDays > 0 {
		n, err := h.store.DeleteObservabilityEventsOlderThan(r.Context(), now.AddDate(0, 0, -payload.ObservabilityDays))
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		result["observability_events_deleted"] = n
	}
	if payload.BroadcastDays > 0 {
		n, err := h.store.DeleteTerminalBroadcastsOlderThan(r.Context(), now.AddDate(0, 0, -payload.BroadcastDays))
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		result["broadcasts_deleted"] = n
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *Handler) agents(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"agents": []map[string]any{{"id": "duraclaw", "name": "Duraclaw", "capabilities": []string{"runs", "events", "artifacts", "resume"}}}})
}

func (h *Handler) ensureSession(w http.ResponseWriter, r *http.Request) {
	c, ok := requiredContext(w, r, false)
	if !ok {
		return
	}
	c.SessionID = r.PathValue("session_id")
	if err := h.store.EnsureSession(r.Context(), c); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if err := h.refreshUserProfile(r.Context(), c); err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"session_id": c.SessionID, "state": "ready"})
}

func (h *Handler) reassignSession(w http.ResponseWriter, r *http.Request) {
	c, ok := requiredContext(w, r, false)
	if !ok {
		return
	}
	if c.SessionID != r.PathValue("session_id") {
		writeError(w, http.StatusBadRequest, fmt.Errorf("X-Session-ID does not match route session id"))
		return
	}
	var payload struct {
		AgentInstanceID string         `json:"agent_instance_id"`
		Reason          string         `json:"reason"`
		Metadata        map[string]any `json:"metadata"`
	}
	if err := decodeJSON(w, r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if payload.AgentInstanceID == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("agent_instance_id is required"))
		return
	}
	transfer, err := h.store.ReassignSession(r.Context(), c.CustomerID, c.SessionID, payload.AgentInstanceID, payload.Reason, payload.Metadata)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, transfer)
}

func (h *Handler) startRun(w http.ResponseWriter, r *http.Request) {
	c, ok := requiredContext(w, r, true)
	if !ok {
		return
	}
	var payload map[string]any
	if err := decodeJSON(w, r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := validateRunInput(payload); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if existing, err := h.store.RunByIdempotencyKey(r.Context(), c.CustomerID, c.SessionID, c.IdempotencyKey); err == nil {
		writeJSON(w, http.StatusAccepted, existing)
		return
	} else if !errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if err := h.store.EnsureSession(r.Context(), c); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if err := h.refreshUserProfile(r.Context(), c); err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	deferred, err := h.store.DeferRunIfActive(r.Context(), c, payload, h.interruptWindow, h.maxRefineDepth)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if deferred != nil {
		typingPayload, _ := json.Marshal(map[string]any{"active_run_id": deferred.ActiveRunID, "state": "processing", "reason": "deferred_followup"})
		_, _, _ = h.store.CreateOutboundIntent(r.Context(), db.OutboundIntent{
			CustomerID: c.CustomerID,
			UserID:     c.UserID,
			SessionID:  c.SessionID,
			RunID:      &deferred.ActiveRunID,
			Type:       "typing",
			Payload:    typingPayload,
		})
		writeJSON(w, http.StatusAccepted, deferred)
		return
	}
	run, err := h.store.CreateRun(r.Context(), c, payload)
	if err != nil {
		if db.IsQuotaExceeded(err) && h.counters != nil {
			h.counters.Inc("quota_exceeded_total")
		}
		writeError(w, statusForError(err), err)
		return
	}
	_ = h.store.AddObservabilityEvent(r.Context(), c.CustomerID, run.ID, "acp_run_created", map[string]any{"request_id": c.RequestID})
	writeJSON(w, http.StatusAccepted, run)
}

func (h *Handler) refreshUserProfile(ctx context.Context, c db.ACPContext) error {
	if h.profileRetriever == nil {
		return nil
	}
	result, err := h.profileRetriever.RetrieveProfile(ctx, profiles.Request{
		CustomerID: c.CustomerID, UserID: c.UserID, AgentInstanceID: c.AgentInstanceID, SessionID: c.SessionID,
	})
	if err != nil {
		return err
	}
	if result == nil || len(result.Profile) == 0 {
		return nil
	}
	metadata, err := h.store.UserMetadata(ctx, c.CustomerID, c.UserID)
	if err != nil {
		return err
	}
	metadata = profiles.MergeMetadata(metadata, result, "customer_profile_retriever", time.Now().UTC())
	if err := h.store.UpdateUserMetadata(ctx, c.CustomerID, c.UserID, metadata); err != nil {
		return err
	}
	_ = h.store.AddObservabilityEvent(ctx, c.CustomerID, "", "user_profile_refreshed", map[string]any{"user_id": c.UserID, "session_id": c.SessionID})
	return nil
}

func (h *Handler) attachArtifacts(w http.ResponseWriter, r *http.Request) {
	c, ok := existingRunContext(w, r)
	if !ok {
		return
	}
	if _, ok := h.requireRunAccess(w, r, c); !ok {
		return
	}
	var payload struct {
		Artifacts []db.Artifact `json:"artifacts"`
	}
	if err := decodeJSON(w, r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	artifactRule := h.artifactRule(r, c)
	for _, a := range payload.Artifacts {
		if a.State == "" {
			a.State = "available"
		}
		if err := policy.AllowArtifact(a.SizeBytes, a.MediaType, artifactRule); err != nil {
			writeError(w, http.StatusForbidden, err)
			return
		}
		if err := policy.RejectRawArtifactMetadata(a.Metadata); err != nil {
			writeError(w, http.StatusForbidden, err)
			return
		}
		if err := h.store.AttachArtifact(r.Context(), r.PathValue("run_id"), a); err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
	}
	_ = h.store.AddEvent(r.Context(), r.PathValue("run_id"), "artifacts.attached", map[string]any{"count": len(payload.Artifacts)})
	_ = h.store.AddObservabilityEvent(r.Context(), c.CustomerID, r.PathValue("run_id"), "artifacts_attached", map[string]any{"count": len(payload.Artifacts)})
	writeJSON(w, http.StatusOK, map[string]any{"attached": len(payload.Artifacts)})
}

func (h *Handler) generateArtifact(w http.ResponseWriter, r *http.Request) {
	c, ok := existingRunContext(w, r)
	if !ok {
		return
	}
	run, ok := h.requireRunAccess(w, r, c)
	if !ok {
		return
	}
	if h.providers == nil {
		writeError(w, http.StatusServiceUnavailable, fmt.Errorf("provider registry is not configured"))
		return
	}
	var payload struct {
		Kind           string `json:"kind"`
		Prompt         string `json:"prompt"`
		Provider       string `json:"provider"`
		Model          string `json:"model"`
		ArtifactID     string `json:"artifact_id"`
		Size           string `json:"size"`
		Quality        string `json:"quality"`
		ResponseFormat string `json:"response_format"`
		Voice          string `json:"voice"`
		Format         string `json:"format"`
		Instructions   string `json:"instructions"`
		Seconds        string `json:"seconds"`
		Count          int    `json:"count"`
	}
	if err := decodeJSON(w, r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if payload.Kind == "" {
		payload.Kind = "image"
	}
	artifact, err := tools.GenerateMediaArtifact(r.Context(), tools.MediaGenerationRequest{
		Kind:           payload.Kind,
		Registry:       h.providers,
		Store:          h.store,
		ModelConfig:    h.modelConfig,
		ArtifactPolicy: h.artifactRule(r, c),
		BlobStore:      h.mediaBlobStore,
		RunID:          run.ID,
		Prompt:         payload.Prompt,
		Provider:       payload.Provider,
		Model:          payload.Model,
		ArtifactID:     payload.ArtifactID,
		Size:           payload.Size,
		Quality:        payload.Quality,
		ResponseFormat: payload.ResponseFormat,
		Voice:          payload.Voice,
		Format:         payload.Format,
		Instructions:   payload.Instructions,
		Seconds:        payload.Seconds,
		Count:          payload.Count,
	})
	if err != nil {
		writeError(w, statusForError(err), err)
		return
	}
	_ = h.store.AddEvent(r.Context(), run.ID, "artifact.generated", map[string]any{"artifact_id": artifact.ID, "modality": artifact.Modality, "source": "acp"})
	_ = h.store.AddObservabilityEvent(r.Context(), c.CustomerID, run.ID, "artifact_generated", map[string]any{"artifact_id": artifact.ID, "modality": artifact.Modality})
	writeJSON(w, http.StatusCreated, artifact)
}

func (h *Handler) adminGenerateMedia(w http.ResponseWriter, r *http.Request) {
	if h.providers == nil {
		writeError(w, http.StatusServiceUnavailable, fmt.Errorf("provider registry is not configured"))
		return
	}
	var payload struct {
		CustomerID     string `json:"customer_id"`
		RunID          string `json:"run_id"`
		Kind           string `json:"kind"`
		Prompt         string `json:"prompt"`
		Provider       string `json:"provider"`
		Model          string `json:"model"`
		ArtifactID     string `json:"artifact_id"`
		Size           string `json:"size"`
		Quality        string `json:"quality"`
		ResponseFormat string `json:"response_format"`
		Voice          string `json:"voice"`
		Format         string `json:"format"`
		Instructions   string `json:"instructions"`
		Seconds        string `json:"seconds"`
		Count          int    `json:"count"`
	}
	if err := decodeJSON(w, r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if payload.RunID == "" || payload.CustomerID == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("customer_id and run_id are required"))
		return
	}
	run, err := h.store.GetRun(r.Context(), payload.RunID)
	if err != nil || run.CustomerID != payload.CustomerID {
		writeError(w, http.StatusNotFound, fmt.Errorf("run not found"))
		return
	}
	c := db.ACPContext{CustomerID: run.CustomerID, UserID: run.UserID, AgentInstanceID: run.AgentInstanceID, SessionID: run.SessionID, RunID: run.ID, RequestID: run.RequestID}
	artifact, err := tools.GenerateMediaArtifact(r.Context(), tools.MediaGenerationRequest{
		Kind: payload.Kind, Registry: h.providers, Store: h.store, ModelConfig: h.modelConfig, ArtifactPolicy: h.artifactRule(r, c), BlobStore: h.mediaBlobStore,
		RunID: run.ID, Prompt: payload.Prompt, Provider: payload.Provider, Model: payload.Model, ArtifactID: payload.ArtifactID,
		Size: payload.Size, Quality: payload.Quality, ResponseFormat: payload.ResponseFormat, Voice: payload.Voice, Format: payload.Format,
		Instructions: payload.Instructions, Seconds: payload.Seconds, Count: payload.Count,
	})
	if err != nil {
		writeError(w, statusForError(err), err)
		return
	}
	_ = h.store.AddEvent(r.Context(), run.ID, "artifact.generated", map[string]any{"artifact_id": artifact.ID, "modality": artifact.Modality, "source": "admin"})
	_ = h.store.AddObservabilityEvent(r.Context(), run.CustomerID, run.ID, "artifact_generated", map[string]any{"artifact_id": artifact.ID, "modality": artifact.Modality, "source": "admin"})
	writeJSON(w, http.StatusCreated, artifact)
}

func (h *Handler) listRunArtifacts(w http.ResponseWriter, r *http.Request) {
	c, ok := existingRunContext(w, r)
	if !ok {
		return
	}
	if _, ok := h.requireRunAccess(w, r, c); !ok {
		return
	}
	artifacts, err := h.store.ArtifactsForRun(r.Context(), r.PathValue("run_id"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"artifacts": artifacts})
}

func (h *Handler) listArtifactRepresentations(w http.ResponseWriter, r *http.Request) {
	customerID, ok := requireCustomerHeader(w, r)
	if !ok {
		return
	}
	if r.PathValue("artifact_id") == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("artifact_id is required"))
		return
	}
	reps, err := h.store.ArtifactRepresentations(r.Context(), customerID, r.PathValue("artifact_id"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"representations": reps})
}

func (h *Handler) artifactRule(r *http.Request, c db.ACPContext) policy.ArtifactRule {
	rule := h.artifactPolicy
	if h.store == nil {
		return rule
	}
	p, err := h.store.AgentPolicy(r.Context(), c.CustomerID, c.AgentInstanceID)
	if err != nil || p == nil {
		return rule
	}
	if p.ArtifactMaxSizeBytes > 0 {
		rule.MaxSizeBytes = p.ArtifactMaxSizeBytes
	}
	if len(p.ArtifactMediaTypes) > 0 {
		rule.MediaTypes = map[string]bool{}
		for _, mediaType := range p.ArtifactMediaTypes {
			rule.MediaTypes[mediaType] = true
		}
	}
	return rule
}

func (h *Handler) runStatus(w http.ResponseWriter, r *http.Request) {
	customerID, ok := requireCustomerHeader(w, r)
	if !ok {
		return
	}
	run, err := h.store.GetRun(r.Context(), r.PathValue("run_id"))
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	if run.CustomerID != customerID {
		writeError(w, http.StatusNotFound, fmt.Errorf("run not found"))
		return
	}
	writeJSON(w, http.StatusOK, run)
}

func (h *Handler) runTrace(w http.ResponseWriter, r *http.Request) {
	c, ok := existingRunContext(w, r)
	if !ok {
		return
	}
	if _, ok := h.requireRunAccess(w, r, c); !ok {
		return
	}
	trace, err := h.store.RunTrace(r.Context(), r.PathValue("run_id"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, trace)
}

func (h *Handler) backgroundStatus(w http.ResponseWriter, r *http.Request) {
	c, ok := existingRunContext(w, r)
	if !ok {
		return
	}
	run, ok := h.requireRunAccess(w, r, c)
	if !ok {
		return
	}
	progress, _ := h.store.RunProgress(r.Context(), run.ID)
	writeJSON(w, http.StatusOK, map[string]any{
		"run_id":            run.ID,
		"state":             run.State,
		"progress":          json.RawMessage(progress),
		"agent_instance_id": run.AgentInstanceID,
		"session_id":        run.SessionID,
		"created_at":        run.CreatedAt,
		"updated_at":        run.UpdatedAt,
		"completed_at":      run.CompletedAt,
		"error":             run.Error,
	})
}

func (h *Handler) events(w http.ResponseWriter, r *http.Request) {
	customerID, ok := requireCustomerHeader(w, r)
	if !ok {
		return
	}
	run, err := h.store.GetRun(r.Context(), r.PathValue("run_id"))
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	if run.CustomerID != customerID {
		writeError(w, http.StatusNotFound, fmt.Errorf("run not found"))
		return
	}
	after, _ := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	events, err := h.store.EventsPage(r.Context(), r.PathValue("run_id"), after, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if strings.Contains(r.Header.Get("Accept"), "text/event-stream") {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		for _, event := range events {
			writeSSEEvent(w, event)
			after = event.ID
		}
		if flusher != nil {
			flusher.Flush()
		}
		if r.URL.Query().Get("follow") != "true" && r.URL.Query().Get("follow") != "1" {
			return
		}
		heartbeat := time.NewTicker(15 * time.Second)
		poll := time.NewTicker(500 * time.Millisecond)
		defer heartbeat.Stop()
		defer poll.Stop()
		for {
			select {
			case <-r.Context().Done():
				return
			case <-heartbeat.C:
				_, _ = fmt.Fprint(w, ": keepalive\n\n")
				if flusher != nil {
					flusher.Flush()
				}
			case <-poll.C:
				page, err := h.store.EventsPage(r.Context(), run.ID, after, limit)
				if err != nil {
					_, _ = fmt.Fprintf(w, "event: error\ndata: %q\n\n", err.Error())
					if flusher != nil {
						flusher.Flush()
					}
					return
				}
				for _, event := range page {
					writeSSEEvent(w, event)
					after = event.ID
				}
				if len(page) > 0 && flusher != nil {
					flusher.Flush()
				}
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": events})
}

func writeSSEEvent(w io.Writer, event db.Event) {
	_, _ = fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", event.ID, event.Type, event.Payload)
}

func (h *Handler) resume(w http.ResponseWriter, r *http.Request) {
	c, ok := existingRunContext(w, r)
	if !ok {
		return
	}
	run, ok := h.requireRunAccess(w, r, c)
	if !ok {
		return
	}
	if run.State != "awaiting_user" {
		writeError(w, http.StatusConflict, fmt.Errorf("run is %s, only awaiting_user can resume", run.State))
		return
	}
	var payload struct {
		WorkflowRunID string         `json:"workflow_run_id"`
		NodeKey       string         `json:"node_key"`
		Response      map[string]any `json:"response"`
	}
	if r.Body != nil && r.ContentLength != 0 {
		if err := decodeJSON(w, r, &payload); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
	}
	var err error
	if payload.WorkflowRunID != "" {
		err = h.workflow.ResumeAwaitingUser(r.Context(), workflow.ResumeRequest{
			RunID: run.ID, WorkflowRunID: payload.WorkflowRunID, NodeKey: payload.NodeKey, Response: payload.Response,
		})
	} else {
		err = h.store.SetRunState(r.Context(), run.ID, "queued", nil)
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": run.ID, "state": "queued"})
}

func (h *Handler) cancel(w http.ResponseWriter, r *http.Request) {
	c, ok := existingRunContext(w, r)
	if !ok {
		return
	}
	if _, ok := h.requireRunAccess(w, r, c); !ok {
		return
	}
	if err := h.store.SetRunState(r.Context(), r.PathValue("run_id"), "cancelled", nil); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": r.PathValue("run_id"), "state": "cancelled"})
}

func (h *Handler) updateOutboundIntentStatus(w http.ResponseWriter, r *http.Request) {
	customerID, ok := requireCustomerHeader(w, r)
	if !ok {
		return
	}
	var payload struct {
		Status string `json:"status"`
	}
	if err := decodeJSON(w, r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if r.PathValue("intent_id") == "" || payload.Status == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("intent_id and status are required"))
		return
	}
	status, err := db.NormalizeOutboundIntentStatus(payload.Status)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := h.store.SetOutboundIntentStatus(r.Context(), r.PathValue("intent_id"), customerID, status); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"outbound_intent_id": r.PathValue("intent_id"), "status": status})
}

func (h *Handler) latest(w http.ResponseWriter, r *http.Request) {
	customerID, ok := requireCustomerHeader(w, r)
	if !ok {
		return
	}
	run, err := h.store.LatestRun(r.Context(), customerID, r.PathValue("session_id"))
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	writeJSON(w, http.StatusOK, run)
}

func (h *Handler) byKey(w http.ResponseWriter, r *http.Request) {
	customerID, ok := requireCustomerHeader(w, r)
	if !ok {
		return
	}
	run, err := h.store.RunByIdempotencyKey(r.Context(), customerID, r.PathValue("session_id"), r.PathValue("key"))
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	writeJSON(w, http.StatusOK, run)
}

func requiredContext(w http.ResponseWriter, r *http.Request, requireIdempotency bool) (db.ACPContext, bool) {
	trace := observability.TraceContextFromHeaders(r.Header)
	c := db.ACPContext{
		CustomerID: r.Header.Get("X-Customer-ID"), UserID: r.Header.Get("X-User-ID"), AgentInstanceID: r.Header.Get("X-Agent-Instance-ID"),
		SessionID: r.Header.Get("X-Session-ID"), RequestID: r.Header.Get("X-Request-ID"), IdempotencyKey: r.Header.Get("X-Idempotency-Key"),
		ChannelType: r.Header.Get("X-Channel-Type"), ChannelUserID: r.Header.Get("X-Channel-User-ID"), ChannelConvID: r.Header.Get("X-Channel-Conversation-ID"), TraceID: trace.TraceID, TraceParent: trace.TraceParent,
	}
	required := []struct {
		name string
		val  string
	}{
		{"X-Customer-ID", c.CustomerID},
		{"X-User-ID", c.UserID},
		{"X-Agent-Instance-ID", c.AgentInstanceID},
		{"X-Session-ID", c.SessionID},
		{"X-Request-ID", c.RequestID},
	}
	if requireIdempotency {
		required = append(required, struct {
			name string
			val  string
		}{"X-Idempotency-Key", c.IdempotencyKey})
	}
	for _, item := range required {
		if strings.TrimSpace(item.val) == "" {
			writeError(w, http.StatusBadRequest, fmt.Errorf("missing required header %s", item.name))
			return c, false
		}
	}
	return c, true
}

func validateRunInput(payload map[string]any) error {
	if payload == nil {
		return fmt.Errorf("request body is required")
	}
	parts, ok := payload["parts"]
	if !ok {
		return nil
	}
	partList, ok := parts.([]any)
	if !ok {
		return fmt.Errorf("parts must be an array")
	}
	for i, raw := range partList {
		part, ok := raw.(map[string]any)
		if !ok {
			return fmt.Errorf("parts[%d] must be an object", i)
		}
		typ, _ := part["type"].(string)
		switch typ {
		case "text", "artifact_ref", "location", "structured_data", "image", "image_url", "document", "pdf", "file", "audio", "input_audio", "video", "video_url":
		default:
			return fmt.Errorf("parts[%d] has unsupported type %q", i, typ)
		}
		if typ == "artifact_ref" {
			data, _ := part["data"].(map[string]any)
			artifactID, _ := data["artifact_id"].(string)
			if strings.TrimSpace(artifactID) == "" {
				return fmt.Errorf("parts[%d] artifact_ref requires data.artifact_id", i)
			}
		}
	}
	return nil
}

func existingRunContext(w http.ResponseWriter, r *http.Request) (db.ACPContext, bool) {
	c, ok := requiredContext(w, r, false)
	if !ok {
		return c, false
	}
	c.RunID = r.Header.Get("X-Run-ID")
	if strings.TrimSpace(c.RunID) == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("missing required header X-Run-ID"))
		return c, false
	}
	if c.RunID != r.PathValue("run_id") {
		writeError(w, http.StatusBadRequest, fmt.Errorf("X-Run-ID does not match route run id"))
		return c, false
	}
	return c, true
}

func requireCustomerHeader(w http.ResponseWriter, r *http.Request) (string, bool) {
	customerID := r.Header.Get("X-Customer-ID")
	if strings.TrimSpace(customerID) == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("missing required header X-Customer-ID"))
		return "", false
	}
	return customerID, true
}

func requireACPIdentityMatch(w http.ResponseWriter, r *http.Request, customerID, userID string) bool {
	headerCustomerID := strings.TrimSpace(r.Header.Get("X-Customer-ID"))
	headerUserID := strings.TrimSpace(r.Header.Get("X-User-ID"))
	if headerCustomerID == "" || headerUserID == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("missing required headers X-Customer-ID and X-User-ID"))
		return false
	}
	if headerCustomerID != customerID || headerUserID != userID {
		writeError(w, http.StatusNotFound, fmt.Errorf("resource not found"))
		return false
	}
	return true
}

func (h *Handler) requireRunAccess(w http.ResponseWriter, r *http.Request, c db.ACPContext) (*db.Run, bool) {
	run, err := h.store.GetRun(r.Context(), r.PathValue("run_id"))
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return nil, false
	}
	if run.CustomerID != c.CustomerID || run.SessionID != c.SessionID || run.AgentInstanceID != c.AgentInstanceID || run.UserID != c.UserID {
		writeError(w, http.StatusNotFound, fmt.Errorf("run not found"))
		return nil, false
	}
	return run, true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	if r.Body == nil {
		return fmt.Errorf("request body is required")
	}
	limited := http.MaxBytesReader(w, r.Body, maxJSONBodyBytes)
	dec := json.NewDecoder(limited)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return err
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return fmt.Errorf("request body must contain a single JSON value")
	}
	return nil
}

func queryLimit(r *http.Request, fallback int) int {
	limit := fallback
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil {
			limit = n
		}
	}
	if limit <= 0 || limit > 500 {
		return fallback
	}
	return limit
}

func mustJSONString(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func rawJSON(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	if len(b) == 0 || string(b) == "null" {
		return json.RawMessage(`{}`)
	}
	return b
}

func contentFormat(contentType string) string {
	contentType = strings.ToLower(contentType)
	switch {
	case strings.Contains(contentType, "yaml"), strings.Contains(contentType, "yml"):
		return "yaml"
	case strings.Contains(contentType, "json"):
		return "json"
	default:
		return ""
	}
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

func statusForError(err error) int {
	if db.IsQuotaExceeded(err) {
		return http.StatusTooManyRequests
	}
	if db.IsValidationError(err) {
		return http.StatusBadRequest
	}
	return http.StatusInternalServerError
}
