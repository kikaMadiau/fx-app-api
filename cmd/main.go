package main

import (
	"fmt"
	"fx-app-api/config"
	"fx-app-api/internal/api"
	customerrepository "fx-app-api/internal/domain/customer/repository"
	customerservice "fx-app-api/internal/domain/customer/service"
	riskflagrepository "fx-app-api/internal/domain/riskflag/repository"
	riskservice "fx-app-api/internal/domain/riskflag/service"
	traderrepository "fx-app-api/internal/domain/trader/repository"
	transactionrepository "fx-app-api/internal/domain/transaction/repository"
	"fx-app-api/internal/storage"
	"log"
)

func main() {
	fmt.Println("Hello congo trader")

	// Charger les variables d'environnement depuis .env
	if err := config.LoadEnv(".env"); err != nil {
		log.Printf("Warning: could not load .env file: %v", err)
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
	server := api.NewServer(":8080",
		traderRepo,
		customerRepo,
		transactionRepo,
		riskFlagRepo,
		riskService,
		customerService,
	)

	log.Fatal(server.Start())
}
