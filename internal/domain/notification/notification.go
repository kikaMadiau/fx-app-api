// Package notification prévient le client par e-mail et SMS après ses opérations de change.
//
// Les modèles sont embarqués dans le binaire (dossier templates/). Un fichier du même nom
// déposé dans NOTIFY_TEMPLATES_DIR (par défaut ./templates) les remplace : on peut y copier
// les fichiers du kit de messages (templates/email/*.html, templates/sms/*.txt).
// Les champs des modèles s'écrivent {{ .ClientName }}, {{ .AmountFrom }}… ; un champ
// inconnu est laissé vide.
package notification

import (
	"bytes"
	"embed"
	"fmt"
	htmltemplate "html/template"
	"math"
	"os"
	"path/filepath"
	"strings"
	texttemplate "text/template"
	"time"
)

//go:embed templates
var embedded embed.FS

// Modèles de message (noms de fichier du kit).
const (
	TemplateConfirmation  = "confirmation-change"
	TemplateDossier       = "dossier-incomplet"
	TemplateDailyLimit    = "limite-journaliere"
	TemplatePasswordReset = "reinitialisation-mot-de-passe"
	ChannelEmail          = "EMAIL"
	ChannelSMS            = "SMS"
	StatusSent            = "SENT"
	StatusSkipped         = "SKIPPED"
	StatusFailed          = "FAILED"
	defaultBrand          = "MOTUYA Fx."
	defaultTemplatesDir   = "templates"
	defaultAssetsBaseURL  = "http://localhost:5173"
	defaultReportURL      = "https://www.bcc.cd"
	defaultDossierDelay   = 60 * time.Second
	dossierReminderPeriod = 7 * 24 * time.Hour
)

// Subjects : objet des e-mails, repris du kit.
var Subjects = map[string]string{
	TemplateConfirmation:  "Votre opération de change est confirmée",
	TemplateDossier:       "Complétez votre dossier MOTUYA Fx.",
	TemplateDailyLimit:    "Limite de change journalière atteinte",
	TemplatePasswordReset: "Réinitialisation de votre mot de passe MOTUYA Fx.",
}

// Data : valeurs disponibles dans les modèles.
type Data map[string]string

// Rendered : un message prêt à partir.
type Rendered struct {
	Subject string
	HTML    string
	SMS     string
}

// Render remplit les modèles e-mail et SMS d'un message.
func Render(name string, data Data) (*Rendered, error) {
	emailSrc, err := templateSource("email/" + name + ".html")
	if err != nil {
		return nil, err
	}
	smsSrc, err := templateSource("sms/" + name + ".txt")
	if err != nil {
		return nil, err
	}

	html, err := htmltemplate.New(name).Option("missingkey=zero").Parse(emailSrc)
	if err != nil {
		return nil, fmt.Errorf("e-mail template %s: %w", name, err)
	}
	var htmlOut bytes.Buffer
	if err := html.Execute(&htmlOut, map[string]string(data)); err != nil {
		return nil, fmt.Errorf("e-mail template %s: %w", name, err)
	}

	sms, err := texttemplate.New(name).Option("missingkey=zero").Parse(smsSrc)
	if err != nil {
		return nil, fmt.Errorf("SMS template %s: %w", name, err)
	}
	var smsOut bytes.Buffer
	if err := sms.Execute(&smsOut, map[string]string(data)); err != nil {
		return nil, fmt.Errorf("SMS template %s: %w", name, err)
	}

	subject := Subjects[name]
	if brand := data["Brand"]; brand != "" {
		subject = strings.ReplaceAll(subject, defaultBrand, brand)
	}
	return &Rendered{
		Subject: subject,
		HTML:    htmlOut.String(),
		// « missingkey=zero » écrit "<no value>" pour une clé absente d'une map en text/template.
		SMS: collapseSpaces(strings.ReplaceAll(smsOut.String(), "<no value>", "")),
	}, nil
}

// RenderEmail remplit un modèle qui n'existe qu'en e-mail (réinitialisation du mot de passe).
func RenderEmail(name string, data Data) (subject, html string, err error) {
	src, err := templateSource("email/" + name + ".html")
	if err != nil {
		return "", "", err
	}
	tpl, err := htmltemplate.New(name).Option("missingkey=zero").Parse(src)
	if err != nil {
		return "", "", fmt.Errorf("e-mail template %s: %w", name, err)
	}
	var out bytes.Buffer
	if err := tpl.Execute(&out, map[string]string(data)); err != nil {
		return "", "", fmt.Errorf("e-mail template %s: %w", name, err)
	}
	return Subjects[name], out.String(), nil
}

// AssetsBaseURL : adresse publique de l'interface (liens et logos des e-mails).
func AssetsBaseURL() string {
	return strings.TrimRight(envOrDefault("NOTIFY_ASSETS_BASE_URL", defaultAssetsBaseURL), "/")
}

// templateSource lit le modèle déposé dans NOTIFY_TEMPLATES_DIR, sinon celui embarqué.
func templateSource(rel string) (string, error) {
	dir := envOrDefault("NOTIFY_TEMPLATES_DIR", defaultTemplatesDir)
	if content, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel))); err == nil {
		return string(content), nil
	}
	content, err := embedded.ReadFile("templates/" + rel)
	if err != nil {
		return "", fmt.Errorf("notification template %s not found", rel)
	}
	return string(content), nil
}

func collapseSpaces(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// SMSInfo : longueur et nombre de SMS facturés (GSM-7 : 160 caractères, Unicode : 70).
func SMSInfo(text string) (chars int, gsm bool, segments int) {
	const gsmChars = "@£$¥èéùìòÇ\nØø\rÅåΔ_ΦΓΛΩΠΨΣΘΞÆæßÉ !\"#¤%&'()*+,-./0123456789:;<=>?¡ABCDEFGHIJKLMNOPQRSTUVWXYZÄÖÑÜ§¿abcdefghijklmnopqrstuvwxyzäöñüà"
	gsm = true
	for _, r := range text {
		chars++
		if !strings.ContainsRune(gsmChars, r) {
			gsm = false
		}
	}
	switch {
	case gsm && chars <= 160:
		segments = 1
	case gsm:
		segments = int(math.Ceil(float64(chars) / 153))
	case chars <= 70:
		segments = 1
	default:
		segments = int(math.Ceil(float64(chars) / 67))
	}
	return chars, gsm, segments
}

// FormatAmount écrit un montant à la française avec une espace simple comme séparateur
// des milliers (compatible SMS) : 2 décimales, aucune pour le franc congolais.
func FormatAmount(value float64, currency string) string {
	decimals := 2
	if strings.EqualFold(currency, "CDF") {
		decimals = 0
	}
	return formatNumber(value, decimals)
}

// FormatRate écrit un taux de change (2 décimales, jusqu'à 6 si nécessaire).
func FormatRate(rate float64) string {
	decimals := 2
	for decimals < 6 && math.Abs(rate*math.Pow10(decimals)-math.Round(rate*math.Pow10(decimals))) > 1e-6 {
		decimals++
	}
	return formatNumber(rate, decimals)
}

func formatNumber(value float64, decimals int) string {
	negative := value < 0
	s := fmt.Sprintf("%.*f", decimals, math.Abs(value))
	intPart, frac, _ := strings.Cut(s, ".")
	var b strings.Builder
	for i, r := range intPart {
		if i > 0 && (len(intPart)-i)%3 == 0 {
			b.WriteByte(' ')
		}
		b.WriteRune(r)
	}
	out := b.String()
	if frac != "" {
		out += "," + frac
	}
	if negative {
		out = "-" + out
	}
	return out
}

var frenchMonths = [...]string{"janvier", "février", "mars", "avril", "mai", "juin", "juillet", "août", "septembre", "octobre", "novembre", "décembre"}

// FormatDateTime : « 03/11/2026 à 10:24 ».
func FormatDateTime(t time.Time) string {
	return t.Format("02/01/2006") + " à " + t.Format("15:04")
}

// FormatResetAt : « demain, 4 novembre 2026 à 00h00 ».
func FormatResetAt(now time.Time) string {
	next := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, now.Location())
	return fmt.Sprintf("demain, %d %s %d à 00h00", next.Day(), frenchMonths[next.Month()-1], next.Year())
}

// FormatReference : même numéro que sur le bordereau (6 chiffres).
func FormatReference(transactionID int) string {
	return fmt.Sprintf("%06d", transactionID)
}

func envOrDefault(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}
