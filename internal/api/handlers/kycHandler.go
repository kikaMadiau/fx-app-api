package handlers

import (
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

	updatedProfile, err := h.customerService.UpdateKYCProfile(id, &profile)
	if err != nil {
		return customerWriteError(err, "update KYC profile")
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
		return customerWriteError(err, "trigger KYC risk reassessment")
	}

	return WriteJson(w, http.StatusOK, customer)
}
