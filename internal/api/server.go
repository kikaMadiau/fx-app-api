package api

import (
	"fx-app-api/internal/api/handlers"
	"fx-app-api/internal/api/middlewareInternal"
	customerservice "fx-app-api/internal/domain/customer/service"
	riskservice "fx-app-api/internal/domain/riskflag/service"
	"fx-app-api/internal/storage"
	"log"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

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
		//r.Use(middlewareInternal.RoleMiddleware([]string{"admin", "trader"}))

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
