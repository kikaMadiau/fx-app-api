package api

import (
	"fx-app-api/internal/api/handlers"
	"fx-app-api/internal/api/middlewareInternal"
	customerservice "fx-app-api/internal/domain/customer/service"
	traderservices "fx-app-api/internal/domain/trader/traderservices"
	riskservice "fx-app-api/internal/domain/riskflag/service"
	"fx-app-api/internal/storage"
	"log"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

// RolePermissions définit les permissions par rôle pour chaque endpoint
type RolePermissions struct {
	Path    string
	Methods []string
	Roles   []string
}

// Permissions par endpoint
var endpointPermissions = []RolePermissions{
	// Routes publiques
	{Path: "/auth/trader/login", Methods: []string{"POST"}, Roles: []string{}},
	{Path: "/analysais", Methods: []string{"GET"}, Roles: []string{}},
	
	// Routes traders (tous les traders peuvent accéder)
	{Path: "/traders", Methods: []string{"POST"}, Roles: []string{"TRADER", "MANAGER"}},
	{Path: "/traders/{id}", Methods: []string{"GET"}, Roles: []string{"TRADER", "MANAGER"}},
	{Path: "/traders/{id}", Methods: []string{"PUT"}, Roles: []string{"TRADER", "MANAGER"}},
	{Path: "/traders/{id}", Methods: []string{"DELETE"}, Roles: []string{"MANAGER"}},
	
	// Routes customers (tous les traders peuvent accéder)
	{Path: "/customers", Methods: []string{"POST"}, Roles: []string{"TRADER", "MANAGER"}},
	{Path: "/customers/kyc", Methods: []string{"POST"}, Roles: []string{"TRADER", "MANAGER"}},
	{Path: "/customers/{id}", Methods: []string{"GET"}, Roles: []string{"TRADER", "MANAGER"}},
	{Path: "/customers/{id}", Methods: []string{"PUT"}, Roles: []string{"TRADER", "MANAGER"}},
	{Path: "/customers/{id}/kyc", Methods: []string{"GET"}, Roles: []string{"TRADER", "MANAGER"}},
	{Path: "/customers/{id}/kyc", Methods: []string{"PUT"}, Roles: []string{"TRADER", "MANAGER"}},
	{Path: "/customers/{id}/kyc/reassess", Methods: []string{"POST"}, Roles: []string{"TRADER", "MANAGER"}},
	
	// Routes transactions (tous les traders peuvent accéder)
	{Path: "/transactions", Methods: []string{"POST"}, Roles: []string{"TRADER", "MANAGER"}},
	{Path: "/transactions/{id}", Methods: []string{"GET"}, Roles: []string{"TRADER", "MANAGER"}},
	
	// Routes risk-flags (tous les traders peuvent accéder)
	{Path: "/risk-flags", Methods: []string{"POST"}, Roles: []string{"TRADER", "MANAGER"}},
	{Path: "/risk-flags", Methods: []string{"GET"}, Roles: []string{"TRADER", "MANAGER"}},
	{Path: "/risk-flags/{id}", Methods: []string{"GET"}, Roles: []string{"TRADER", "MANAGER"}},
	{Path: "/risk-flags/{id}", Methods: []string{"DELETE"}, Roles: []string{"MANAGER"}},
}

// RoleMiddlewareWithPermissions vérifie les permissions selon le path et la méthode
type RoleMiddlewareWithPermissions struct {
	permissions []RolePermissions
}

func NewRoleMiddlewareWithPermissions(permissions []RolePermissions) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user := traderservices.GetUser(r)
			if user == nil {
				http.Error(w, `{"message": "Utilisateur non authentifié"}`, http.StatusUnauthorized)
				return
			}

			// Vérifier si l'utilisateur a les permissions pour cette route
			if !hasPermission(user.Roles, r.Method, r.URL.Path, permissions) {
				http.Error(w, `{"message": "Permissions insuffisantes"}`, http.StatusForbidden)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

func hasPermission(userRoles string, method, path string, permissions []RolePermissions) bool {
	roles := strings.Split(userRoles, ",")
	for i := range roles {
		roles[i] = strings.TrimSpace(roles[i])
	}
	
	for _, perm := range permissions {
		if perm.Methods[0] == method && pathMatches(path, perm.Path) {
			// Si aucune role requise, route publique
			if len(perm.Roles) == 0 {
				return true
			}
			// Vérifier si l'utilisateur a au moins un des rôles requis
			for _, role := range roles {
				for _, requiredRole := range perm.Roles {
					if role == requiredRole {
						return true
					}
				}
			}
			return false
		}
	}
	return true // Si pas de permission définie, autoriser par défaut
}

func pathMatches(requestPath, pattern string) bool {
	// Gérer les patterns avec {id}
	parts := strings.Split(pattern, "/")
	requestParts := strings.Split(requestPath, "/")
	
	if len(parts) != len(requestParts) {
		return false
	}
	
	for i, part := range parts {
		if part == "{" || (strings.HasPrefix(part, "{") && strings.HasSuffix(part, "}")) {
			// C'est un paramètre dynamique, on l'ignore
			continue
		}
		if part != requestParts[i] {
			return false
		}
	}
	return true
}

type ApiServer struct {
	listenAddr string
	handlers   *handlers.Handler
	traderRepo storage.TraderStorage
}

func (s *ApiServer) Start() error {
	h := s.handlers

	router := chi.NewRouter()

	router.Use(middlewareInternal.CorsMiddleware)
	router.Use(middleware.CleanPath)
	router.Use(middleware.Logger)    // Ajout d'un logger pour les requêtes
	router.Use(middleware.Recoverer) // Pour éviter que le serveur ne crash en cas de panic

	// --- Routes Publiques (certaines protégées par reCAPTCHA) ---
	router.Group(func(r chi.Router) {
		r.Post("/auth/trader/login", MakeHttpHandleFunc(h.HandleTraderLogin))
	})

	router.Group(func(r chi.Router) {
		r.Use(middlewareInternal.AuthMiddleware(s.traderRepo))
		r.Use(NewRoleMiddlewareWithPermissions(endpointPermissions))

		r.Get("/analysais", MakeHttpHandleFunc(h.HandleListAnalysais))

		r.Post("/traders", MakeHttpHandleFunc(h.HandleCreateTrader))
		r.Get("/traders/{id}", MakeHttpHandleFunc(h.HandleGetTrader))
		r.Put("/traders/{id}", MakeHttpHandleFunc(h.HandleUpdateTrader))
		r.Delete("/traders/{id}", MakeHttpHandleFunc(h.HandleDeleteTrader))

		r.Post("/customers", MakeHttpHandleFunc(h.HandleCreateCustomer))
		r.Post("/customers/kyc", MakeHttpHandleFunc(h.HandleCreateCustomerWithKYC))
		r.Get("/customers/{id}", MakeHttpHandleFunc(h.HandleGetCustomer))
		r.Put("/customers/{id}", MakeHttpHandleFunc(h.HandleUpdateCustomer))
		r.Get("/customers/{id}/kyc", MakeHttpHandleFunc(h.HandleGetKYCProfile))
		r.Put("/customers/{id}/kyc", MakeHttpHandleFunc(h.HandleUpdateKYCProfile))
		r.Post("/customers/{id}/kyc/reassess", MakeHttpHandleFunc(h.HandleTriggerKYCReassessment))

		r.Post("/transactions", MakeHttpHandleFunc(h.HandleCreateTransaction))
		r.Get("/transactions/{id}", MakeHttpHandleFunc(h.HandleGetTransaction))

		r.Post("/risk-flags", MakeHttpHandleFunc(h.HandleCreateRiskFlag))
		r.Get("/risk-flags", MakeHttpHandleFunc(h.HandleListRiskFlags))
		r.Get("/risk-flags/{id}", MakeHttpHandleFunc(h.HandleGetRiskFlag))
		r.Delete("/risk-flags/{id}", MakeHttpHandleFunc(h.HandleDeleteRiskFlag))
	})

	log.Println("API server running on", s.listenAddr)

	return http.ListenAndServe(s.listenAddr, router)
}

type ApiFunc func(http.ResponseWriter, *http.Request) error
type ApiError struct {
	Message string `json:"message"`
}

type HTTPError struct {
	StatusCode int
	Message    string
	Err        error
}

type statusCodeError interface {
	error
	HTTPStatusCode() int
}

func NewServer(
	listenAddr string,
	traderStore storage.TraderStorage,
	customerStore storage.CustomerStorage,
	transactionStore storage.TransactionStorage,
	riskFlagStore storage.RiskFlagStorage,
	riskService *riskservice.RiskService,
	customerService *customerservice.CustomerService,
) *ApiServer {
	return &ApiServer{
		listenAddr: listenAddr,
		handlers:   handlers.New(traderStore, customerStore, transactionStore, riskFlagStore, riskService, customerService),
		traderRepo: traderStore,
	}
}
