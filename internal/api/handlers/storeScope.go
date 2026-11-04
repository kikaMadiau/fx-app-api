package handlers

import (
	"errors"
	"net/http"
	"strconv"

	storerepository "fx-app-api/internal/domain/store/repository"
	traderservices "fx-app-api/internal/domain/trader/traderservices"

	"github.com/go-chi/chi/v5"
)

// Isolation entre bureaux de change : chaque compte ne voit et ne modifie que les clients,
// transactions et alertes de son bureau. L'administrateur de la plateforme voit tout.

// inStore indique si la ligne (client, transaction, alerte) appartient au bureau du compte connecté.
// Une ligne inexistante renvoie false, sans erreur.
func (h *Handler) inStore(r *http.Request, table string, id int) (bool, error) {
	scope := traderservices.RequestStoreScope(r)
	if scope == 0 {
		return true, nil
	}
	if h.storeRepo == nil {
		return false, errors.New("store repository is unavailable")
	}
	owner, err := h.storeRepo.OwnerStore(table, id)
	if errors.Is(err, storerepository.ErrStoreNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return owner == scope, nil
}

// StoreGuard protège une route /{table}/{id} : une ligne d'un autre bureau répond 404, comme si
// elle n'existait pas (on ne révèle pas son existence).
func (h *Handler) StoreGuard(table, notFoundMessage string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id, err := strconv.Atoi(chi.URLParam(r, "id"))
			if err != nil || id <= 0 {
				next.ServeHTTP(w, r) // le handler renvoie lui-même l'erreur 400
				return
			}
			ok, err := h.inStore(r, table, id)
			if err != nil {
				_ = WriteJson(w, http.StatusInternalServerError, map[string]string{"message": "internal server error"})
				return
			}
			if !ok {
				_ = WriteJson(w, http.StatusNotFound, map[string]string{"message": notFoundMessage})
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// requireInStore : même contrôle pour un identifiant reçu dans le corps de la requête.
func (h *Handler) requireInStore(r *http.Request, table string, id int, notFoundMessage string) error {
	ok, err := h.inStore(r, table, id)
	if err != nil {
		return err
	}
	if !ok {
		return notFound(notFoundMessage)
	}
	return nil
}
