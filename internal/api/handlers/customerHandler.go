package handlers

import (
	"fmt"
	customerservice "fx-app-api/internal/domain/customer/service"
	"net/http"
	"strings"
)

// --- Handlers de Customer / KYC ---

type UpsertCustomerRequest struct {
	FullName string `json:"full_name"`
	IDNumber string `json:"id_number"`
	IDType   string `json:"id_type"`
	Phone    string `json:"phone"`
	Address  string `json:"address"`
}

type CreateCustomerWithKYCRequest struct {
	Customer   UpsertCustomerRequest              `json:"customer"`
	KYCProfile customerservice.CustomerKYCProfile `json:"kyc_profile"`
}

func (h *Handler) HandleCreateCustomer(w http.ResponseWriter, r *http.Request) error {
	var req UpsertCustomerRequest
	if err := decodeJSON(r, &req); err != nil {
		return badRequest("invalid request body", err)
	}
	if err := validateCustomerRequest(req); err != nil {
		return err
	}

	customer := customerFromRequest(req)
	if err := h.customerStore.CreateCustomer(customer); err != nil {
		return fmt.Errorf("failed to create customer: %w", err)
	}

	return WriteJson(w, http.StatusCreated, customer)
}

func (h *Handler) HandleCreateCustomerWithKYC(w http.ResponseWriter, r *http.Request) error {
	var req CreateCustomerWithKYCRequest
	if err := decodeJSON(r, &req); err != nil {
		return badRequest("invalid request body", err)
	}
	if err := validateCustomerRequest(req.Customer); err != nil {
		return err
	}

	customer := customerFromRequest(req.Customer)
	createdCustomer, err := h.customerService.CreateCustomerWithKYC(customer, &req.KYCProfile)
	if err != nil {
		return fmt.Errorf("failed to create customer with KYC: %w", err)
	}

	return WriteJson(w, http.StatusCreated, createdCustomer)
}

func (h *Handler) HandleGetCustomer(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return badRequest("invalid customer id", err)
	}

	customer, err := h.customerStore.GetCustomer(id)
	if err != nil {
		return notFound(err.Error())
	}

	return WriteJson(w, http.StatusOK, customer)
}

func (h *Handler) HandleUpdateCustomer(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return badRequest("invalid customer id", err)
	}

	customer, err := h.customerStore.GetCustomer(id)
	if err != nil {
		return notFound(err.Error())
	}

	var req UpsertCustomerRequest
	if err := decodeJSON(r, &req); err != nil {
		return badRequest("invalid request body", err)
	}
	if err := validateCustomerRequest(req); err != nil {
		return err
	}

	customer.FullName = req.FullName
	customer.IDNumber = req.IDNumber
	customer.IDType = req.IDType
	customer.Phone = req.Phone
	customer.Address = req.Address

	if err := h.customerStore.UpdateCustomer(customer); err != nil {
		return fmt.Errorf("failed to update customer: %w", err)
	}

	return WriteJson(w, http.StatusOK, customer)
}

func validateCustomerRequest(req UpsertCustomerRequest) error {
	if strings.TrimSpace(req.FullName) == "" {
		return badRequest("full_name is required", nil)
	}
	if strings.TrimSpace(req.IDNumber) == "" {
		return badRequest("id_number is required", nil)
	}

	return nil
}
