package repository

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"fx-app-api/internal/storage"
)

type AuditRepository struct{ db *sql.DB }

func NewAuditRepository(store *storage.PostgresStore) *AuditRepository {
	return &AuditRepository{db: store.DB()}
}
func (r *AuditRepository) Init() error {
	_, err := r.db.Exec(`CREATE TABLE IF NOT EXISTS audit_logs (
		id BIGSERIAL PRIMARY KEY,
		actor_trader_id INT REFERENCES traders(id),
		action VARCHAR(100) NOT NULL,
		resource_type VARCHAR(100) NOT NULL,
		resource_id INT,
		before_data JSONB,
		after_data JSONB,
		created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
	);
	CREATE INDEX IF NOT EXISTS audit_logs_resource_idx ON audit_logs(resource_type, resource_id, created_at DESC);
	CREATE INDEX IF NOT EXISTS audit_logs_actor_idx ON audit_logs(actor_trader_id, created_at DESC);`)
	return err
}
func (r *AuditRepository) Record(actorID int, action, resourceType string, resourceID int, before, after any) error {
	beforeJSON, err := marshalNullable(before)
	if err != nil {
		return fmt.Errorf("marshal audit before state: %w", err)
	}
	afterJSON, err := marshalNullable(after)
	if err != nil {
		return fmt.Errorf("marshal audit after state: %w", err)
	}
	_, err = r.db.Exec(`INSERT INTO audit_logs(actor_trader_id, action, resource_type, resource_id, before_data, after_data) VALUES($1,$2,$3,$4,$5,$6)`, actorID, action, resourceType, resourceID, beforeJSON, afterJSON)
	if err != nil {
		return fmt.Errorf("write audit record: %w", err)
	}
	return nil
}
func marshalNullable(value any) (any, error) {
	if value == nil {
		return nil, nil
	}
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return string(data), nil
}
