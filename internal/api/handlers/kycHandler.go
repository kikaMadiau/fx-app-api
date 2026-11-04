package handlers

import (
	"errors"
	"fmt"
	customerservice "fx-app-api/internal/domain/customer/service"
	"fx-app-api/internal/domain/trader/permissions"
	traderservices "fx-app-api/internal/domain/trader/traderservices"
	"net/http"
)

func (h *Handler) HandleGetKYCProfile(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return badRequest("invalid customer id", err)
	}

	profile, err := h.customerService.GetKYCProfile(id)
	if err != nil {
		return customerWriteError(err, "get KYC profile")
	}

	return WriteJson(w, http.StatusOK, profile)
}

func (h *Handler) HandleUpdateKYCProfile(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return badRequest("invalid customer id", err)
	}

	var profile customerservice.CustomerKYCProfile
	if err := decodeJSON(r, &profile); err != nil {
		return badRequest("invalid request body", err)
	}
	// Le statut se modifie exclusivement via PATCH /kyc/status.
	existing, err := h.customerService.GetKYCProfile(id)
	if errors.Is(err, customerservice.ErrKYCProfileNotFound) {
		// Client ouvert au guichet sans dossier : le premier enregistrement crée le dossier (non vérifié).
		return h.createKYCProfile(w, r, id, &profile)
	}
	if err != nil {
		return customerWriteError(err, "get KYC profile")
	}
	profile.KYCStatus = existing.KYCStatus
	profile.LastVerificationDate = existing.LastVerificationDate
	var updatedProfile *customerservice.CustomerKYCProfile
	if h.auditLogger != nil {
		tx, txErr := h.customerStore.BeginTx()
		if txErr != nil {
			return fmt.Errorf("begin KYC profile update: %w", txErr)
		}
		defer tx.Rollback()
		updatedProfile, err = h.customerService.UpdateKYCProfileWithTx(tx, id, &profile)
		if err != nil {
			return customerWriteError(err, "update KYC profile")
		}
		if err := h.reassessCustomerRisk(tx, id); err != nil {
			return err
		}
		if auditErr := h.recordAuditWithTx(r, tx, "kyc.profile.update", "customer_kyc_profile", id, existing, updatedProfile); auditErr != nil {
			return fmt.Errorf("failed to audit KYC profile update: %w", auditErr)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit KYC profile update: %w", err)
		}
	} else {
		updatedProfile, err = h.customerService.UpdateKYCProfile(id, &profile)
		if err == nil {
			err = h.reassessCustomerRisk(nil, id)
		}
	}
	if err != nil {
		return customerWriteError(err, "update KYC profile")
	}

	return WriteJson(w, http.StatusOK, updatedProfile)
}

func (h *Handler) createKYCProfile(w http.ResponseWriter, r *http.Request, id int, profile *customerservice.CustomerKYCProfile) error {
	tx, err := h.customerStore.BeginTx()
	if err != nil {
		return fmt.Errorf("begin KYC profile creation: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	created, err := h.customerService.CreateKYCProfileWithTx(tx, id, profile)
	if err != nil {
		return customerWriteError(err, "create KYC profile")
	}
	if err := h.reassessCustomerRisk(tx, id); err != nil {
		return err
	}
	if err := h.recordAuditWithTx(r, tx, "kyc.profile.create", "customer_kyc_profile", id, nil, created); err != nil {
		return fmt.Errorf("failed to audit KYC profile creation: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit KYC profile creation: %w", err)
	}
	return WriteJson(w, http.StatusCreated, created)
}

type UpdateKYCStatusRequest struct {
	KYCStatus customerservice.KYCStatus `json:"kyc_status"`
}

func (h *Handler) HandleUpdateKYCStatus(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return badRequest("invalid customer id", err)
	}
	var req UpdateKYCStatusRequest
	if err := decodeJSON(r, &req); err != nil {
		return badRequest("invalid request body", err)
	}
	user := traderservices.GetUser(r)
	if user == nil {
		return &HTTPError{StatusCode: http.StatusUnauthorized, Message: "unauthenticated"}
	}
	if !permissions.Has(user.Roles, permissions.KYCVerify) {
		return &HTTPError{StatusCode: http.StatusForbidden, Message: "insufficient permissions"}
	}
	before, err := h.customerService.GetKYCProfile(id)
	if err != nil {
		return customerWriteError(err, "get KYC profile")
	}
	var profile *customerservice.CustomerKYCProfile
	if h.auditLogger != nil {
		tx, txErr := h.customerStore.BeginTx()
		if txErr != nil {
			return fmt.Errorf("begin KYC status update: %w", txErr)
		}
		defer tx.Rollback()
		profile, err = h.customerService.UpdateKYCStatusWithTx(tx, id, req.KYCStatus)
		if err != nil {
			return customerWriteError(err, "update KYC status")
		}
		if err := h.reassessCustomerRisk(tx, id); err != nil {
			return err
		}
		if auditErr := h.recordAuditWithTx(r, tx, "kyc.status.update", "customer_kyc_profile", id, before, profile); auditErr != nil {
			return fmt.Errorf("failed to audit KYC status update: %w", auditErr)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit KYC status update: %w", err)
		}
	} else {
		profile, err = h.customerService.UpdateKYCStatus(id, req.KYCStatus)
		if err == nil {
			err = h.reassessCustomerRisk(nil, id)
		}
	}
	if err != nil {
		return customerWriteError(err, "update KYC status")
	}
	return WriteJson(w, http.StatusOK, profile)
}

func (h *Handler) HandleTriggerKYCReassessment(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return badRequest("invalid customer id", err)
	}

	customer, err := h.customerService.TriggerRiskReassessment(id)
	if err != nil {
		return customerWriteError(err, "trigger KYC risk reassessment")
	}
	if h.kycRiskRepo != nil && h.riskService != nil {
		if customer, err = h.riskService.ReassessCustomer(nil, id); err != nil {
			return fmt.Errorf("reassess customer risk: %w", err)
		}
	}

	return WriteJson(w, http.StatusOK, customer)
}
