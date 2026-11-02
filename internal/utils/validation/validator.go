package validation

import (
	"fmt"
	"net/mail"
	"regexp"
	"strings"
	"unicode"

	"github.com/go-playground/validator/v10"
)

var (
	// validate est une instance singleton de validator
	validate *validator.Validate

	// Patterns de validation
	phoneRegex      = regexp.MustCompile(`^\+?[1-9]\d{1,14}$`) // Format E.164
	idNumberRegex   = regexp.MustCompile(`^[A-Za-z0-9\-\.\/\s]{1,50}$`)
	currencyRegex   = regexp.MustCompile(`^[A-Z]{3}$`)
	passwordRegex   = regexp.MustCompile(`^.{8,}$`) // Minimum 8 caractères
	emailRegex      = regexp.MustCompile(`^[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}$`)
	nameRegex       = regexp.MustCompile(`^[A-Za-zÀ-ÿ\s\-']{2,100}$`)
	addressRegex    = regexp.MustCompile(`^[A-Za-z0-9À-ÿ\s\-\.,#'\/]{5,200}$`)
	postalCodeRegex = regexp.MustCompile(`^[A-Za-z0-9\s\-]{3,10}$`)
)

// Init initialise le validateur avec les règles personnalisées
func Init() *validator.Validate {
	if validate == nil {
		validate = validator.New()

		// Enregistrer les validations personnalisées
		registerCustomValidations(validate)
	}

	return validate
}

// Validate valide une structure avec les règles définies
func Validate(s interface{}) error {
	v := Init()
	return v.Struct(s)
}

// ValidateRequest valide une requête et retourne les erreurs formatées
func ValidateRequest(s interface{}) map[string]string {
	err := Validate(s)
	if err == nil {
		return nil
	}

	errors := make(map[string]string)

	if validationErrors, ok := err.(validator.ValidationErrors); ok {
		for _, fieldError := range validationErrors {
			fieldName := fieldError.Field()
			tag := fieldError.Tag()
			param := fieldError.Param()

			// Traduire les erreurs
			switch tag {
			case "required":
				errors[fieldName] = fmt.Sprintf("%s est requis", fieldName)
			case "email":
				errors[fieldName] = fmt.Sprintf("%s doit être une adresse email valide", fieldName)
			case "min":
				errors[fieldName] = fmt.Sprintf("%s doit contenir au moins %s caractères", fieldName, param)
			case "max":
				errors[fieldName] = fmt.Sprintf("%s ne doit pas dépasser %s caractères", fieldName, param)
			case "e164":
				errors[fieldName] = fmt.Sprintf("%s doit être un numéro de téléphone valide au format E.164", fieldName)
			case "currency":
				errors[fieldName] = fmt.Sprintf("%s doit être un code de devise ISO 4217 valide (3 lettres)", fieldName)
			case "idnumber":
				errors[fieldName] = fmt.Sprintf("%s doit être un numéro d'identification valide", fieldName)
			case "strongpassword":
				errors[fieldName] = fmt.Sprintf("%s doit contenir au moins 8 caractères avec majuscule, minuscule, chiffre et caractère spécial", fieldName)
			case "name":
				errors[fieldName] = fmt.Sprintf("%s doit être un nom valide (2-100 caractères alphabétiques)", fieldName)
			default:
				errors[fieldName] = fmt.Sprintf("%s invalide", fieldName)
			}
		}
	}

	return errors
}

// Validation personnalisée pour les numéros de téléphone (format E.164)
func validateE164(fl validator.FieldLevel) bool {
	phone, ok := fl.Field().Interface().(string)
	if !ok {
		return false
	}
	return phoneRegex.MatchString(phone)
}

// Validation personnalisée pour les codes de devise
func validateCurrency(fl validator.FieldLevel) bool {
	currency, ok := fl.Field().Interface().(string)
	if !ok {
		return false
	}
	return currencyRegex.MatchString(strings.ToUpper(currency))
}

// Validation personnalisée pour les numéros d'identification
func validateIDNumber(fl validator.FieldLevel) bool {
	idNumber, ok := fl.Field().Interface().(string)
	if !ok {
		return false
	}
	return idNumberRegex.MatchString(idNumber)
}

// Validation personnalisée pour les mots de passe forts
func validateStrongPassword(fl validator.FieldLevel) bool {
	password, ok := fl.Field().Interface().(string)
	if !ok {
		return false
	}

	// Vérifier la longueur
	if len(password) < 8 {
		return false
	}

	// Vérifier les critères de complexité
	var (
		hasUpper   bool
		hasLower   bool
		hasNumber  bool
		hasSpecial bool
	)

	for _, char := range password {
		switch {
		case unicode.IsUpper(char):
			hasUpper = true
		case unicode.IsLower(char):
			hasLower = true
		case unicode.IsDigit(char):
			hasNumber = true
		case unicode.IsPunct(char) || unicode.IsSymbol(char):
			hasSpecial = true
		}
	}

	return hasUpper && hasLower && hasNumber && hasSpecial
}

// Validation personnalisée pour les noms
func validateName(fl validator.FieldLevel) bool {
	name, ok := fl.Field().Interface().(string)
	if !ok {
		return false
	}
	return nameRegex.MatchString(name)
}

// Validation d'email avec net/mail pour plus de robustesse
func validateEmailFormat(fl validator.FieldLevel) bool {
	email, ok := fl.Field().Interface().(string)
	if !ok {
		return false
	}

	// Vérifier avec regex d'abord pour la performance
	if !emailRegex.MatchString(email) {
		return false
	}

	// Vérifier avec net/mail pour la robustesse
	_, err := mail.ParseAddress(email)
	return err == nil
}

// SanitizeInput nettoie les entrées utilisateur
func SanitizeInput(input string) string {
	// Supprimer les espaces en début et fin
	input = strings.TrimSpace(input)

	// Échapper les caractères dangereux pour HTML
	input = strings.ReplaceAll(input, "<", "&lt;")
	input = strings.ReplaceAll(input, ">", "&gt;")
	input = strings.ReplaceAll(input, "\"", "&quot;")
	input = strings.ReplaceAll(input, "'", "&#39;")

	// Limiter la longueur (sécurité contre les attaques par déni de service)
	if len(input) > 10000 {
		input = input[:10000]
	}

	return input
}

// SanitizePhone nettoie un numéro de téléphone
func SanitizePhone(phone string) string {
	// Supprimer tous les caractères non numériques sauf le +
	phone = strings.TrimSpace(phone)

	// Garder seulement les chiffres et le signe +
	var builder strings.Builder
	for _, r := range phone {
		if (r >= '0' && r <= '9') || r == '+' {
			builder.WriteRune(r)
		}
	}

	result := builder.String()

	// S'assurer que c'est au format E.164
	if result != "" && !strings.HasPrefix(result, "+") {
		// Ajouter le préfixe par défaut si absent
		result = "+" + result
	}

	return result
}

// ValidateAmount valide un montant financier
func ValidateAmount(amount float64) error {
	if amount <= 0 {
		return fmt.Errorf("le montant doit être positif")
	}

	// Limiter à 10 millions (sécurité contre les dépassements)
	if amount > 10000000 {
		return fmt.Errorf("le montant est trop élevé")
	}

	// Valider la précision (2 décimales max pour les devises)
	// Note: Cette validation peut être ajustée selon les besoins
	return nil
}

// ValidateBusinessID valide un ID métier
func ValidateBusinessID(id int) error {
	if id <= 0 {
		return fmt.Errorf("l'ID doit être positif")
	}

	// Limiter la taille pour éviter les dépassements
	if id > 1000000000 {
		return fmt.Errorf("ID invalide")
	}

	return nil
}

// registerCustomValidations enregistre toutes les validations personnalisées
func registerCustomValidations(v *validator.Validate) {
	// Enregistrer les validations personnalisées
	v.RegisterValidation("e164", validateE164)
	v.RegisterValidation("currency", validateCurrency)
	v.RegisterValidation("idnumber", validateIDNumber)
	v.RegisterValidation("strongpassword", validateStrongPassword)
	v.RegisterValidation("name", validateName)
	v.RegisterValidation("emailformat", validateEmailFormat)

	// Alias pour compatibilité
	v.RegisterAlias("email", "emailformat")
}

// GetValidator retourne l'instance du validateur
func GetValidator() *validator.Validate {
	return Init()
}

// ValidationRules définit les règles de validation communes
type ValidationRules struct{}

// CustomerRules règles pour les clients
func (vr *ValidationRules) CustomerRules() map[string]string {
	return map[string]string{
		"FullName":   "required,name,min=2,max=100",
		"IDNumber":   "required,idnumber",
		"Phone":      "required,e164",
		"Email":      "required,email",
		"Address":    "max=200",
		"PostalCode": "max=10",
	}
}

// TransactionRules règles pour les transactions
func (vr *ValidationRules) TransactionRules() map[string]string {
	return map[string]string{
		"Amount":         "required,gt=0",
		"Currency":       "required,currency",
		"TargetCurrency": "required,currency",
		"Type":           "required",
		"Status":         "required",
		"CustomerID":     "required,gt=0",
		"TraderID":       "required,gt=0",
	}
}

// TraderRules règles pour les traders
func (vr *ValidationRules) TraderRules() map[string]string {
	return map[string]string{
		"Email":    "required,email",
		"Password": "required,strongpassword,min=8",
		"Name":     "required,name,min=2,max=100",
		"Role":     "required",
	}
}

// LoginRules règles pour la connexion
func (vr *ValidationRules) LoginRules() map[string]string {
	return map[string]string{
		"Email":    "required,email",
		"Password": "required",
	}
}
