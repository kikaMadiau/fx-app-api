package api

import (
	//"fmt"
	"fx-app-api/internal/api/handlers"
	"fx-app-api/internal/api/middlewareInternal"
	auditrepository "fx-app-api/internal/domain/audit/repository"
	"fx-app-api/internal/domain/customer/iddocument"
	"fx-app-api/internal/domain/customer/kycrisk"
	customerservice "fx-app-api/internal/domain/customer/service"
	dashboardrepository "fx-app-api/internal/domain/dashboard/repository"
	"fx-app-api/internal/domain/notification"
	"fx-app-api/internal/domain/report"
	riskservice "fx-app-api/internal/domain/riskflag/service"
	storerepository "fx-app-api/internal/domain/store/repository"
	"fx-app-api/internal/domain/trader/passwordreset"
	"fx-app-api/internal/domain/trader/permissions"
	"fx-app-api/internal/domain/trader/profile"
	"fx-app-api/internal/domain/trader/registration"
	"fx-app-api/internal/storage"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"

	//"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"golang.org/x/time/rate"
)

type ApiServer struct {
	listenAddr        string
	handlers          *handlers.Handler
	traderRepo        storage.TraderStorage
	rateLimiter       *middlewareInternal.RateLimiter
	loginMonitor      *LoginMonitor
	auditRepo         *auditrepository.AuditRepository
	profileRepo       *profile.Repository
	idDocRepo         *iddocument.Repository
	kycRiskRepo       *kycrisk.Repository
	notificationRepo  *notification.Repository
	notifySettings    *notification.SettingsRepository
	passwordResetRepo *passwordreset.Repository
}

func (s *ApiServer) Start() error {
	if s.auditRepo != nil {
		if err := s.auditRepo.Init(); err != nil {
			return err
		}
	}
	if s.profileRepo != nil {
		if err := s.profileRepo.Init(); err != nil {
			return err
		}
	}
	if s.idDocRepo != nil {
		if err := s.idDocRepo.Init(); err != nil {
			return err
		}
	}
	if s.passwordResetRepo != nil {
		if err := s.passwordResetRepo.Init(); err != nil {
			return err
		}
	}
	if s.notifySettings != nil {
		if err := s.notifySettings.Init(); err != nil {
			return err
		}
	}
	if s.notificationRepo != nil {
		if err := s.notificationRepo.Init(); err != nil {
			return err
		}
	}
	if s.kycRiskRepo != nil {
		if err := s.kycRiskRepo.Init(); err != nil {
			return err
		}
	}
	h := s.handlers

	router := chi.NewRouter()

	// Middleware de sécurité de base
	router.Use(middlewareInternal.SecurityHeaders)
	router.Use(middlewareInternal.CorsMiddleware)
	router.Use(middleware.CleanPath)
	router.Use(middleware.Logger)    // Ajout d'un logger pour les requêtes
	router.Use(middleware.Recoverer) // Pour éviter que le serveur ne crash en cas de panic

	// Rate limiting global (si configuré)
	if s.rateLimiter != nil {
		router.Use(s.rateLimiter.RateLimit)
		log.Println("Rate limiting global activé")
	}

	// Le préfixe est configuré une seule fois pour l'ensemble des routes API.
	router.Route(apiPrefix(), func(r chi.Router) {
		// --- Routes Publiques (certaines protégées par reCAPTCHA) ---
		r.Group(func(r chi.Router) {
			// Rate limiting spécifique pour l'authentification
			if s.loginMonitor != nil {
				r.Use(s.loginMonitor.RateLimitMiddleware())
			}
			r.Post("/auth/trader/login", MakeHttpHandleFunc(h.HandleTraderLogin))
			// Mot de passe oublié : mêmes limites que la connexion.
			r.Post("/auth/password/forgot", MakeHttpHandleFunc(h.HandleForgotPassword))
			r.Post("/auth/password/reset", MakeHttpHandleFunc(h.HandleResetPassword))
			// Créer un compte : seulement avec un numéro d'agrément BCC valable.
			r.Post("/auth/register", MakeHttpHandleFunc(h.HandleRegister))
		})

		r.Group(func(r chi.Router) {
			r.Use(middlewareInternal.AuthMiddleware(s.traderRepo))
			registerProtectedRoutes(r, h)
		})
	})

	log.Println("API server running on", s.listenAddr)

	server := &http.Server{
		Addr:              s.listenAddr,
		Handler:           router,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}
	return server.ListenAndServe()
}

func apiPrefix() string {
	prefix := strings.TrimSpace(getEnvOrDefault("API_PREFIX", "/v1"))
	if prefix == "" || prefix == "/" {
		return "/"
	}
	return "/" + strings.Trim(prefix, "/")
}

type protectedRoute struct {
	Method, Path, Permission string
	Handler                  func(*handlers.Handler) ApiFunc
}

var protectedRouteDefinitions = []protectedRoute{
	{http.MethodGet, "/auth/me", permissions.ProfileRead, func(h *handlers.Handler) ApiFunc { return h.HandleGetCurrentTrader }},
	{http.MethodPost, "/stores", permissions.PlatformManageStores, func(h *handlers.Handler) ApiFunc { return h.HandleCreateStore }},
	{http.MethodGet, "/stores", permissions.PlatformManageStores, func(h *handlers.Handler) ApiFunc { return h.HandleListStores }},
	{http.MethodGet, "/stores/{id}", permissions.StoreRead, func(h *handlers.Handler) ApiFunc { return h.HandleGetStore }},
	{http.MethodPatch, "/stores/{id}", permissions.PlatformManageStores, func(h *handlers.Handler) ApiFunc { return h.HandleUpdateStore }},
	{http.MethodGet, "/stores/{id}/members", permissions.StoreManageMembers, func(h *handlers.Handler) ApiFunc { return h.HandleListStoreMembers }},
	{http.MethodPost, "/stores/{id}/members", permissions.StoreManageMembers, func(h *handlers.Handler) ApiFunc { return h.HandleAddStoreMember }},
	{http.MethodPatch, "/stores/{id}/members/{traderId}", permissions.StoreManageMembers, func(h *handlers.Handler) ApiFunc { return h.HandleUpdateStoreMember }},
	{http.MethodDelete, "/stores/{id}/members/{traderId}", permissions.StoreManageMembers, func(h *handlers.Handler) ApiFunc { return h.HandleRejectStoreMember }},
	{http.MethodGet, "/auth/me/profile", permissions.ProfileRead, func(h *handlers.Handler) ApiFunc { return h.HandleGetProfile }},
	{http.MethodPut, "/auth/me/profile", permissions.ProfileUpdate, func(h *handlers.Handler) ApiFunc { return h.HandleUpdateProfile }},
	{http.MethodGet, "/auth/me/photo", permissions.ProfileRead, func(h *handlers.Handler) ApiFunc { return h.HandleGetProfilePhoto }},
	{http.MethodPut, "/auth/me/photo", permissions.ProfileUpdate, func(h *handlers.Handler) ApiFunc { return h.HandleUploadProfilePhoto }},
	{http.MethodDelete, "/auth/me/photo", permissions.ProfileUpdate, func(h *handlers.Handler) ApiFunc { return h.HandleDeleteProfilePhoto }},
	{http.MethodPut, "/auth/me/location", permissions.ProfileUpdate, func(h *handlers.Handler) ApiFunc { return h.HandleUpdateLocation }},
	{http.MethodDelete, "/auth/me/location", permissions.ProfileUpdate, func(h *handlers.Handler) ApiFunc { return h.HandleStopLocation }},
	// Administrateur de la plateforme : annuaire du personnel et réglages d'envoi (Twilio, SMTP).
	{http.MethodGet, "/staff", permissions.PlatformManageStores, func(h *handlers.Handler) ApiFunc { return h.HandleListStaff }},
	{http.MethodGet, "/staff/{id}/photo", permissions.PlatformManageStores, func(h *handlers.Handler) ApiFunc { return h.HandleGetStaffPhoto }},
	{http.MethodGet, "/admin/notification-settings", permissions.PlatformManageStores, func(h *handlers.Handler) ApiFunc { return h.HandleGetNotificationSettings }},
	{http.MethodPut, "/admin/notification-settings", permissions.PlatformManageStores, func(h *handlers.Handler) ApiFunc { return h.HandleUpdateNotificationSettings }},
	{http.MethodPost, "/admin/notification-settings/test", permissions.PlatformManageStores, func(h *handlers.Handler) ApiFunc { return h.HandleTestNotification }},
	{http.MethodPut, "/auth/me/notifications", permissions.ProfileUpdate, func(h *handlers.Handler) ApiFunc { return h.HandleUpdateNotifications }},
	{http.MethodPost, "/auth/me/2fa/setup", permissions.ProfileUpdate, func(h *handlers.Handler) ApiFunc { return h.HandleStartTwoFactorSetup }},
	{http.MethodPost, "/auth/me/2fa/enable", permissions.ProfileUpdate, func(h *handlers.Handler) ApiFunc { return h.HandleConfirmTwoFactorSetup }},
	{http.MethodPost, "/auth/me/2fa/disable", permissions.ProfileUpdate, func(h *handlers.Handler) ApiFunc { return h.HandleDisableTwoFactor }},
	{http.MethodPut, "/auth/me/preferences", permissions.ProfileUpdate, func(h *handlers.Handler) ApiFunc { return h.HandleUpdatePreferences }},
	{http.MethodPost, "/auth/me/password", permissions.ProfileUpdate, func(h *handlers.Handler) ApiFunc { return h.HandleChangePassword }},
	{http.MethodGet, "/reports/activity", permissions.DashboardRead, func(h *handlers.Handler) ApiFunc { return h.HandleActivityReport }},
	{http.MethodGet, "/reports/compliance", permissions.DashboardRead, func(h *handlers.Handler) ApiFunc { return h.HandleComplianceReport }},
	{http.MethodGet, "/dashboard/summary", permissions.DashboardRead, func(h *handlers.Handler) ApiFunc { return h.HandleDashboardSummary }},
	{http.MethodGet, "/analysais", permissions.AnalysisRead, func(h *handlers.Handler) ApiFunc { return h.HandleListAnalysais }},
	{http.MethodPost, "/traders", permissions.TraderManage, func(h *handlers.Handler) ApiFunc { return h.HandleCreateTrader }},
	{http.MethodGet, "/traders/{id}", permissions.TraderManage, func(h *handlers.Handler) ApiFunc { return h.HandleGetTrader }},
	{http.MethodPut, "/traders/{id}", permissions.TraderManage, func(h *handlers.Handler) ApiFunc { return h.HandleUpdateTrader }},
	{http.MethodDelete, "/traders/{id}", permissions.TraderManage, func(h *handlers.Handler) ApiFunc { return h.HandleDeleteTrader }},
	{http.MethodPost, "/customers", permissions.CustomerCreate, func(h *handlers.Handler) ApiFunc { return h.HandleCreateCustomer }},
	{http.MethodGet, "/customers", permissions.CustomerRead, func(h *handlers.Handler) ApiFunc { return h.HandleListCustomers }},
	{http.MethodPost, "/customers/kyc", permissions.KYCUpdate, func(h *handlers.Handler) ApiFunc { return h.HandleCreateCustomerWithKYC }},
	{http.MethodGet, "/customers/search", permissions.CustomerRead, func(h *handlers.Handler) ApiFunc { return h.HandleSearchCustomerByPhone }},
	{http.MethodGet, "/customers/{id}/id-document", permissions.CustomerRead, func(h *handlers.Handler) ApiFunc { return h.HandleGetIDDocumentStatus }},
	{http.MethodGet, "/customers/{id}/id-document/{side}", permissions.KYCDocumentRead, func(h *handlers.Handler) ApiFunc { return h.HandleGetIDDocument }},
	{http.MethodPut, "/customers/{id}/id-document/{side}", permissions.KYCUpdate, func(h *handlers.Handler) ApiFunc { return h.HandleUploadIDDocument }},
	{http.MethodGet, "/customers/{id}/kyc-risk", permissions.CustomerRead, func(h *handlers.Handler) ApiFunc { return h.HandleGetKYCRisk }},
	{http.MethodPost, "/customers/{id}/kyc-override", permissions.KYCVerify, func(h *handlers.Handler) ApiFunc { return h.HandleGrantKYCOverride }},
	{http.MethodPatch, "/customers/{id}/identity", permissions.KYCUpdate, func(h *handlers.Handler) ApiFunc { return h.HandleUpdateCustomerIdentity }},
	{http.MethodGet, "/customers/{id}", permissions.CustomerRead, func(h *handlers.Handler) ApiFunc { return h.HandleGetCustomer }},
	{http.MethodPut, "/customers/{id}", permissions.KYCUpdate, func(h *handlers.Handler) ApiFunc { return h.HandleUpdateCustomer }},
	{http.MethodGet, "/customers/{id}/kyc", permissions.KYCRead, func(h *handlers.Handler) ApiFunc { return h.HandleGetKYCProfile }},
	{http.MethodPut, "/customers/{id}/kyc", permissions.KYCUpdate, func(h *handlers.Handler) ApiFunc { return h.HandleUpdateKYCProfile }},
	{http.MethodPatch, "/customers/{id}/kyc/status", permissions.KYCVerify, func(h *handlers.Handler) ApiFunc { return h.HandleUpdateKYCStatus }},
	{http.MethodPost, "/customers/{id}/kyc/reassess", permissions.KYCReassess, func(h *handlers.Handler) ApiFunc { return h.HandleTriggerKYCReassessment }},
	{http.MethodPost, "/transactions", permissions.TransactionCreate, func(h *handlers.Handler) ApiFunc { return h.HandleCreateTransaction }},
	{http.MethodGet, "/transactions/{id}", permissions.TransactionRead, func(h *handlers.Handler) ApiFunc { return h.HandleGetTransaction }},
	{http.MethodGet, "/transactions/{id}/notifications", permissions.TransactionRead, func(h *handlers.Handler) ApiFunc { return h.HandleListTransactionNotifications }},
	{http.MethodGet, "/transactions", permissions.TransactionRead, func(h *handlers.Handler) ApiFunc { return h.HandleListTransactions }},
	{http.MethodPost, "/risk-flags", permissions.RiskFlagCreate, func(h *handlers.Handler) ApiFunc { return h.HandleCreateRiskFlag }},
	{http.MethodGet, "/risk-flags", permissions.RiskFlagRead, func(h *handlers.Handler) ApiFunc { return h.HandleListRiskFlags }},
	{http.MethodGet, "/risk-flags/{id}", permissions.RiskFlagRead, func(h *handlers.Handler) ApiFunc { return h.HandleGetRiskFlag }},
	{http.MethodDelete, "/risk-flags/{id}", permissions.RiskFlagDelete, func(h *handlers.Handler) ApiFunc { return h.HandleDeleteRiskFlag }},
	{http.MethodPatch, "/risk-flags/{id}/status", permissions.RiskFlagInvestigate, func(h *handlers.Handler) ApiFunc { return h.HandleTransitionRiskFlagStatus }},
	{http.MethodGet, "/risk-flags/{id}/history", permissions.RiskFlagRead, func(h *handlers.Handler) ApiFunc { return h.HandleGetRiskFlagHistory }},
}

func registerProtectedRoutes(router chi.Router, h *handlers.Handler) {
	for _, route := range protectedRouteDefinitions {
		var handler http.Handler = MakeHttpHandleFunc(route.Handler(h))
		if table, message, ok := storeOwnedRoute(route.Path); ok {
			handler = h.StoreGuard(table, message)(handler)
		}
		wrapped := middlewareInternal.RequirePermission(route.Permission)(handler)
		router.Method(route.Method, route.Path, wrapped)
	}
}

// storeOwnedRoute : routes /customers/{id}, /transactions/{id} et /risk-flags/{id} (et leurs
// sous-routes), dont la ligne doit appartenir au bureau du compte connecté.
func storeOwnedRoute(path string) (table, notFoundMessage string, ok bool) {
	for prefix, t := range map[string][2]string{
		"/customers/{id}":    {"customers", "customer not found"},
		"/transactions/{id}": {"transactions", "transaction not found"},
		"/risk-flags/{id}":   {"risk_flags", "risk flag not found"},
	} {
		if path == prefix || strings.HasPrefix(path, prefix+"/") {
			return t[0], t[1], true
		}
	}
	return "", "", false
}

type ApiFunc func(http.ResponseWriter, *http.Request) error
type ApiError struct {
	Message string `json:"message"`
	Code    string `json:"code,omitempty"`
}

type HTTPError struct {
	StatusCode int
	Message    string
	Code       string
	Err        error
}

type statusCodeError interface {
	error
	HTTPStatusCode() int
}

// getEnvOrDefault retourne une valeur d'environnement ou une valeur par défaut
func getEnvOrDefault(key string, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

// getEnvOrDefaultInt retourne une valeur d'environnement comme int ou une valeur par défaut
func getEnvOrDefaultInt(key string, defaultValue int) int {
	if value := os.Getenv(key); value != "" {
		if intValue, err := strconv.Atoi(value); err == nil {
			return intValue
		}
	}
	return defaultValue
}

// getEnvOrDefaultDuration retourne une valeur d'environnement comme durée ou une valeur par défaut
func getEnvOrDefaultDuration(key string, defaultValue time.Duration) time.Duration {
	if value := os.Getenv(key); value != "" {
		if duration, err := time.ParseDuration(value); err == nil {
			return duration
		}
	}
	return defaultValue
}

// parseTrustedProxies parse les proxies de confiance depuis une variable d'environnement
func parseTrustedProxies() []*net.IPNet {
	proxyStr := os.Getenv("TRUSTED_PROXIES")
	if proxyStr == "" {
		return nil
	}

	var proxies []*net.IPNet
	parts := strings.Split(proxyStr, ",")

	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}

		// Essayer comme CIDR d'abord
		if _, ipNet, err := net.ParseCIDR(part); err == nil {
			proxies = append(proxies, ipNet)
			continue
		}

		// Essayer comme IP simple
		if ip := net.ParseIP(part); ip != nil {
			mask := net.CIDRMask(32, 32) // /32 pour IPv4
			if ip.To4() == nil {
				mask = net.CIDRMask(128, 128) // /128 pour IPv6
			}
			ipNet := &net.IPNet{
				IP:   ip,
				Mask: mask,
			}
			proxies = append(proxies, ipNet)
		}
	}

	return proxies
}

// NewServer crée un nouveau serveur API avec sécurité activée
func NewServer(
	listenAddr string,
	traderStore storage.TraderStorage,
	customerStore storage.CustomerStorage,
	transactionStore storage.TransactionStorage,
	riskFlagStore storage.RiskFlagStorage,
	riskService *riskservice.RiskService,
	customerService *customerservice.CustomerService,
) *ApiServer {
	server := &ApiServer{
		listenAddr: listenAddr,
		handlers:   handlers.New(traderStore, customerStore, transactionStore, riskFlagStore, riskService, customerService),
		traderRepo: traderStore,
	}
	if provider, ok := transactionStore.(interface{ Store() *storage.PostgresStore }); ok {
		server.handlers.SetDashboardRepository(dashboardrepository.NewDashboardRepository(provider.Store()))
		server.auditRepo = auditrepository.NewAuditRepository(provider.Store())
		server.handlers.SetAuditLogger(server.auditRepo)
		server.handlers.SetStoreRepository(storerepository.New(provider.Store().DB()))
		server.profileRepo = profile.NewRepository(provider.Store())
		server.handlers.SetProfileRepository(server.profileRepo)
		server.idDocRepo = iddocument.NewRepository(provider.Store())
		server.handlers.SetIDDocumentRepository(server.idDocRepo)
		server.handlers.SetReportRepository(report.NewRepository(provider.Store()))
		server.kycRiskRepo = kycrisk.NewRepository(provider.Store())
		server.handlers.SetKYCRiskRepository(server.kycRiskRepo)
		if riskService != nil {
			riskService.SetSignalSource(server.kycRiskRepo)
		}
		server.passwordResetRepo = passwordreset.NewRepository(provider.Store())
		server.handlers.SetPasswordResetRepository(server.passwordResetRepo)
		server.handlers.SetRegistrationRepository(registration.NewRepository(provider.Store()))
		server.notificationRepo = notification.NewRepository(provider.Store())
		// Réglages Twilio/SMTP de l'administrateur : à brancher avant de créer l'envoi.
		server.notifySettings = notification.NewSettingsRepository(provider.Store())
		notification.UseSettings(server.notifySettings)
		server.handlers.SetNotificationSettings(server.notifySettings)
		server.handlers.SetNotifications(server.notificationRepo, notification.NewService(server.notificationRepo, notification.NewSenderFromEnv()))
	}

	// Configurer le rate limiting global
	if getEnvOrDefault("RATE_LIMIT_ENABLED", "true") == "true" {
		rateLimit := getEnvOrDefaultInt("RATE_LIMIT_REQUESTS_PER_SECOND", 10)
		burstLimit := getEnvOrDefaultInt("RATE_LIMIT_BURST", 20)
		maxBadAttempts := getEnvOrDefaultInt("RATE_LIMIT_MAX_BAD_ATTEMPTS", 50)
		banDuration := getEnvOrDefaultDuration("RATE_LIMIT_BAN_DURATION", 30*time.Minute)
		trustedProxies := parseTrustedProxies()

		server.rateLimiter = middlewareInternal.NewRateLimiter(
			rate.Limit(rateLimit),
			burstLimit,
			maxBadAttempts,
			banDuration,
			trustedProxies,
		)

		log.Printf("Rate limiting configuré: %d req/sec, burst: %d, ban après %d tentatives pour %v",
			rateLimit, burstLimit, maxBadAttempts, banDuration)
	}

	// Configurer le monitoring des tentatives de connexion
	if getEnvOrDefault("LOGIN_PROTECTION_ENABLED", "true") == "true" {
		loginRateLimit := getEnvOrDefaultInt("LOGIN_RATE_LIMIT_REQUESTS_PER_MINUTE", 5)
		loginMaxAttempts := getEnvOrDefaultInt("LOGIN_MAX_ATTEMPTS_PER_HOUR", 10)
		loginBanDuration := getEnvOrDefaultDuration("LOGIN_BAN_DURATION", 1*time.Hour)

		server.loginMonitor = NewLoginMonitor(
			loginRateLimit,
			loginMaxAttempts,
			loginBanDuration,
			parseTrustedProxies(),
		)

		log.Printf("Protection login activée: %d req/min, max %d tentatives/heure, ban: %v",
			loginRateLimit, loginMaxAttempts, loginBanDuration)
	}

	return server
}
