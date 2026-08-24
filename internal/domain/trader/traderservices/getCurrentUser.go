package traderservices

import (
	"context"
	traderentity "fx-app-api/internal/domain/trader/entity"
	"net/http"
)

type contextKey string

const userContextKey contextKey = "user"

func WithUser(ctx context.Context, user *traderentity.Trader) context.Context {
	return context.WithValue(ctx, userContextKey, user)
}

func GetUser(r *http.Request) *traderentity.Trader {
	u, ok := r.Context().Value(userContextKey).(*traderentity.Trader)
	if !ok {
		return nil // Aucun utilisateur connecté
	}
	return u
}
