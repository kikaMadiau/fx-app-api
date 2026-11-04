package handlers

import (
	"database/sql"
	"net/http"

	"fx-app-api/internal/domain/notification"
	traderservices "fx-app-api/internal/domain/trader/traderservices"
)

// SetNotifications branche le journal des messages client et le service d'envoi.
func (h *Handler) SetNotifications(repository *notification.Repository, service *notification.Service) {
	h.notificationRepo = repository
	h.notifier = service
}

// NotificationListResponse : messages envoyés au client pour une opération.
type NotificationListResponse struct {
	Items []notification.Record `json:"items"`
	// "test" : rien n'est réellement envoyé, les messages sont écrits dans le dossier outbox de l'API.
	Mode string `json:"mode"`
}

// HandleListTransactionNotifications : GET /transactions/{id}/notifications.
func (h *Handler) HandleListTransactionNotifications(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return badRequest("invalid transaction id", err)
	}
	tx, err := h.transactionStore.GetTransaction(id)
	if err != nil {
		return transactionWriteError(err, "get transaction")
	}
	if !canReadTransaction(tx, traderservices.GetUser(r)) {
		return notFound("transaction not found")
	}
	response := NotificationListResponse{Items: []notification.Record{}, Mode: "off"}
	if h.notificationRepo != nil {
		if response.Items, err = h.notificationRepo.ListForTransaction(id); err != nil {
			return err
		}
	}
	if sender := notification.NewSenderFromEnv(); sender != nil {
		response.Mode = sender.Mode()
	}
	return WriteJson(w, http.StatusOK, response)
}

// setCustomerEmail enregistre l'e-mail du client dans la transaction SQL en cours.
func (h *Handler) setCustomerEmail(tx *sql.Tx, customerID int, email string) error {
	if h.notificationRepo == nil {
		return nil
	}
	return h.notificationRepo.SetCustomerEmailWithTx(tx, customerID, email)
}

// notifyDailyLimitReached prévient le client dont l'opération est refusée pour limite journalière.
func (h *Handler) notifyDailyLimitReached(customerID, traderID int, dayTotalUSD float64) {
	if h.notifier != nil {
		h.notifier.DailyLimitReached(customerID, traderID, dayTotalUSD)
	}
}

// notifyTransaction prévient le client après une opération enregistrée (sans bloquer la réponse).
func (h *Handler) notifyTransaction(transactionID int) {
	if h.notifier != nil {
		h.notifier.TransactionCompleted(transactionID)
	}
}
