package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

type OutboxRepository struct{}

func NewOutboxRepository() *OutboxRepository { return &OutboxRepository{} }

func (r *OutboxRepository) CreateWithTx(ctx context.Context, tx *sql.Tx, eventType, aggregateType string, aggregateID int64, payload any) error {
	if tx == nil || eventType == "" || aggregateType == "" {
		return errors.New("outbox event requires transaction, event type, and aggregate type")
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal outbox event %s: %w", eventType, err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO aml_outbox(event_type,aggregate_type,aggregate_id,payload) VALUES($1,$2,NULLIF($3,0),$4::jsonb)`, eventType, aggregateType, aggregateID, encoded); err != nil {
		return fmt.Errorf("append outbox event %s: %w", eventType, err)
	}
	return nil
}
