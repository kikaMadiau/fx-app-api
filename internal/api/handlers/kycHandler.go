package handlers

import (
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
	if err != nil {
		return customerWriteError(err, "get KYC profile")
	}
	profile.KYCStatus = existing.KYCStatus
	profile.LastVerificationDate = existing.LastVerificationDate

	updatedProfile, err := h.customerService.UpdateKYCProfile(id, &profile)
	if updatedProfile != nil {
		if auditErr := h.recordAudit(r, "kyc.profile.update", "customer_kyc_profile", id, existing, updatedProfile); auditErr != nil {
			return fmt.Errorf("failed to audit KYC profile update: %w", auditErr)
		}
	}
	if err != nil {
		return customerWriteError(err, "update KYC profile")
	}

	return WriteJson(w, http.StatusOK, updatedProfile)
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
	profile, err := h.customerService.UpdateKYCStatus(id, req.KYCStatus)
	if profile != nil {
		if auditErr := h.recordAudit(r, "kyc.status.update", "customer_kyc_profile", id, before, profile); auditErr != nil {
			return fmt.Errorf("failed to audit KYC status update: %w", auditErr)
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

	return WriteJson(w, http.StatusOK, customer)
}
