package repository

import (
	"database/sql"
	"errors"
	"fmt"
	customerservice "fx-app-api/internal/domain/customer/service"
	"fx-app-api/internal/storage"
	"time"
)

// kycProfileRepository est l'implémentation concrète du stockage des profils KYC.
type kycProfileRepository struct {
	store *storage.PostgresStore
}

// NewKYCProfileRepository crée une nouvelle instance pour les profils KYC.
func NewKYCProfileRepository(store *storage.PostgresStore) customerservice.KYCProfileStorage {
	return &kycProfileRepository{store: store}
}

// Init crée la table 'customer_kyc_profiles' si elle n'existe pas.
func (r *kycProfileRepository) Init() error {
	createTableSQL := `
		CREATE TABLE IF NOT EXISTS customer_kyc_profiles (
			id SERIAL PRIMARY KEY,
			customer_id INT UNIQUE NOT NULL REFERENCES customers(id),
			legal_nature VARCHAR(50) NOT NULL,
			activity_profile VARCHAR(50) NOT NULL,
			kyc_status VARCHAR(50) NOT NULL,
			profession VARCHAR(255),
			employer VARCHAR(255),
			company_name VARCHAR(255),
			registration_number VARCHAR(255),
			source_of_funds TEXT,
			purpose_of_operations TEXT,
			expected_volume NUMERIC(14, 2) DEFAULT 0,
			expected_frequency INT DEFAULT 0,
			last_verification_date TIMESTAMPTZ,
			created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP
		);`

	if _, err := r.store.DB().Exec(createTableSQL); err != nil {
		return err
	}

	// Champs de vigilance (pièce, nationalité, PPE, secteur, mandat), ajoutés aux bases existantes.
	if _, err := r.store.DB().Exec(`
		ALTER TABLE customer_kyc_profiles
			ADD COLUMN IF NOT EXISTS id_expiry_date DATE,
			ADD COLUMN IF NOT EXISTS nationality VARCHAR(2),
			ADD COLUMN IF NOT EXISTS residence_country VARCHAR(2),
			ADD COLUMN IF NOT EXISTS is_pep BOOLEAN NOT NULL DEFAULT FALSE,
			ADD COLUMN IF NOT EXISTS pep_function VARCHAR(255),
			ADD COLUMN IF NOT EXISTS activity_sector VARCHAR(50),
			ADD COLUMN IF NOT EXISTS acts_for_third_party BOOLEAN NOT NULL DEFAULT FALSE,
			ADD COLUMN IF NOT EXISTS third_party_name VARCHAR(255),
			ADD COLUMN IF NOT EXISTS third_party_id_number VARCHAR(100);`); err != nil {
		return fmt.Errorf("add KYC vigilance columns: %w", err)
	}

	// Accélère le filtre par statut KYC de la liste des clients.
	_, err := r.store.DB().Exec(`
		CREATE INDEX IF NOT EXISTS customer_kyc_profiles_status_idx
		ON customer_kyc_profiles (kyc_status);`)
	return err
}

// CreateKYCProfile insère un nouveau profil KYC.
func (r *kycProfileRepository) CreateKYCProfile(profile *customerservice.CustomerKYCProfile) error {
	return r.CreateKYCProfileWithTx(nil, profile)
}

// CreateKYCProfileWithTx insère un nouveau profil KYC, dans la transaction fournie si elle n'est pas nil.
func (r *kycProfileRepository) CreateKYCProfileWithTx(tx *sql.Tx, profile *customerservice.CustomerKYCProfile) error {
	query := `
		INSERT INTO customer_kyc_profiles (
			customer_id, legal_nature, activity_profile, kyc_status, profession, employer,
			company_name, registration_number, source_of_funds, purpose_of_operations,
			expected_volume, expected_frequency, last_verification_date, created_at, updated_at,
			id_expiry_date, nationality, residence_country, is_pep, pep_function, activity_sector,
			acts_for_third_party, third_party_name, third_party_id_number, store_id
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15,
			NULLIF($16, '')::date, NULLIF($17, ''), NULLIF($18, ''), $19, NULLIF($20, ''), NULLIF($21, ''),
			$22, NULLIF($23, ''), NULLIF($24, ''),
			(SELECT store_id FROM customers WHERE id = $1)
		) RETURNING id, created_at, updated_at`

	now := time.Now()
	profile.CreatedAt = now
	profile.UpdatedAt = now

	err := pickExecutor(r.store.DB(), tx).QueryRow(query,
		profile.CustomerID,
		profile.LegalNature,
		profile.ActivityProfile,
		profile.KYCStatus,
		profile.Profession,
		profile.Employer,
		profile.CompanyName,
		profile.RegistrationNumber,
		profile.SourceOfFunds,
		profile.PurposeOfOperations,
		profile.ExpectedVolume,
		profile.ExpectedFrequency,
		nullableTime(profile.LastVerificationDate),
		profile.CreatedAt,
		profile.UpdatedAt,
		profile.IDExpiryDate,
		profile.Nationality,
		profile.ResidenceCountry,
		profile.IsPEP,
		profile.PEPFunction,
		profile.ActivitySector,
		profile.ActsForThirdParty,
		profile.ThirdPartyName,
		profile.ThirdPartyIDNumber,
	).Scan(&profile.ID, &profile.CreatedAt, &profile.UpdatedAt)

	if err != nil {
		return fmt.Errorf("failed to create KYC profile: %w", err)
	}

	return nil
}

// GetKYCProfileByCustomerID récupère le profil KYC d'un client.
func (r *kycProfileRepository) GetKYCProfileByCustomerID(customerID int) (*customerservice.CustomerKYCProfile, error) {
	return r.GetKYCProfileByCustomerIDWithTx(nil, customerID)
}

// GetKYCProfileByCustomerIDWithTx récupère le profil KYC d'un client, dans la transaction fournie si elle n'est pas nil.
func (r *kycProfileRepository) GetKYCProfileByCustomerIDWithTx(tx *sql.Tx, customerID int) (*customerservice.CustomerKYCProfile, error) {
	query := `
		SELECT id, customer_id, legal_nature, activity_profile, kyc_status,
			COALESCE(profession, ''),
			COALESCE(employer, ''),
			COALESCE(company_name, ''),
			COALESCE(registration_number, ''),
			COALESCE(source_of_funds, ''),
			COALESCE(purpose_of_operations, ''),
			COALESCE(expected_volume, 0),
			COALESCE(expected_frequency, 0),
			last_verification_date, created_at, updated_at,
			COALESCE(TO_CHAR(id_expiry_date, 'YYYY-MM-DD'), ''),
			COALESCE(nationality, ''),
			COALESCE(residence_country, ''),
			is_pep,
			COALESCE(pep_function, ''),
			COALESCE(activity_sector, ''),
			acts_for_third_party,
			COALESCE(third_party_name, ''),
			COALESCE(third_party_id_number, '')
		FROM customer_kyc_profiles
		WHERE customer_id = $1`

	profile := &customerservice.CustomerKYCProfile{}
	var lastVerificationDate sql.NullTime

	err := pickExecutor(r.store.DB(), tx).QueryRow(query, customerID).Scan(
		&profile.ID,
		&profile.CustomerID,
		&profile.LegalNature,
		&profile.ActivityProfile,
		&profile.KYCStatus,
		&profile.Profession,
		&profile.Employer,
		&profile.CompanyName,
		&profile.RegistrationNumber,
		&profile.SourceOfFunds,
		&profile.PurposeOfOperations,
		&profile.ExpectedVolume,
		&profile.ExpectedFrequency,
		&lastVerificationDate,
		&profile.CreatedAt,
		&profile.UpdatedAt,
		&profile.IDExpiryDate,
		&profile.Nationality,
		&profile.ResidenceCountry,
		&profile.IsPEP,
		&profile.PEPFunction,
		&profile.ActivitySector,
		&profile.ActsForThirdParty,
		&profile.ThirdPartyName,
		&profile.ThirdPartyIDNumber,
	)

	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("%w: customer %d", customerservice.ErrKYCProfileNotFound, customerID)
		}
		return nil, fmt.Errorf("failed to get KYC profile: %w", err)
	}
	if lastVerificationDate.Valid {
		profile.LastVerificationDate = lastVerificationDate.Time
	}

	return profile, nil
}

// UpdateKYCProfile met à jour un profil KYC existant.
func (r *kycProfileRepository) UpdateKYCProfile(profile *customerservice.CustomerKYCProfile) error {
	return r.UpdateKYCProfileWithTx(nil, profile)
}

func (r *kycProfileRepository) UpdateKYCProfileWithTx(tx *sql.Tx, profile *customerservice.CustomerKYCProfile) error {
	query := `
		UPDATE customer_kyc_profiles SET
			legal_nature = $1,
			activity_profile = $2,
			kyc_status = $3,
			profession = $4,
			employer = $5,
			company_name = $6,
			registration_number = $7,
			source_of_funds = $8,
			purpose_of_operations = $9,
			expected_volume = $10,
			expected_frequency = $11,
			last_verification_date = $12,
			updated_at = $13,
			id_expiry_date = NULLIF($16, '')::date,
			nationality = NULLIF($17, ''),
			residence_country = NULLIF($18, ''),
			is_pep = $19,
			pep_function = NULLIF($20, ''),
			activity_sector = NULLIF($21, ''),
			acts_for_third_party = $22,
			third_party_name = NULLIF($23, ''),
			third_party_id_number = NULLIF($24, '')
		WHERE id = $14 AND customer_id = $15`

	profile.UpdatedAt = time.Now()

	result, err := pickExecutor(r.store.DB(), tx).Exec(query,
		profile.LegalNature,
		profile.ActivityProfile,
		profile.KYCStatus,
		profile.Profession,
		profile.Employer,
		profile.CompanyName,
		profile.RegistrationNumber,
		profile.SourceOfFunds,
		profile.PurposeOfOperations,
		profile.ExpectedVolume,
		profile.ExpectedFrequency,
		nullableTime(profile.LastVerificationDate),
		profile.UpdatedAt,
		profile.ID,
		profile.CustomerID,
		profile.IDExpiryDate,
		profile.Nationality,
		profile.ResidenceCountry,
		profile.IsPEP,
		profile.PEPFunction,
		profile.ActivitySector,
		profile.ActsForThirdParty,
		profile.ThirdPartyName,
		profile.ThirdPartyIDNumber,
	)
	if err != nil {
		return fmt.Errorf("failed to update KYC profile: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to check KYC profile update result: %w", err)
	}
	if rowsAffected == 0 {
		return fmt.Errorf("%w: profile %d for customer %d", customerservice.ErrKYCProfileNotFound, profile.ID, profile.CustomerID)
	}

	return nil
}

func nullableTime(value time.Time) any {
	if value.IsZero() {
		return nil
	}

	return value
}
