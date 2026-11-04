package handlers

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	customerentity "fx-app-api/internal/domain/customer/entity"
	"fx-app-api/internal/domain/customer/kycrisk"
	customerservice "fx-app-api/internal/domain/customer/service"
	"fx-app-api/internal/domain/notification"
	riskservice "fx-app-api/internal/domain/riskflag/service"
	traderservices "fx-app-api/internal/domain/trader/traderservices"
	transactionentity "fx-app-api/internal/domain/transaction/entity"
)

func (h *Handler) SetKYCRiskRepository(repository *kycrisk.Repository) {
	h.kycRiskRepo = repository
}

// KYCRiskFactor est un facteur de risque du dossier, issu des règles client.
type KYCRiskFactor struct {
	Code     string  `json:"code"`
	Reason   string  `json:"reason"`
	Score    float64 `json:"score"`
	Severity string  `json:"severity"` // HIGH, MEDIUM ou LOW
}

// KYCRiskResponse est le profil de risque KYC affiché sur le dossier du client et au guichet.
type KYCRiskResponse struct {
	CustomerID int `json:"customer_id"`
	// Niveau enregistré sur le client : c'est lui qui décide d'une validation par la conformité.
	RiskLevel string  `json:"risk_level"`
	RiskScore float64 `json:"risk_score"`
	// Score des seuls facteurs du dossier (sans les opérations).
	ProfileScore float64         `json:"profile_score"`
	ProfileLevel string          `json:"profile_level"`
	Factors      []KYCRiskFactor `json:"factors"`

	HasProfile           bool       `json:"has_profile"`
	KYCStatus            string     `json:"kyc_status"`
	IDType               string     `json:"id_type"`
	IDExpiryDate         string     `json:"id_expiry_date"`
	IDExpired            bool       `json:"id_expired"`
	LastVerificationDate *time.Time `json:"last_verification_date"`
	ReviewDueDate        string     `json:"review_due_date"`
	ReviewOverdue        bool       `json:"review_overdue"`

	IDRequiredAboveUSD float64 `json:"id_required_above_usd"`
	ValidationAboveUSD float64 `json:"validation_above_usd"`
	// Limite journalière (0 = aucune) et opérations du client déjà faites aujourd'hui.
	DailyLimitUSD float64           `json:"daily_limit_usd"`
	DayTotalUSD   float64           `json:"day_total_usd"`
	Override      *kycrisk.Override `json:"override"`
}

func factorSeverity(score float64) string {
	switch {
	case score >= 30:
		return "HIGH"
	case score >= 15:
		return "MEDIUM"
	default:
		return "LOW"
	}
}

// HandleGetKYCRisk renvoie les facteurs de risque KYC d'un client, l'échéance de revue de son
// dossier, les seuils de blocage et la dérogation éventuellement accordée.
func (h *Handler) HandleGetKYCRisk(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return badRequest("invalid customer id", err)
	}
	customer, err := h.customerStore.GetCustomer(id)
	if err != nil {
		return customerWriteError(err, "get customer")
	}

	res := KYCRiskResponse{
		CustomerID:         id,
		RiskLevel:          customer.RiskLevel,
		RiskScore:          customer.RiskScore,
		IDType:             customer.IDType,
		Factors:            []KYCRiskFactor{},
		IDRequiredAboveUSD: kycrisk.IDRequiredAboveUSD(),
		ValidationAboveUSD: kycrisk.ValidationAboveUSD(),
		DailyLimitUSD:      kycrisk.DailyLimitUSD(),
	}
	if h.kycRiskRepo != nil {
		if res.DayTotalUSD, err = h.kycRiskRepo.DayTotalUSD(nil, id, 0, time.Now()); err != nil {
			return err
		}
	}

	if h.riskService != nil {
		flags, score, err := h.riskService.CustomerFactors(nil, id)
		if err != nil {
			return fmt.Errorf("evaluate KYC risk: %w", err)
		}
		for _, f := range flags {
			res.Factors = append(res.Factors, KYCRiskFactor{Code: f.Flag, Reason: f.Reason, Score: f.Score, Severity: factorSeverity(f.Score)})
		}
		res.ProfileScore = score
		res.ProfileLevel = riskservice.CustomerLevel(score)
	}

	if profile, err := h.customerService.GetKYCProfile(id); err == nil {
		res.HasProfile = true
		res.KYCStatus = string(profile.KYCStatus)
		res.IDExpiryDate = profile.IDExpiryDate
		res.IDExpired = kycrisk.IDExpired(profile.IDExpiryDate, time.Now())
		if !profile.LastVerificationDate.IsZero() {
			last := profile.LastVerificationDate
			res.LastVerificationDate = &last
			due := kycrisk.ReviewDueDate(last, customer.RiskLevel)
			res.ReviewDueDate = due.In(kycrisk.Location()).Format("2006-01-02")
			verified := profile.KYCStatus == customerservice.Verified || profile.KYCStatus == customerservice.EnhancedVerification
			res.ReviewOverdue = verified && time.Now().After(due)
		}
	} else if !errors.Is(err, customerservice.ErrKYCProfileNotFound) {
		return fmt.Errorf("get KYC profile: %w", err)
	}

	if h.kycRiskRepo != nil {
		if res.Override, err = h.kycRiskRepo.ActiveOverride(nil, id); err != nil {
			return err
		}
	}

	return WriteJson(w, http.StatusOK, res)
}

type GrantKYCOverrideRequest struct {
	Reason string `json:"reason"`
}

// HandleGrantKYCOverride : la conformité lève une fois, pendant 24 h, les blocages KYC d'un client.
func (h *Handler) HandleGrantKYCOverride(w http.ResponseWriter, r *http.Request) error {
	if h.kycRiskRepo == nil {
		return fmt.Errorf("KYC override repository is not configured")
	}
	id, err := pathID(r)
	if err != nil {
		return badRequest("invalid customer id", err)
	}
	var req GrantKYCOverrideRequest
	if err := decodeJSON(r, &req); err != nil {
		return badRequest("invalid request body", err)
	}
	req.Reason = strings.TrimSpace(req.Reason)
	if len([]rune(req.Reason)) < 5 {
		return badRequest("reason is required (at least 5 characters)", nil)
	}
	if _, err := h.customerStore.GetCustomer(id); err != nil {
		return customerWriteError(err, "get customer")
	}
	user := traderservices.GetUser(r)
	if user == nil {
		return &HTTPError{StatusCode: http.StatusUnauthorized, Message: "unauthenticated"}
	}

	tx, err := h.customerStore.BeginTx()
	if err != nil {
		return fmt.Errorf("begin KYC override: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	override, err := h.kycRiskRepo.GrantOverride(tx, id, user.Id, req.Reason)
	if err != nil {
		return err
	}
	if err := h.recordAuditWithTx(r, tx, "kyc.override.grant", "customer", id, nil, override); err != nil {
		return fmt.Errorf("failed to audit KYC override: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit KYC override: %w", err)
	}
	return WriteJson(w, http.StatusCreated, override)
}

type UpdateCustomerIdentityRequest struct {
	IDType       string  `json:"id_type"`
	IDNumber     string  `json:"id_number"`
	IDExpiryDate string  `json:"id_expiry_date"`
	Address      *string `json:"address"`
	// E-mail du client pour les notifications (absent = inchangé, vide = effacé).
	Email *string `json:"email"`
}

// HandleUpdateCustomerIdentity régularise la pièce d'identité d'un client (type, numéro, date
// d'expiration) et, si elle est fournie, son adresse. Utilisé au guichet comme sur le dossier KYC.
func (h *Handler) HandleUpdateCustomerIdentity(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return badRequest("invalid customer id", err)
	}
	var req UpdateCustomerIdentityRequest
	if err := decodeJSON(r, &req); err != nil {
		return badRequest("invalid request body", err)
	}
	idType, err := customerentity.NormalizeIDType(req.IDType)
	if err != nil {
		return badRequest(err.Error(), nil)
	}
	req.IDNumber = strings.TrimSpace(req.IDNumber)
	req.IDExpiryDate = strings.TrimSpace(req.IDExpiryDate)
	if idType == "" || req.IDNumber == "" {
		return badRequest("id_type and id_number are required", nil)
	}
	if req.IDExpiryDate != "" && !kycrisk.ValidDate(req.IDExpiryDate) {
		return badRequest("id_expiry_date must be a date YYYY-MM-DD", nil)
	}
	if req.Email != nil {
		email, err := notification.NormalizeEmail(*req.Email)
		if err != nil {
			return badRequest(err.Error(), nil)
		}
		req.Email = &email
	}

	tx, err := h.customerStore.BeginTx()
	if err != nil {
		return fmt.Errorf("begin identity update: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	customer, err := h.customerStore.GetCustomerWithTx(tx, id)
	if err != nil {
		return customerWriteError(err, "get customer")
	}
	before := *customer
	customer.IDType = idType
	customer.IDNumber = req.IDNumber
	if req.Address != nil {
		customer.Address = strings.TrimSpace(*req.Address)
	}
	if err := h.customerStore.UpdateCustomerWithTx(tx, customer); err != nil {
		return customerWriteError(err, "update customer identity")
	}
	if err := h.customerService.SetIDExpiryDateWithTx(tx, id, req.IDExpiryDate); err != nil {
		return customerWriteError(err, "update identity document expiry")
	}
	if req.Email != nil {
		if err := h.setCustomerEmail(tx, id, *req.Email); err != nil {
			return err
		}
	}
	if err := h.reassessCustomerRisk(tx, id); err != nil {
		return err
	}
	if customer, err = h.customerStore.GetCustomerWithTx(tx, id); err != nil {
		return customerWriteError(err, "get customer")
	}
	after := map[string]any{"customer": customer, "id_expiry_date": req.IDExpiryDate}
	if err := h.recordAuditWithTx(r, tx, "customer.identity.update", "customer", id, before, after); err != nil {
		return fmt.Errorf("failed to audit identity update: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit identity update: %w", err)
	}
	return WriteJson(w, http.StatusOK, customer)
}

// reassessCustomerRisk recalcule le niveau de risque du client avec toutes les règles client
// après une modification de son dossier (le calcul d'origine ne tient compte que du profil d'activité).
func (h *Handler) reassessCustomerRisk(tx *sql.Tx, customerID int) error {
	if h.kycRiskRepo == nil || h.riskService == nil {
		return nil
	}
	if _, err := h.riskService.ReassessCustomer(tx, customerID); err != nil {
		return fmt.Errorf("reassess customer risk: %w", err)
	}
	return nil
}

// enforceKYCGate refuse l'opération qui vient d'être insérée (dans dbTx) quand le dossier du
// client l'interdit : pas de pièce au-delà du seuil, pièce expirée, client à risque élevé
// au-delà du seuil de validation. Une dérogation de la conformité lève le blocage une fois.
func (h *Handler) enforceKYCGate(r *http.Request, dbTx *sql.Tx, tx *transactionentity.Transaction) error {
	if h.kycRiskRepo == nil || dbTx == nil {
		return nil
	}
	customer, err := h.customerStore.GetCustomerWithTx(dbTx, tx.CustomerID)
	if err != nil {
		return transactionWriteError(err, "get customer for KYC check")
	}
	var expiry string
	if profile, err := h.customerService.GetKYCProfileWithTx(dbTx, customer.ID); err == nil {
		expiry = profile.IDExpiryDate
	} else if !errors.Is(err, customerservice.ErrKYCProfileNotFound) {
		return fmt.Errorf("get KYC profile for KYC check: %w", err)
	}

	dayTotal, err := h.kycRiskRepo.DayTotalUSD(dbTx, customer.ID, tx.ID, time.Now())
	if err != nil {
		return err
	}
	amountUSD := kycrisk.USDEquivalent(tx.Amount, tx.Currency, tx.ConvertedAmount, tx.TargetCurrency)
	blocks := kycrisk.Blocks(kycrisk.GateInput{
		IDType:       customer.IDType,
		IDExpiryDate: expiry,
		RiskLevel:    customer.RiskLevel,
		AmountUSD:    amountUSD,
		DayTotalUSD:  dayTotal,
	}, time.Now())
	if len(blocks) == 0 {
		return nil
	}

	override, err := h.kycRiskRepo.ActiveOverride(dbTx, customer.ID)
	if err != nil {
		return err
	}
	if override == nil {
		if kycrisk.HasBlock(blocks, kycrisk.BlockDailyLimit) {
			// Le client est prévenu que sa limite du jour est atteinte (une fois par jour).
			h.notifyDailyLimitReached(customer.ID, tx.TraderID, dayTotal)
		}
		return &HTTPError{StatusCode: http.StatusUnprocessableEntity, Message: kycrisk.BlockMessage(blocks)}
	}
	if err := h.kycRiskRepo.ConsumeOverride(dbTx, override.ID, tx.ID); err != nil {
		return err
	}
	if err := h.recordAuditWithTx(r, dbTx, "kyc.override.use", "transaction", tx.ID, override, blocks); err != nil {
		return fmt.Errorf("failed to audit KYC override use: %w", err)
	}
	return nil
}
