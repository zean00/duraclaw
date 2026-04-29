package db

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
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
