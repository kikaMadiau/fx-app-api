package main

import (
	"bufio"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"fx-app-api/config"
	storeentity "fx-app-api/internal/domain/store/entity"
	traderentity "fx-app-api/internal/domain/trader/entity"
	traderservices "fx-app-api/internal/domain/trader/traderservices"
	"fx-app-api/internal/storage"
	"github.com/lib/pq"
)

type options struct {
	name, license, issuedAt, expiresAt string
	firstName, lastName, email, phone  string
	interactive                        bool
}

func main() {
	if err := run(os.Args[1:], os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "create-store:", err)
		os.Exit(1)
	}
}

func run(args []string, in io.Reader, out io.Writer) error {
	if err := loadProjectEnv(); err != nil {
		return fmt.Errorf("load .env: %w", err)
	}
	fs := flag.NewFlagSet("create-store", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var o options
	fs.StringVar(&o.name, "name", "", "store name")
	fs.StringVar(&o.license, "license", "", "BCC license number")
	fs.StringVar(&o.issuedAt, "license-issued-at", "", "license issue date (YYYY-MM-DD)")
	fs.StringVar(&o.expiresAt, "license-expires-at", "", "license expiry date (YYYY-MM-DD)")
	fs.StringVar(&o.firstName, "supervisor-first-name", "", "supervisor first name")
	fs.StringVar(&o.lastName, "supervisor-last-name", "", "supervisor last name")
	fs.StringVar(&o.email, "supervisor-email", "", "supervisor email")
	fs.StringVar(&o.phone, "supervisor-phone", "", "supervisor international phone")
	fs.BoolVar(&o.interactive, "interactive", false, "prompt for missing values")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected positional arguments: %s", strings.Join(fs.Args(), " "))
	}
	if o.interactive {
		if err := promptMissing(&o, in, out); err != nil {
			return err
		}
	}
	store, supervisor, issued, expires, err := validateOptions(o)
	if err != nil {
		return err
	}

	if err := storage.CheckDatabaseSecurity(); err != nil {
		return fmt.Errorf("database configuration rejected: %w", err)
	}
	db, err := storage.NewPostgresStore()
	if err != nil {
		return fmt.Errorf("database unavailable: %w", err)
	}
	defer db.Close()

	password, err := generatedPassword()
	if err != nil {
		return fmt.Errorf("generate supervisor password: %w", err)
	}
	supervisor.PasswordHash, err = traderservices.HashPassword(password)
	if err != nil {
		return fmt.Errorf("hash supervisor password: %w", err)
	}
	store.LicenseIssuedAt, store.LicenseExpiresAt = issued, expires
	if err := createStoreWithSupervisor(db.DB(), store, supervisor); err != nil {
		return err
	}
	fmt.Fprintf(out, "Store créé avec succès\nstore_id: %d\nname: %s\nlicense: %s\nsupervisor_email: %s\nsupervisor_password: %s\n", store.ID, store.Name, store.BCCLicenseNumber, supervisor.Email, password)
	return nil
}

func loadProjectEnv() error {
	return config.LoadEnv(".env")
}

func promptMissing(o *options, in io.Reader, out io.Writer) error {
	scanner := bufio.NewScanner(in)
	prompts := []struct {
		label    string
		target   *string
		optional bool
	}{
		{"Nom du store", &o.name, false}, {"Numéro d'agrément BCC", &o.license, false},
		{"Prénom du superviseur", &o.firstName, false}, {"Nom du superviseur", &o.lastName, false},
		{"E-mail du superviseur", &o.email, false}, {"Téléphone international du superviseur", &o.phone, false},
		{"Date d'émission (YYYY-MM-DD, facultative)", &o.issuedAt, true}, {"Date d'expiration (YYYY-MM-DD, facultative)", &o.expiresAt, true},
	}
	for _, p := range prompts {
		if strings.TrimSpace(*p.target) != "" {
			continue
		}
		if p.optional {
			fmt.Fprintf(out, "%s : ", p.label)
		} else {
			fmt.Fprintf(out, "%s : ", p.label)
		}
		if !scanner.Scan() {
			if scanner.Err() != nil {
				return scanner.Err()
			}
			*p.target = ""
			continue
		}
		*p.target = strings.TrimSpace(scanner.Text())
	}
	return scanner.Err()
}

func validateOptions(o options) (*storeentity.Store, *traderentity.Trader, *time.Time, *time.Time, error) {
	missing := make([]string, 0, 6)
	for _, field := range []struct{ name, value string }{
		{"--name", o.name}, {"--license", o.license}, {"--supervisor-first-name", o.firstName},
		{"--supervisor-last-name", o.lastName}, {"--supervisor-email", o.email}, {"--supervisor-phone", o.phone},
	} {
		if strings.TrimSpace(field.value) == "" {
			missing = append(missing, field.name)
		}
	}
	if len(missing) != 0 {
		return nil, nil, nil, nil, fmt.Errorf("champs obligatoires manquants : %s", strings.Join(missing, ", "))
	}

	o.name = strings.TrimSpace(o.name)
	license, err := storage.NormalizeBCCLicense(o.license)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("agrément invalide : %w", err)
	}
	o.email = strings.TrimSpace(o.email)
	o.email, o.phone, err = traderservices.NormalizeTraderIdentity(o.firstName, o.lastName, o.email, o.phone)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("identité du superviseur invalide : %w", err)
	}
	issued, err := parseDate(o.issuedAt)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("--license-issued-at invalide (attendu YYYY-MM-DD) : %w", err)
	}
	expires, err := parseDate(o.expiresAt)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("--license-expires-at invalide (attendu YYYY-MM-DD) : %w", err)
	}
	if issued != nil && expires != nil && !expires.After(*issued) {
		return nil, nil, nil, nil, errors.New("la date d'expiration doit être après la date d'émission")
	}
	return &storeentity.Store{Name: o.name, BCCLicenseNumber: license, Status: "ACTIVE", LicenseIssuedAt: issued, LicenseExpiresAt: expires}, &traderentity.Trader{FirstName: strings.TrimSpace(o.firstName), LastName: strings.TrimSpace(o.lastName), Email: o.email, Phone: o.phone, Status: "ACTIVE", Roles: "SUPERVISOR", IsActive: true, MustChangePassword: true}, issued, expires, nil
}

func parseDate(raw string) (*time.Time, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	t, err := time.Parse("2006-01-02", strings.TrimSpace(raw))
	if err != nil {
		return nil, err
	}
	return &t, nil
}

func generatedPassword() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func createStoreWithSupervisor(db *sql.DB, store *storeentity.Store, supervisor *traderentity.Trader) (err error) {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("début de transaction impossible : %w", err)
	}
	defer tx.Rollback()
	var exists bool
	if err = tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM stores WHERE bcc_license_number=$1 AND deleted_at IS NULL)`, store.BCCLicenseNumber).Scan(&exists); err != nil {
		return fmt.Errorf("vérifier l'agrément : %w", err)
	}
	if exists {
		return fmt.Errorf("agrément déjà utilisé : %s", store.BCCLicenseNumber)
	}
	if err = tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM traders WHERE LOWER(email)=LOWER($1) AND deleted_at IS NULL)`, supervisor.Email).Scan(&exists); err != nil {
		return fmt.Errorf("vérifier l'e-mail : %w", err)
	}
	if exists {
		return fmt.Errorf("e-mail déjà utilisé : %s", supervisor.Email)
	}
	err = tx.QueryRow(`INSERT INTO stores(name,bcc_license_number,license_issued_at,license_expires_at,status) VALUES($1,$2,$3,$4,'ACTIVE') RETURNING id,created_at,updated_at`, store.Name, store.BCCLicenseNumber, store.LicenseIssuedAt, store.LicenseExpiresAt).Scan(&store.ID, &store.CreatedAt, &store.UpdatedAt)
	if err != nil {
		return classifyUniqueError(err, store.BCCLicenseNumber, supervisor.Email)
	}
	err = tx.QueryRow(`INSERT INTO traders(first_name,last_name,email,phone,status,role,roles,password_hash,store_id,is_active,must_change_password,created_at,updated_at) VALUES($1,$2,$3,$4,'ACTIVE','SUPERVISOR',ARRAY['SUPERVISOR']::TEXT[],$5,$6,TRUE,TRUE,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP) RETURNING id,created_at,updated_at`, supervisor.FirstName, supervisor.LastName, supervisor.Email, supervisor.Phone, supervisor.PasswordHash, store.ID).Scan(&supervisor.Id, &supervisor.CreatedAt, &supervisor.UpdatedAt)
	if err != nil {
		return classifyUniqueError(err, store.BCCLicenseNumber, supervisor.Email)
	}
	supervisor.StoreId = store.ID
	after, err := json.Marshal(map[string]any{"actor": "cli", "store": store, "supervisor_id": supervisor.Id})
	if err != nil {
		return fmt.Errorf("préparer l'audit : %w", err)
	}
	if _, err = tx.Exec(`INSERT INTO audit_log(store_id,actor_id,action,entity,entity_id,before,after) VALUES($1,NULL,'store.created','store',$2,NULL,$3)`, store.ID, store.ID, string(after)); err != nil {
		return fmt.Errorf("écrire l'audit : %w", err)
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("valider la création : %w", err)
	}
	return nil
}

func classifyUniqueError(err error, license, email string) error {
	var pgErr *pq.Error
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		if strings.Contains(pgErr.Constraint, "trader") || strings.Contains(pgErr.Constraint, "email") {
			return fmt.Errorf("e-mail déjà utilisé : %s", email)
		}
		if strings.Contains(pgErr.Constraint, "store") || strings.Contains(pgErr.Constraint, "license") {
			return fmt.Errorf("agrément déjà utilisé : %s", license)
		}
		return errors.New("une valeur unique existe déjà")
	}
	return fmt.Errorf("création store/superviseur : %w", err)
}
