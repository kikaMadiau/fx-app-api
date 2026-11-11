package storage

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
)

// InitAMLSchema creates the AML rule and decision tables, then seeds the global rule catalogue
// from the existing risk-rules JSON file. It is safe to call on each startup.
func InitAMLSchema(db *sql.DB) error {
	if db == nil {
		return errors.New("initialize AML schema: database is nil")
	}
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin AML migration: %w", err)
	}
	defer tx.Rollback()

	statements := []string{
		`CREATE TABLE IF NOT EXISTS aml_outbox (
			id BIGSERIAL PRIMARY KEY,
			event_type TEXT NOT NULL CHECK (event_type IN ('operation.created','operation.corrected','alert.created','alert.status_changed','str.filed','operator.status_changed','rate.gap_exceeded')),
			aggregate_type TEXT NOT NULL,
			aggregate_id BIGINT,
			payload JSONB NOT NULL CHECK (jsonb_typeof(payload) = 'object'),
			created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
			published_at TIMESTAMPTZ,
			attempts INT NOT NULL DEFAULT 0 CHECK (attempts >= 0),
			last_error TEXT
		)`,
		`CREATE INDEX IF NOT EXISTS aml_outbox_pending_idx ON aml_outbox(created_at,id) WHERE published_at IS NULL`,
		`CREATE TABLE IF NOT EXISTS aml_bcc_rates (
			currency CHAR(3) NOT NULL,
			reference_day DATE NOT NULL,
			units_per_usd_ppm BIGINT NOT NULL CHECK (units_per_usd_ppm > 0),
			source TEXT NOT NULL,
			published_at TIMESTAMPTZ NOT NULL,
			created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY(currency, reference_day)
		)`,
		`CREATE TABLE IF NOT EXISTS aml_rules (
			code TEXT PRIMARY KEY,
			rule_type TEXT NOT NULL,
			version TEXT NOT NULL,
			category TEXT NOT NULL CHECK (category IN ('HARD_BLOCK','KYC_LEVEL','SCREENING','DETECTION')),
			mode TEXT NOT NULL DEFAULT 'OFF' CHECK (mode IN ('OFF','SHADOW','ENFORCE')),
			enabled BOOLEAN NOT NULL DEFAULT TRUE,
			parameters JSONB NOT NULL DEFAULT '{}'::JSONB CHECK (jsonb_typeof(parameters) = 'object'),
			score NUMERIC NOT NULL DEFAULT 0 CHECK (score >= 0),
			severity TEXT NOT NULL DEFAULT 'UNSPECIFIED',
			action TEXT NOT NULL DEFAULT 'ALERT' CHECK (action IN ('SCORE_ONLY','ALERT','REVIEW','BLOCK')),
			scope TEXT CHECK (scope IS NULL OR scope IN ('PER_OPERATOR','ALL_OPERATORS')),
			effective_from TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
			effective_to TIMESTAMPTZ,
			created_by INT REFERENCES traders(id),
			updated_by INT REFERENCES traders(id),
			created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
			CHECK (effective_to IS NULL OR effective_to >= effective_from)
		)`,
		`CREATE TABLE IF NOT EXISTS aml_rule_overrides (
			store_id INT NOT NULL REFERENCES stores(id),
			rule_code TEXT NOT NULL REFERENCES aml_rules(code),
			enabled BOOLEAN,
			mode TEXT CHECK (mode IS NULL OR mode IN ('OFF','SHADOW','ENFORCE')),
			parameters JSONB CHECK (parameters IS NULL OR jsonb_typeof(parameters) = 'object'),
			score NUMERIC CHECK (score IS NULL OR score >= 0),
			severity TEXT,
			action TEXT CHECK (action IS NULL OR action IN ('SCORE_ONLY','ALERT','REVIEW','BLOCK')),
			effective_from TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
			effective_to TIMESTAMPTZ,
			created_by INT REFERENCES traders(id),
			updated_by INT REFERENCES traders(id),
			created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY (store_id, rule_code),
			CHECK (effective_to IS NULL OR effective_to >= effective_from)
		)`,
		`CREATE INDEX IF NOT EXISTS aml_rules_effective_idx ON aml_rules(category, enabled, effective_from, effective_to)`,
		`CREATE TABLE IF NOT EXISTS aml_decisions (
			id BIGSERIAL PRIMARY KEY,
			person_id INT NOT NULL REFERENCES persons(id),
			operation_id BIGINT,
			idempotency_key TEXT UNIQUE,
			decision TEXT NOT NULL CHECK (decision IN ('ACCEPT','ACCEPT_WITH_APPROVAL','BLOCK')),
			engine_version TEXT NOT NULL,
			input_snapshot JSONB NOT NULL CHECK (jsonb_typeof(input_snapshot) = 'object'),
			rule_results JSONB NOT NULL CHECK (jsonb_typeof(rule_results) = 'array'),
			corrected_from_id BIGINT REFERENCES aml_decisions(id),
			created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
			retention_until TIMESTAMPTZ NOT NULL DEFAULT (CURRENT_TIMESTAMP + INTERVAL '10 years')
		)`,
		`CREATE INDEX IF NOT EXISTS aml_decisions_person_created_idx ON aml_decisions(person_id, created_at DESC)`,
		`CREATE INDEX IF NOT EXISTS aml_decisions_retention_idx ON aml_decisions(retention_until)`,
		`CREATE TABLE IF NOT EXISTS aml_person_daily (
			person_id INT NOT NULL REFERENCES persons(id),
			reference_day DATE NOT NULL,
			total_usd_minor BIGINT NOT NULL DEFAULT 0 CHECK (total_usd_minor >= 0),
			operation_count BIGINT NOT NULL DEFAULT 0 CHECK (operation_count >= 0),
			last_operation_at TIMESTAMPTZ,
			updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY (person_id, reference_day)
		)`,
		`CREATE OR REPLACE FUNCTION aml_decisions_append_only() RETURNS TRIGGER AS $$
		BEGIN
			RAISE EXCEPTION 'aml_decisions is append-only; insert a correcting decision instead';
		END;
		$$ LANGUAGE plpgsql`,
		`DROP TRIGGER IF EXISTS aml_decisions_no_update_or_delete ON aml_decisions`,
		`CREATE TRIGGER aml_decisions_no_update_or_delete
			BEFORE UPDATE OR DELETE ON aml_decisions
			FOR EACH ROW EXECUTE FUNCTION aml_decisions_append_only()`,
	}
	for _, statement := range statements {
		if _, err := tx.Exec(statement); err != nil {
			return fmt.Errorf("AML migration failed: %w", err)
		}
	}

	seedPath, err := amlRulesConfigPath()
	if err != nil {
		return err
	}
	seed, err := readAMLRuleSeed(seedPath)
	if err != nil {
		return fmt.Errorf("read AML rule seed %q: %w", seedPath, err)
	}
	for _, section := range []struct {
		key      string
		category string
		rules    []map[string]json.RawMessage
	}{
		{key: "transaction_rules", category: "DETECTION", rules: seed.TransactionRules},
		{key: "customer_rules", category: "KYC_LEVEL", rules: seed.CustomerRules},
	} {
		for _, rule := range section.rules {
			if err := insertAMLSeedRule(tx, section.category, rule); err != nil {
				return fmt.Errorf("seed AML rule from %s: %w", section.key, err)
			}
		}
	}
	for _, rule := range seed.NewRules {
		if err := insertParameterizedAMLSeedRule(tx, rule); err != nil {
			return fmt.Errorf("seed new AML rule: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit AML migration: %w", err)
	}
	return nil
}

type amlRuleSeedFile struct {
	TransactionRules []map[string]json.RawMessage `json:"transaction_rules"`
	CustomerRules    []map[string]json.RawMessage `json:"customer_rules"`
	NewRules         []map[string]json.RawMessage `json:"new_rules"`
}

func amlRulesConfigPath() (string, error) {
	if path := strings.TrimSpace(os.Getenv("RISK_RULES_CONFIG_PATH")); path != "" {
		return path, nil
	}
	for _, path := range []string{"config/risk_rules.json", "config/riskRules.json"} {
		if _, err := os.Stat(path); err == nil {
			return path, nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("inspect AML rule seed %q: %w", path, err)
		}
	}
	return "", errors.New("AML rule seed not found (looked for config/risk_rules.json and config/riskRules.json)")
}

func readAMLRuleSeed(path string) (amlRuleSeedFile, error) {
	file, err := os.Open(path)
	if err != nil {
		return amlRuleSeedFile{}, err
	}
	defer file.Close()

	var seed amlRuleSeedFile
	decoder := json.NewDecoder(file)
	if err := decoder.Decode(&seed); err != nil {
		return amlRuleSeedFile{}, fmt.Errorf("decode JSON: %w", err)
	}
	if len(seed.TransactionRules)+len(seed.CustomerRules)+len(seed.NewRules) == 0 {
		return amlRuleSeedFile{}, errors.New("no transaction_rules, customer_rules, or new_rules were defined")
	}
	return seed, nil
}

func insertParameterizedAMLSeedRule(tx *sql.Tx, raw map[string]json.RawMessage) error {
	var code, ruleType, version, category, mode, severity, action string
	for key, target := range map[string]*string{
		"code": &code, "type": &ruleType, "version": &version, "category": &category,
		"mode": &mode, "severity": &severity, "action": &action,
	} {
		if err := json.Unmarshal(raw[key], target); err != nil || strings.TrimSpace(*target) == "" {
			return fmt.Errorf("new AML rule %q requires %s", code, key)
		}
	}
	var enabled bool
	if err := json.Unmarshal(raw["enabled"], &enabled); err != nil {
		return fmt.Errorf("new AML rule %q enabled must be boolean: %w", code, err)
	}
	var score int64
	if err := json.Unmarshal(raw["score"], &score); err != nil || score < 0 {
		return fmt.Errorf("new AML rule %q score must be a non-negative integer", code)
	}
	parameters := raw["parameters"]
	var parametersObject map[string]json.RawMessage
	if len(parameters) == 0 || json.Unmarshal(parameters, &parametersObject) != nil || parametersObject == nil {
		return fmt.Errorf("new AML rule %q parameters must be a JSON object", code)
	}
	_, err := tx.Exec(`
		INSERT INTO aml_rules (
			code, rule_type, version, category, mode, enabled, parameters, score,
			severity, action, scope
		) VALUES ($1,$2,$3,$4,$5,$6,$7::JSONB,$8,$9,$10,NULL)
		ON CONFLICT (code) DO NOTHING`,
		code, ruleType, version, category, mode, enabled, string(parameters), score, severity, action,
	)
	if err != nil {
		return fmt.Errorf("insert rule %q: %w", code, err)
	}
	return nil
}

func insertAMLSeedRule(tx *sql.Tx, category string, raw map[string]json.RawMessage) error {
	var code, ruleType string
	if err := json.Unmarshal(raw["name"], &code); err != nil || strings.TrimSpace(code) == "" {
		return errors.New("rule name is required")
	}
	if err := json.Unmarshal(raw["type"], &ruleType); err != nil || strings.TrimSpace(ruleType) == "" {
		return fmt.Errorf("rule %q type is required", code)
	}

	var enabled, alert = true, true
	if value, ok := raw["enabled"]; ok {
		if err := json.Unmarshal(value, &enabled); err != nil {
			return fmt.Errorf("rule %q enabled must be boolean: %w", code, err)
		}
	}
	if value, ok := raw["alert"]; ok {
		if err := json.Unmarshal(value, &alert); err != nil {
			return fmt.Errorf("rule %q alert must be boolean: %w", code, err)
		}
	}
	var score float64
	if value, ok := raw["score"]; ok {
		if err := json.Unmarshal(value, &score); err != nil || score < 0 {
			return fmt.Errorf("rule %q score must be a non-negative number", code)
		}
	}

	parameters := make(map[string]json.RawMessage, len(raw))
	for key, value := range raw {
		switch key {
		case "name", "type", "score", "enabled", "alert":
			continue
		default:
			parameters[key] = value
		}
	}
	encodedParameters, err := json.Marshal(parameters)
	if err != nil {
		return fmt.Errorf("encode parameters for %q: %w", code, err)
	}
	mode := "ENFORCE"
	if !enabled {
		mode = "OFF"
	}
	action := "ALERT"
	if !alert {
		action = "SCORE_ONLY"
	}
	_, err = tx.Exec(`
		INSERT INTO aml_rules (
			code, rule_type, version, category, mode, enabled, parameters, score,
			severity, action, scope
		) VALUES ($1, $2, 'legacy-1', $3, $4, $5, $6::JSONB, $7, 'UNSPECIFIED', $8, NULL)
		ON CONFLICT (code) DO NOTHING`,
		code, ruleType, category, mode, enabled, string(encodedParameters), score, action,
	)
	if err != nil {
		return fmt.Errorf("insert rule %q: %w", code, err)
	}
	return nil
}
