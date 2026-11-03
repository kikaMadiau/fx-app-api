package storage

import (
	"database/sql"
	"fmt"
)

// InitTenantSchema upgrades the legacy single-office schema to the store model.
// It is safe to run on every startup.
func InitTenantSchema(db *sql.DB) error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin tenant migration: %w", err)
	}
	defer tx.Rollback()
	statements := []string{
		`CREATE TABLE IF NOT EXISTS stores (
		 id SERIAL PRIMARY KEY, name VARCHAR(255) NOT NULL, bcc_license_number VARCHAR(255) NOT NULL,
		 license_issued_at TIMESTAMPTZ, license_expires_at TIMESTAMPTZ,
		 status VARCHAR(20) NOT NULL DEFAULT 'ACTIVE' CHECK (status IN ('ACTIVE','SUSPENDED','CLOSED')),
		 created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP, updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP, deleted_at TIMESTAMPTZ)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS stores_bcc_license_active_idx ON stores(bcc_license_number) WHERE deleted_at IS NULL`,
		`ALTER TABLE traders ADD COLUMN IF NOT EXISTS store_id INT REFERENCES stores(id), ADD COLUMN IF NOT EXISTS roles TEXT[] NOT NULL DEFAULT ARRAY['TRADER']::TEXT[], ADD COLUMN IF NOT EXISTS is_platform_admin BOOLEAN NOT NULL DEFAULT FALSE, ADD COLUMN IF NOT EXISTS must_change_password BOOLEAN NOT NULL DEFAULT FALSE`,
		`INSERT INTO stores(name,bcc_license_number) SELECT 'Default','MIGRATION-PENDING' WHERE NOT EXISTS (SELECT 1 FROM stores WHERE bcc_license_number='MIGRATION-PENDING' AND deleted_at IS NULL)`,
		`UPDATE traders SET store_id=(SELECT id FROM stores WHERE bcc_license_number='MIGRATION-PENDING' AND deleted_at IS NULL ORDER BY id LIMIT 1) WHERE store_id IS NULL AND NOT is_platform_admin`,
		`UPDATE traders SET roles=ARRAY[CASE UPPER(COALESCE(role,'')) WHEN 'MANAGER' THEN 'SUPERVISOR' WHEN 'COMPLIANCE_OFFICER' THEN 'COMPLIANCE_OFFICER' WHEN 'SUPERVISOR' THEN 'SUPERVISOR' ELSE 'TRADER' END]::TEXT[] WHERE roles IS NULL OR cardinality(roles)=0`,
		`UPDATE traders SET is_platform_admin=TRUE,store_id=NULL,roles='{}'::TEXT[] WHERE UPPER(COALESCE(role,''))='ADMIN'`,
		`ALTER TABLE customers ADD COLUMN IF NOT EXISTS store_id INT REFERENCES stores(id)`,
		`ALTER TABLE transactions ADD COLUMN IF NOT EXISTS store_id INT REFERENCES stores(id)`,
		`ALTER TABLE risk_flags ADD COLUMN IF NOT EXISTS store_id INT REFERENCES stores(id)`,
		`ALTER TABLE risk_flag_status_history ADD COLUMN IF NOT EXISTS store_id INT REFERENCES stores(id)`,
		`ALTER TABLE customer_kyc_profiles ADD COLUMN IF NOT EXISTS store_id INT REFERENCES stores(id)`,
		`UPDATE customers SET store_id=COALESCE((SELECT store_id FROM traders WHERE store_id IS NOT NULL ORDER BY id LIMIT 1),(SELECT id FROM stores WHERE bcc_license_number='MIGRATION-PENDING' AND deleted_at IS NULL ORDER BY id LIMIT 1)) WHERE store_id IS NULL`,
		`UPDATE transactions t SET store_id=COALESCE(tr.store_id,(SELECT id FROM stores WHERE bcc_license_number='MIGRATION-PENDING' AND deleted_at IS NULL ORDER BY id LIMIT 1)) FROM traders tr WHERE t.trader_id=tr.id AND t.store_id IS NULL`,
		`UPDATE risk_flags rf SET store_id=COALESCE((SELECT store_id FROM transactions t WHERE t.id=rf.transaction_id),(SELECT store_id FROM traders tr WHERE tr.id=rf.trader_id),(SELECT id FROM stores WHERE bcc_license_number='MIGRATION-PENDING' AND deleted_at IS NULL ORDER BY id LIMIT 1)) WHERE rf.store_id IS NULL`,
		`UPDATE risk_flag_status_history h SET store_id=rf.store_id FROM risk_flags rf WHERE h.risk_flag_id=rf.id AND h.store_id IS NULL`,
		`UPDATE customer_kyc_profiles p SET store_id=c.store_id FROM customers c WHERE p.customer_id=c.id AND p.store_id IS NULL`,
		`ALTER TABLE customers ALTER COLUMN store_id SET NOT NULL`,
		`ALTER TABLE transactions ALTER COLUMN store_id SET NOT NULL`,
		`ALTER TABLE risk_flags ALTER COLUMN store_id SET NOT NULL`,
		`ALTER TABLE risk_flag_status_history ALTER COLUMN store_id SET NOT NULL`,
		`ALTER TABLE customer_kyc_profiles ALTER COLUMN store_id SET NOT NULL`,
		`DROP INDEX IF EXISTS customers_id_number_unique_idx`,
		`DROP INDEX IF EXISTS customers_phone_unique_idx`,
		`CREATE UNIQUE INDEX IF NOT EXISTS customers_store_id_number_unique_idx ON customers(store_id,id_number) WHERE deleted_at IS NULL`,
		`CREATE UNIQUE INDEX IF NOT EXISTS customers_store_phone_unique_idx ON customers(store_id,phone) WHERE phone IS NOT NULL AND phone<>'' AND deleted_at IS NULL`,
		`CREATE INDEX IF NOT EXISTS customers_store_idx ON customers(store_id)`,
		`CREATE INDEX IF NOT EXISTS transactions_store_idx ON transactions(store_id)`,
		`CREATE INDEX IF NOT EXISTS risk_flags_store_idx ON risk_flags(store_id)`,
		`CREATE INDEX IF NOT EXISTS risk_flag_history_store_idx ON risk_flag_status_history(store_id)`,
		`CREATE TABLE IF NOT EXISTS audit_log (id BIGSERIAL PRIMARY KEY, store_id INT REFERENCES stores(id), actor_id INT REFERENCES traders(id), action TEXT NOT NULL, entity TEXT NOT NULL, entity_id BIGINT, before JSONB, after JSONB, created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP)`,
	}
	for _, statement := range statements {
		if _, err := tx.Exec(statement); err != nil {
			return fmt.Errorf("tenant migration failed: %w", err)
		}
	}
	var defaultStoreID int
	if err := tx.QueryRow(`SELECT id FROM stores WHERE bcc_license_number='MIGRATION-PENDING' AND deleted_at IS NULL ORDER BY id LIMIT 1`).Scan(&defaultStoreID); err != nil {
		return fmt.Errorf("find migration store: %w", err)
	}
	for _, table := range []string{"customers", "transactions", "risk_flags", "risk_flag_status_history", "customer_kyc_profiles"} {
		if _, err := tx.Exec(fmt.Sprintf(`ALTER TABLE %s ALTER COLUMN store_id SET DEFAULT %d`, table, defaultStoreID)); err != nil {
			return fmt.Errorf("set default store on %s: %w", table, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit tenant migration: %w", err)
	}
	return nil
}
