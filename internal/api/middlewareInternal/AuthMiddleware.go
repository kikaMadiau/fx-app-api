package middlewareInternal

import (
	"encoding/json"
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
			// Mot de passe changé depuis la connexion : la session n'est plus valable.
			if claims.PasswordFingerprint != "" && claims.PasswordFingerprint != services.PasswordFingerprint(currentUser.PasswordHash) {
				http.Error(w, `{"message": "Session expirée, reconnectez-vous"}`, http.StatusUnauthorized)
				return
			}
			// Les comptes provisionnés ne peuvent accéder qu'au changement de mot de passe.
			if currentUser.MustChangePassword && (r.Method != http.MethodPost || r.URL.Path != "/auth/me/password") {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusForbidden)
				_ = json.NewEncoder(w).Encode(map[string]string{
					"code":    "password_change_required",
					"message": "Vous devez changer votre mot de passe avant de continuer.",
				})
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

			if !permissions.Has(traderservices.EffectiveRoles(user), requiredPermission) {
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
	case permissions.CustomerCreate, permissions.CustomerUpdate, permissions.KYCUpdate, permissions.KYCVerify, permissions.TransactionCreate:
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
