package service

import (
	"fmt"
	"fx-app-api/internal/domain/customer/entity"
	"fx-app-api/internal/storage"
	"log"
	"time"
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

// CreateCustomerWithKYC crée un nouveau client et son profil KYC initial.
func (s *CustomerService) CreateCustomerWithKYC(customer *entity.Customer, kycProfile *CustomerKYCProfile) (*entity.Customer, error) {
	// 1. Valider les informations
	if err := s.validateKYCProfile(kycProfile); err != nil {
		return nil, fmt.Errorf("invalid KYC profile: %w", err)
	}

	// 2. Créer le client de base
	if err := s.customerRepo.CreateCustomer(customer); err != nil {
		return nil, fmt.Errorf("failed to create base customer: %w", err)
	}

	// 3. Lier le profil KYC au client nouvellement créé
	kycProfile.CustomerID = customer.ID
	kycProfile.KYCStatus = Unverified // Le statut initial est toujours non vérifié

	if s.kycProfileRepo != nil {
		if err := s.kycProfileRepo.CreateKYCProfile(kycProfile); err != nil {
			// TODO: Ajouter une transaction DB pour rollback la création du client si nécessaire.
			return nil, fmt.Errorf("failed to create KYC profile: %w", err)
		}
	}

	// 4. Calculer le score de risque initial
	initialRiskScore, initialRiskLevel, reasons := s.calculateInitialRisk(customer, kycProfile)
	customer.RiskScore = initialRiskScore
	customer.RiskLevel = initialRiskLevel

	if err := s.customerRepo.UpdateCustomer(customer); err != nil {
		return nil, fmt.Errorf("failed to set initial risk score: %w", err)
	}

	log.Printf("Initial risk for customer %d set to %s. Reasons: %v", customer.ID, initialRiskLevel, reasons)

	return customer, nil
}

// validateKYCProfile vérifie la cohérence des données du profil KYC.
func (s *CustomerService) validateKYCProfile(profile *CustomerKYCProfile) error {
	if profile == nil {
		return fmt.Errorf("KYC profile is required")
	}
	if profile.LegalNature == LegalEntity && profile.ActivityProfile == Personal {
		return fmt.Errorf("a legal entity cannot have a 'Personal' activity profile")
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
		return nil, fmt.Errorf("KYC profile customer_id %d does not match customer %d", updatedProfile.CustomerID, customerID)
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
		return nil, fmt.Errorf("invalid KYC profile: %w", err)
	}

	if err := s.kycProfileRepo.UpdateKYCProfile(updatedProfile); err != nil {
		return nil, fmt.Errorf("failed to update KYC profile: %w", err)
	}

	if _, err := s.TriggerRiskReassessment(customerID); err != nil {
		return nil, err
	}

	return updatedProfile, nil
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
