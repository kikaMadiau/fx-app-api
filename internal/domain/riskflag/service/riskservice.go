package service

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	customerentity "fx-app-api/internal/domain/customer/entity"
	customerservice "fx-app-api/internal/domain/customer/service"
	riskflagentity "fx-app-api/internal/domain/riskflag/entity"
	transactionentity "fx-app-api/internal/domain/transaction/entity"
	"fx-app-api/internal/storage"
	"log"
	"os"
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

type riskRulesFile struct {
	TransactionRules []ruleDefinition `json:"transaction_rules"`
	CustomerRules    []ruleDefinition `json:"customer_rules"`
}

type ruleDefinition struct {
	Type            string   `json:"type"`
	Name            string   `json:"name"`
	Score           float64  `json:"score"`
	Enabled         *bool    `json:"enabled,omitempty"`
	ThresholdAmount *float64 `json:"threshold_amount,omitempty"`
	ThresholdCount  *int     `json:"threshold_count,omitempty"`
	Window          string   `json:"window,omitempty"`
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
	transactionRules, customerRules := loadRules()

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
// Si une transaction SQL est fournie, elle l'utilise pour toutes les opérations de mise à jour.
func (s *RiskService) AnalyzeTransactionAndCustomer(tx *transactionentity.Transaction) error {
	return s.AnalyzeTransactionAndCustomerWithTx(nil, tx)
}

// AnalyzeTransactionAndCustomerWithTx analyse la transaction avec une transaction SQL fournie.
func (s *RiskService) AnalyzeTransactionAndCustomerWithTx(dbTx *sql.Tx, tx *transactionentity.Transaction) error {
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
	if err := s.transactionRepo.UpdateTransactionWithTx(dbTx, tx); err != nil {
		return fmt.Errorf("failed to update transaction with risk scores: %w", err)
	}

	// Création des RiskFlags associés à la transaction
	// 3. Analyse comportementale et KYC du client
	customerRisk, customerFlags := s.runCustomerRules(ctx)

	for _, flag := range append(transactionFlags, customerFlags...) {
		flag.TransactionId = tx.ID
		flag.TraderId = tx.TraderID
		flag.CustomerId = tx.CustomerID
		if err := s.riskFlagRepo.CreateRiskFlagWithTx(dbTx, flag); err != nil {
			// Logguer l'erreur mais ne pas bloquer le flux principal
			log.Printf("Warning: failed to create risk flag for transaction %d: %v", tx.ID, err)
		}
	}

	// Sauvegarde du score de risque global du client
	customer.RiskScore = transactionRisk.Score + customerRisk.Score
	customer.RiskLevel = calculateLevel(customer.RiskScore, 90, 60, 30)
	if err := s.customerRepo.UpdateCustomerWithTx(dbTx, customer); err != nil {
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

func loadRules() ([]Rule, []Rule) {
	configPath := os.Getenv("RISK_RULES_CONFIG_PATH")
	if configPath == "" {
		configPath = "config/risk_rules.json"
	}

	transactionRules, customerRules, err := loadRulesFromFile(configPath)
	if err == nil {
		return transactionRules, customerRules
	}
	if !errors.Is(err, os.ErrNotExist) {
		log.Printf("Warning: failed to load risk rules from %s: %v. Falling back to defaults", configPath, err)
	}

	defaults := defaultRiskRulesFile()
	return buildTransactionRules(defaults.TransactionRules), buildCustomerRules(defaults.CustomerRules)
}

func loadRulesFromFile(path string) ([]Rule, []Rule, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	defer file.Close()

	var config riskRulesFile
	if err := json.NewDecoder(file).Decode(&config); err != nil {
		return nil, nil, fmt.Errorf("decode risk rules config: %w", err)
	}

	transactionRules := buildTransactionRules(config.TransactionRules)
	customerRules := buildCustomerRules(config.CustomerRules)
	if len(transactionRules) == 0 && len(customerRules) == 0 {
		return nil, nil, errors.New("risk rules config does not define any enabled rule")
	}

	return transactionRules, customerRules, nil
}

func buildTransactionRules(definitions []ruleDefinition) []Rule {
	var rules []Rule
	for _, definition := range definitions {
		config, ok := ruleConfigFromDefinition(definition)
		if !ok {
			continue
		}

		switch definition.Type {
		case "high_amount":
			rules = append(rules, &HighAmountRule{Config: config})
		case "cumulative_volume":
			rules = append(rules, &CumulativeVolumeRule{Config: config})
		case "transaction_frequency":
			rules = append(rules, &TransactionFrequencyRule{Config: config})
		default:
			log.Printf("Warning: unknown transaction risk rule type %q", definition.Type)
		}
	}

	return rules
}

func buildCustomerRules(definitions []ruleDefinition) []Rule {
	var rules []Rule
	for _, definition := range definitions {
		config, ok := ruleConfigFromDefinition(definition)
		if !ok {
			continue
		}

		switch definition.Type {
		case "insufficient_kyc_status":
			rules = append(rules, &InsufficientKYCStatusRule{Config: config})
		case "inconsistent_profile":
			rules = append(rules, &InconsistentProfileRule{Config: config})
		case "business_activity":
			rules = append(rules, &BusinessActivityRule{Config: config})
		case "expected_volume_exceeded":
			rules = append(rules, &ExpectedVolumeExceededRule{Config: config})
		default:
			log.Printf("Warning: unknown customer risk rule type %q", definition.Type)
		}
	}

	return rules
}

func ruleConfigFromDefinition(definition ruleDefinition) (RuleConfig, bool) {
	enabled := true
	if definition.Enabled != nil {
		enabled = *definition.Enabled
	}
	if !enabled {
		return RuleConfig{}, false
	}

	config := RuleConfig{
		Name:    definition.Name,
		Score:   definition.Score,
		Enabled: enabled,
	}
	if definition.ThresholdAmount != nil {
		config.ThresholdAmount = *definition.ThresholdAmount
	}
	if definition.ThresholdCount != nil {
		config.ThresholdCount = *definition.ThresholdCount
	}
	if definition.Window != "" {
		window, err := time.ParseDuration(definition.Window)
		if err != nil {
			log.Printf("Warning: invalid window %q for risk rule %q: %v", definition.Window, definition.Name, err)
			return RuleConfig{}, false
		}
		config.Window = window
	}

	return config, true
}

func defaultRiskRulesFile() riskRulesFile {
	return riskRulesFile{
		TransactionRules: []ruleDefinition{
			{
				Type:            "high_amount",
				Name:            "HIGH_TRANSACTION_AMOUNT",
				Score:           30,
				Enabled:         boolPtr(true),
				ThresholdAmount: float64Ptr(10000.0),
			},
			{
				Type:            "cumulative_volume",
				Name:            "CUMULATIVE_VOLUME_24H",
				Score:           35,
				Enabled:         boolPtr(true),
				ThresholdAmount: float64Ptr(20000.0),
				Window:          "24h",
			},
			{
				Type:            "cumulative_volume",
				Name:            "CUMULATIVE_VOLUME_30D",
				Score:           40,
				Enabled:         boolPtr(true),
				ThresholdAmount: float64Ptr(100000.0),
				Window:          "720h",
			},
			{
				Type:           "transaction_frequency",
				Name:           "UNUSUAL_FREQUENCY_24H",
				Score:          25,
				Enabled:        boolPtr(true),
				ThresholdCount: intPtr(10),
				Window:         "24h",
			},
		},
		CustomerRules: []ruleDefinition{
			{Type: "insufficient_kyc_status", Name: "INSUFFICIENT_KYC_STATUS", Score: 45, Enabled: boolPtr(true)},
			{Type: "inconsistent_profile", Name: "INCONSISTENT_KYC_PROFILE", Score: 35, Enabled: boolPtr(true)},
			{Type: "business_activity", Name: "BUSINESS_ACTIVITY_PROFILE", Score: 15, Enabled: boolPtr(true)},
			{Type: "expected_volume_exceeded", Name: "EXPECTED_VOLUME_EXCEEDED", Score: 30, Enabled: boolPtr(true)},
		},
	}
}

func boolPtr(value bool) *bool {
	return &value
}

func float64Ptr(value float64) *float64 {
	return &value
}

func intPtr(value int) *int {
	return &value
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
