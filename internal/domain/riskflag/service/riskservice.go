package service

import (
	"fmt"
	customerentity "fx-app-api/internal/domain/customer/entity"
	customerservice "fx-app-api/internal/domain/customer/service"
	riskflagentity "fx-app-api/internal/domain/riskflag/entity"
	transactionentity "fx-app-api/internal/domain/transaction/entity"
	"fx-app-api/internal/storage"
	"log"
	"time"
)

// --- Moteur de Règles ---

// RuleContext contient toutes les données nécessaires pour évaluer une règle.
type RuleContext struct {
	Transaction        *transactionentity.Transaction
	Customer           *customerentity.Customer
	KYCProfile         *customerservice.CustomerKYCProfile
	RecentTransactions []*transactionentity.Transaction
}

// Rule est l'interface pour toutes les règles de risque.
type Rule interface {
	Evaluate(ctx *RuleContext) *riskflagentity.RiskFlag
	Name() string
}

// RuleConfig contient la configuration d'une règle (score, seuils, etc.).
type RuleConfig struct {
	Name            string
	Score           float64
	Enabled         bool
	ThresholdAmount float64
	ThresholdCount  int
	Window          time.Duration
}

// RiskService orchestre l'analyse de risque.
type RiskService struct {
	customerRepo    storage.CustomerStorage
	customerService *customerservice.CustomerService
	transactionRepo storage.TransactionStorage
	riskFlagRepo    storage.RiskFlagStorage

	// Moteur de règles
	transactionRules []Rule
	customerRules    []Rule
}

// NewRiskService crée une nouvelle instance de RiskService.
func NewRiskService(
	customerRepo storage.CustomerStorage,
	transactionRepo storage.TransactionStorage,
	riskFlagRepo storage.RiskFlagStorage,
	customerService *customerservice.CustomerService,
) *RiskService {
	// TODO: Charger les règles et leur configuration depuis un fichier ou une DB.
	// Pour l'instant, on les initialise en dur.
	transactionRules := loadTransactionRules()
	customerRules := loadCustomerRules()

	return &RiskService{
		customerRepo:     customerRepo,
		transactionRepo:  transactionRepo,
		riskFlagRepo:     riskFlagRepo,
		customerService:  customerService,
		transactionRules: transactionRules,
		customerRules:    customerRules,
	}
}

// AnalyzeTransactionAndCustomer est la méthode principale appelée après la création d'une transaction.
// Elle analyse la transaction, puis met à jour le score de risque du client.
func (s *RiskService) AnalyzeTransactionAndCustomer(tx *transactionentity.Transaction) error {
	// 1. Analyse de la transaction individuelle
	customer, err := s.customerRepo.GetCustomer(tx.CustomerID)
	if err != nil {
		return fmt.Errorf("failed to get customer %d for risk analysis: %w", tx.CustomerID, err)
	}

	var kycProfile *customerservice.CustomerKYCProfile
	if s.customerService != nil {
		if profile, err := s.customerService.GetKYCProfile(customer.ID); err == nil {
			kycProfile = profile
		} else {
			log.Printf("Warning: KYC profile unavailable for customer %d: %v", customer.ID, err)
		}
	}

	recentTransactions, err := s.transactionRepo.ListTransactions(storage.TransactionFilter{
		CustomerID: customer.ID,
		Since:      time.Now().AddDate(0, 0, -30),
	})
	if err != nil {
		log.Printf("Warning: recent transactions unavailable for customer %d: %v", customer.ID, err)
		recentTransactions = []*transactionentity.Transaction{tx}
	}

	ctx := &RuleContext{
		Transaction:        tx,
		Customer:           customer,
		KYCProfile:         kycProfile,
		RecentTransactions: recentTransactions,
	}

	transactionRisk, transactionFlags := s.runTransactionRules(ctx)

	// Sauvegarde des résultats de l'analyse de transaction
	tx.RiskLevel = transactionRisk.Level
	tx.RiskScore = transactionRisk.Score
	if err := s.transactionRepo.UpdateTransaction(tx); err != nil {
		return fmt.Errorf("failed to update transaction with risk scores: %w", err)
	}

	// Création des RiskFlags associés à la transaction
	// 3. Analyse comportementale et KYC du client
	customerRisk, customerFlags := s.runCustomerRules(ctx)

	for _, flag := range append(transactionFlags, customerFlags...) {
		flag.TransactionId = tx.ID
		flag.TraderId = tx.TraderID
		flag.CustomerId = tx.CustomerID
		if err := s.riskFlagRepo.CreateRiskFlag(flag); err != nil {
			// Logguer l'erreur mais ne pas bloquer le flux principal
			log.Printf("Warning: failed to create risk flag for transaction %d: %v", tx.ID, err)
		}
	}

	// Sauvegarde du score de risque global du client
	customer.RiskScore = transactionRisk.Score + customerRisk.Score
	customer.RiskLevel = calculateLevel(customer.RiskScore, 90, 60, 30)
	if err := s.customerRepo.UpdateCustomer(customer); err != nil {
		return fmt.Errorf("failed to update customer %d with risk scores: %w", customer.ID, err)
	}

	log.Printf("Customer %d risk updated to %s (Score: %.2f). Flags: %d", customer.ID, customer.RiskLevel, customer.RiskScore, len(transactionFlags)+len(customerFlags))

	return nil
}

// RiskResult contient le score et le niveau de risque.
type RiskResult struct {
	Score float64
	Level string // LOW, MEDIUM, HIGH, CRITICAL
}

// runTransactionRules exécute toutes les règles transactionnelles.
func (s *RiskService) runTransactionRules(ctx *RuleContext) (RiskResult, []*riskflagentity.RiskFlag) {
	var score float64
	var flags []*riskflagentity.RiskFlag

	for _, rule := range s.transactionRules {
		if flag := rule.Evaluate(ctx); flag != nil {
			flags = append(flags, flag)
			score += flag.Score
		}
	}

	// Détermination du niveau de risque de la transaction
	// TODO: Utiliser des seuils configurables
	level := calculateLevel(score, 75, 50, 25)
	return RiskResult{Score: score, Level: level}, flags
}

// runCustomerRules implémente la logique d'analyse comportementale pour un client.
func (s *RiskService) runCustomerRules(ctx *RuleContext) (RiskResult, []*riskflagentity.RiskFlag) {
	var score float64
	var flags []*riskflagentity.RiskFlag

	for _, rule := range s.customerRules {
		if flag := rule.Evaluate(ctx); flag != nil {
			flags = append(flags, flag)
			score += flag.Score
		}
	}

	// Détermination du niveau de risque du client
	level := calculateLevel(score, 70, 40, 20) // Adjusted medium threshold
	return RiskResult{Score: score, Level: level}, flags
}

func calculateLevel(score, critical, high, medium float64) string {
	switch {
	case score >= critical:
		return "CRITICAL"
	case score >= high:
		return "HIGH"
	case score >= medium:
		return "MEDIUM"
	default:
		return "LOW"
	}
}

// --- Implémentation des Règles ---

// HighAmountRule vérifie si le montant de la transaction dépasse un seuil.
type HighAmountRule struct {
	Config RuleConfig
}

func (r *HighAmountRule) Name() string { return r.Config.Name }

func (r *HighAmountRule) Evaluate(ctx *RuleContext) *riskflagentity.RiskFlag {
	if ctx.Transaction.Amount > r.Config.ThresholdAmount {
		return &riskflagentity.RiskFlag{
			Flag:   r.Name(),
			Reason: fmt.Sprintf("Transaction amount %.2f exceeds threshold %.2f", ctx.Transaction.Amount, r.Config.ThresholdAmount),
			Score:  r.Config.Score,
		}
	}
	return nil
}

// CumulativeVolumeRule vérifie le cumul des transactions sur une fenêtre donnée.
type CumulativeVolumeRule struct {
	Config RuleConfig
}

func (r *CumulativeVolumeRule) Name() string { return r.Config.Name }

func (r *CumulativeVolumeRule) Evaluate(ctx *RuleContext) *riskflagentity.RiskFlag {
	total := sumTransactionsSince(ctx.RecentTransactions, time.Now().Add(-r.Config.Window))
	if total > r.Config.ThresholdAmount {
		return &riskflagentity.RiskFlag{
			Flag:   r.Name(),
			Reason: fmt.Sprintf("Cumulative amount %.2f exceeds threshold %.2f over %s", total, r.Config.ThresholdAmount, r.Config.Window),
			Score:  r.Config.Score,
		}
	}
	return nil
}

// TransactionFrequencyRule vérifie une fréquence de transactions inhabituelle.
type TransactionFrequencyRule struct {
	Config RuleConfig
}

func (r *TransactionFrequencyRule) Name() string { return r.Config.Name }

func (r *TransactionFrequencyRule) Evaluate(ctx *RuleContext) *riskflagentity.RiskFlag {
	count := countTransactionsSince(ctx.RecentTransactions, time.Now().Add(-r.Config.Window))
	if count > r.Config.ThresholdCount {
		return &riskflagentity.RiskFlag{
			Flag:   r.Name(),
			Reason: fmt.Sprintf("Transaction count %d exceeds threshold %d over %s", count, r.Config.ThresholdCount, r.Config.Window),
			Score:  r.Config.Score,
		}
	}
	return nil
}

// InconsistentProfileRule vérifie les incohérences évidentes dans le profil KYC.
type InconsistentProfileRule struct {
	Config RuleConfig
}

func (r *InconsistentProfileRule) Name() string { return r.Config.Name }

func (r *InconsistentProfileRule) Evaluate(ctx *RuleContext) *riskflagentity.RiskFlag {
	profile := ctx.KYCProfile
	if profile == nil {
		return &riskflagentity.RiskFlag{
			Flag:   r.Name(),
			Reason: "Customer has no KYC profile",
			Score:  r.Config.Score,
		}
	}

	switch {
	case profile.LegalNature == customerservice.LegalEntity && profile.ActivityProfile == customerservice.Personal:
		return &riskflagentity.RiskFlag{Flag: r.Name(), Reason: "Legal entity cannot have PERSONAL activity profile", Score: r.Config.Score}
	case profile.LegalNature == customerservice.LegalEntity && (profile.CompanyName == "" || profile.RegistrationNumber == ""):
		return &riskflagentity.RiskFlag{Flag: r.Name(), Reason: "Legal entity KYC is missing company name or registration number", Score: r.Config.Score}
	case profile.LegalNature == customerservice.Individual && profile.Profession == "" && profile.ActivityProfile != customerservice.Personal:
		return &riskflagentity.RiskFlag{Flag: r.Name(), Reason: "Individual professional/business profile is missing profession", Score: r.Config.Score}
	case profile.SourceOfFunds == "" || profile.PurposeOfOperations == "":
		return &riskflagentity.RiskFlag{Flag: r.Name(), Reason: "KYC profile is missing source of funds or purpose of operations", Score: r.Config.Score}
	}

	return nil
}

// InsufficientKYCStatusRule vérifie que le niveau KYC permet une activité transactionnelle.
type InsufficientKYCStatusRule struct {
	Config RuleConfig
}

func (r *InsufficientKYCStatusRule) Name() string { return r.Config.Name }

func (r *InsufficientKYCStatusRule) Evaluate(ctx *RuleContext) *riskflagentity.RiskFlag {
	if ctx.KYCProfile == nil {
		return &riskflagentity.RiskFlag{Flag: r.Name(), Reason: "Customer has no KYC profile", Score: r.Config.Score}
	}

	status := ctx.KYCProfile.KYCStatus
	if status != customerservice.Verified && status != customerservice.EnhancedVerification {
		return &riskflagentity.RiskFlag{
			Flag:   r.Name(),
			Reason: fmt.Sprintf("KYC status %s is insufficient for transaction", status),
			Score:  r.Config.Score,
		}
	}

	return nil
}

// BusinessActivityRule applique un risque additionnel aux profils business.
type BusinessActivityRule struct {
	Config RuleConfig
}

func (r *BusinessActivityRule) Name() string { return r.Config.Name }

func (r *BusinessActivityRule) Evaluate(ctx *RuleContext) *riskflagentity.RiskFlag {
	if ctx.KYCProfile != nil && ctx.KYCProfile.ActivityProfile == customerservice.Business {
		return &riskflagentity.RiskFlag{
			Flag:   r.Name(),
			Reason: "Customer activity profile is BUSINESS",
			Score:  r.Config.Score,
		}
	}
	return nil
}

// ExpectedVolumeExceededRule vérifie le dépassement du volume mensuel déclaré au KYC.
type ExpectedVolumeExceededRule struct {
	Config RuleConfig
}

func (r *ExpectedVolumeExceededRule) Name() string { return r.Config.Name }

func (r *ExpectedVolumeExceededRule) Evaluate(ctx *RuleContext) *riskflagentity.RiskFlag {
	if ctx.KYCProfile == nil || ctx.KYCProfile.ExpectedVolume <= 0 {
		return nil
	}

	total30d := sumTransactionsSince(ctx.RecentTransactions, time.Now().AddDate(0, 0, -30))
	if total30d > ctx.KYCProfile.ExpectedVolume {
		return &riskflagentity.RiskFlag{
			Flag:   r.Name(),
			Reason: fmt.Sprintf("30-day amount %.2f exceeds expected monthly volume %.2f", total30d, ctx.KYCProfile.ExpectedVolume),
			Score:  r.Config.Score,
		}
	}

	return nil
}

// loadTransactionRules charge la configuration des règles.
// Dans une vraie application, cela viendrait d'un fichier de config (YAML, JSON) ou d'une DB.
func loadTransactionRules() []Rule {
	var rules []Rule

	rules = append(rules, &HighAmountRule{
		Config: RuleConfig{
			Name:            "HIGH_TRANSACTION_AMOUNT",
			Score:           30,
			Enabled:         true,
			ThresholdAmount: 10000.0,
		},
	})
	rules = append(rules, &CumulativeVolumeRule{
		Config: RuleConfig{
			Name:            "CUMULATIVE_VOLUME_24H",
			Score:           35,
			Enabled:         true,
			ThresholdAmount: 20000.0,
			Window:          24 * time.Hour,
		},
	})
	rules = append(rules, &CumulativeVolumeRule{
		Config: RuleConfig{
			Name:            "CUMULATIVE_VOLUME_30D",
			Score:           40,
			Enabled:         true,
			ThresholdAmount: 100000.0,
			Window:          30 * 24 * time.Hour,
		},
	})
	rules = append(rules, &TransactionFrequencyRule{
		Config: RuleConfig{
			Name:           "UNUSUAL_FREQUENCY_24H",
			Score:          25,
			Enabled:        true,
			ThresholdCount: 10,
			Window:         24 * time.Hour,
		},
	})

	return rules
}

func loadCustomerRules() []Rule {
	return []Rule{
		&InsufficientKYCStatusRule{Config: RuleConfig{Name: "INSUFFICIENT_KYC_STATUS", Score: 45, Enabled: true}},
		&InconsistentProfileRule{Config: RuleConfig{Name: "INCONSISTENT_KYC_PROFILE", Score: 35, Enabled: true}},
		&BusinessActivityRule{Config: RuleConfig{Name: "BUSINESS_ACTIVITY_PROFILE", Score: 15, Enabled: true}},
		&ExpectedVolumeExceededRule{Config: RuleConfig{Name: "EXPECTED_VOLUME_EXCEEDED", Score: 30, Enabled: true}},
	}
}

func sumTransactionsSince(transactions []*transactionentity.Transaction, since time.Time) float64 {
	var total float64
	for _, tx := range transactions {
		if tx.CreatedAt.Before(since) {
			continue
		}
		total += tx.Amount
	}

	return total
}

func countTransactionsSince(transactions []*transactionentity.Transaction, since time.Time) int {
	var count int
	for _, tx := range transactions {
		if tx.CreatedAt.Before(since) {
			continue
		}
		count++
	}

	return count
}
