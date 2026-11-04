package storage

import (
	"database/sql"
	"fmt"
	"os"
	"strings"
)

// InitTenantSchema upgrades the legacy single-office schema to the store model.
// It is safe to run on every startup.
func InitTenantSchema(db *sql.DB) error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin tenant migration: %w", err)
	}
	defer tx.Rollback()
	setupStatements := []string{
		`CREATE TABLE IF NOT EXISTS stores (
		 id SERIAL PRIMARY KEY, name VARCHAR(255) NOT NULL, bcc_license_number VARCHAR(255) NOT NULL,
		 license_issued_at TIMESTAMPTZ, license_expires_at TIMESTAMPTZ,
		 status VARCHAR(20) NOT NULL DEFAULT 'ACTIVE' CHECK (status IN ('ACTIVE','SUSPENDED','CLOSED')),
		 created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP, updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP, deleted_at TIMESTAMPTZ)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS stores_bcc_license_active_idx ON stores(bcc_license_number) WHERE deleted_at IS NULL`,
		`CREATE TABLE IF NOT EXISTS persons (id SERIAL PRIMARY KEY, full_name VARCHAR(255) NOT NULL, phone VARCHAR(50), id_type VARCHAR(50) NOT NULL DEFAULT '', id_number VARCHAR(100) NOT NULL DEFAULT '', date_of_birth DATE, created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP, updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS persons_identity_unique_idx ON persons(id_type,id_number) WHERE id_number <> ''`,
		`ALTER TABLE traders ADD COLUMN IF NOT EXISTS store_id INT REFERENCES stores(id), ADD COLUMN IF NOT EXISTS roles TEXT[] NOT NULL DEFAULT ARRAY['TRADER']::TEXT[], ADD COLUMN IF NOT EXISTS is_platform_admin BOOLEAN NOT NULL DEFAULT FALSE, ADD COLUMN IF NOT EXISTS must_change_password BOOLEAN NOT NULL DEFAULT FALSE`,
		`ALTER TABLE traders DROP COLUMN IF EXISTS name`,
		`UPDATE traders SET roles=ARRAY[CASE UPPER(COALESCE(role,'')) WHEN 'MANAGER' THEN 'SUPERVISOR' WHEN 'COMPLIANCE_OFFICER' THEN 'COMPLIANCE_OFFICER' WHEN 'SUPERVISOR' THEN 'SUPERVISOR' ELSE 'TRADER' END]::TEXT[] WHERE roles IS NULL OR cardinality(roles)=0 OR (roles=ARRAY['TRADER']::TEXT[] AND UPPER(COALESCE(role,'')) IN ('MANAGER','COMPLIANCE_OFFICER','SUPERVISOR'))`,
		`UPDATE traders SET is_platform_admin=TRUE,store_id=NULL,roles='{}'::TEXT[] WHERE UPPER(COALESCE(role,''))='ADMIN'`,
		`ALTER TABLE customers ADD COLUMN IF NOT EXISTS store_id INT REFERENCES stores(id)`,
		`ALTER TABLE customers ADD COLUMN IF NOT EXISTS person_id INT REFERENCES persons(id)`,
		`ALTER TABLE transactions ADD COLUMN IF NOT EXISTS store_id INT REFERENCES stores(id)`,
		`ALTER TABLE risk_flags ADD COLUMN IF NOT EXISTS store_id INT REFERENCES stores(id)`,
		`ALTER TABLE risk_flag_status_history ADD COLUMN IF NOT EXISTS store_id INT REFERENCES stores(id)`,
		`ALTER TABLE customer_kyc_profiles ADD COLUMN IF NOT EXISTS store_id INT REFERENCES stores(id)`,
	}
	for _, statement := range setupStatements {
		if _, err := tx.Exec(statement); err != nil {
			return fmt.Errorf("tenant migration failed: %w", err)
		}
	}

	storeName := strings.TrimSpace(os.Getenv("LEGACY_STORE_NAME"))
	if storeName == "" {
		storeName = "Demo Maison de Change"
	}
	licenseNumber := strings.TrimSpace(os.Getenv("LEGACY_STORE_BCC_LICENSE_NUMBER"))
	if licenseNumber == "" {
		licenseNumber = "DEMO-0001"
	}
	var legacyStoreID int
	if err := tx.QueryRow(`
		INSERT INTO stores(name,bcc_license_number,status)
		VALUES($1,$2,'ACTIVE')
		ON CONFLICT (bcc_license_number) WHERE deleted_at IS NULL
		DO UPDATE SET name=EXCLUDED.name,status='ACTIVE',updated_at=CURRENT_TIMESTAMP
		RETURNING id`, storeName, licenseNumber).Scan(&legacyStoreID); err != nil {
		return fmt.Errorf("create or find legacy migration store: %w", err)
	}

	statements := []string{
		`INSERT INTO persons(full_name,phone,id_type,id_number) SELECT DISTINCT ON (COALESCE(id_type,''),id_number) full_name,phone,COALESCE(id_type,''),id_number FROM customers WHERE id_number IS NOT NULL AND id_number<>'' ORDER BY COALESCE(id_type,''),id_number,id ON CONFLICT (id_type,id_number) WHERE id_number<>'' DO NOTHING`,
		`UPDATE customers c SET person_id=p.id FROM persons p WHERE c.person_id IS NULL AND c.id_number IS NOT NULL AND c.id_number<>'' AND COALESCE(c.id_type,'')=p.id_type AND c.id_number=p.id_number`,
		`UPDATE transactions t SET store_id=tr.store_id FROM traders tr WHERE t.trader_id=tr.id AND t.store_id IS NULL AND tr.store_id IS NOT NULL`,
		`UPDATE customers c SET store_id=(SELECT MIN(t.store_id) FROM transactions t WHERE t.customer_id=c.id AND t.store_id IS NOT NULL) WHERE c.store_id IS NULL AND EXISTS(SELECT 1 FROM transactions t WHERE t.customer_id=c.id AND t.store_id IS NOT NULL)`,
		`UPDATE risk_flags rf SET store_id=COALESCE((SELECT t.store_id FROM transactions t WHERE t.id=rf.transaction_id),(SELECT tr.store_id FROM traders tr WHERE tr.id=rf.trader_id)) WHERE rf.store_id IS NULL`,
		`UPDATE risk_flag_status_history h SET store_id=rf.store_id FROM risk_flags rf WHERE h.risk_flag_id=rf.id AND h.store_id IS NULL`,
		`UPDATE customer_kyc_profiles p SET store_id=c.store_id FROM customers c WHERE p.customer_id=c.id AND p.store_id IS NULL`,
		`UPDATE customers SET store_id=$1 WHERE store_id IS NULL`,
		`UPDATE transactions SET store_id=$1 WHERE store_id IS NULL`,
		`UPDATE risk_flags SET store_id=$1 WHERE store_id IS NULL`,
		`UPDATE risk_flag_status_history SET store_id=$1 WHERE store_id IS NULL`,
		`UPDATE customer_kyc_profiles SET store_id=$1 WHERE store_id IS NULL`,
		`UPDATE traders SET store_id=$1 WHERE store_id IS NULL AND NOT is_platform_admin`,
		`ALTER TABLE customers ALTER COLUMN store_id SET NOT NULL`,
		`ALTER TABLE transactions ALTER COLUMN store_id SET NOT NULL`,
		`ALTER TABLE risk_flags ALTER COLUMN store_id SET NOT NULL`,
		`ALTER TABLE risk_flag_status_history ALTER COLUMN store_id SET NOT NULL`,
		`ALTER TABLE customer_kyc_profiles ALTER COLUMN store_id SET NOT NULL`,
		`DROP INDEX IF EXISTS customers_id_number_unique_idx`,
		`DROP INDEX IF EXISTS customers_phone_unique_idx`,
		`CREATE UNIQUE INDEX IF NOT EXISTS customers_store_id_number_unique_idx ON customers(store_id,id_number) WHERE deleted_at IS NULL AND id_number IS NOT NULL AND id_number<>''`,
		`CREATE UNIQUE INDEX IF NOT EXISTS customers_store_phone_unique_idx ON customers(store_id,phone) WHERE phone IS NOT NULL AND phone<>'' AND deleted_at IS NULL`,
		`CREATE INDEX IF NOT EXISTS customers_store_idx ON customers(store_id)`,
		`CREATE INDEX IF NOT EXISTS transactions_store_idx ON transactions(store_id)`,
		`CREATE INDEX IF NOT EXISTS risk_flags_store_idx ON risk_flags(store_id)`,
		`CREATE INDEX IF NOT EXISTS risk_flag_history_store_idx ON risk_flag_status_history(store_id)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS customers_store_id_id_unique_idx ON customers(store_id,id)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS transactions_store_id_id_unique_idx ON transactions(store_id,id)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS risk_flags_store_id_id_unique_idx ON risk_flags(store_id,id)`,
		`DO $$ BEGIN IF NOT EXISTS(SELECT 1 FROM pg_constraint WHERE conname='transactions_store_customer_fk') THEN ALTER TABLE transactions ADD CONSTRAINT transactions_store_customer_fk FOREIGN KEY(store_id,customer_id) REFERENCES customers(store_id,id); END IF; END $$`,
		`DO $$ BEGIN IF NOT EXISTS(SELECT 1 FROM pg_constraint WHERE conname='risk_flags_store_customer_fk') THEN ALTER TABLE risk_flags ADD CONSTRAINT risk_flags_store_customer_fk FOREIGN KEY(store_id,customer_id) REFERENCES customers(store_id,id); END IF; END $$`,
		`DO $$ BEGIN IF NOT EXISTS(SELECT 1 FROM pg_constraint WHERE conname='risk_flags_store_transaction_fk') THEN ALTER TABLE risk_flags ADD CONSTRAINT risk_flags_store_transaction_fk FOREIGN KEY(store_id,transaction_id) REFERENCES transactions(store_id,id); END IF; END $$`,
		`DO $$ BEGIN IF NOT EXISTS(SELECT 1 FROM pg_constraint WHERE conname='risk_history_store_flag_fk') THEN ALTER TABLE risk_flag_status_history ADD CONSTRAINT risk_history_store_flag_fk FOREIGN KEY(store_id,risk_flag_id) REFERENCES risk_flags(store_id,id); END IF; END $$`,
		`DO $$ BEGIN IF NOT EXISTS(SELECT 1 FROM pg_constraint WHERE conname='kyc_store_customer_fk') THEN ALTER TABLE customer_kyc_profiles ADD CONSTRAINT kyc_store_customer_fk FOREIGN KEY(store_id,customer_id) REFERENCES customers(store_id,id); END IF; END $$`,
		`CREATE TABLE IF NOT EXISTS audit_log (id BIGSERIAL PRIMARY KEY, store_id INT REFERENCES stores(id), actor_id INT REFERENCES traders(id), action TEXT NOT NULL, entity TEXT NOT NULL, entity_id BIGINT, before JSONB, after JSONB, created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP)`,
	}
	for i, statement := range statements {
		var execErr error
		if i >= 7 && i <= 12 {
			_, execErr = tx.Exec(statement, legacyStoreID)
		} else {
			_, execErr = tx.Exec(statement)
		}
		if execErr != nil {
			return fmt.Errorf("tenant migration failed: %w", execErr)
		}
	}
	rows, err := tx.Query(`SELECT id,full_name,phone,COALESCE(id_type,''),id_number FROM customers WHERE person_id IS NULL ORDER BY id`)
	if err != nil {
		return fmt.Errorf("find customers without person: %w", err)
	}
	type legacyPerson struct {
		id                            int
		name, phone, idType, idNumber string
	}
	var missing []legacyPerson
	for rows.Next() {
		var p legacyPerson
		if err := rows.Scan(&p.id, &p.name, &p.phone, &p.idType, &p.idNumber); err != nil {
			rows.Close()
			return err
		}
		missing = append(missing, p)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, p := range missing {
		var personID int
		if p.idNumber != "" {
			err = tx.QueryRow(`INSERT INTO persons(full_name,phone,id_type,id_number) VALUES($1,$2,$3,$4) ON CONFLICT (id_type,id_number) WHERE id_number<>'' DO UPDATE SET updated_at=persons.updated_at RETURNING id`, p.name, p.phone, p.idType, p.idNumber).Scan(&personID)
		} else {
			err = tx.QueryRow(`INSERT INTO persons(full_name,phone,id_type,id_number) VALUES($1,$2,$3,$4) RETURNING id`, p.name, p.phone, p.idType, p.idNumber).Scan(&personID)
		}
		if err != nil {
			return fmt.Errorf("backfill person for customer %d: %w", p.id, err)
		}
		if _, err = tx.Exec(`UPDATE customers SET person_id=$1 WHERE id=$2`, personID, p.id); err != nil {
			return err
		}
	}
	if _, err = tx.Exec(`ALTER TABLE customers ALTER COLUMN person_id SET NOT NULL`); err != nil {
		return fmt.Errorf("require customer person identity: %w", err)
	}
	for _, table := range []string{"customers", "transactions", "risk_flags", "risk_flag_status_history", "customer_kyc_profiles"} {
		if _, err = tx.Exec(fmt.Sprintf(`ALTER TABLE %s ALTER COLUMN store_id DROP DEFAULT`, table)); err != nil {
			return fmt.Errorf("drop default store on %s: %w", table, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit tenant migration: %w", err)
	}
	return nil
}
