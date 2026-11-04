package notification

import (
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"fx-app-api/internal/domain/customer/kycrisk"
)

// Store : ce dont le service a besoin en base (le Repository, ou un faux dans les tests).
type Store interface {
	LoadTransaction(transactionID int) (*TransactionContext, error)
	DayOperations(customerID int, since time.Time) ([]DayOperation, error)
	SentSince(customerID int, template string, since time.Time) (bool, error)
	LoadCustomer(customerID, traderID int) (*TransactionContext, error)
	Save(rec *Record) error
}

// Service écrit au client après chaque opération :
//   - confirmation de change, tout de suite ;
//   - limite journalière (DAILY_LIMIT_USD) atteinte, ou opération refusée parce qu'elle la dépasserait ;
//   - dossier incomplet, après NOTIFY_DOSSIER_DELAY (le temps que le guichet envoie les
//     photos de la pièce), au plus une fois par semaine.
//
// Un échec d'envoi n'annule jamais l'opération : il est noté dans le journal.
type Service struct {
	store        Store
	sender       Sender
	now          func() time.Time
	dossierDelay time.Duration
	after        func(time.Duration, func())
}

func NewService(store Store, sender Sender) *Service {
	return &Service{
		store:        store,
		sender:       sender,
		now:          time.Now,
		dossierDelay: envDuration("NOTIFY_DOSSIER_DELAY", defaultDossierDelay),
		after:        func(d time.Duration, f func()) { time.AfterFunc(d, f) },
	}
}

// TransactionCompleted lance les notifications d'une opération enregistrée, sans bloquer la réponse.
func (s *Service) TransactionCompleted(transactionID int) {
	if s == nil || !Active(s.sender) {
		return
	}
	go s.notifyNow(transactionID)
	s.after(s.dossierDelay, func() { s.notifyDossier(transactionID) })
}

func (s *Service) notifyNow(transactionID int) {
	ctx, err := s.store.LoadTransaction(transactionID)
	if err != nil {
		log.Printf("notification: %v", err)
		return
	}
	if !strings.EqualFold(ctx.Status, "COMPLETED") {
		return
	}
	data := s.baseData(ctx)

	// Cumul du jour (équivalent USD), affiché dans la confirmation et utilisé pour le plafond.
	limit := kycrisk.DailyLimitUSD()
	now := s.now().In(kycrisk.Location())
	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	ops, err := s.store.DayOperations(ctx.CustomerID, dayStart)
	if err != nil {
		log.Printf("notification: %v", err)
	}
	var before, total float64
	for _, op := range ops {
		usd := kycrisk.USDEquivalent(op.Amount, op.Currency, op.ConvertedAmount, op.TargetCurrency)
		total += usd
		if op.ID != ctx.TransactionID {
			before += usd
		}
	}
	data["DailyUsed"] = formatNumber(total, 0)
	data["DailyLimit"] = formatNumber(limit, 0)
	data["ResetAt"] = FormatResetAt(now)

	s.deliver(ctx, TemplateConfirmation, data)

	// L'opération qui atteint la limite prévient le client (au plus un message par jour ; une
	// opération qui la dépasserait est refusée, voir DailyLimitReached).
	if err == nil && limit > 0 && before < limit && total >= limit-0.005 && !s.sentToday(ctx.CustomerID, TemplateDailyLimit, dayStart) {
		s.deliver(ctx, TemplateDailyLimit, data)
	}
}

// DailyLimitReached prévient le client dont une opération vient d'être refusée parce qu'elle
// dépasserait sa limite journalière : « Prochain change possible : demain ». Une fois par jour.
func (s *Service) DailyLimitReached(customerID, traderID int, dayTotalUSD float64) {
	if s == nil || !Active(s.sender) {
		return
	}
	go func() {
		now := s.now().In(kycrisk.Location())
		dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
		if s.sentToday(customerID, TemplateDailyLimit, dayStart) {
			return
		}
		ctx, err := s.store.LoadCustomer(customerID, traderID)
		if err != nil {
			log.Printf("notification: %v", err)
			return
		}
		data := s.baseData(ctx)
		data["DailyUsed"] = formatNumber(dayTotalUSD, 0)
		data["DailyLimit"] = formatNumber(kycrisk.DailyLimitUSD(), 0)
		data["ResetAt"] = FormatResetAt(now)
		s.deliver(ctx, TemplateDailyLimit, data)
	}()
}

func (s *Service) sentToday(customerID int, template string, dayStart time.Time) bool {
	already, err := s.store.SentSince(customerID, template, dayStart)
	if err != nil {
		log.Printf("notification: %v", err)
	}
	return already
}

func (s *Service) notifyDossier(transactionID int) {
	ctx, err := s.store.LoadTransaction(transactionID)
	if err != nil {
		log.Printf("notification: %v", err)
		return
	}
	missing := MissingDocument(ctx, s.now().In(kycrisk.Location()))
	if missing == "" {
		return
	}
	already, err := s.store.SentSince(ctx.CustomerID, TemplateDossier, s.now().Add(-dossierReminderPeriod))
	if err != nil {
		log.Printf("notification: %v", err)
		return
	}
	if already {
		return
	}
	data := s.baseData(ctx)
	data["MissingDocument"] = missing
	s.deliver(ctx, TemplateDossier, data)
}

// MissingDocument : ce qu'il faut demander au client, ou "" si le dossier est complet.
func MissingDocument(ctx *TransactionContext, now time.Time) string {
	// Formulé pour le kit : « Il manque : … », « Veuillez enregistrer … ».
	switch {
	case strings.TrimSpace(ctx.IDType) == "" || strings.TrimSpace(ctx.IDNumber) == "":
		return "votre pièce d'identité"
	case kycrisk.IDExpired(ctx.IDExpiryDate, now):
		return "une pièce d'identité en cours de validité"
	case !ctx.HasIDFront && !ctx.HasIDBack:
		return "la photo de votre pièce d'identité (recto et verso)"
	case !ctx.HasIDFront:
		return "la photo du recto de votre pièce d'identité"
	case !ctx.HasIDBack:
		return "la photo du verso de votre pièce d'identité"
	}
	return ""
}

func (s *Service) baseData(ctx *TransactionContext) Data {
	// Achat : le bureau achète la devise au client, qui remet la devise et obtient des francs.
	// Vente : le client remet des francs et obtient la devise.
	counter := ctx.TargetCurrency
	if counter == "" {
		counter = "CDF"
	}
	counterValue := ctx.ConvertedAmount
	if counterValue == 0 {
		counterValue = ctx.Amount * ctx.Rate
	}
	fromAmount, fromCur, toAmount, toCur := ctx.Amount, ctx.Currency, counterValue, counter
	if strings.EqualFold(ctx.Type, "SELL") {
		fromAmount, fromCur, toAmount, toCur = counterValue, counter, ctx.Amount, ctx.Currency
	}

	bureau := strings.TrimSpace(ctx.ExchangeOffice)
	if bureau == "" && ctx.StoreName != "" && ctx.StoreName != "Default" {
		bureau = ctx.StoreName
	}
	if bureau == "" {
		bureau = "Bureau de change"
	}
	license := strings.TrimSpace(ctx.StoreLicense)
	if license == "" || license == "MIGRATION-PENDING" {
		license = strings.TrimSpace(os.Getenv("NOTIFY_BCC_AGREMENT"))
	}
	assets := strings.TrimRight(envOrDefault("NOTIFY_ASSETS_BASE_URL", defaultAssetsBaseURL), "/")
	// Les modèles du kit affichent toujours le logo : par défaut celui servi par l'interface.
	logo := strings.TrimSpace(os.Getenv("NOTIFY_LOGO_URL"))
	if logo == "" {
		logo = assets + "/branding/motuya-mark.png"
	}

	return Data{
		"Brand":         envOrDefault("NOTIFY_BRAND", defaultBrand),
		"LogoURL":       logo,
		"BCCLogoURL":    assets + "/branding/logo-bcc.png",
		"ReportURL":     envOrDefault("NOTIFY_REPORT_URL", defaultReportURL),
		"Civility":      "",
		"ClientName":    strings.TrimSpace(ctx.CustomerName),
		"AmountFrom":    FormatAmount(fromAmount, fromCur),
		"CurrencyFrom":  strings.ToUpper(fromCur),
		"AmountTo":      FormatAmount(toAmount, toCur),
		"CurrencyTo":    strings.ToUpper(toCur),
		"Rate":          FormatRate(ctx.Rate),
		"TraderName":    strings.TrimSpace(ctx.TraderFirstName + " " + ctx.TraderLastName),
		"TraderLicense": license,
		"BureauName":    bureau,
		"Reference":     FormatReference(ctx.TransactionID),
		"DateTime":      FormatDateTime(ctx.CreatedAt.In(kycrisk.Location())),
	}
}

// deliver prépare l'e-mail et le SMS d'un modèle, les envoie et les note dans le journal.
func (s *Service) deliver(ctx *TransactionContext, template string, data Data) {
	rendered, err := Render(template, data)
	if err != nil {
		log.Printf("notification %s: %v", template, err)
		return
	}
	// Sans opération (refus pour limite journalière), le message n'est rattaché qu'au client.
	var txID *int
	if ctx.TransactionID > 0 {
		id := ctx.TransactionID
		txID = &id
	}
	s.send(ctx, &Record{CustomerID: ctx.CustomerID, TransactionID: txID, Channel: ChannelSMS, Template: template,
		Recipient: ctx.Phone, Body: rendered.SMS}, "pas de numéro de téléphone")
	s.send(ctx, &Record{CustomerID: ctx.CustomerID, TransactionID: txID, Channel: ChannelEmail, Template: template,
		Recipient: ctx.Email, Subject: rendered.Subject, Body: rendered.HTML}, "pas d'adresse e-mail")
}

func (s *Service) send(ctx *TransactionContext, rec *Record, noRecipient string) {
	rec.Mode = s.sender.Mode()
	switch {
	case strings.TrimSpace(rec.Recipient) == "":
		rec.Status, rec.Detail = StatusSkipped, noRecipient
	default:
		location, err := s.sender.Send(Message{Channel: rec.Channel, Template: rec.Template, To: rec.Recipient,
			Subject: rec.Subject, Body: rec.Body, TransactionID: ctx.TransactionID, CreatedAt: s.now()})
		if err != nil {
			rec.Status, rec.Detail = StatusFailed, err.Error()
		} else {
			rec.Status, rec.Detail = StatusSent, location
		}
	}
	if err := s.store.Save(rec); err != nil {
		log.Printf("notification: %v", err)
	}
}

func envFloat(key string, fallback float64) float64 {
	if v, err := strconv.ParseFloat(strings.TrimSpace(os.Getenv(key)), 64); err == nil {
		return v
	}
	return fallback
}

func envDuration(key string, fallback time.Duration) time.Duration {
	if d, err := time.ParseDuration(strings.TrimSpace(os.Getenv(key))); err == nil && d >= 0 {
		return d
	}
	return fallback
}
