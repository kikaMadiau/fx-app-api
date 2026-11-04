package validation

import (
	"fx-app-api/internal/utils/validation"
)

// LoginRequest validation pour la connexion
type LoginRequest struct {
	Email    string `json:"email" validate:"required,emailformat"`
	Password string `json:"password" validate:"required,min=8"`
}

// CreateTraderRequest validation pour la création de trader
type CreateTraderRequest struct {
	FirstName string `json:"first_name" validate:"required,name,min=2,max=100"`
	LastName  string `json:"last_name" validate:"required,name,min=2,max=100"`
	Email     string `json:"email" validate:"required,emailformat"`
	Phone     string `json:"phone" validate:"required,e164"`
	Password  string `json:"password" validate:"required,strongpassword,min=8"`
	Role      string `json:"role" validate:"omitempty,oneof=TRADER COMPLIANCE_OFFICER SUPERVISOR ADMIN"`
	Status    string `json:"status,omitempty" validate:"omitempty,oneof=ACTIVE INACTIVE SUSPENDED"`
	StoreID   int    `json:"store_id,omitempty" validate:"omitempty,gt=0"`
}

// CreateCustomerRequest validation pour la création de client
type CreateCustomerRequest struct {
	FullName   string  `json:"full_name" validate:"required,name,min=2,max=100"`
	IDNumber   string  `json:"id_number" validate:"required,idnumber"`
	Phone      string  `json:"phone" validate:"required,e164"`
	Email      string  `json:"email,omitempty" validate:"omitempty,emailformat"`
	Address    string  `json:"address,omitempty" validate:"omitempty,max=200"`
	PostalCode string  `json:"postal_code,omitempty" validate:"omitempty,max=10"`
	RiskLevel  string  `json:"risk_level,omitempty" validate:"omitempty,oneof=LOW MEDIUM HIGH"`
	Notes      string  `json:"notes,omitempty" validate:"omitempty,max=500"`
	TraderID   int     `json:"trader_id,omitempty" validate:"omitempty,gt=0"`
	Latitude   float64 `json:"latitude,omitempty" validate:"omitempty,latitude"`
	Longitude  float64 `json:"longitude,omitempty" validate:"omitempty,longitude"`
}

// CreateTransactionRequest validation pour la création de transaction
type CreateTransactionRequest struct {
	Amount         float64 `json:"amount" validate:"required,gt=0,lte=10000000"`
	Currency       string  `json:"currency" validate:"required,currency"`
	TargetCurrency string  `json:"target_currency" validate:"required,currency"`
	Type           string  `json:"type" validate:"required,oneof=BUY SELL EXCHANGE TRANSFER"`
	Status         string  `json:"status,omitempty" validate:"omitempty,oneof=PENDING COMPLETED CANCELLED REJECTED"`
	CustomerID     int     `json:"customer_id" validate:"required,gt=0"`
	TraderID       int     `json:"trader_id,omitempty" validate:"omitempty,gt=0"`
	Description    string  `json:"description,omitempty" validate:"omitempty,max=500"`
	ExchangeRate   float64 `json:"exchange_rate,omitempty" validate:"omitempty,gt=0"`
	Fees           float64 `json:"fees,omitempty" validate:"omitempty,gte=0"`
}

// CreateRiskFlagRequest validation pour la création de drapeau de risque
type CreateRiskFlagRequest struct {
	Flag       string `json:"flag" validate:"required,min=3,max=255"`
	Reason     string `json:"reason" validate:"required,min=10,max=1000"`
	Level      string `json:"level" validate:"required,oneof=LOW MEDIUM HIGH CRITICAL"`
	Status     string `json:"status,omitempty" validate:"omitempty,oneof=ACTIVE RESOLVED DISMISSED"`
	CustomerID int    `json:"customer_id" validate:"required,gt=0"`
	TraderID   int    `json:"trader_id,omitempty" validate:"omitempty,gt=0"`
}

// UpdateKYCProfileRequest validation pour la mise à jour du profil KYC
type UpdateKYCProfileRequest struct {
	LegalNature    string `json:"legal_nature" validate:"required,oneof=INDIVIDUAL COMPANY NGO GOVERNMENT"`
	BusinessType   string `json:"business_type,omitempty" validate:"omitempty,max=100"`
	Industry       string `json:"industry,omitempty" validate:"omitempty,max=100"`
	AnnualRevenue  string `json:"annual_revenue,omitempty" validate:"omitempty,max=50"`
	SourceOfFunds  string `json:"source_of_funds" validate:"required,max=200"`
	ExpectedVolume string `json:"expected_volume" validate:"required,max=100"`
	RiskCategory   string `json:"risk_category" validate:"required,oneof=LOW MEDIUM HIGH"`
	Notes          string `json:"notes,omitempty" validate:"omitempty,max=1000"`
}

// SearchRequest validation pour la recherche
type SearchRequest struct {
	Query  string `json:"query" validate:"required,min=1,max=100"`
	Limit  int    `json:"limit,omitempty" validate:"omitempty,min=1,max=100"`
	Offset int    `json:"offset,omitempty" validate:"omitempty,min=0"`
}

// ValidateRequest valide une structure de requête
func ValidateRequest(req interface{}) map[string]string {
	return validation.ValidateRequest(req)
}

// SanitizeInput nettoie une entrée utilisateur
func SanitizeInput(input string) string {
	return validation.SanitizeInput(input)
}

// SanitizePhone nettoie un numéro de téléphone
func SanitizePhone(phone string) string {
	return validation.SanitizePhone(phone)
}

// ValidateAmount valide un montant financier
func ValidateAmount(amount float64) error {
	return validation.ValidateAmount(amount)
}

// ValidateBusinessID valide un ID métier
func ValidateBusinessID(id int) error {
	return validation.ValidateBusinessID(id)
}

// ValidationError représente une erreur de validation
type ValidationError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

// ValidationErrors représente une liste d'erreurs de validation
type ValidationErrors struct {
	Errors []ValidationError `json:"errors"`
}

// NewValidationErrors crée une nouvelle instance de ValidationErrors
func NewValidationErrors(errors map[string]string) *ValidationErrors {
	validationErrors := &ValidationErrors{
		Errors: make([]ValidationError, 0, len(errors)),
	}

	for field, message := range errors {
		validationErrors.Errors = append(validationErrors.Errors, ValidationError{
			Field:   field,
			Message: message,
		})
	}

	return validationErrors
}

// ToMap convertit les erreurs en map
func (ve *ValidationErrors) ToMap() map[string]string {
	result := make(map[string]string)
	for _, err := range ve.Errors {
		result[err.Field] = err.Message
	}
	return result
}

// HasErrors vérifie s'il y a des erreurs
func (ve *ValidationErrors) HasErrors() bool {
	return len(ve.Errors) > 0
}

// ValidateAndSanitize valide et nettoie une requête
func ValidateAndSanitize(req interface{}) (*ValidationErrors, interface{}) {
	// Valider la requête
	errors := ValidateRequest(req)
	if len(errors) > 0 {
		return NewValidationErrors(errors), nil
	}

	// Ici, on pourrait ajouter la logique de nettoyage
	// spécifique à chaque type de requête

	return nil, req
}
