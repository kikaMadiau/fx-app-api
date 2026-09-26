package service

import (
	"fmt"
	riskflagentity "fx-app-api/internal/domain/riskflag/entity"
	"fx-app-api/internal/storage"
)

// InvestigationService gère le workflow d'investigation des risk_flags.
type InvestigationService struct {
	riskFlagRepo storage.RiskFlagStorage
}

// NewInvestigationService crée une nouvelle instance de InvestigationService.
func NewInvestigationService(riskFlagRepo storage.RiskFlagStorage) *InvestigationService {
	return &InvestigationService{riskFlagRepo: riskFlagRepo}
}

// TransitionRequest représente la requête de transition de statut.
type TransitionRequest struct {
	Status         string  `json:"status"`
	AssignedTo     *int    `json:"assigned_to,omitempty"`
	ResolutionNote string  `json:"resolution_note,omitempty"`
}

// TransitionRiskFlag effectue une transition de statut avec validation et historique.
func (s *InvestigationService) TransitionRiskFlag(flagID int, newStatus string, changedBy int, note string, assignedTo *int) (*riskflagentity.RiskFlag, error) {
	// Valider le statut
	if !riskflagentity.IsValidRiskFlagStatus(newStatus) {
		return nil, fmt.Errorf("invalid status: %s", newStatus)
	}

	// Effectuer la transition dans la base de données
	updatedFlag, err := s.riskFlagRepo.UpdateRiskFlagStatus(nil, flagID, newStatus, changedBy, note, assignedTo)
	if err != nil {
		return nil, err
	}

	return updatedFlag, nil
}

// GetStatusHistory récupère l'historique des statuts d'un risk_flag.
func (s *InvestigationService) GetStatusHistory(flagID int) ([]*riskflagentity.RiskFlagStatusHistory, error) {
	return s.riskFlagRepo.ListRiskFlagStatusHistory(flagID)
}
