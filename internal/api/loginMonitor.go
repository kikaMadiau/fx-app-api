package api

import (
	"net"
	"net/http"
	"time"

	"fx-app-api/internal/api/middlewareInternal"
	"golang.org/x/time/rate"
)

// LoginMonitor applique une limite de débit spécifique à l'authentification.
type LoginMonitor struct {
	limiter *middlewareInternal.RateLimiter
}

func NewLoginMonitor(requestsPerMinute, maxAttemptsPerHour int, banDuration time.Duration, trustedProxies []*net.IPNet) *LoginMonitor {
	if requestsPerMinute < 1 {
		requestsPerMinute = 1
	}
	if maxAttemptsPerHour < 1 {
		maxAttemptsPerHour = 1
	}
	return &LoginMonitor{limiter: middlewareInternal.NewRateLimiter(rate.Limit(float64(requestsPerMinute)/60), requestsPerMinute, maxAttemptsPerHour, banDuration, trustedProxies)}
}

func (m *LoginMonitor) RateLimitMiddleware() func(http.Handler) http.Handler {
	return m.limiter.RateLimitFailedRequests
}
