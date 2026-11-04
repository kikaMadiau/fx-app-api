// Package kycrisk regroupe ce qui fait respecter le KYC au guichet : les règles de blocage
// d'une opération, la fraîcheur du dossier, les secteurs sensibles, les signaux calculés en
// base (photos de la pièce, doublons) et les dérogations accordées par la conformité.
package kycrisk

import (
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
	"time"
)

// Codes renvoyés quand une opération est bloquée.
const (
	BlockIDRequired         = "KYC_ID_REQUIRED"
	BlockIDExpired          = "KYC_ID_EXPIRED"
	BlockValidationRequired = "KYC_VALIDATION_REQUIRED"
	BlockDailyLimit         = "DAILY_LIMIT_EXCEEDED"
)

// HomeCountry est le code pays (ISO 3166) de la RDC : nationalité et résidence de référence.
const HomeCountry = "CD"

// Durée de validité d'une dérogation accordée par la conformité (utilisable une seule fois).
const OverrideValidity = 24 * time.Hour

// Sectors liste les secteurs d'activité acceptés pour le profil KYC.
var Sectors = []string{
	"NONE", "PUBLIC_SECTOR", "PRIVATE_EMPLOYEE", "TRADE", "IMPORT_EXPORT", "MINING",
	"CURRENCY_EXCHANGE", "MOBILE_MONEY", "REAL_ESTATE", "TRANSPORT", "NGO", "OTHER",
}

// sensitiveSectors : activités où circule beaucoup d'argent liquide ou exposées au blanchiment.
var sensitiveSectors = map[string]bool{
	"MINING":            true,
	"CURRENCY_EXCHANGE": true,
	"MOBILE_MONEY":      true,
	"REAL_ESTATE":       true,
}

// ValidSector indique si le secteur fait partie de la liste.
func ValidSector(sector string) bool {
	for _, s := range Sectors {
		if s == sector {
			return true
		}
	}
	return false
}

// SensitiveSector indique si le secteur demande une vigilance particulière.
func SensitiveSector(sector string) bool { return sensitiveSectors[sector] }

// ReviewMonths donne la périodicité de revue du dossier selon le niveau de risque du client :
// 12 mois en risque élevé, 24 mois en risque moyen, 36 mois sinon.
func ReviewMonths(riskLevel string) int {
	switch strings.ToUpper(riskLevel) {
	case "HIGH", "CRITICAL":
		return 12
	case "MEDIUM":
		return 24
	default:
		return 36
	}
}

// ReviewDueDate renvoie la date à laquelle le dossier doit être revu (zéro s'il n'a jamais été vérifié).
func ReviewDueDate(lastVerification time.Time, riskLevel string) time.Time {
	if lastVerification.IsZero() {
		return time.Time{}
	}
	return lastVerification.AddDate(0, ReviewMonths(riskLevel), 0)
}

// Location renvoie le fuseau du bureau (APP_TIMEZONE, Kinshasa par défaut).
func Location() *time.Location {
	name := strings.TrimSpace(os.Getenv("APP_TIMEZONE"))
	if name == "" {
		name = "Africa/Kinshasa"
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return time.Local
	}
	return loc
}

// ValidDate vérifie le format AAAA-MM-JJ.
func ValidDate(value string) bool {
	_, err := time.Parse("2006-01-02", value)
	return err == nil
}

// IDExpired indique si la date d'expiration (AAAA-MM-JJ) est passée : la pièce reste valable
// le jour de son expiration.
func IDExpired(expiry string, now time.Time) bool {
	if !ValidDate(expiry) {
		return false
	}
	return expiry < now.In(Location()).Format("2006-01-02")
}

// USDEquivalent ramène le montant d'une opération en dollars. Les opérations se font contre
// des francs congolais : une devise autre que le dollar passe par sa contre-valeur en CDF.
func USDEquivalent(amount float64, currency string, convertedAmount float64, targetCurrency string) float64 {
	switch {
	case strings.EqualFold(currency, "USD"):
		return amount
	case strings.EqualFold(targetCurrency, "USD"):
		return convertedAmount
	}
	usdCDF := envFloat("EXCHANGE_RATE_USD_CDF_BUY", 2770)
	switch {
	case strings.EqualFold(targetCurrency, "CDF") && convertedAmount > 0:
		return convertedAmount / usdCDF
	case strings.EqualFold(currency, "CDF"):
		return amount / usdCDF
	}
	return amount
}

// IDRequiredAboveUSD : montant au-delà duquel une pièce d'identité est obligatoire.
func IDRequiredAboveUSD() float64 { return envFloat("KYC_ID_REQUIRED_ABOVE_USD", 1000) }

// ValidationAboveUSD : montant au-delà duquel un client à risque élevé doit être validé par la conformité.
func ValidationAboveUSD() float64 { return envFloat("KYC_VALIDATION_ABOVE_USD", 1000) }

// DailyLimitUSD : cumul journalier maximal d'un client (équivalent USD) ; 0 = pas de limite.
func DailyLimitUSD() float64 {
	if value, err := strconv.ParseFloat(strings.TrimSpace(os.Getenv("DAILY_LIMIT_USD")), 64); err == nil && value >= 0 {
		return value
	}
	return 5000
}

// GateInput rassemble ce qu'il faut savoir du client et de l'opération pour décider d'un blocage.
type GateInput struct {
	IDType       string
	IDExpiryDate string
	RiskLevel    string
	AmountUSD    float64
	// Opérations du client déjà faites aujourd'hui (équivalent USD), hors opération en cours.
	DayTotalUSD float64
}

// Block décrit une raison de refuser l'opération ; Message est affiché tel quel au guichet.
type Block struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Blocks renvoie les raisons de refuser l'opération : pas de pièce au-delà du seuil, pièce
// expirée, client à risque élevé au-delà du seuil de validation, limite journalière dépassée.
// Tous les autres facteurs de risque ne font qu'avertir.
func Blocks(in GateInput, now time.Time) []Block {
	var blocks []Block
	if strings.TrimSpace(in.IDType) == "" && in.AmountUSD > IDRequiredAboveUSD() {
		blocks = append(blocks, Block{
			Code: BlockIDRequired,
			Message: fmt.Sprintf("Le client n'a pas de pièce d'identité enregistrée : elle est obligatoire au-delà de %s USD.",
				formatUSD(IDRequiredAboveUSD())),
		})
	}
	if IDExpired(in.IDExpiryDate, now) {
		expiry, _ := time.Parse("2006-01-02", in.IDExpiryDate)
		blocks = append(blocks, Block{
			Code:    BlockIDExpired,
			Message: fmt.Sprintf("La pièce d'identité du client est expirée depuis le %s.", expiry.Format("02/01/2006")),
		})
	}
	if level := strings.ToUpper(in.RiskLevel); (level == "HIGH" || level == "CRITICAL") && in.AmountUSD > ValidationAboveUSD() {
		blocks = append(blocks, Block{
			Code: BlockValidationRequired,
			Message: fmt.Sprintf("Client à risque élevé : au-delà de %s USD, l'opération doit être validée par la conformité.",
				formatUSD(ValidationAboveUSD())),
		})
	}
	if limit := DailyLimitUSD(); limit > 0 && in.DayTotalUSD+in.AmountUSD > limit+0.005 {
		blocks = append(blocks, Block{
			Code: BlockDailyLimit,
			Message: fmt.Sprintf("Limite journalière de %s USD : le client a déjà changé %s USD aujourd'hui, cette opération porterait le cumul à %s USD.",
				formatUSD(limit), formatUSD(in.DayTotalUSD), formatUSD(in.DayTotalUSD+in.AmountUSD)),
		})
	}
	return blocks
}

// HasBlock indique si la liste contient le blocage donné.
func HasBlock(blocks []Block, code string) bool {
	for _, b := range blocks {
		if b.Code == code {
			return true
		}
	}
	return false
}

// BlockMessage assemble le message d'erreur renvoyé au guichet.
func BlockMessage(blocks []Block) string {
	parts := make([]string, 0, len(blocks))
	for _, b := range blocks {
		parts = append(parts, b.Message)
	}
	return "Opération refusée. " + strings.Join(parts, " ") +
		" Régularisez le dossier du client ou demandez une dérogation à la conformité."
}

func formatUSD(value float64) string {
	s := strconv.FormatFloat(math.Round(value), 'f', 0, 64)
	var out []byte
	for i := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			out = append(out, ' ')
		}
		out = append(out, s[i])
	}
	return string(out)
}

func envFloat(name string, fallback float64) float64 {
	if value, err := strconv.ParseFloat(strings.TrimSpace(os.Getenv(name)), 64); err == nil && value > 0 {
		return value
	}
	return fallback
}
