package service

import (
	"database/sql"
	"testing"

	riskflagentity "fx-app-api/internal/domain/riskflag/entity"
	"fx-app-api/internal/storage"
)

// fakeRiskFlagStorage est un mock pour les tests
type fakeRiskFlagStorage struct {
	flags         map[int]*riskflagentity.RiskFlag
	history       map[int][]*riskflagentity.RiskFlagStatusHistory
	nextFlagID    int
	nextHistoryID int
}

func newFakeRiskFlagStorage() *fakeRiskFlagStorage {
	return &fakeRiskFlagStorage{
		flags:         make(map[int]*riskflagentity.RiskFlag),
		history:       make(map[int][]*riskflagentity.RiskFlagStatusHistory),
		nextFlagID:    1,
		nextHistoryID: 1,
	}
}

func (f *fakeRiskFlagStorage) Init() error {
	return nil
}

func (f *fakeRiskFlagStorage) CreateRiskFlag(flag *riskflagentity.RiskFlag) error {
	return f.CreateRiskFlagWithTx(nil, flag)
}

func (f *fakeRiskFlagStorage) CreateRiskFlagWithTx(tx *sql.Tx, flag *riskflagentity.RiskFlag) error {
	flag.ID = f.nextFlagID
	f.nextFlagID++
	f.flags[flag.ID] = flag
	return nil
}

func (f *fakeRiskFlagStorage) GetRiskFlag(id int) (*riskflagentity.RiskFlag, error) {
	if flag, ok := f.flags[id]; ok {
		return flag, nil
	}
	return nil, storage.ErrRiskFlagNotFound
}

func (f *fakeRiskFlagStorage) ListRiskFlags(filter storage.RiskFlagFilter) ([]*riskflagentity.RiskFlag, error) {
	var result []*riskflagentity.RiskFlag
	for _, flag := range f.flags {
		if filter.Status != "" && flag.Status != filter.Status {
			continue
		}
		if filter.AssignedTo > 0 && (flag.AssignedTo == nil || *flag.AssignedTo != filter.AssignedTo) {
			continue
		}
		result = append(result, flag)
	}
	return result, nil
}

func (f *fakeRiskFlagStorage) DeleteRiskFlag(id int) error {
	delete(f.flags, id)
	return nil
}

func (f *fakeRiskFlagStorage) UpdateRiskFlagStatus(tx *sql.Tx, flagID int, newStatus string, changedBy int, note string, assignedTo *int) (*riskflagentity.RiskFlag, error) {
	flag, ok := f.flags[flagID]
	if !ok {
		return nil, storage.ErrRiskFlagNotFound
	}

	// Vérifier la transition
	if !isValidStatusTransitionForTest(flag.Status, newStatus) {
		return nil, storage.ErrInvalidStatusTransition
	}

	// Valider resolution_note
	if (newStatus == string(riskflagentity.RiskFlagStatusResolved) || newStatus == string(riskflagentity.RiskFlagStatusFalsePositive)) && note == "" {
		return nil, storage.ErrResolutionNoteRequired
	}

	// Valider assigned_to pour IN_REVIEW
	if newStatus == string(riskflagentity.RiskFlagStatusInReview) {
		if assignedTo == nil || *assignedTo == 0 {
			if flag.AssignedTo == nil || *flag.AssignedTo == 0 {
				return nil, storage.ErrAssignedToRequired
			}
			assignedTo = flag.AssignedTo
		}
	}

	// Mettre à jour le flag
	oldStatus := flag.Status
	flag.Status = newStatus
	if assignedTo != nil {
		flag.AssignedTo = assignedTo
	}
	if newStatus == string(riskflagentity.RiskFlagStatusResolved) || newStatus == string(riskflagentity.RiskFlagStatusFalsePositive) {
		flag.ResolutionNote = note
	}

	// Ajouter à l'historique
	h := &riskflagentity.RiskFlagStatusHistory{
		ID:          f.nextHistoryID,
		RiskFlagID:  flagID,
		FromStatus:  &oldStatus,
		ToStatus:    newStatus,
		ChangedBy:   changedBy,
		Note:        note,
	}
	f.nextHistoryID++
	f.history[flagID] = append(f.history[flagID], h)

	return flag, nil
}

func (f *fakeRiskFlagStorage) ListRiskFlagStatusHistory(flagID int) ([]*riskflagentity.RiskFlagStatusHistory, error) {
	return f.history[flagID], nil
}

// isValidStatusTransitionForTest est une version locale pour les tests
func isValidStatusTransitionForTest(from, to string) bool {
	switch from {
	case string(riskflagentity.RiskFlagStatusOpen):
		return to == string(riskflagentity.RiskFlagStatusInReview) ||
			to == string(riskflagentity.RiskFlagStatusFalsePositive)
	case string(riskflagentity.RiskFlagStatusInReview):
		return to == string(riskflagentity.RiskFlagStatusResolved) ||
			to == string(riskflagentity.RiskFlagStatusFalsePositive) ||
			to == string(riskflagentity.RiskFlagStatusOpen)
	case string(riskflagentity.RiskFlagStatusResolved), string(riskflagentity.RiskFlagStatusFalsePositive):
		return false
	default:
		return false
	}
}

// TestTransitionRiskFlag teste les transitions valides et invalides
func TestTransitionRiskFlag(t *testing.T) {
	storage := newFakeRiskFlagStorage()

	// Créer un flag initial
	flag := &riskflagentity.RiskFlag{
		Flag:          "HIGH_AMOUNT",
		Reason:        "Amount exceeds threshold",
		Score:         30,
		Level:         "MEDIUM",
		TransactionId: 1,
		TraderId:      1,
		CustomerId:    1,
		Status:        string(riskflagentity.RiskFlagStatusOpen),
	}
	storage.CreateRiskFlag(flag)

	service := NewInvestigationService(storage)

	// Test 1: Transition OPEN → IN_REVIEW avec assigned_to
	assignedTo := 2
	result, err := service.TransitionRiskFlag(flag.ID, string(riskflagentity.RiskFlagStatusInReview), 1, "", &assignedTo)
	if err != nil {
		t.Errorf("Transition OPEN → IN_REVIEW failed: %v", err)
	}
	if result.Status != string(riskflagentity.RiskFlagStatusInReview) {
		t.Errorf("Expected status IN_REVIEW, got %s", result.Status)
	}

	// Test 2: Transition IN_REVIEW → RESOLVED avec resolution_note
	result, err = service.TransitionRiskFlag(flag.ID, string(riskflagentity.RiskFlagStatusResolved), 2, "Investigation completed", nil)
	if err != nil {
		t.Errorf("Transition IN_REVIEW → RESOLVED failed: %v", err)
	}
	if result.Status != string(riskflagentity.RiskFlagStatusResolved) {
		t.Errorf("Expected status RESOLVED, got %s", result.Status)
	}
	if result.ResolutionNote != "Investigation completed" {
		t.Errorf("Expected resolution_note 'Investigation completed', got %s", result.ResolutionNote)
	}

	// Test 3: Transition invalide RESOLVED → IN_REVIEW (ne devrait pas fonctionner)
	_, err = service.TransitionRiskFlag(flag.ID, string(riskflagentity.RiskFlagStatusInReview), 1, "", nil)
	if err == nil {
		t.Error("Expected error for invalid transition RESOLVED → IN_REVIEW")
	}

	// Test 4: Transition OPEN → FALSE_POSITIVE
	flag2 := &riskflagentity.RiskFlag{
		Flag:          "FALSE_POSITIVE",
		Reason:        "False alert",
		Score:         10,
		Level:         "LOW",
		TransactionId: 2,
		TraderId:      1,
		CustomerId:    1,
		Status:        string(riskflagentity.RiskFlagStatusOpen),
	}
	storage.CreateRiskFlag(flag2)

	result, err = service.TransitionRiskFlag(flag2.ID, string(riskflagentity.RiskFlagStatusFalsePositive), 1, "Verified as false positive", nil)
	if err != nil {
		t.Errorf("Transition OPEN → FALSE_POSITIVE failed: %v", err)
	}
	if result.Status != string(riskflagentity.RiskFlagStatusFalsePositive) {
		t.Errorf("Expected status FALSE_POSITIVE, got %s", result.Status)
	}

	// Test 5: Resolution note vide pour RESOLVED (ne devrait pas fonctionner)
	flag3 := &riskflagentity.RiskFlag{
		Flag:          "TEST",
		Reason:        "Test",
		Score:         5,
		Level:         "LOW",
		TransactionId: 3,
		TraderId:      1,
		CustomerId:    1,
		Status:        string(riskflagentity.RiskFlagStatusOpen),
	}
	storage.CreateRiskFlag(flag3)

	_, err = service.TransitionRiskFlag(flag3.ID, string(riskflagentity.RiskFlagStatusResolved), 1, "", nil)
	if err == nil {
		t.Error("Expected error for RESOLVED without resolution_note")
	}

	// Test 6: Transition IN_REVIEW → OPEN (réouverture)
	flag4 := &riskflagentity.RiskFlag{
		Flag:          "REOPEN",
		Reason:        "Reopen test",
		Score:         15,
		Level:         "LOW",
		TransactionId: 4,
		TraderId:      1,
		CustomerId:    1,
		Status:        string(riskflagentity.RiskFlagStatusOpen),
	}
	storage.CreateRiskFlag(flag4)

	assignedTo2 := 3
	result, err = service.TransitionRiskFlag(flag4.ID, string(riskflagentity.RiskFlagStatusInReview), 1, "", &assignedTo2)
	if err != nil {
		t.Errorf("Transition OPEN → IN_REVIEW failed: %v", err)
	}

	result, err = service.TransitionRiskFlag(flag4.ID, string(riskflagentity.RiskFlagStatusOpen), 1, "Reopened for further review", nil)
	if err != nil {
		t.Errorf("Transition IN_REVIEW → OPEN failed: %v", err)
	}
	if result.Status != string(riskflagentity.RiskFlagStatusOpen) {
		t.Errorf("Expected status OPEN, got %s", result.Status)
	}
}

// TestGetStatusHistory teste la récupération de l'historique
func TestGetStatusHistory(t *testing.T) {
	storage := newFakeRiskFlagStorage()

	// Créer un flag
	flag := &riskflagentity.RiskFlag{
		Flag:          "TEST_HISTORY",
		Reason:        "Test history",
		Score:         10,
		Level:         "LOW",
		TransactionId: 1,
		TraderId:      1,
		CustomerId:    1,
		Status:        string(riskflagentity.RiskFlagStatusOpen),
	}
	storage.CreateRiskFlag(flag)

	service := NewInvestigationService(storage)

	// Effectuer plusieurs transitions
	assignedTo := 2
	_, err := service.TransitionRiskFlag(flag.ID, string(riskflagentity.RiskFlagStatusInReview), 1, "", &assignedTo)
	if err != nil {
		t.Errorf("First transition failed: %v", err)
	}

	_, err = service.TransitionRiskFlag(flag.ID, string(riskflagentity.RiskFlagStatusResolved), 2, "Investigation completed", nil)
	if err != nil {
		t.Errorf("Second transition failed: %v", err)
	}

	// Récupérer l'historique
	history, err := service.GetStatusHistory(flag.ID)
	if err != nil {
		t.Errorf("GetStatusHistory failed: %v", err)
	}

	if len(history) != 2 {
		t.Errorf("Expected 2 history entries, got %d", len(history))
	}

	if history[0].ToStatus != string(riskflagentity.RiskFlagStatusInReview) {
		t.Errorf("Expected first status IN_REVIEW, got %s", history[0].ToStatus)
	}

	if history[1].ToStatus != string(riskflagentity.RiskFlagStatusResolved) {
		t.Errorf("Expected second status RESOLVED, got %s", history[1].ToStatus)
	}
}

// TestIsValidRiskFlagStatus teste la validation des statuts
func TestIsValidRiskFlagStatus(t *testing.T) {
	validStatuses := []string{
		string(riskflagentity.RiskFlagStatusOpen),
		string(riskflagentity.RiskFlagStatusInReview),
		string(riskflagentity.RiskFlagStatusResolved),
		string(riskflagentity.RiskFlagStatusFalsePositive),
	}

	for _, status := range validStatuses {
		if !riskflagentity.IsValidRiskFlagStatus(status) {
			t.Errorf("Expected %s to be valid", status)
		}
	}

	invalidStatuses := []string{
		"INVALID",
		"",
		"open", // lowercase
		"IN REVIEW", // with space
	}

	for _, status := range invalidStatuses {
		if riskflagentity.IsValidRiskFlagStatus(status) {
			t.Errorf("Expected %s to be invalid", status)
		}
	}
}

// TestRiskFlagStatusConstants teste que les constantes sont correctes
func TestRiskFlagStatusConstants(t *testing.T) {
	expected := map[string]bool{
		"OPEN":           true,
		"IN_REVIEW":      true,
		"RESOLVED":       true,
		"FALSE_POSITIVE": true,
	}

	for status, _ := range expected {
		if !riskflagentity.IsValidRiskFlagStatus(status) {
			t.Errorf("Expected %s to be a valid constant", status)
		}
	}
}
