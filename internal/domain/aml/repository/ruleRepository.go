package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"

	"fx-app-api/internal/domain/aml/engine"
)

type RuleRepository struct {
	db *sql.DB
}

type ruleQueryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func NewRuleRepository(db *sql.DB) *RuleRepository {
	return &RuleRepository{db: db}
}

// LoadShadowRules reads effective, enabled SHADOW rules, applying the current store override
// when one exists. Parameter validation and implementation selection remain in the engine.
func (r *RuleRepository) LoadShadowRules(ctx context.Context, storeID *int64) ([]engine.PersistedRule, error) {
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("load AML rules: database is nil")
	}
	return loadRules(ctx, r.db, storeID, false)
}

// LoadShadowRulesWithTx reads the catalogue using the decision transaction's snapshot.
func (r *RuleRepository) LoadShadowRulesWithTx(ctx context.Context, tx *sql.Tx, storeID *int64) ([]engine.PersistedRule, error) {
	if r == nil || tx == nil {
		return nil, fmt.Errorf("load AML rules: transaction is required")
	}
	return loadRules(ctx, tx, storeID, false)
}

func (r *RuleRepository) LoadDecisionRulesWithTx(ctx context.Context, tx *sql.Tx, storeID *int64) ([]engine.PersistedRule, error) {
	if r == nil || tx == nil {
		return nil, fmt.Errorf("load AML rules: transaction is required")
	}
	return loadRules(ctx, tx, storeID, true)
}

func loadRules(ctx context.Context, queryer ruleQueryer, storeID *int64, allowEnforce bool) ([]engine.PersistedRule, error) {
	modeFilter := `= 'SHADOW'`
	enabledFilter := `AND COALESCE(o.enabled, r.enabled) = TRUE`
	if allowEnforce {
		modeFilter = `IN ('OFF','SHADOW','ENFORCE')`
		enabledFilter = ``
	}
	query := `
		SELECT r.code, r.version, r.category,
			COALESCE(o.mode, r.mode), COALESCE(o.enabled, r.enabled),
			COALESCE(o.score, r.score)::TEXT,
			COALESCE(o.severity, r.severity), COALESCE(o.action, r.action),
			COALESCE(o.parameters, r.parameters)
		FROM aml_rules r
		LEFT JOIN aml_rule_overrides o
			ON o.rule_code = r.code AND o.store_id = $1
			AND o.effective_from <= CURRENT_TIMESTAMP
			AND (o.effective_to IS NULL OR o.effective_to > CURRENT_TIMESTAMP)
		WHERE r.effective_from <= CURRENT_TIMESTAMP
			AND (r.effective_to IS NULL OR r.effective_to > CURRENT_TIMESTAMP)
			` + enabledFilter + `
			AND COALESCE(o.mode, r.mode) ` + modeFilter + `
		ORDER BY r.code`
	rows, err := queryer.QueryContext(ctx, query, storeID)
	if err != nil {
		return nil, fmt.Errorf("query AML shadow rules: %w", err)
	}
	defer rows.Close()

	var rules []engine.PersistedRule
	for rows.Next() {
		var rule engine.PersistedRule
		var category, mode, score string
		var parameters []byte
		if err := rows.Scan(
			&rule.Code, &rule.Version, &category, &mode, &rule.Enabled, &score,
			&rule.Severity, &rule.Action, &parameters,
		); err != nil {
			return nil, fmt.Errorf("scan AML shadow rule: %w", err)
		}
		rule.Category = engine.Category(category)
		rule.Mode = engine.Mode(mode)
		rule.Score, err = strconv.Atoi(score)
		if err != nil || rule.Score < 0 {
			return nil, fmt.Errorf("AML rule %q score %q must be a non-negative integer", rule.Code, score)
		}
		if !json.Valid(parameters) {
			return nil, fmt.Errorf("AML rule %q parameters are not valid JSON", rule.Code)
		}
		rule.Parameters = append(json.RawMessage(nil), parameters...)
		rules = append(rules, rule)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read AML shadow rules: %w", err)
	}
	return rules, nil
}
