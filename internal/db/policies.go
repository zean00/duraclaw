package db

import (
	"context"
	"encoding/json"
)

type AgentPolicy struct {
	CustomerID           string   `json:"customer_id"`
	AgentInstanceID      string   `json:"agent_instance_id"`
	ArtifactMaxSizeBytes int64    `json:"artifact_max_size_bytes"`
	ArtifactMediaTypes   []string `json:"artifact_media_types"`
}

func (s *Store) UpsertAgentPolicy(ctx context.Context, p AgentPolicy) error {
	mediaTypes, _ := json.Marshal(p.ArtifactMediaTypes)
	_, err := s.pool.Exec(ctx, `
		INSERT INTO agent_policies(customer_id,agent_instance_id,artifact_max_size_bytes,artifact_media_types)
		VALUES($1,$2,$3,$4)
		ON CONFLICT (customer_id, agent_instance_id)
		DO UPDATE SET artifact_max_size_bytes=EXCLUDED.artifact_max_size_bytes, artifact_media_types=EXCLUDED.artifact_media_types, updated_at=now()`,
		p.CustomerID, p.AgentInstanceID, p.ArtifactMaxSizeBytes, mediaTypes)
	return err
}

func (s *Store) AgentPolicy(ctx context.Context, customerID, agentInstanceID string) (*AgentPolicy, error) {
	var p AgentPolicy
	var mediaTypes []byte
	err := s.pool.QueryRow(ctx, `
		SELECT customer_id, agent_instance_id, artifact_max_size_bytes, artifact_media_types
		FROM agent_policies
		WHERE customer_id=$1 AND agent_instance_id=$2`, customerID, agentInstanceID).
		Scan(&p.CustomerID, &p.AgentInstanceID, &p.ArtifactMaxSizeBytes, &mediaTypes)
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal(mediaTypes, &p.ArtifactMediaTypes)
	return &p, nil
}
