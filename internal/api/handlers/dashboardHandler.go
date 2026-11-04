package handlers

import (
	"fx-app-api/internal/domain/trader/permissions"
	traderservices "fx-app-api/internal/domain/trader/traderservices"
	"net/http"
	"strings"
	"time"
)

func (h *Handler) HandleDashboardSummary(w http.ResponseWriter, r *http.Request) error {
	if h.dashboardRepo == nil {
		return &HTTPError{StatusCode: http.StatusInternalServerError, Message: "dashboard repository is unavailable"}
	}
	var traderIDs []int
	if user := traderservices.GetUser(r); user != nil && strings.EqualFold(user.Roles, string(permissions.Trader)) {
		traderIDs = []int{user.Id}
	}
	summary, err := h.dashboardRepo.Summary(time.Now(), traderservices.RequestStoreScope(r), traderIDs...)
	if err != nil {
		return err
	}
	return WriteJson(w, http.StatusOK, summary)
}
