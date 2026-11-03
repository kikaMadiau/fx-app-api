package entity

import (
	"fmt"
	"strings"
	"time"
)

type Customer struct {
	ID        int        `json:"id"`
	FullName  string     `json:"full_name"`
	IDNumber  string     `json:"id_number"`
	IDType    string     `json:"id_type"`
	Phone     string     `json:"phone"`
	Address   string     `json:"address"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
	DeletedAt *time.Time `json:"deleted_at,omitempty"`
	RiskLevel string     `json:"risk_level,omitempty"`
	RiskScore float64    `json:"risk_score"`
}

// CustomerSummary est la vue « liste » d'un client : ses données de base enrichies
// du statut KYC, du nombre de transactions et des alertes de risque encore ouvertes.
// Les champs de Customer sont aplatis dans le JSON.
type CustomerSummary struct {
	Customer
	KYCStatus        string `json:"kyc_status"`
	TransactionCount int    `json:"transaction_count"`
	OpenRiskFlags    int    `json:"open_risk_flags"`
	LatestFlagReason string `json:"latest_flag_reason,omitempty"`
}

// IDType décrit le type de pièce d'identité présentée par le client.
type IDType string

const (
	IDTypeNationalID    IDType = "NATIONAL_ID"
	IDTypePassport      IDType = "PASSPORT"
	IDTypeDriverLicense IDType = "DRIVER_LICENSE"
	IDTypeVoterCard     IDType = "VOTER_CARD"
)

// ValidIDTypes liste les types de pièce acceptés.
var ValidIDTypes = []IDType{
	IDTypeNationalID,
	IDTypePassport,
	IDTypeDriverLicense,
	IDTypeVoterCard,
}

// NormalizeIDType met la valeur en majuscules et vérifie qu'elle appartient à l'enum.
// Une valeur vide est acceptée (le type de pièce est optionnel) et renvoyée vide.
func NormalizeIDType(value string) (string, error) {
	normalized := strings.ToUpper(strings.TrimSpace(value))
	// Accept the labels displayed by the French frontend as well as API enum values.
	switch normalized {
	case "CARTE D'IDENTITE", "CARTE D’IDENTITE", "CARTE D'IDENTITÉ", "CARTE D’IDENTITÉ":
		normalized = string(IDTypeNationalID)
	case "PASSEPORT":
		normalized = string(IDTypePassport)
	case "PERMIS DE CONDUIRE":
		normalized = string(IDTypeDriverLicense)
	case "CARTE D'ELECTEUR", "CARTE D’ELECTEUR", "CARTE D'ÉLECTEUR", "CARTE D’ÉLECTEUR":
		normalized = string(IDTypeVoterCard)
	}
	if normalized == "" {
		return "", nil
	}

	for _, valid := range ValidIDTypes {
		if string(valid) == normalized {
			return normalized, nil
		}
	}

	allowed := make([]string, 0, len(ValidIDTypes))
	for _, valid := range ValidIDTypes {
		allowed = append(allowed, string(valid))
	}

	return "", fmt.Errorf("id_type must be one of: %s", strings.Join(allowed, ", "))
}
