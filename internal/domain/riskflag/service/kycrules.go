package service

import (
	"fmt"
	"strings"
	"time"

	"fx-app-api/internal/domain/customer/kycrisk"
	customerservice "fx-app-api/internal/domain/customer/service"
	riskflagentity "fx-app-api/internal/domain/riskflag/entity"
)

// --- Règles client : identité, joignabilité, profil, fraîcheur du dossier ---

func newFlag(config RuleConfig, reason string) *riskflagentity.RiskFlag {
	return &riskflagentity.RiskFlag{Flag: config.Name, Reason: reason, Score: config.Score}
}

// MissingIDDocumentRule : aucune pièce d'identité enregistrée.
type MissingIDDocumentRule struct{ Config RuleConfig }

func (r *MissingIDDocumentRule) Name() string { return r.Config.Name }

func (r *MissingIDDocumentRule) Evaluate(ctx *RuleContext) *riskflagentity.RiskFlag {
	if ctx.Customer != nil && strings.TrimSpace(ctx.Customer.IDType) == "" {
		return newFlag(r.Config, "Customer has no identity document on file")
	}
	return nil
}

// MissingIDPhotoRule : recto ou verso de la pièce non photographié.
type MissingIDPhotoRule struct{ Config RuleConfig }

func (r *MissingIDPhotoRule) Name() string { return r.Config.Name }

func (r *MissingIDPhotoRule) Evaluate(ctx *RuleContext) *riskflagentity.RiskFlag {
	if ctx.Signals == nil || (ctx.Signals.HasIDFront && ctx.Signals.HasIDBack) {
		return nil
	}
	missing := "front and back"
	switch {
	case ctx.Signals.HasIDFront:
		missing = "back"
	case ctx.Signals.HasIDBack:
		missing = "front"
	}
	return newFlag(r.Config, fmt.Sprintf("Identity document photo missing (%s)", missing))
}

// MissingPhoneRule : client injoignable par téléphone.
type MissingPhoneRule struct{ Config RuleConfig }

func (r *MissingPhoneRule) Name() string { return r.Config.Name }

func (r *MissingPhoneRule) Evaluate(ctx *RuleContext) *riskflagentity.RiskFlag {
	if ctx.Customer != nil && strings.TrimSpace(ctx.Customer.Phone) == "" {
		return newFlag(r.Config, "Customer has no phone number")
	}
	return nil
}

// MissingAddressRule : adresse non renseignée.
type MissingAddressRule struct{ Config RuleConfig }

func (r *MissingAddressRule) Name() string { return r.Config.Name }

func (r *MissingAddressRule) Evaluate(ctx *RuleContext) *riskflagentity.RiskFlag {
	if ctx.Customer != nil && strings.TrimSpace(ctx.Customer.Address) == "" {
		return newFlag(r.Config, "Customer has no address")
	}
	return nil
}

// DuplicateIDNumberRule : la même pièce est enregistrée sur un autre client (doublon ou fraude).
type DuplicateIDNumberRule struct{ Config RuleConfig }

func (r *DuplicateIDNumberRule) Name() string { return r.Config.Name }

func (r *DuplicateIDNumberRule) Evaluate(ctx *RuleContext) *riskflagentity.RiskFlag {
	if ctx.Signals != nil && ctx.Signals.DuplicateIDCount > 0 {
		return newFlag(r.Config, fmt.Sprintf("Identity document number shared with %d other customer(s)", ctx.Signals.DuplicateIDCount))
	}
	return nil
}

// ExpiredIDDocumentRule : pièce d'identité expirée.
type ExpiredIDDocumentRule struct{ Config RuleConfig }

func (r *ExpiredIDDocumentRule) Name() string { return r.Config.Name }

func (r *ExpiredIDDocumentRule) Evaluate(ctx *RuleContext) *riskflagentity.RiskFlag {
	if ctx.KYCProfile != nil && kycrisk.IDExpired(ctx.KYCProfile.IDExpiryDate, time.Now()) {
		return newFlag(r.Config, fmt.Sprintf("Identity document expired on %s", ctx.KYCProfile.IDExpiryDate))
	}
	return nil
}

// PoliticallyExposedRule : personne politiquement exposée (ou proche).
type PoliticallyExposedRule struct{ Config RuleConfig }

func (r *PoliticallyExposedRule) Name() string { return r.Config.Name }

func (r *PoliticallyExposedRule) Evaluate(ctx *RuleContext) *riskflagentity.RiskFlag {
	if ctx.KYCProfile == nil || !ctx.KYCProfile.IsPEP {
		return nil
	}
	reason := "Customer is a politically exposed person"
	if ctx.KYCProfile.PEPFunction != "" {
		reason += fmt.Sprintf(" (%s)", ctx.KYCProfile.PEPFunction)
	}
	return newFlag(r.Config, reason)
}

// SensitiveSectorRule : activité exposée (minerais, change, mobile money, immobilier).
type SensitiveSectorRule struct{ Config RuleConfig }

func (r *SensitiveSectorRule) Name() string { return r.Config.Name }

func (r *SensitiveSectorRule) Evaluate(ctx *RuleContext) *riskflagentity.RiskFlag {
	if ctx.KYCProfile != nil && kycrisk.SensitiveSector(ctx.KYCProfile.ActivitySector) {
		return newFlag(r.Config, fmt.Sprintf("Customer works in a sensitive sector: %s", ctx.KYCProfile.ActivitySector))
	}
	return nil
}

// ThirdPartyRule : le client opère pour le compte d'un tiers.
type ThirdPartyRule struct{ Config RuleConfig }

func (r *ThirdPartyRule) Name() string { return r.Config.Name }

func (r *ThirdPartyRule) Evaluate(ctx *RuleContext) *riskflagentity.RiskFlag {
	if ctx.KYCProfile != nil && ctx.KYCProfile.ActsForThirdParty {
		return newFlag(r.Config, fmt.Sprintf("Customer acts on behalf of a third party: %s", ctx.KYCProfile.ThirdPartyName))
	}
	return nil
}

// ForeignOrNonResidentRule : client étranger ou ne résidant pas en RDC.
type ForeignOrNonResidentRule struct{ Config RuleConfig }

func (r *ForeignOrNonResidentRule) Name() string { return r.Config.Name }

func (r *ForeignOrNonResidentRule) Evaluate(ctx *RuleContext) *riskflagentity.RiskFlag {
	if ctx.KYCProfile == nil {
		return nil
	}
	var parts []string
	if n := ctx.KYCProfile.Nationality; n != "" && n != kycrisk.HomeCountry {
		parts = append(parts, fmt.Sprintf("foreign national (%s)", n))
	}
	if c := ctx.KYCProfile.ResidenceCountry; c != "" && c != kycrisk.HomeCountry {
		parts = append(parts, fmt.Sprintf("not resident in the DRC (%s)", c))
	}
	if len(parts) == 0 {
		return nil
	}
	return newFlag(r.Config, "Customer is "+strings.Join(parts, " and "))
}

// KYCReviewDueRule : dossier vérifié mais pas revu dans les délais (12, 24 ou 36 mois selon le risque).
type KYCReviewDueRule struct{ Config RuleConfig }

func (r *KYCReviewDueRule) Name() string { return r.Config.Name }

func (r *KYCReviewDueRule) Evaluate(ctx *RuleContext) *riskflagentity.RiskFlag {
	profile := ctx.KYCProfile
	if profile == nil || ctx.Customer == nil {
		return nil
	}
	if profile.KYCStatus != customerservice.Verified && profile.KYCStatus != customerservice.EnhancedVerification {
		return nil
	}
	due := kycrisk.ReviewDueDate(profile.LastVerificationDate, ctx.Customer.RiskLevel)
	if due.IsZero() || time.Now().Before(due) {
		return nil
	}
	return newFlag(r.Config, fmt.Sprintf("KYC review overdue since %s", due.In(kycrisk.Location()).Format("2006-01-02")))
}

// --- Règles de comportement, calculées à partir des opérations ---

// StructuringRule détecte le fractionnement : plusieurs opérations juste sous le seuil sur la fenêtre.
type StructuringRule struct{ Config RuleConfig }

func (r *StructuringRule) Name() string { return r.Config.Name }

func (r *StructuringRule) Evaluate(ctx *RuleContext) *riskflagentity.RiskFlag {
	threshold := r.Config.ThresholdAmount
	if ctx.Transaction == nil || threshold <= 0 || r.Config.ThresholdCount <= 0 {
		return nil
	}
	lower := threshold * (1 - r.Config.Margin)
	inBand := func(amount float64) bool { return amount >= lower && amount < threshold }
	if !inBand(ctx.Transaction.Amount) {
		return nil
	}
	since := time.Now().Add(-r.Config.Window)
	count := 0
	countedCurrent := false
	for _, tx := range ctx.RecentTransactions {
		if tx.CreatedAt.Before(since) || !inBand(tx.Amount) {
			continue
		}
		count++
		if tx.ID == ctx.Transaction.ID {
			countedCurrent = true
		}
	}
	if !countedCurrent {
		count++
	}
	if count < r.Config.ThresholdCount {
		return nil
	}
	return newFlag(r.Config, fmt.Sprintf("%d transactions just below threshold %.2f over %s", count, threshold, r.Config.Window))
}

// RoundTripRule détecte un aller-retour : achat puis vente (ou l'inverse) de la même devise sur la fenêtre.
type RoundTripRule struct{ Config RuleConfig }

func (r *RoundTripRule) Name() string { return r.Config.Name }

func (r *RoundTripRule) Evaluate(ctx *RuleContext) *riskflagentity.RiskFlag {
	current := ctx.Transaction
	if current == nil {
		return nil
	}
	since := time.Now().Add(-r.Config.Window)
	for _, tx := range ctx.RecentTransactions {
		if tx.ID == current.ID || tx.CreatedAt.Before(since) {
			continue
		}
		if strings.EqualFold(tx.Currency, current.Currency) && !strings.EqualFold(tx.Type, current.Type) {
			return newFlag(r.Config, fmt.Sprintf("Opposite %s after %s on %s within %s (transaction %d)",
				current.Type, tx.Type, current.Currency, r.Config.Window, tx.ID))
		}
	}
	return nil
}

// DormantReactivationRule : client inactif depuis longtemps qui revient avec un montant important.
type DormantReactivationRule struct{ Config RuleConfig }

func (r *DormantReactivationRule) Name() string { return r.Config.Name }

func (r *DormantReactivationRule) Evaluate(ctx *RuleContext) *riskflagentity.RiskFlag {
	if ctx.Transaction == nil || ctx.Signals == nil || ctx.Signals.PreviousTransactionAt == nil {
		return nil
	}
	idle := time.Since(*ctx.Signals.PreviousTransactionAt)
	if idle < r.Config.Window || ctx.Transaction.Amount < r.Config.ThresholdAmount {
		return nil
	}
	return newFlag(r.Config, fmt.Sprintf("Customer inactive for %d days returns with amount %.2f",
		int(idle.Hours()/24), ctx.Transaction.Amount))
}
