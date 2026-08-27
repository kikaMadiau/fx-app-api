package middlewareInternal

import (
	"net/http"
	"strings"

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
			if err != nil || currentUser == nil {
				http.Error(w, `{"message": "Utilisateur du jeton non trouvé"}`, http.StatusUnauthorized)
				return
			}

			ctx := traderservices.WithUser(r.Context(), currentUser)

			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func RoleMiddleware(requiredRole string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user := traderservices.GetUser(r)
			if user == nil {

				http.Error(w, `{"message": "Utilisateur non authentifié"}`, http.StatusUnauthorized)
				return
			}

			if !hasRole(user.Roles, requiredRole) {
				http.Error(w, `{"message": "Permissions insuffisantes"}`, http.StatusForbidden)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

func hasRole(roles string, requiredRole string) bool {
	for _, role := range strings.Split(roles, ",") {
		if strings.TrimSpace(role) == requiredRole {
			return true
		}
	}

	return strings.TrimSpace(roles) == requiredRole
}

/*
func AuthMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// 1. Extraire l'en-tête Authorization
		authHeader := r.Header.Get("Authorization")
		if authHeader == "" {
			http.Error(w, `{"message": "Jeton manquant"}`, http.StatusUnauthorized)
			return
		}

		// 2. Vérifier le format "Bearer <token>"
		parts := strings.Split(authHeader, " ")
		if len(parts) != 2 || parts[0] != "Bearer" {
			http.Error(w, `{"message": "Format de jeton invalide"}`, http.StatusUnauthorized)
			return
		}
		tokenString := parts[1]

		// 3. Parser et valider le token
		claims := &Claims{}
		token, err := jwt.ParseWithClaims(tokenString, claims, func(token *jwt.Token) (interface{}, error) {
			// Valider la méthode de signature
			if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, fmt.Errorf("méthode de signature inattendue : %v", token.Header["alg"])
			}
			return jwtSecret, nil
		})

		if err != nil || !token.Valid {
			http.Error(w, `{"message": "Jeton invalide ou expiré"}`, http.StatusUnauthorized)
			return
		}

		// 4. Créer l'objet utilisateur et l'injecter dans le contexte
		currentUser := &User{
			ID:    claims.UserID,
			Roles: claims.Roles,
		}

		ctx := context.WithValue(r.Context(), UserContextKey, currentUser)

		// 5. Continuer vers la route suivante avec le nouveau contexte
		next(w, r.WithContext(ctx))
	}
}
*/
