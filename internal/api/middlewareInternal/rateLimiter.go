package middlewareInternal

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

type client struct {
	limiter     *rate.Limiter
	lastSeen    time.Time
	infractions int
}

type RateLimiter struct {
	mu sync.RWMutex

	clients     map[string]*client
	bannedUntil map[string]time.Time

	rate           rate.Limit
	burst          int
	maxBadAttempts int
	banDuration    time.Duration

	// Proxies dont l'application accepte les IPs transmises.
	// Exemple : reverse proxy interne ou proxy de confiance.
	trustedProxies []*net.IPNet

	stopCleanup chan struct{}
	stopOnce    sync.Once
}

// NewRateLimiter crée un rate limiter.
//
// Exemple :
//
//	rl := NewRateLimiter(
//	    5,              // 5 requêtes/sec
//	    10,             // burst de 10
//	    20,             // 20 refus avant ban
//	    10*time.Minute, // durée du ban
//	    nil,            // proxies de confiance
//	)
func NewRateLimiter(
	r rate.Limit,
	burst int,
	maxBadAttempts int,
	banDuration time.Duration,
	trustedProxies []*net.IPNet,
) *RateLimiter {

	rl := &RateLimiter{
		clients:     make(map[string]*client),
		bannedUntil: make(map[string]time.Time),

		rate:           r,
		burst:          burst,
		maxBadAttempts: maxBadAttempts,
		banDuration:    banDuration,

		trustedProxies: trustedProxies,

		stopCleanup: make(chan struct{}),
	}

	go rl.cleanupClients()

	return rl
}

// Close arrête proprement la goroutine de nettoyage.
func (rl *RateLimiter) Close() {
	rl.stopOnce.Do(func() {
		close(rl.stopCleanup)
	})
}

// IsBanned vérifie si une IP est actuellement bannie.
func (rl *RateLimiter) IsBanned(ip string) (bool, time.Duration) {
	rl.mu.RLock()
	unbanTime, exists := rl.bannedUntil[ip]
	rl.mu.RUnlock()

	if !exists {
		return false, 0
	}

	remaining := time.Until(unbanTime)

	if remaining <= 0 {
		rl.mu.Lock()

		// Double-check après acquisition du lock.
		if current, ok := rl.bannedUntil[ip]; ok && !time.Now().Before(current) {
			delete(rl.bannedUntil, ip)

			if c, ok := rl.clients[ip]; ok {
				c.infractions = 0
			}
		}

		rl.mu.Unlock()

		return false, 0
	}

	return true, remaining
}

// getLimiter retourne le limiter d'une IP et met à jour lastSeen.
func (rl *RateLimiter) getLimiter(ip string) *rate.Limiter {
	now := time.Now()

	rl.mu.Lock()
	defer rl.mu.Unlock()

	if c, exists := rl.clients[ip]; exists {
		c.lastSeen = now
		return c.limiter
	}

	limiter := rate.NewLimiter(rl.rate, rl.burst)

	rl.clients[ip] = &client{
		limiter:     limiter,
		lastSeen:    now,
		infractions: 0,
	}

	return limiter
}

// registerInfraction enregistre un refus.
// Retourne true si l'IP vient d'être bannie.
func (rl *RateLimiter) registerInfraction(ip string) (bool, time.Duration) {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	c, exists := rl.clients[ip]
	if !exists {
		return false, 0
	}

	c.infractions++

	if c.infractions < rl.maxBadAttempts {
		return false, 0
	}

	unbanTime := time.Now().Add(rl.banDuration)
	rl.bannedUntil[ip] = unbanTime

	return true, rl.banDuration
}

// resetInfractions peut être utilisé après une période normale.
func (rl *RateLimiter) resetInfractions(ip string) {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	if c, exists := rl.clients[ip]; exists {
		c.infractions = 0
	}
}

// RateLimit middleware.
func (rl *RateLimiter) RateLimit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		ip := getRealIP(r, rl.trustedProxies)

		if ip == "" {
			writeJSONError(
				w,
				http.StatusInternalServerError,
				"Unable to determine client IP.",
			)
			return
		}

		// --------------------------------------------------
		// 1. Vérification du bannissement
		// --------------------------------------------------

		if banned, remaining := rl.IsBanned(ip); banned {
			setRetryAfter(w, remaining)

			writeJSONError(
				w,
				http.StatusTooManyRequests,
				"Too many requests. Your IP has been temporarily blocked.",
			)

			return
		}

		// --------------------------------------------------
		// 2. Rate limit
		// --------------------------------------------------

		limiter := rl.getLimiter(ip)

		if !limiter.Allow() {
			banned, banDuration := rl.registerInfraction(ip)

			if banned {
				setRetryAfter(w, banDuration)

				writeJSONError(
					w,
					http.StatusTooManyRequests,
					"Too many requests. Your IP has been temporarily blocked.",
				)

				return
			}

			// Retry-After approximatif pour le client.
			setRetryAfter(w, time.Second)

			writeJSONError(
				w,
				http.StatusTooManyRequests,
				"Too many requests. Please slow down.",
			)

			return
		}

		// --------------------------------------------------
		// 3. Requête autorisée
		// --------------------------------------------------

		next.ServeHTTP(w, r)
	})
}

// cleanupClients nettoie périodiquement les clients inactifs
// et les bannissements expirés.
func (rl *RateLimiter) cleanupClients() {
	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			rl.cleanup()

		case <-rl.stopCleanup:
			return
		}
	}
}

func (rl *RateLimiter) cleanup() {
	now := time.Now()

	rl.mu.Lock()
	defer rl.mu.Unlock()

	// Nettoyage des clients inactifs.
	for ip, c := range rl.clients {
		if now.Sub(c.lastSeen) > 30*time.Minute {
			delete(rl.clients, ip)
		}
	}

	// Nettoyage des bans expirés.
	for ip, unbanTime := range rl.bannedUntil {
		if !now.Before(unbanTime) {
			delete(rl.bannedUntil, ip)
		}
	}
}

// ------------------------------------------------------
// HTTP helpers
// ------------------------------------------------------

func writeJSONError(
	w http.ResponseWriter,
	status int,
	message string,
) {
	w.WriteHeader(status)

	_ = json.NewEncoder(w).Encode(map[string]string{
		"message": message,
	})
}

func setRetryAfter(w http.ResponseWriter, duration time.Duration) {
	seconds := int(duration.Seconds())

	if seconds < 1 {
		seconds = 1
	}

	w.Header().Set(
		"Retry-After",
		strconv.Itoa(seconds),
	)
}

// ------------------------------------------------------
// IP extraction
// ------------------------------------------------------

// getRealIP récupère l'IP réelle du client.
//
// IMPORTANT :
// On ne fait confiance à X-Forwarded-For / CF-Connecting-IP
// que si la requête provient d'un proxy de confiance.
//
// Sans cette vérification, un attaquant peut envoyer :
//
//	X-Forwarded-For: 1.2.3.4
//
// et contourner le rate limiter.
func getRealIP(r *http.Request, trustedProxies []*net.IPNet) string {

	remoteIP := extractRemoteIP(r.RemoteAddr)

	if remoteIP == "" {
		return ""
	}

	// Si le serveur reçoit directement la requête,
	// on utilise RemoteAddr.
	if !isTrustedProxy(remoteIP, trustedProxies) {
		return remoteIP
	}

	// --------------------------------------------------
	// Cloudflare
	// --------------------------------------------------

	if cfIP := strings.TrimSpace(
		r.Header.Get("CF-Connecting-IP"),
	); cfIP != "" {
		if parsed := net.ParseIP(cfIP); parsed != nil {
			return parsed.String()
		}
	}

	// --------------------------------------------------
	// X-Forwarded-For
	// --------------------------------------------------

	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")

		// On parcourt de droite à gauche afin de trouver
		// la première IP non-proxy de confiance.
		for i := len(parts) - 1; i >= 0; i-- {
			ip := strings.TrimSpace(parts[i])

			parsed := net.ParseIP(ip)

			if parsed == nil {
				continue
			}

			if !isTrustedProxy(ip, trustedProxies) {
				return parsed.String()
			}
		}
	}

	return remoteIP
}

func extractRemoteIP(remoteAddr string) string {
	host, _, err := net.SplitHostPort(remoteAddr)

	if err == nil {
		return host
	}

	// Cas où RemoteAddr contient déjà une IP.
	ip := net.ParseIP(strings.TrimSpace(remoteAddr))

	if ip != nil {
		return ip.String()
	}

	return ""
}

func isTrustedProxy(ipString string, trustedProxies []*net.IPNet) bool {

	ip := net.ParseIP(ipString)

	if ip == nil {
		return false
	}

	for _, network := range trustedProxies {
		if network.Contains(ip) {
			return true
		}
	}

	return false
}

// ------------------------------------------------------
// Context helper
// ------------------------------------------------------

// ClientIPKey permet éventuellement de récupérer
// l'IP dans les handlers suivants.
type rateLimitContextKey string

const ClientIPKey rateLimitContextKey = "client_ip"

func withClientIP(ctx context.Context, ip string) context.Context {
	return context.WithValue(ctx, ClientIPKey, ip)
}
