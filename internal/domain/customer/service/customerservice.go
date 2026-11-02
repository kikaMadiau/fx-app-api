package service

import (
	"database/sql"
	"errors"
	"fmt"
	"fx-app-api/internal/domain/customer/entity"
	"fx-app-api/internal/storage"
	"log"
	"strings"
	"time"
)

var (
	// ErrInvalidKYCProfile signale un profil KYC incohérent ou hors énumération (erreur côté client).
	ErrInvalidKYCProfile = errors.New("invalid KYC profile")
	// ErrKYCProfileNotFound signale qu'aucun profil KYC n'existe pour ce client.
	ErrKYCProfileNotFound = errors.New("KYC profile not found")
)

// --- Énumérations pour la Catégorisation et le Statut KYC ---

type LegalNature string

const (
	Individual  LegalNature = "INDIVIDUAL"
	LegalEntity LegalNature = "LEGAL_ENTITY"
)

type ActivityProfile string

const (
	Personal     ActivityProfile = "PERSONAL"
	Professional ActivityProfile = "PROFESSIONAL"
	Business     ActivityProfile = "BUSINESS"
	Other        ActivityProfile = "OTHER"
)

type KYCStatus string

const (
	Unverified           KYCStatus = "UNVERIFIED"
	PartiallyVerified    KYCStatus = "PARTIALLY_VERIFIED"
	Verified             KYCStatus = "VERIFIED"
	EnhancedVerification KYCStatus = "ENHANCED_VERIFICATION"
)

// Valid indique si la valeur fait partie de l'énumération.
func (n LegalNature) Valid() bool {
	return n == Individual || n == LegalEntity
}

// Valid indique si la valeur fait partie de l'énumération.
func (p ActivityProfile) Valid() bool {
	return p == Personal || p == Professional || p == Business || p == Other
}

// Valid indique si la valeur fait partie de l'énumération.
func (s KYCStatus) Valid() bool {
	return s == Unverified || s == PartiallyVerified || s == Verified || s == EnhancedVerification
}

// --- Structure du Profil KYC ---

// CustomerKYCProfile contient toutes les informations détaillées du KYC,
// séparées de l'entité client de base.
type CustomerKYCProfile struct {
	ID              int             `json:"id"`
	CustomerID      int             `json:"customer_id"`
	LegalNature     LegalNature     `json:"legal_nature"`
	ActivityProfile ActivityProfile `json:"activity_profile"`
	KYCStatus       KYCStatus       `json:"kyc_status"`

	// Champs pour Personne Physique
	Profession string `json:"profession,omitempty"`
	Employer   string `json:"employer,omitempty"`

	// Champs pour Personne Morale
	CompanyName        string `json:"company_name,omitempty"`
	RegistrationNumber string `json:"registration_number,omitempty"`
	// TODO: Ajouter des structures pour les représentants légaux, bénéficiaires effectifs, etc.

	// Champs communs
	SourceOfFunds       string  `json:"source_of_funds"`
	PurposeOfOperations string  `json:"purpose_of_operations"`
	ExpectedVolume      float64 `json:"expected_volume"`    // Volume mensuel attendu
	ExpectedFrequency   int     `json:"expected_frequency"` // Transactions par mois attendues

	LastVerificationDate time.Time `json:"last_verification_date"`
	CreatedAt            time.Time `json:"created_at"`
	UpdatedAt            time.Time `json:"updated_at"`
}

// --- Service KYC ---

// CustomerService gère la logique métier liée au KYC et au profilage des clients.
type CustomerService struct {
	customerRepo   storage.CustomerStorage
	kycProfileRepo KYCProfileStorage
}

// KYCProfileStorage décrit les opérations de persistance nécessaires au KYC dynamique.
type KYCProfileStorage interface {
	Init() error
	CreateKYCProfile(profile *CustomerKYCProfile) error
	CreateKYCProfileWithTx(tx *sql.Tx, profile *CustomerKYCProfile) error
	GetKYCProfileByCustomerID(customerID int) (*CustomerKYCProfile, error)
	UpdateKYCProfile(profile *CustomerKYCProfile) error
}

// NewCustomerService crée une nouvelle instance de CustomerService.
func NewCustomerService(customerRepo storage.CustomerStorage) *CustomerService {
	return &CustomerService{
		customerRepo: customerRepo,
	}
}

// NewCustomerServiceWithKYC crée une nouvelle instance de CustomerService avec stockage KYC.
func NewCustomerServiceWithKYC(customerRepo storage.CustomerStorage, kycProfileRepo KYCProfileStorage) *CustomerService {
	return &CustomerService{
		customerRepo:   customerRepo,
		kycProfileRepo: kycProfileRepo,
	}
}

// CreateCustomerWithKYC enregistre un client et son profil KYC initial.
//
// Si un client actif existe déjà avec ce téléphone, il est réutilisé au lieu de renvoyer une
// erreur de doublon : created vaut alors false. Ses données existantes ne sont jamais écrasées
// par cette requête (une modification passe par la mise à jour du client et du profil KYC) ;
// seul un profil KYC manquant est créé à partir des informations reçues.
//
// Pour un nouveau client, les trois écritures (client, profil KYC, score de risque initial)
// partagent une même transaction SQL : si l'une échoue, rien n'est enregistré.
func (s *CustomerService) CreateCustomerWithKYC(customer *entity.Customer, kycProfile *CustomerKYCProfile) (*entity.Customer, bool, error) {
	// 1. Valider les informations avant toute écriture
	if err := s.validateKYCProfile(kycProfile); err != nil {
		return nil, false, err
	}

	// 2. Client déjà connu (même téléphone) : on travaille avec l'existant
	if strings.TrimSpace(customer.Phone) != "" {
		existing, err := s.customerRepo.GetCustomerByPhone(customer.Phone)
		switch {
		case err == nil:
			return s.completeExistingCustomer(existing, kycProfile)
		case !errors.Is(err, storage.ErrCustomerNotFound):
			return nil, false, fmt.Errorf("failed to look up customer by phone: %w", err)
		}
	}

	// 3. Ouvrir la transaction (tx est nil quand le stockage ne supporte pas les transactions,
	// par exemple avec les faux stockages des tests : les écritures se font alors sans transaction).
	tx, err := s.customerRepo.BeginTx()
	if err != nil {
		return nil, false, fmt.Errorf("failed to begin transaction: %w", err)
	}
	if tx != nil {
		// Sans effet après un Commit réussi ; annule tout dans les autres cas.
		defer func() { _ = tx.Rollback() }()
	}

	// 4. Créer le client de base
	if err := s.customerRepo.CreateCustomerWithTx(tx, customer); err != nil {
		if errors.Is(err, storage.ErrCustomerPhoneExists) {
			// Un autre appel a créé ce client entre la vérification et l'insertion.
			if tx != nil {
				_ = tx.Rollback()
			}
			existing, lookupErr := s.customerRepo.GetCustomerByPhone(customer.Phone)
			if lookupErr != nil {
				return nil, false, fmt.Errorf("failed to create base customer: %w", err)
			}
			return s.completeExistingCustomer(existing, kycProfile)
		}
		return nil, false, fmt.Errorf("failed to create base customer: %w", err)
	}

	// 5. Lier le profil KYC au client nouvellement créé
	kycProfile.CustomerID = customer.ID
	kycProfile.KYCStatus = Unverified // Le statut initial est toujours non vérifié

	if s.kycProfileRepo != nil {
		if err := s.kycProfileRepo.CreateKYCProfileWithTx(tx, kycProfile); err != nil {
			return nil, false, fmt.Errorf("failed to create KYC profile: %w", err)
		}
	}

	// 6. Calculer le score de risque initial
	initialRiskScore, initialRiskLevel, reasons := s.calculateInitialRisk(customer, kycProfile)
	customer.RiskScore = initialRiskScore
	customer.RiskLevel = initialRiskLevel

	if err := s.customerRepo.UpdateCustomerWithTx(tx, customer); err != nil {
		return nil, false, fmt.Errorf("failed to set initial risk score: %w", err)
	}

	// 7. Valider la transaction
	if tx != nil {
		if err := tx.Commit(); err != nil {
			return nil, false, fmt.Errorf("failed to commit customer creation: %w", err)
		}
	}

	log.Printf("Initial risk for customer %d set to %s. Reasons: %v", customer.ID, initialRiskLevel, reasons)

	return customer, true, nil
}

// completeExistingCustomer réutilise un client existant : son profil KYC est conservé tel quel
// s'il existe, et créé à partir des informations reçues sinon.
func (s *CustomerService) completeExistingCustomer(existing *entity.Customer, kycProfile *CustomerKYCProfile) (*entity.Customer, bool, error) {
	if s.kycProfileRepo == nil {
		return existing, false, nil
	}

	_, err := s.kycProfileRepo.GetKYCProfileByCustomerID(existing.ID)
	if err == nil {
		return existing, false, nil
	}
	if !errors.Is(err, ErrKYCProfileNotFound) {
		return nil, false, fmt.Errorf("failed to get KYC profile for customer %d: %w", existing.ID, err)
	}

	kycProfile.CustomerID = existing.ID
	kycProfile.KYCStatus = Unverified
	if err := s.kycProfileRepo.CreateKYCProfile(kycProfile); err != nil {
		return nil, false, fmt.Errorf("failed to create KYC profile: %w", err)
	}

	reassessed, err := s.TriggerRiskReassessment(existing.ID)
	if err != nil {
		return nil, false, err
	}

	return reassessed, false, nil
}

// validateKYCProfile vérifie la cohérence des données du profil KYC.
// Toute erreur enveloppe ErrInvalidKYCProfile pour pouvoir être traduite en 400 par l'API.
func (s *CustomerService) validateKYCProfile(profile *CustomerKYCProfile) error {
	if profile == nil {
		return fmt.Errorf("%w: KYC profile is required", ErrInvalidKYCProfile)
	}
	if !profile.LegalNature.Valid() {
		return fmt.Errorf("%w: legal_nature must be one of INDIVIDUAL, LEGAL_ENTITY", ErrInvalidKYCProfile)
	}
	if !profile.ActivityProfile.Valid() {
		return fmt.Errorf("%w: activity_profile must be one of PERSONAL, PROFESSIONAL, BUSINESS, OTHER", ErrInvalidKYCProfile)
	}
	if profile.KYCStatus != "" && !profile.KYCStatus.Valid() {
		return fmt.Errorf("%w: kyc_status must be one of UNVERIFIED, PARTIALLY_VERIFIED, VERIFIED, ENHANCED_VERIFICATION", ErrInvalidKYCProfile)
	}
	if profile.LegalNature == LegalEntity && profile.ActivityProfile == Personal {
		return fmt.Errorf("%w: a legal entity cannot have a 'Personal' activity profile", ErrInvalidKYCProfile)
	}
	if profile.ExpectedVolume < 0 {
		return fmt.Errorf("%w: expected_volume cannot be negative", ErrInvalidKYCProfile)
	}
	if profile.ExpectedFrequency < 0 {
		return fmt.Errorf("%w: expected_frequency cannot be negative", ErrInvalidKYCProfile)
	}
	// ... ajouter d'autres règles de validation
	return nil
}

// calculateInitialRisk calcule le score de risque initial basé sur les informations KYC.
func (s *CustomerService) calculateInitialRisk(customer *entity.Customer, profile *CustomerKYCProfile) (score float64, level string, reasons []string) {
	// Cette fonction implémente la logique du point 6.

	// Règle 1: Le profil "Business" a un risque de base plus élevé
	if profile.ActivityProfile == Business {
		score += 20
		reasons = append(reasons, "Activity profile is Business")
	}

	// ... ajouter d'autres règles basées sur la source des fonds, le volume attendu, etc.

	// Déterminer le niveau de risque
	if score > 70 {
		level = "HIGH"
	} else if score > 40 {
		level = "MEDIUM"
	} else {
		level = "LOW"
	}

	return score, level, reasons
}

// UpdateKYCProfile met à jour le profil KYC d'un client puis relance son évaluation de risque.
func (s *CustomerService) UpdateKYCProfile(customerID int, updatedProfile *CustomerKYCProfile) (*CustomerKYCProfile, error) {
	if s.kycProfileRepo == nil {
		return nil, fmt.Errorf("KYC profile repository is not configured")
	}
	if updatedProfile == nil {
		return nil, fmt.Errorf("KYC profile is required")
	}

	if _, err := s.customerRepo.GetCustomer(customerID); err != nil {
		return nil, fmt.Errorf("failed to get customer %d: %w", customerID, err)
	}

	existingProfile, err := s.kycProfileRepo.GetKYCProfileByCustomerID(customerID)
	if err != nil {
		return nil, fmt.Errorf("failed to get KYC profile for customer %d: %w", customerID, err)
	}

	if updatedProfile.CustomerID != 0 && updatedProfile.CustomerID != customerID {
		return nil, fmt.Errorf("%w: customer_id %d does not match customer %d", ErrInvalidKYCProfile, updatedProfile.CustomerID, customerID)
	}

	updatedProfile.ID = existingProfile.ID
	updatedProfile.CustomerID = customerID
	updatedProfile.CreatedAt = existingProfile.CreatedAt
	if updatedProfile.KYCStatus == "" {
		updatedProfile.KYCStatus = existingProfile.KYCStatus
	}
	if updatedProfile.LastVerificationDate.IsZero() {
		updatedProfile.LastVerificationDate = existingProfile.LastVerificationDate
	}
	if err := s.validateKYCProfile(updatedProfile); err != nil {
		return nil, err
	}

	if err := s.kycProfileRepo.UpdateKYCProfile(updatedProfile); err != nil {
		return nil, fmt.Errorf("failed to update KYC profile: %w", err)
	}

	if _, err := s.TriggerRiskReassessment(customerID); err != nil {
		return updatedProfile, err
	}

	return updatedProfile, nil
}

// UpdateKYCStatus ne modifie que le statut KYC via le parcours de validation conformité.
func (s *CustomerService) UpdateKYCStatus(customerID int, status KYCStatus) (*CustomerKYCProfile, error) {
	if !status.Valid() {
		return nil, fmt.Errorf("%w: invalid kyc_status", ErrInvalidKYCProfile)
	}
	profile, err := s.GetKYCProfile(customerID)
	if err != nil {
		return nil, err
	}
	profile.KYCStatus = status
	if status == Verified || status == EnhancedVerification {
		profile.LastVerificationDate = time.Now()
	}
	if err := s.kycProfileRepo.UpdateKYCProfile(profile); err != nil {
		return nil, fmt.Errorf("failed to update KYC status: %w", err)
	}
	if _, err := s.TriggerRiskReassessment(customerID); err != nil {
		return profile, err
	}
	return profile, nil
}

// GetKYCProfile récupère le profil KYC courant d'un client.
func (s *CustomerService) GetKYCProfile(customerID int) (*CustomerKYCProfile, error) {
	if s.kycProfileRepo == nil {
		return nil, fmt.Errorf("KYC profile repository is not configured")
	}

	profile, err := s.kycProfileRepo.GetKYCProfileByCustomerID(customerID)
	if err != nil {
		return nil, fmt.Errorf("failed to get KYC profile for customer %d: %w", customerID, err)
	}

	return profile, nil
}

// TriggerRiskReassessment recalcule le risque global d'un client à partir de son profil KYC courant.
func (s *CustomerService) TriggerRiskReassessment(customerID int) (*entity.Customer, error) {
	if s.kycProfileRepo == nil {
		return nil, fmt.Errorf("KYC profile repository is not configured")
	}

	customer, err := s.customerRepo.GetCustomer(customerID)
	if err != nil {
		return nil, fmt.Errorf("failed to get customer %d: %w", customerID, err)
	}

	kycProfile, err := s.kycProfileRepo.GetKYCProfileByCustomerID(customerID)
	if err != nil {
		return nil, fmt.Errorf("failed to get KYC profile for customer %d: %w", customerID, err)
	}

	riskScore, riskLevel, reasons := s.calculateInitialRisk(customer, kycProfile)
	customer.RiskScore = riskScore
	customer.RiskLevel = riskLevel

	if err := s.customerRepo.UpdateCustomer(customer); err != nil {
		return nil, fmt.Errorf("failed to update customer %d risk score: %w", customerID, err)
	}

	log.Printf("Risk reassessment for customer %d set to %s. Reasons: %v", customerID, riskLevel, reasons)

	return customer, nil
}
