package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"fx-app-api/internal/domain/aml/engine"
)

var ErrIdempotencyConflict = errors.New("AML idempotency key was already used")

type DecisionRepository struct{ db *sql.DB }

func NewDecisionRepository(db *sql.DB) *DecisionRepository { return &DecisionRepository{db: db} }

// LockPerson serializes all AML evaluations for a person in the caller's SQL transaction.
func (r *DecisionRepository) LockPerson(ctx context.Context, tx *sql.Tx, personID int64) error {
	if tx == nil || personID <= 0 {
		return errors.New("AML person lock requires a transaction and positive person_id")
	}
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('AML-PERSON:' || $1::text, 0))`, personID); err != nil {
		return fmt.Errorf("lock AML person %d: %w", personID, err)
	}
	return nil
}

// LockIdempotencyKey serializes retries even when a client accidentally reuses the key for another person.
func (r *DecisionRepository) LockIdempotencyKey(ctx context.Context, tx *sql.Tx, key string) error {
	if tx == nil || key == "" {
		return errors.New("AML idempotency lock requires a transaction and key")
	}
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('AML-IDEMP:' || $1, 0))`, key); err != nil {
		return fmt.Errorf("lock AML idempotency key: %w", err)
	}
	return nil
}

// FindByIdempotencyKey must be called after LockPerson and before evaluating the operation.
func (r *DecisionRepository) FindByIdempotencyKey(ctx context.Context, tx *sql.Tx, key string) (int64, engine.DecisionOutcome, bool, error) {
	if tx == nil || key == "" {
		return 0, "", false, errors.New("AML idempotency lookup requires a transaction and key")
	}
	var id int64
	var outcome string
	err := tx.QueryRowContext(ctx, `SELECT id, decision FROM aml_decisions WHERE idempotency_key=$1`, key).Scan(&id, &outcome)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, "", false, nil
	}
	if err != nil {
		return 0, "", false, fmt.Errorf("lookup AML idempotency key: %w", err)
	}
	return id, engine.DecisionOutcome(outcome), true, nil
}

func (r *DecisionRepository) FindSubmission(ctx context.Context, tx *sql.Tx, key string) (decisionID, operationID, personID int64, outcome engine.DecisionOutcome, snapshot []byte, found bool, err error) {
	if tx == nil || key == "" {
		return 0, 0, 0, "", nil, false, errors.New("AML idempotency lookup requires a transaction and key")
	}
	err = tx.QueryRowContext(ctx, `SELECT id, COALESCE(operation_id,0), person_id, decision, input_snapshot FROM aml_decisions WHERE idempotency_key=$1`, key).Scan(&decisionID, &operationID, &personID, &outcome, &snapshot)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, 0, 0, "", nil, false, nil
	}
	if err != nil {
		return 0, 0, 0, "", nil, false, fmt.Errorf("lookup AML idempotency key: %w", err)
	}
	return decisionID, operationID, personID, outcome, snapshot, true, nil
}

// CreateWithTx appends the decision snapshot in the same transaction as the operation.
func (r *DecisionRepository) CreateWithTx(ctx context.Context, tx *sql.Tx, personID int64, operationID *int64, key, version string, input any, decision engine.Decision) (int64, error) {
	if tx == nil || personID <= 0 || key == "" || version == "" {
		return 0, errors.New("AML decision requires transaction, person_id, idempotency key, and engine version")
	}
	if decision.Outcome != engine.DecisionAccept && decision.Outcome != engine.DecisionAcceptWithApproval && decision.Outcome != engine.DecisionBlock {
		return 0, fmt.Errorf("invalid AML decision outcome %q", decision.Outcome)
	}
	inputJSON, err := json.Marshal(input)
	if err != nil {
		return 0, fmt.Errorf("marshal AML decision input: %w", err)
	}
	resultsJSON, err := json.Marshal(decision.Results)
	if err != nil {
		return 0, fmt.Errorf("marshal AML rule results: %w", err)
	}
	var id int64
	err = tx.QueryRowContext(ctx, `INSERT INTO aml_decisions
		(person_id, operation_id, idempotency_key, decision, engine_version, input_snapshot, rule_results)
		VALUES ($1,$2,$3,$4,$5,$6::jsonb,$7::jsonb) RETURNING id`,
		personID, operationID, key, decision.Outcome, version, inputJSON, resultsJSON).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("append AML decision: %w", err)
	}
	return id, nil
}
