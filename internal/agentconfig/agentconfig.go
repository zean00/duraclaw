package agentconfig

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"duraclaw/internal/db"

	"gopkg.in/yaml.v3"
)

type Store interface {
	CreateAgentInstanceVersion(ctx context.Context, spec db.AgentInstanceVersionSpec) (*db.AgentInstanceVersion, error)
	AgentInstanceVersion(ctx context.Context, versionID string) (*db.AgentInstanceVersion, error)
}

type Document struct {
	CustomerID          string         `json:"customer_id" yaml:"customer_id"`
	AgentInstanceID     string         `json:"agent_instance_id" yaml:"agent_instance_id"`
	Version             int            `json:"version,omitempty" yaml:"version,omitempty"`
	Name                string         `json:"name,omitempty" yaml:"name,omitempty"`
	ModelConfig         map[string]any `json:"model_config,omitempty" yaml:"model_config,omitempty"`
	SystemInstructions  string         `json:"system_instructions,omitempty" yaml:"system_instructions,omitempty"`
	ToolConfig          map[string]any `json:"tool_config,omitempty" yaml:"tool_config,omitempty"`
	MCPConfig           map[string]any `json:"mcp_config,omitempty" yaml:"mcp_config,omitempty"`
	WorkflowConfig      map[string]any `json:"workflow_config,omitempty" yaml:"workflow_config,omitempty"`
	PolicyConfig        map[string]any `json:"policy_config,omitempty" yaml:"policy_config,omitempty"`
	ProfileConfig       map[string]any `json:"profile_config,omitempty" yaml:"profile_config,omitempty"`
	Metadata            map[string]any `json:"metadata,omitempty" yaml:"metadata,omitempty"`
	ActivateImmediately bool           `json:"activate_immediately,omitempty" yaml:"activate_immediately,omitempty"`
}

func Import(ctx context.Context, store Store, doc Document) (*db.AgentInstanceVersion, error) {
	if store == nil {
		return nil, fmt.Errorf("store is required")
	}
	if strings.TrimSpace(doc.CustomerID) == "" {
		return nil, fmt.Errorf("customer_id is required")
	}
	if strings.TrimSpace(doc.AgentInstanceID) == "" {
		return nil, fmt.Errorf("agent_instance_id is required")
	}
	return store.CreateAgentInstanceVersion(ctx, db.AgentInstanceVersionSpec{
		CustomerID:          doc.CustomerID,
		AgentInstanceID:     doc.AgentInstanceID,
		Version:             doc.Version,
		Name:                doc.Name,
		ModelConfig:         doc.ModelConfig,
		SystemInstructions:  doc.SystemInstructions,
		ToolConfig:          doc.ToolConfig,
		MCPConfig:           doc.MCPConfig,
		WorkflowConfig:      doc.WorkflowConfig,
		PolicyConfig:        doc.PolicyConfig,
		ProfileConfig:       doc.ProfileConfig,
		Metadata:            doc.Metadata,
		ActivateImmediately: doc.ActivateImmediately,
	})
}

func Export(ctx context.Context, store Store, versionID string) (Document, error) {
	if store == nil {
		return Document{}, fmt.Errorf("store is required")
	}
	version, err := store.AgentInstanceVersion(ctx, versionID)
	if err != nil {
		return Document{}, err
	}
	if version == nil {
		return Document{}, fmt.Errorf("agent instance version not found")
	}
	return DocumentFromVersion(*version)
}

func DocumentFromVersion(version db.AgentInstanceVersion) (Document, error) {
	doc := Document{
		CustomerID:         version.CustomerID,
		AgentInstanceID:    version.AgentInstanceID,
		Version:            version.Version,
		Name:               version.Name,
		SystemInstructions: version.SystemInstructions,
	}
	var err error
	if doc.ModelConfig, err = decodeMap(version.ModelConfig); err != nil {
		return Document{}, fmt.Errorf("model_config: %w", err)
	}
	if doc.ToolConfig, err = decodeMap(version.ToolConfig); err != nil {
		return Document{}, fmt.Errorf("tool_config: %w", err)
	}
	if doc.MCPConfig, err = decodeMap(version.MCPConfig); err != nil {
		return Document{}, fmt.Errorf("mcp_config: %w", err)
	}
	if doc.WorkflowConfig, err = decodeMap(version.WorkflowConfig); err != nil {
		return Document{}, fmt.Errorf("workflow_config: %w", err)
	}
	if doc.PolicyConfig, err = decodeMap(version.PolicyConfig); err != nil {
		return Document{}, fmt.Errorf("policy_config: %w", err)
	}
	if doc.ProfileConfig, err = decodeMap(version.ProfileConfig); err != nil {
		return Document{}, fmt.Errorf("profile_config: %w", err)
	}
	if doc.Metadata, err = decodeMap(version.Metadata); err != nil {
		return Document{}, fmt.Errorf("metadata: %w", err)
	}
	return doc, nil
}

func Decode(r io.Reader, format string) (Document, error) {
	raw, err := io.ReadAll(r)
	if err != nil {
		return Document{}, err
	}
	var doc Document
	switch normalizeFormat(format, raw) {
	case "yaml":
		if err := yaml.Unmarshal(raw, &doc); err != nil {
			return Document{}, err
		}
	case "json":
		if err := json.Unmarshal(raw, &doc); err != nil {
			return Document{}, err
		}
	default:
		return Document{}, fmt.Errorf("unsupported format %q", format)
	}
	return normalizeDocument(doc)
}

func Encode(doc Document, format string) ([]byte, error) {
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "", "json":
		return json.MarshalIndent(doc, "", "  ")
	case "yaml", "yml":
		return yaml.Marshal(doc)
	default:
		return nil, fmt.Errorf("unsupported format %q", format)
	}
}

func decodeMap(raw json.RawMessage) (map[string]any, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

func normalizeFormat(format string, raw []byte) string {
	format = strings.ToLower(strings.TrimSpace(format))
	switch format {
	case "":
	case "yml":
		return "yaml"
	case "yaml", "json":
		return format
	default:
		return format
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) > 0 && (trimmed[0] == '{' || trimmed[0] == '[') {
		return "json"
	}
	return "yaml"
}

func normalizeDocument(doc Document) (Document, error) {
	raw, err := json.Marshal(doc)
	if err != nil {
		return Document{}, fmt.Errorf("agent config must be JSON-compatible: %w", err)
	}
	var normalized Document
	if err := json.Unmarshal(raw, &normalized); err != nil {
		return Document{}, err
	}
	return normalized, nil
}
