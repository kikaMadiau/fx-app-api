package handlers

import (
	"net/http"

	transactionservice "fx-app-api/internal/domain/transaction/service"
)

func (h *Handler) HandleListAnalysais(w http.ResponseWriter, r *http.Request) error {
	analyses := transactionservice.ConfiguredExchangeRates()

	return WriteJson(w, http.StatusOK, analyses)
}
