package traderservices

import (
	"context"
	traderentity "fx-app-api/internal/domain/trader/entity"
	"fx-app-api/internal/domain/trader/permissions"
	"net/http"
	"strings"
)

type contextKey string

const userContextKey contextKey = "user"
const permissionsContextKey contextKey = "permissions"
const storeContextKey contextKey = "store"

func WithUser(ctx context.Context, user *traderentity.Trader) context.Context {
	ctx = context.WithValue(ctx, userContextKey, user)
	ctx = context.WithValue(ctx, storeContextKey, user.Store)
	return context.WithValue(ctx, permissionsContextKey, permissions.ForRole(EffectiveRoles(user)))
}

// EffectiveRoles : rôles de store du compte, plus PLATFORM_ADMIN pour l'administrateur de la
// plateforme (indicateur du compte, sans store), pour appliquer la matrice des permissions.
func EffectiveRoles(user *traderentity.Trader) string {
	if user == nil {
		return ""
	}
	if !user.IsPlatformAdmin {
		return user.Roles
	}
	if strings.TrimSpace(user.Roles) == "" {
		return string(permissions.PlatformAdmin)
	}
	return user.Roles + "," + string(permissions.PlatformAdmin)
}

// StoreScope : bureau dont le compte peut voir les données. 0 = tous les bureaux (administrateur
// de la plateforme) ; -1 = aucun (compte sans bureau, qui ne doit rien voir).
func StoreScope(user *traderentity.Trader) int {
	switch {
	case user == nil:
		return -1
	case user.IsPlatformAdmin:
		return 0
	case user.StoreId > 0:
		return user.StoreId
	default:
		return -1
	}
}

// RequestStoreScope : StoreScope du compte connecté.
func RequestStoreScope(r *http.Request) int { return StoreScope(GetUser(r)) }

func GetStore(r *http.Request) *traderentity.Store {
	store, _ := r.Context().Value(storeContextKey).(*traderentity.Store)
	return store
}

func GetPermissions(r *http.Request) []string {
	values, _ := r.Context().Value(permissionsContextKey).([]string)
	return append([]string(nil), values...)
}

func HasPermission(r *http.Request, permission string) bool {
	user := GetUser(r)
	return user != nil && permissions.Has(EffectiveRoles(user), permission)
}

func RoleList(user *traderentity.Trader) []string {
	if user == nil || strings.TrimSpace(user.Roles) == "" {
		return []string{}
	}
	items := strings.Split(user.Roles, ",")
	for i := range items {
		items[i] = strings.TrimSpace(items[i])
	}
	return items
}

func GetUser(r *http.Request) *traderentity.Trader {
	u, ok := r.Context().Value(userContextKey).(*traderentity.Trader)
	if !ok {
		return nil // Aucun utilisateur connecté
	}
	return u
}
