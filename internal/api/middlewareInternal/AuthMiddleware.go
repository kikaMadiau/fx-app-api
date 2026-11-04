package middlewareInternal

import (
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"time"

	traderentity "fx-app-api/internal/domain/trader/entity"
	"fx-app-api/internal/domain/trader/permissions"
	traderservices "fx-app-api/internal/domain/trader/traderservices"
	"fx-app-api/internal/storage"
	"fx-app-api/internal/utils/services"

	"github.com/golang-jwt/jwt/v5"
)

// Structure des données contenues dans le token JWT
type Claims struct {
	Name  string   `json:"name"`
	Email string   `json:"email"`
	Roles []string `json:"roles"`
	jwt.RegisteredClaims
}

func AuthMiddleware(traderRepo storage.TraderStorage) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			authHeader := r.Header.Get("Authorization")
			if authHeader == "" {
				http.Error(w, `{"message": "Jeton d'autorisation manquant"}`, http.StatusUnauthorized)
				return
			}

			tokenString, ok := strings.CutPrefix(authHeader, "Bearer ")
			if !ok || strings.TrimSpace(tokenString) == "" {
				http.Error(w, `{"message": "Format de jeton invalide"}`, http.StatusUnauthorized)
				return
			}

			claims, err := services.ParseToken(tokenString)
			if err != nil {
				http.Error(w, `{"message": "Jeton invalide ou expiré"}`, http.StatusUnauthorized)
				return
			}

			currentUser, err := traderRepo.GetTraderByEmail(claims.Email)
			if err != nil || !isActiveTrader(currentUser) {
				http.Error(w, `{"message": "Utilisateur du jeton non trouvé"}`, http.StatusUnauthorized)
				return
			}

			ctx := traderservices.WithUser(r.Context(), currentUser)

			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func isActiveTrader(trader *traderentity.Trader) bool { return trader != nil && trader.IsActive }

func RequirePermission(requiredPermission string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user := traderservices.GetUser(r)
			if user == nil {

				http.Error(w, `{"message": "Utilisateur non authentifié"}`, http.StatusUnauthorized)
				return
			}

			if !(user.IsPlatformAdmin && requiredPermission == permissions.PlatformManageStores) && !permissions.Has(user.Roles, requiredPermission) {
				log.Printf("permission denied: trader_id=%d roles=%q required_permission=%q method=%s path=%s", user.Id, user.Roles, requiredPermission, r.Method, r.URL.Path)
				http.Error(w, `{"message": "Permissions insuffisantes"}`, http.StatusForbidden)
				return
			}
			if requiresValidStoreLicense(requiredPermission) && !user.IsPlatformAdmin && invalidStoreLicense(user.Store, time.Now()) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusForbidden)
				_ = json.NewEncoder(w).Encode(map[string]string{"code": "store_license_invalid", "message": "store license is not valid"})
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

func requiresValidStoreLicense(permission string) bool {
	switch permission {
	case permissions.CustomerCreate, permissions.CustomerUpdate, permissions.KYCUpdate, permissions.KYCVerify, permissions.KYCReassess, permissions.TransactionCreate:
		return true
	default:
		return false
	}
}

func invalidStoreLicense(store *traderentity.Store, now time.Time) bool {
	if store == nil || (store.Status != "ACTIVE" && store.Status != "") {
		return true
	}
	return store.LicenseExpiresAt != nil && !store.LicenseExpiresAt.After(now)
}
