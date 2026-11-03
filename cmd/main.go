package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"fx-app-api/config"
	"fx-app-api/internal/api"
	customerrepository "fx-app-api/internal/domain/customer/repository"
	customerservice "fx-app-api/internal/domain/customer/service"
	riskflagrepository "fx-app-api/internal/domain/riskflag/repository"
	riskservice "fx-app-api/internal/domain/riskflag/service"
	traderentity "fx-app-api/internal/domain/trader/entity"
	"fx-app-api/internal/domain/trader/permissions"
	traderrepository "fx-app-api/internal/domain/trader/repository"
	traderauthservice "fx-app-api/internal/domain/trader/traderservices"
	transactionrepository "fx-app-api/internal/domain/transaction/repository"
	"fx-app-api/internal/storage"
	"fx-app-api/internal/utils/services"
	"io"
	"log"
	"os"
	"strings"
)

func main() {
	// Charger les variables d'environnement depuis .env
	if err := config.LoadEnv(".env"); err != nil {
		log.Printf("Warning: could not load .env file: %v", err)
	}
	if len(os.Args) > 1 && os.Args[1] == "user" {
		if err := runUserCommand(os.Args[2:]); err != nil {
			log.Fatal(err)
		}
		return
	}
	fmt.Println("Hello congo trader")

	// Vérifier la configuration des clés JWT
	if err := services.CheckJWTKeys(); err != nil {
		log.Printf("WARNING: JWT key configuration issue: %v", err)
		log.Println("Authentication may fail. Please configure JWT keys properly.")
	}

	// Vérifier la sécurité de la base de données
	if err := storage.CheckDatabaseSecurity(); err != nil {
		log.Fatalf("SECURITY ERROR: database configuration rejected: %v", err)
	}

	// Initialiser la connexion à la base de données
	store, err := storage.NewPostgresStore()
	if err != nil {
		log.Fatalf("Failed to connect to the database: %v", err)
	}

	// Initialiser les repositories et leurs schémas
	traderRepo := traderrepository.NewTraderRepository(store)
	if err := traderRepo.Init(); err != nil {
		log.Fatalf("Failed to initialize trader repository: %v", err)
	}
	if err := bootstrapAdmin(traderRepo); err != nil {
		log.Fatalf("Failed to bootstrap administrator: %v", err)
	}

	customerRepo := customerrepository.NewCustomerRepository(store)
	if err := customerRepo.Init(); err != nil {
		log.Fatalf("Failed to initialize customer repository: %v", err)
	}

	kycProfileRepo := customerrepository.NewKYCProfileRepository(store)
	if err := kycProfileRepo.Init(); err != nil {
		log.Fatalf("Failed to initialize KYC profile repository: %v", err)
	}

	riskFlagRepo := riskflagrepository.NewRiskFlagRepository(store)
	if err := riskFlagRepo.Init(); err != nil {
		log.Fatalf("Failed to initialize risk_flag repository: %v", err)
	}

	transactionRepo := transactionrepository.NewTransactionRepository(store)
	if err := transactionRepo.Init(); err != nil {
		log.Fatalf("Failed to initialize transaction repository: %v", err)
	}

	// Initialiser les services métier
	customerService := customerservice.NewCustomerServiceWithKYC(customerRepo, kycProfileRepo)

	// Initialiser le service de risque
	riskService := riskservice.NewRiskService(
		customerRepo,
		transactionRepo,
		riskFlagRepo,
		customerService,
	)

	log.Println("Database and repositories initialized successfully")

	// Initialiser le serveur API avec toutes les dépendances
	listenAddr := strings.TrimSpace(os.Getenv("API_ADDR"))
	if listenAddr == "" {
		listenAddr = ":8080"
	}
	server := api.NewServer(listenAddr,
		traderRepo,
		customerRepo,
		transactionRepo,
		riskFlagRepo,
		riskService,
		customerService,
	)

	log.Fatal(server.Start())
}

func runUserCommand(args []string) error {
	if len(args) == 0 || args[0] != "create" {
		return fmt.Errorf("usage: fx-app user create < user.json")
	}
	if len(args) != 1 {
		return fmt.Errorf("usage: fx-app user create < user.json")
	}
	var req struct {
		Name      string `json:"name"`
		FirstName string `json:"first_name"`
		LastName  string `json:"last_name"`
		Email     string `json:"email"`
		Phone     string `json:"phone"`
		Status    string `json:"status"`
		Role      string `json:"role"`
		Password  string `json:"password"`
		StoreID   int    `json:"store_id"`
		IsActive  *bool  `json:"is_active"`
	}
	decoder := json.NewDecoder(os.Stdin)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		return fmt.Errorf("lire le JSON utilisateur depuis stdin: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return fmt.Errorf("stdin doit contenir un seul objet JSON")
	}
	if strings.TrimSpace(req.Email) == "" {
		return fmt.Errorf("email est obligatoire")
	}
	roleName := strings.TrimSpace(req.Role)
	if roleName == "" {
		roleName = string(permissions.Trader)
	}
	role, ok := permissions.NormalizeRole(roleName)
	if !ok {
		return fmt.Errorf("rôle invalide: utiliser TRADER, COMPLIANCE_OFFICER, SUPERVISOR ou ADMIN")
	}
	hash, err := traderauthservice.HashPassword(req.Password)
	if err != nil {
		return err
	}
	if err := storage.CheckDatabaseSecurity(); err != nil {
		return fmt.Errorf("database security check: %w", err)
	}
	store, err := storage.NewPostgresStore()
	if err != nil {
		return err
	}
	defer store.Close()
	repo := traderrepository.NewTraderRepository(store)
	if err := repo.Init(); err != nil {
		return fmt.Errorf("initialize trader repository: %w", err)
	}
	status := strings.TrimSpace(req.Status)
	if status == "" {
		status = "ACTIVE"
	}
	isActive := true
	if req.IsActive != nil {
		isActive = *req.IsActive
	}
	trader := &traderentity.Trader{
		Name: req.Name, FirstName: req.FirstName, LastName: req.LastName,
		Email: strings.TrimSpace(req.Email), Phone: req.Phone, Status: status,
		Roles: string(role), PasswordHash: hash, StoreId: req.StoreID, IsActive: isActive,
	}
	if err := repo.CreateTrader(trader); err != nil {
		return fmt.Errorf("create user: %w", err)
	}
	fmt.Printf("Utilisateur créé: id=%d email=%s rôle=%s\n", trader.Id, trader.Email, trader.Roles)
	return nil
}

func bootstrapAdmin(repo storage.TraderStorage) error {
	count, err := repo.CountActiveAdmins()
	if err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	email := strings.TrimSpace(os.Getenv("BOOTSTRAP_ADMIN_EMAIL"))
	password := os.Getenv("BOOTSTRAP_ADMIN_PASSWORD")
	if email == "" || password == "" {
		return fmt.Errorf("no active administrator exists; set BOOTSTRAP_ADMIN_EMAIL and BOOTSTRAP_ADMIN_PASSWORD")
	}
	hash, err := traderauthservice.HashPassword(password)
	if err != nil {
		return err
	}
	admin, lookupErr := repo.GetTraderByEmail(email)
	if lookupErr == nil {
		admin.Roles, admin.IsActive, admin.PasswordHash = string(permissions.Admin), true, hash
		return repo.UpdateTrader(admin)
	}
	if !errors.Is(lookupErr, storage.ErrTraderNotFound) {
		return lookupErr
	}
	return repo.CreateTrader(&traderentity.Trader{Email: email, Roles: string(permissions.Admin), IsActive: true, PasswordHash: hash, Status: "ACTIVE"})
}
