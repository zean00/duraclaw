package db

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

type User struct {
	CustomerID string          `json:"customer_id"`
	ID         string          `json:"id"`
	Metadata   json.RawMessage `json:"metadata"`
	CreatedAt  time.Time       `json:"created_at"`
}

func (s *Store) User(ctx context.Context, customerID, userID string) (*User, error) {
	if customerID == "" || userID == "" {
		return nil, fmt.Errorf("customer_id and user_id are required")
	}
	var user User
	err := s.pool.QueryRow(ctx, `
		SELECT customer_id, id, metadata, created_at
		FROM users
		WHERE customer_id=$1 AND id=$2`, customerID, userID).
		Scan(&user.CustomerID, &user.ID, &user.Metadata, &user.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &user, nil
}

func (s *Store) UserMetadata(ctx context.Context, customerID, userID string) (map[string]any, error) {
	user, err := s.User(ctx, customerID, userID)
	if err != nil {
		return nil, err
	}
	var metadata map[string]any
	_ = json.Unmarshal(user.Metadata, &metadata)
	if metadata == nil {
		metadata = map[string]any{}
	}
	return metadata, nil
}

func (s *Store) UpdateUserMetadata(ctx context.Context, customerID, userID string, metadata any) error {
	if customerID == "" || userID == "" {
		return fmt.Errorf("customer_id and user_id are required")
	}
	b, _ := json.Marshal(metadata)
	if len(b) == 0 || string(b) == "null" {
		b = []byte(`{}`)
	}
	tag, err := s.pool.Exec(ctx, `
		UPDATE users
		SET metadata=$3
		WHERE customer_id=$1 AND id=$2`, customerID, userID, b)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("user not found")
	}
	return nil
}

func (s *Store) MergeUserMetadata(ctx context.Context, customerID, userID string, metadata any) error {
	if customerID == "" || userID == "" {
		return fmt.Errorf("customer_id and user_id are required")
	}
	b, _ := json.Marshal(metadata)
	if len(b) == 0 || string(b) == "null" {
		b = []byte(`{}`)
	}
	tag, err := s.pool.Exec(ctx, `
		UPDATE users
		SET metadata=metadata || $3::jsonb
		WHERE customer_id=$1 AND id=$2`, customerID, userID, b)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("user not found")
	}
	return nil
}

func (s *Store) UpsertUserRecommendationPolicy(ctx context.Context, customerID, userID string, policy SessionRecommendationPolicy) error {
	if customerID == "" || userID == "" {
		return fmt.Errorf("customer_id and user_id are required")
	}
	policy = NormalizeRecommendationPolicy(policy)
	payload := map[string]any{"recommendation": policy}
	b, _ := json.Marshal(payload)
	if len(b) == 0 || string(b) == "null" {
		b = []byte(`{}`)
	}
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `
			INSERT INTO users(customer_id,id,metadata)
			VALUES($1,$2,$3)
			ON CONFLICT(customer_id,id) DO UPDATE
			SET metadata=users.metadata || EXCLUDED.metadata`, customerID, userID, b); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `
			UPDATE sessions
			SET metadata=metadata || $3::jsonb, updated_at=now()
			WHERE customer_id=$1
			  AND (user_id=$2 OR metadata->>'channel_user_id'=$2 OR lower(metadata->>'channel_user_id')=$4)`,
			customerID, userID, b, strings.ToLower(userID))
		return err
	})
}
