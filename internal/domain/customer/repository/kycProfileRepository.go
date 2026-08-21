package repository

import (
	"database/sql"
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

	_, err := r.store.DB().Exec(createTableSQL)
	return err
}

// CreateKYCProfile insère un nouveau profil KYC.
func (r *kycProfileRepository) CreateKYCProfile(profile *customerservice.CustomerKYCProfile) error {
	query := `
		INSERT INTO customer_kyc_profiles (
			customer_id, legal_nature, activity_profile, kyc_status, profession, employer,
			company_name, registration_number, source_of_funds, purpose_of_operations,
			expected_volume, expected_frequency, last_verification_date, created_at, updated_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15
		) RETURNING id, created_at, updated_at`

	now := time.Now()
	profile.CreatedAt = now
	profile.UpdatedAt = now

	err := r.store.DB().QueryRow(query,
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
	).Scan(&profile.ID, &profile.CreatedAt, &profile.UpdatedAt)

	if err != nil {
		return fmt.Errorf("failed to create KYC profile: %w", err)
	}

	return nil
}

// GetKYCProfileByCustomerID récupère le profil KYC d'un client.
func (r *kycProfileRepository) GetKYCProfileByCustomerID(customerID int) (*customerservice.CustomerKYCProfile, error) {
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
			last_verification_date, created_at, updated_at
		FROM customer_kyc_profiles
		WHERE customer_id = $1`

	profile := &customerservice.CustomerKYCProfile{}
	var lastVerificationDate sql.NullTime

	err := r.store.DB().QueryRow(query, customerID).Scan(
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
	)

	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("KYC profile for customer %d not found", customerID)
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
			updated_at = $13
		WHERE id = $14 AND customer_id = $15`

	profile.UpdatedAt = time.Now()

	result, err := r.store.DB().Exec(query,
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
	)
	if err != nil {
		return fmt.Errorf("failed to update KYC profile: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to check KYC profile update result: %w", err)
	}
	if rowsAffected == 0 {
		return fmt.Errorf("KYC profile %d for customer %d not found", profile.ID, profile.CustomerID)
	}

	return nil
}

func nullableTime(value time.Time) any {
	if value.IsZero() {
		return nil
	}

	return value
}
