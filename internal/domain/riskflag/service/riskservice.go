package service

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	customerentity "fx-app-api/internal/domain/customer/entity"
	"fx-app-api/internal/domain/customer/kycrisk"
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
	// Signaux lus en base (photos de la pièce, doublons, opération précédente) ; nil s'ils sont indisponibles.
	Signals *kycrisk.Signals
}

// SignalSource fournit les signaux KYC calculés en base et les alertes encore ouvertes d'un client.
type SignalSource interface {
	CustomerSignals(tx *sql.Tx, customerID, excludeTransactionID int) (*kycrisk.Signals, error)
	OpenFlagNames(tx *sql.Tx, customerID int) (map[string]bool, error)
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
	// Margin : part sous le seuil considérée comme « juste en dessous » (règle de fractionnement).
	Margin float64
	// Alert vaut false pour une règle qui compte dans le score sans créer d'alerte.
	Alert bool
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
	Margin          *float64 `json:"margin,omitempty"`
	Alert           *bool    `json:"alert,omitempty"`
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
	// Règles qui comptent dans le score sans créer d'alerte (option "alert": false).
	silentRules map[string]bool
	signals     SignalSource

	// Service d'investigation des risk_flags
	Investigation *InvestigationService
}

// NewRiskService crée une nouvelle instance de RiskService.
func NewRiskService(
	customerRepo storage.CustomerStorage,
	transactionRepo storage.TransactionStorage,
	riskFlagRepo storage.RiskFlagStorage,
	customerService *customerservice.CustomerService,
) *RiskService {
	definitions := loadRuleDefinitions()
	transactionRules := buildTransactionRules(definitions.TransactionRules)
	customerRules := buildCustomerRules(definitions.CustomerRules)
	investigationService := NewInvestigationService(riskFlagRepo)

	return &RiskService{
		customerRepo:     customerRepo,
		transactionRepo:  transactionRepo,
		riskFlagRepo:     riskFlagRepo,
		customerService:  customerService,
		transactionRules: transactionRules,
		customerRules:    customerRules,
		silentRules:      silentRuleNames(definitions),
		Investigation:    investigationService,
	}
}

// SetSignalSource branche la lecture des signaux KYC (photos, doublons, alertes ouvertes).
func (s *RiskService) SetSignalSource(source SignalSource) {
	s.signals = source
}

func (s *RiskService) loadSignals(dbTx *sql.Tx, customerID, transactionID int) *kycrisk.Signals {
	if s.signals == nil {
		return nil
	}
	signals, err := s.signals.CustomerSignals(dbTx, customerID, transactionID)
	if err != nil {
		log.Printf("Warning: KYC signals unavailable for customer %d: %v", customerID, err)
		return nil
	}
	return signals
}

// CustomerFactors évalue les règles client sans rien enregistrer : ce sont les facteurs de
// risque KYC affichés sur le dossier du client, et le score qui en découle.
func (s *RiskService) CustomerFactors(dbTx *sql.Tx, customerID int) ([]*riskflagentity.RiskFlag, float64, error) {
	customer, err := s.customerRepo.GetCustomerWithTx(dbTx, customerID)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to get customer %d: %w", customerID, err)
	}
	var kycProfile *customerservice.CustomerKYCProfile
	if s.customerService != nil {
		if profile, err := s.customerService.GetKYCProfileWithTx(dbTx, customerID); err == nil {
			kycProfile = profile
		}
	}
	recent, err := s.transactionRepo.ListTransactionsWithTx(dbTx, storage.TransactionFilter{
		CustomerID: customerID,
		Since:      time.Now().AddDate(0, 0, -30),
	})
	if err != nil {
		recent = nil
	}
	result, flags := s.runCustomerRules(&RuleContext{
		Customer:           customer,
		KYCProfile:         kycProfile,
		RecentTransactions: recent,
		Signals:            s.loadSignals(dbTx, customerID, 0),
	})
	return flags, result.Score, nil
}

// ReassessCustomer recalcule le score et le niveau de risque du client à partir des règles
// client (pièce, dossier, vigilance) et les enregistre. Appelé quand le dossier change : le
// niveau enregistré décide d'une validation par la conformité au guichet.
func (s *RiskService) ReassessCustomer(dbTx *sql.Tx, customerID int) (*customerentity.Customer, error) {
	_, score, err := s.CustomerFactors(dbTx, customerID)
	if err != nil {
		return nil, err
	}
	customer, err := s.customerRepo.GetCustomerWithTx(dbTx, customerID)
	if err != nil {
		return nil, fmt.Errorf("failed to get customer %d: %w", customerID, err)
	}
	customer.RiskScore = score
	customer.RiskLevel = CustomerLevel(score)
	if err := s.customerRepo.UpdateCustomerWithTx(dbTx, customer); err != nil {
		return nil, fmt.Errorf("failed to update customer %d risk: %w", customerID, err)
	}
	return customer, nil
}

// CustomerLevel convertit un score client en niveau, avec les mêmes seuils que l'analyse des opérations.
func CustomerLevel(score float64) string {
	return calculateLevel(score, 90, 60, 30)
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
	// La création d'un nouveau client et de sa transaction n'est pas commitée à ce stade.
	// Le lire via la même transaction SQL est nécessaire pour voir cette nouvelle ligne.
	customer, err := s.customerRepo.GetCustomerWithTx(dbTx, tx.CustomerID)
	if err != nil {
		return fmt.Errorf("failed to get customer %d for risk analysis: %w", tx.CustomerID, err)
	}

	var kycProfile *customerservice.CustomerKYCProfile
	if s.customerService != nil {
		if profile, err := s.customerService.GetKYCProfileWithTx(dbTx, customer.ID); err == nil {
			kycProfile = profile
		} else {
			log.Printf("Warning: KYC profile unavailable for customer %d: %v", customer.ID, err)
		}
	}

	recentTransactions, err := s.transactionRepo.ListTransactionsWithTx(dbTx, storage.TransactionFilter{
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
		Signals:            s.loadSignals(dbTx, customer.ID, tx.ID),
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

	// Une alerte client (dossier incomplet, pièce expirée…) n'est pas recréée à chaque opération
	// tant que la précédente n'est pas traitée.
	openFlags := map[string]bool{}
	if s.signals != nil {
		if names, err := s.signals.OpenFlagNames(dbTx, customer.ID); err == nil {
			openFlags = names
		} else {
			log.Printf("Warning: open risk flags unavailable for customer %d: %v", customer.ID, err)
		}
	}
	alerts := make([]*riskflagentity.RiskFlag, 0, len(transactionFlags)+len(customerFlags))
	for _, flag := range transactionFlags {
		if !s.silentRules[flag.Flag] {
			alerts = append(alerts, flag)
		}
	}
	for _, flag := range customerFlags {
		if !s.silentRules[flag.Flag] && !openFlags[flag.Flag] {
			alerts = append(alerts, flag)
		}
	}

	for _, flag := range alerts {
		transactionID := tx.ID
		flag.TransactionId = &transactionID
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

// loadRuleDefinitions lit le catalogue de règles (RISK_RULES_CONFIG_PATH ou config/risk_rules.json),
// et revient au catalogue par défaut si le fichier est absent ou invalide.
func loadRuleDefinitions() riskRulesFile {
	configPath := os.Getenv("RISK_RULES_CONFIG_PATH")
	if configPath == "" {
		configPath = "config/risk_rules.json"
	}

	config, err := readRulesFile(configPath)
	if err == nil {
		return config
	}
	if !errors.Is(err, os.ErrNotExist) {
		log.Printf("Warning: failed to load risk rules from %s: %v. Falling back to defaults", configPath, err)
	}

	return defaultRiskRulesFile()
}

func readRulesFile(path string) (riskRulesFile, error) {
	var config riskRulesFile
	file, err := os.Open(path)
	if err != nil {
		return config, err
	}
	defer file.Close()

	if err := json.NewDecoder(file).Decode(&config); err != nil {
		return config, fmt.Errorf("decode risk rules config: %w", err)
	}
	if len(buildTransactionRules(config.TransactionRules)) == 0 && len(buildCustomerRules(config.CustomerRules)) == 0 {
		return config, errors.New("risk rules config does not define any enabled rule")
	}
	return config, nil
}

func loadRulesFromFile(path string) ([]Rule, []Rule, error) {
	config, err := readRulesFile(path)
	if err != nil {
		return nil, nil, err
	}
	return buildTransactionRules(config.TransactionRules), buildCustomerRules(config.CustomerRules), nil
}

// silentRuleNames liste les règles actives marquées "alert": false.
func silentRuleNames(config riskRulesFile) map[string]bool {
	names := map[string]bool{}
	for _, definition := range append(append([]ruleDefinition{}, config.TransactionRules...), config.CustomerRules...) {
		if ruleConfig, ok := ruleConfigFromDefinition(definition); ok && !ruleConfig.Alert {
			names[ruleConfig.Name] = true
		}
	}
	return names
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
		case "structuring":
			rules = append(rules, &StructuringRule{Config: config})
		case "round_trip":
			rules = append(rules, &RoundTripRule{Config: config})
		case "dormant_reactivation":
			rules = append(rules, &DormantReactivationRule{Config: config})
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
		case "missing_id_document":
			rules = append(rules, &MissingIDDocumentRule{Config: config})
		case "missing_id_photo":
			rules = append(rules, &MissingIDPhotoRule{Config: config})
		case "missing_phone":
			rules = append(rules, &MissingPhoneRule{Config: config})
		case "missing_address":
			rules = append(rules, &MissingAddressRule{Config: config})
		case "duplicate_id_number":
			rules = append(rules, &DuplicateIDNumberRule{Config: config})
		case "expired_id_document":
			rules = append(rules, &ExpiredIDDocumentRule{Config: config})
		case "politically_exposed":
			rules = append(rules, &PoliticallyExposedRule{Config: config})
		case "sensitive_sector":
			rules = append(rules, &SensitiveSectorRule{Config: config})
		case "third_party":
			rules = append(rules, &ThirdPartyRule{Config: config})
		case "foreign_or_non_resident":
			rules = append(rules, &ForeignOrNonResidentRule{Config: config})
		case "kyc_review_due":
			rules = append(rules, &KYCReviewDueRule{Config: config})
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
		Margin:  0.2,
		Alert:   definition.Alert == nil || *definition.Alert,
	}
	if definition.Margin != nil && *definition.Margin > 0 && *definition.Margin < 1 {
		config.Margin = *definition.Margin
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
				Window:          "72h",
			},
			{
				Type:           "transaction_frequency",
				Name:           "UNUSUAL_FREQUENCY_24H",
				Score:          25,
				Enabled:        boolPtr(true),
				ThresholdCount: intPtr(10),
				Window:         "24h",
			},
			{Type: "structuring", Name: "STRUCTURING_BELOW_THRESHOLD", Score: 40, Enabled: boolPtr(true), ThresholdAmount: float64Ptr(10000.0), ThresholdCount: intPtr(3), Margin: float64Ptr(0.2), Window: "24h"},
			{Type: "round_trip", Name: "ROUND_TRIP_EXCHANGE", Score: 30, Enabled: boolPtr(true), Window: "48h"},
			{Type: "dormant_reactivation", Name: "DORMANT_REACTIVATION", Score: 20, Enabled: boolPtr(true), ThresholdAmount: float64Ptr(5000.0), Window: "4320h"},
		},
		CustomerRules: []ruleDefinition{
			{Type: "insufficient_kyc_status", Name: "INSUFFICIENT_KYC_STATUS", Score: 45, Enabled: boolPtr(true)},
			{Type: "inconsistent_profile", Name: "INCONSISTENT_KYC_PROFILE", Score: 35, Enabled: boolPtr(true)},
			{Type: "business_activity", Name: "BUSINESS_ACTIVITY_PROFILE", Score: 15, Enabled: boolPtr(true)},
			{Type: "expected_volume_exceeded", Name: "EXPECTED_VOLUME_EXCEEDED", Score: 30, Enabled: boolPtr(true)},
			{Type: "missing_id_document", Name: "MISSING_ID_DOCUMENT", Score: 40, Enabled: boolPtr(true)},
			{Type: "missing_id_photo", Name: "MISSING_ID_PHOTO", Score: 15, Enabled: boolPtr(true), Alert: boolPtr(false)},
			{Type: "missing_phone", Name: "MISSING_PHONE", Score: 20, Enabled: boolPtr(true)},
			{Type: "missing_address", Name: "MISSING_ADDRESS", Score: 5, Enabled: boolPtr(true), Alert: boolPtr(false)},
			{Type: "duplicate_id_number", Name: "DUPLICATE_ID_NUMBER", Score: 35, Enabled: boolPtr(true)},
			{Type: "expired_id_document", Name: "EXPIRED_ID_DOCUMENT", Score: 45, Enabled: boolPtr(true)},
			{Type: "politically_exposed", Name: "POLITICALLY_EXPOSED_PERSON", Score: 35, Enabled: boolPtr(true)},
			{Type: "sensitive_sector", Name: "SENSITIVE_SECTOR", Score: 25, Enabled: boolPtr(true)},
			{Type: "third_party", Name: "ACTS_FOR_THIRD_PARTY", Score: 30, Enabled: boolPtr(true)},
			{Type: "foreign_or_non_resident", Name: "FOREIGN_OR_NON_RESIDENT", Score: 15, Enabled: boolPtr(true), Alert: boolPtr(false)},
			{Type: "kyc_review_due", Name: "KYC_REVIEW_DUE", Score: 20, Enabled: boolPtr(true), Alert: boolPtr(false)},
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
