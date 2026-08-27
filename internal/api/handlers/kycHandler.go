package handlers

import (
	"fmt"
	customerservice "fx-app-api/internal/domain/customer/service"
	"net/http"
)

func (h *Handler) HandleGetKYCProfile(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return badRequest("invalid customer id", err)
	}

	profile, err := h.customerService.GetKYCProfile(id)
	if err != nil {
		return notFound(err.Error())
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

	updatedProfile, err := h.customerService.UpdateKYCProfile(id, &profile)
	if err != nil {
		return fmt.Errorf("failed to update KYC profile: %w", err)
	}

	return WriteJson(w, http.StatusOK, updatedProfile)
}

func (h *Handler) HandleTriggerKYCReassessment(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return badRequest("invalid customer id", err)
	}

	customer, err := h.customerService.TriggerRiskReassessment(id)
	if err != nil {
		return fmt.Errorf("failed to trigger KYC risk reassessment: %w", err)
	}

	return WriteJson(w, http.StatusOK, customer)
}
