package repository

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	storeentity "fx-app-api/internal/domain/store/entity"
	traderentity "fx-app-api/internal/domain/trader/entity"
	"fx-app-api/internal/storage"

	"github.com/lib/pq"
)

var (
	ErrStoreNotFound        = errors.New("store not found")
	ErrMemberInOtherStore   = errors.New("trader already belongs to another store")
	ErrMemberAlreadyInStore = errors.New("trader already belongs to this store")
	ErrLastSupervisor       = errors.New("cannot remove the last store supervisor")
)

type StoreRepository struct{ db *sql.DB }

func New(db *sql.DB) *StoreRepository { return &StoreRepository{db: db} }

func (r *StoreRepository) CreateWithSupervisor(store *storeentity.Store, member *traderentity.Trader, actorID int) error {
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	license, err := storage.NormalizeBCCLicense(store.BCCLicenseNumber)
	if err != nil {
		return err
	}
	store.BCCLicenseNumber = license
	if store.Status == "" {
		store.Status = "ACTIVE"
	}
	err = tx.QueryRow(`INSERT INTO stores(name,bcc_license_number,license_issued_at,license_expires_at,status) VALUES($1,$2,$3,$4,$5) RETURNING id,created_at,updated_at`, store.Name, store.BCCLicenseNumber, store.LicenseIssuedAt, store.LicenseExpiresAt, store.Status).Scan(&store.ID, &store.CreatedAt, &store.UpdatedAt)
	if err != nil {
		return fmt.Errorf("create store: %w", err)
	}
	member.StoreId = store.ID
	member.IsActive = true
	member.Roles = "SUPERVISOR"
	err = tx.QueryRow(`INSERT INTO traders(first_name,last_name,email,phone,status,role,roles,password_hash,store_id,is_active,must_change_password,created_at,updated_at) VALUES($1,$2,$3,$4,'ACTIVE','SUPERVISOR',ARRAY['SUPERVISOR']::TEXT[],$5,$6,TRUE,TRUE,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP) RETURNING id,created_at,updated_at`, member.FirstName, member.LastName, member.Email, member.Phone, member.PasswordHash, store.ID).Scan(&member.Id, &member.CreatedAt, &member.UpdatedAt)
	if err != nil {
		return fmt.Errorf("create first store supervisor: %w", err)
	}
	if err = audit(tx, store.ID, actorID, "store.create", "store", store.ID, nil, store); err != nil {
		return err
	}
	if err = audit(tx, store.ID, actorID, "member.add", "trader", member.Id, nil, member); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *StoreRepository) List() ([]*storeentity.Store, error) {
	rows, err := r.db.Query(`SELECT id,name,bcc_license_number,license_issued_at,license_expires_at,status,created_at,updated_at,deleted_at FROM stores WHERE deleted_at IS NULL ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []*storeentity.Store
	for rows.Next() {
		s := new(storeentity.Store)
		if err := rows.Scan(&s.ID, &s.Name, &s.BCCLicenseNumber, &s.LicenseIssuedAt, &s.LicenseExpiresAt, &s.Status, &s.CreatedAt, &s.UpdatedAt, &s.DeletedAt); err != nil {
			return nil, err
		}
		result = append(result, s)
	}
	return result, rows.Err()
}

func (r *StoreRepository) Get(id int) (*storeentity.Store, error) {
	s := new(storeentity.Store)
	err := r.db.QueryRow(`SELECT id,name,bcc_license_number,license_issued_at,license_expires_at,status,created_at,updated_at,deleted_at FROM stores WHERE id=$1 AND deleted_at IS NULL`, id).Scan(&s.ID, &s.Name, &s.BCCLicenseNumber, &s.LicenseIssuedAt, &s.LicenseExpiresAt, &s.Status, &s.CreatedAt, &s.UpdatedAt, &s.DeletedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrStoreNotFound
	}
	return s, err
}

func (r *StoreRepository) Update(id, actorID int, patch map[string]any) (*storeentity.Store, error) {
	tx, err := r.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	before, err := scanStore(tx.QueryRow(`SELECT id,name,bcc_license_number,license_issued_at,license_expires_at,status,created_at,updated_at,deleted_at FROM stores WHERE id=$1 AND deleted_at IS NULL FOR UPDATE`, id))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrStoreNotFound
		}
		return nil, err
	}
	name := before.Name
	license := before.BCCLicenseNumber
	issued := before.LicenseIssuedAt
	expires := before.LicenseExpiresAt
	status := before.Status
	if v, ok := patch["name"].(string); ok {
		name = v
	}
	if v, ok := patch["bcc_license_number"].(string); ok {
		license, err = storage.NormalizeBCCLicense(v)
		if err != nil {
			return nil, err
		}
	}
	if v, ok := patch["license_issued_at"]; ok {
		issued, _ = v.(*time.Time)
	}
	if v, ok := patch["license_expires_at"]; ok {
		expires, _ = v.(*time.Time)
	}
	if v, ok := patch["status"].(string); ok {
		status = v
	}
	after := &storeentity.Store{ID: id, Name: name, BCCLicenseNumber: license, LicenseIssuedAt: issued, LicenseExpiresAt: expires, Status: status}
	_, err = tx.Exec(`UPDATE stores SET name=$1,bcc_license_number=$2,license_issued_at=$3,license_expires_at=$4,status=$5,updated_at=CURRENT_TIMESTAMP WHERE id=$6`, name, license, issued, expires, status, id)
	if err != nil {
		return nil, err
	}
	if err = audit(tx, before.ID, actorID, "store.update", "store", id, before, after); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return r.Get(id)
}

func (r *StoreRepository) Members(storeID int) ([]*traderentity.Trader, error) {
	rows, err := r.db.Query(`SELECT id,COALESCE(first_name,''),COALESCE(last_name,''),email,COALESCE(phone,''),status,array_to_string(roles,','),store_id,is_active,created_at,updated_at FROM traders WHERE store_id=$1 AND deleted_at IS NULL ORDER BY id`, storeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []*traderentity.Trader
	for rows.Next() {
		u := new(traderentity.Trader)
		if err := rows.Scan(&u.Id, &u.FirstName, &u.LastName, &u.Email, &u.Phone, &u.Status, &u.Roles, &u.StoreId, &u.IsActive, &u.CreatedAt, &u.UpdatedAt); err != nil {
			return nil, err
		}
		list = append(list, u)
	}
	return list, rows.Err()
}

func (r *StoreRepository) AddMember(storeID, actorID int, member *traderentity.Trader) (bool, error) {
	tx, err := r.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	created := false
	var id int
	var existingStore sql.NullInt64
	var platformAdmin bool
	err = tx.QueryRow(`SELECT id,store_id,COALESCE(is_platform_admin,FALSE) FROM traders WHERE email=$1 AND deleted_at IS NULL FOR UPDATE`, member.Email).Scan(&id, &existingStore, &platformAdmin)
	if err == nil {
		// Un administrateur de la plateforme n'appartient à aucun bureau : on ne le rattache jamais.
		if platformAdmin {
			return false, ErrMemberInOtherStore
		}
		if existingStore.Valid {
			if int(existingStore.Int64) != storeID {
				return false, ErrMemberInOtherStore
			}
			return false, ErrMemberAlreadyInStore
		}
		_, err = tx.Exec(`UPDATE traders SET store_id=$1,roles=$2,role=$3,updated_at=CURRENT_TIMESTAMP WHERE id=$4`, storeID, pq.Array(strings.Split(member.Roles, ",")), firstRole(member.Roles), id)
		member.Id = id
	} else if errors.Is(err, sql.ErrNoRows) {
		created = true
		err = tx.QueryRow(`INSERT INTO traders(first_name,last_name,email,phone,status,role,roles,password_hash,store_id,is_active,must_change_password,created_at,updated_at) VALUES($1,$2,$3,$4,'ACTIVE',$5,$6,$7,$8,TRUE,TRUE,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP) RETURNING id`, member.FirstName, member.LastName, member.Email, member.Phone, firstRole(member.Roles), pq.Array(strings.Split(member.Roles, ",")), member.PasswordHash, storeID).Scan(&member.Id)
	}
	if err != nil {
		return false, err
	}
	member.StoreId = storeID
	if err = audit(tx, storeID, actorID, "member.add", "trader", member.Id, nil, member); err != nil {
		return false, err
	}
	if err = tx.Commit(); err != nil {
		return false, err
	}
	return created, nil
}

func (r *StoreRepository) UpdateMember(storeID, actorID, traderID int, roles []string, active bool) error {
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var before struct {
		Roles  string `json:"roles"`
		Active bool   `json:"is_active"`
	}
	err = tx.QueryRow(`SELECT array_to_string(roles,','),is_active FROM traders WHERE id=$1 AND store_id=$2 AND deleted_at IS NULL FOR UPDATE`, traderID, storeID).Scan(&before.Roles, &before.Active)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrStoreNotFound
	}
	if err != nil {
		return err
	}
	if (!active || !hasRole(roles, "SUPERVISOR")) && hasRole(strings.Split(before.Roles, ","), "SUPERVISOR") && before.Active {
		var n int
		if err = tx.QueryRow(`SELECT COUNT(*) FROM traders WHERE store_id=$1 AND is_active=TRUE AND deleted_at IS NULL AND 'SUPERVISOR'=ANY(roles)`, storeID).Scan(&n); err != nil {
			return err
		}
		if n <= 1 {
			return ErrLastSupervisor
		}
	}
	if _, err = tx.Exec(`UPDATE traders SET roles=$1,role=$2,is_active=$3,status=CASE WHEN $3 AND status='PENDING' THEN 'ACTIVE' ELSE status END,updated_at=CURRENT_TIMESTAMP WHERE id=$4 AND store_id=$5`, pq.Array(roles), firstRole(strings.Join(roles, ",")), active, traderID, storeID); err != nil {
		return err
	}
	if err = audit(tx, storeID, actorID, "member.update", "trader", traderID, before, map[string]any{"roles": roles, "is_active": active}); err != nil {
		return err
	}
	return tx.Commit()
}

// RejectPending refuse une inscription en attente : le compte est supprimé (logiquement) et son
// adresse e-mail libérée pour une nouvelle demande. Un compte déjà accepté n'est pas concerné.
func (r *StoreRepository) RejectPending(storeID, actorID, traderID int) error {
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var email string
	err = tx.QueryRow(`SELECT email FROM traders WHERE id=$1 AND store_id=$2 AND status='PENDING' AND is_active=FALSE AND deleted_at IS NULL FOR UPDATE`, traderID, storeID).Scan(&email)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrStoreNotFound
	}
	if err != nil {
		return err
	}
	if _, err = tx.Exec(`UPDATE traders SET status='REJECTED',deleted_at=CURRENT_TIMESTAMP,updated_at=CURRENT_TIMESTAMP,email='refuse-'||id||'-'||email WHERE id=$1`, traderID); err != nil {
		return err
	}
	if err = audit(tx, storeID, actorID, "member.reject", "trader", traderID, map[string]any{"email": email, "status": "PENDING"}, map[string]any{"status": "REJECTED"}); err != nil {
		return err
	}
	return tx.Commit()
}

func scanStore(row *sql.Row) (*storeentity.Store, error) {
	s := new(storeentity.Store)
	err := row.Scan(&s.ID, &s.Name, &s.BCCLicenseNumber, &s.LicenseIssuedAt, &s.LicenseExpiresAt, &s.Status, &s.CreatedAt, &s.UpdatedAt, &s.DeletedAt)
	return s, err
}
func firstRole(roles string) string {
	parts := strings.Split(roles, ",")
	if len(parts) == 0 {
		return "TRADER"
	}
	return strings.TrimSpace(parts[0])
}
func hasRole(roles []string, role string) bool {
	for _, v := range roles {
		if strings.EqualFold(strings.TrimSpace(v), role) {
			return true
		}
	}
	return false
}
func audit(tx *sql.Tx, storeID, actorID int, action, entity string, id int, before, after any) error {
	b, _ := json.Marshal(before)
	a, _ := json.Marshal(after)
	_, err := tx.Exec(`INSERT INTO audit_log(store_id,actor_id,action,entity,entity_id,before,after) VALUES($1,$2,$3,$4,$5,$6,$7)`, storeID, actorID, action, entity, id, nullJSON(b), nullJSON(a))
	return err
}
func nullJSON(b []byte) any {
	if len(b) == 0 || string(b) == "null" {
		return nil
	}
	return string(b)
}

// storeOwnedTables : tables métier rattachées à un bureau, consultables par OwnerStore.
var storeOwnedTables = map[string]bool{"customers": true, "transactions": true, "risk_flags": true}

// OwnerStore renvoie le bureau d'une ligne (client, transaction, alerte), ou ErrStoreNotFound
// si la ligne n'existe pas.
func (r *StoreRepository) OwnerStore(table string, id int) (int, error) {
	if !storeOwnedTables[table] {
		return 0, fmt.Errorf("table %q is not store-owned", table)
	}
	var storeID int
	err := r.db.QueryRow(`SELECT store_id FROM `+table+` WHERE id=$1`, id).Scan(&storeID)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrStoreNotFound
	}
	return storeID, err
}
