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
	values := permissions.ForRole(user.Roles)
	if user.IsPlatformAdmin {
		values = append(values, permissions.PlatformManageStores, permissions.TraderManage)
	}
	return context.WithValue(ctx, permissionsContextKey, values)
}

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
	return user != nil && ((user.IsPlatformAdmin && permission == permissions.PlatformManageStores) || permissions.Has(user.Roles, permission))
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
