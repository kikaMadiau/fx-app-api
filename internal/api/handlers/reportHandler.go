package handlers

import (
	"net/http"
	"strings"

	"fx-app-api/internal/domain/report"
	"fx-app-api/internal/domain/trader/permissions"
	traderservices "fx-app-api/internal/domain/trader/traderservices"
)

func (h *Handler) SetReportRepository(repository *report.Repository) { h.reportRepo = repository }

// reportParams lit la période (?from=AAAA-MM-JJ&to=AAAA-MM-JJ) et, pour un trader,
// limite le rapport à ses propres opérations.
func (h *Handler) reportParams(r *http.Request) (string, string, int, error) {
	if h.reportRepo == nil {
		return "", "", 0, &HTTPError{StatusCode: http.StatusInternalServerError, Message: "report repository is unavailable"}
	}
	from := strings.TrimSpace(r.URL.Query().Get("from"))
	to := strings.TrimSpace(r.URL.Query().Get("to"))
	if from == "" || to == "" {
		return "", "", 0, badRequest("from and to are required (YYYY-MM-DD)", nil)
	}
	traderID := 0
	if user := traderservices.GetUser(r); user != nil && strings.EqualFold(user.Roles, string(permissions.Trader)) {
		traderID = user.Id
	}
	return from, to, traderID, nil
}

// HandleActivityReport : GET /reports/activity — journal et synthèse des opérations.
func (h *Handler) HandleActivityReport(w http.ResponseWriter, r *http.Request) error {
	from, to, traderID, err := h.reportParams(r)
	if err != nil {
		return err
	}
	res, err := h.reportRepo.Activity(from, to, traderID, traderservices.RequestStoreScope(r))
	if err != nil {
		if isReportInputError(err) {
			return badRequest(err.Error(), nil)
		}
		return err
	}
	return WriteJson(w, http.StatusOK, res)
}

// HandleComplianceReport : GET /reports/compliance — clients, KYC, alertes, opérations importantes.
func (h *Handler) HandleComplianceReport(w http.ResponseWriter, r *http.Request) error {
	from, to, traderID, err := h.reportParams(r)
	if err != nil {
		return err
	}
	res, err := h.reportRepo.Compliance(from, to, traderID, traderservices.RequestStoreScope(r))
	if err != nil {
		if isReportInputError(err) {
			return badRequest(err.Error(), nil)
		}
		return err
	}
	return WriteJson(w, http.StatusOK, res)
}

func isReportInputError(err error) bool {
	msg := err.Error()
	return strings.HasPrefix(msg, "from must") || strings.HasPrefix(msg, "to must") || strings.HasPrefix(msg, "the period")
}
