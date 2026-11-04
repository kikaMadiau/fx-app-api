package notification

import (
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net"
	"net/http"
	"net/smtp"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// configuredSender suit les réglages du moment (ils peuvent changer sans redémarrer l'API) :
// outbox en mode test, Twilio et SMTP en mode réel.
type configuredSender struct {
	outbox *OutboxSender
}

func (s *configuredSender) Mode() string { return CurrentSettings().Mode }

func (s *configuredSender) Send(msg Message) (string, error) {
	settings := CurrentSettings()
	switch settings.Mode {
	case ModeOff:
		return "", errors.New("notifications désactivées")
	case ModeLive:
		return sendLive(settings, msg)
	default:
		return s.outbox.Send(msg)
	}
}

// sendLive : un message « SMS » part par SMS et/ou WhatsApp selon les canaux activés.
func sendLive(s Settings, msg Message) (string, error) {
	if msg.Channel == ChannelEmail {
		if !s.EmailEnabled {
			return "", errors.New("e-mail désactivé dans les réglages")
		}
		if err := SendEmail(s, msg.To, msg.Subject, msg.Body); err != nil {
			return "", err
		}
		return "smtp:" + msg.To, nil
	}
	if !s.SMSEnabled && !s.WhatsAppEnabled {
		return "", errors.New("SMS et WhatsApp désactivés dans les réglages")
	}
	var sent, failed []string
	if s.SMSEnabled {
		if id, err := SendTwilio(s, "sms", msg.To, msg.Body); err != nil {
			failed = append(failed, "SMS : "+err.Error())
		} else {
			sent = append(sent, "sms:"+id)
		}
	}
	if s.WhatsAppEnabled {
		if id, err := SendTwilio(s, "whatsapp", msg.To, msg.Body); err != nil {
			failed = append(failed, "WhatsApp : "+err.Error())
		} else {
			sent = append(sent, "whatsapp:"+id)
		}
	}
	if len(sent) == 0 {
		return "", errors.New(strings.Join(failed, " ; "))
	}
	location := strings.Join(sent, " ")
	if len(failed) > 0 {
		location += " (" + strings.Join(failed, " ; ") + ")"
	}
	return location, nil
}

var httpClient = &http.Client{Timeout: 15 * time.Second}

// twilioAPI : adresse de l'API Twilio (remplacée dans les tests).
var twilioAPI = "https://api.twilio.com"

// SendTwilio envoie un SMS ou un WhatsApp par l'API Twilio et renvoie l'identifiant du message.
// Une erreur Twilio est renvoyée telle quelle (code et message), pour le diagnostic.
func SendTwilio(s Settings, channel, to, body string) (string, error) {
	if s.TwilioAccountSID == "" || s.TwilioAuthToken == "" {
		return "", errors.New("Twilio non configuré (Account SID ou Auth Token manquant)")
	}
	to = strings.TrimSpace(strings.TrimPrefix(to, "whatsapp:"))
	if to == "" {
		return "", errors.New("aucun numéro de destination")
	}
	from := strings.TrimSpace(s.TwilioFrom)
	if channel == "whatsapp" {
		from = strings.TrimSpace(strings.TrimPrefix(s.TwilioWhatsAppFrom, "whatsapp:"))
		if from == "" {
			return "", errors.New("numéro WhatsApp expéditeur manquant")
		}
		to, from = "whatsapp:"+to, "whatsapp:"+from
	} else if from == "" {
		return "", errors.New("numéro SMS expéditeur manquant")
	}

	form := url.Values{"To": {to}, "From": {from}, "Body": {body}}
	endpoint := fmt.Sprintf("%s/2010-04-01/Accounts/%s/Messages.json", twilioAPI, url.PathEscape(s.TwilioAccountSID))
	req, err := http.NewRequest(http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.SetBasicAuth(s.TwilioAccountSID, s.TwilioAuthToken)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("Twilio injoignable : %v", err)
	}
	defer resp.Body.Close()
	var reply struct {
		SID     string `json:"sid"`
		Code    int    `json:"code"`
		Message string `json:"message"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&reply)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if reply.Message != "" {
			return "", fmt.Errorf("Twilio %d (code %d) : %s", resp.StatusCode, reply.Code, reply.Message)
		}
		return "", fmt.Errorf("Twilio HTTP %d", resp.StatusCode)
	}
	return reply.SID, nil
}

// SendEmail envoie un e-mail HTML par SMTP (Gmail, SendGrid de Twilio, serveur de l'hébergeur…).
func SendEmail(s Settings, to, subject, html string) error {
	if s.SMTPHost == "" {
		return errors.New("serveur SMTP non renseigné")
	}
	to = strings.TrimSpace(to)
	if to == "" {
		return errors.New("aucune adresse e-mail de destination")
	}
	port := s.SMTPPort
	if port == 0 {
		port = 587
		if s.SMTPSSL {
			port = 465
		}
	}
	from := s.SMTPFrom
	if from == "" {
		from = s.SMTPUser
	}
	if from == "" {
		return errors.New("adresse d'expédition manquante")
	}

	var b strings.Builder
	fmt.Fprintf(&b, "From: %s\r\nTo: %s\r\nSubject: %s\r\nDate: %s\r\nMIME-Version: 1.0\r\n",
		from, to, mime.QEncoding.Encode("utf-8", subject), time.Now().Format(time.RFC1123Z))
	b.WriteString("Content-Type: text/html; charset=\"utf-8\"\r\nContent-Transfer-Encoding: 8bit\r\n\r\n")
	b.WriteString(strings.ReplaceAll(html, "\n", "\r\n"))

	addr := net.JoinHostPort(s.SMTPHost, strconv.Itoa(port))
	var conn net.Conn
	var err error
	dialer := &net.Dialer{Timeout: 12 * time.Second}
	if s.SMTPSSL {
		conn, err = tls.DialWithDialer(dialer, "tcp", addr, &tls.Config{ServerName: s.SMTPHost})
	} else {
		conn, err = dialer.Dial("tcp", addr)
	}
	if err != nil {
		return fmt.Errorf("serveur SMTP injoignable : %v", err)
	}
	_ = conn.SetDeadline(time.Now().Add(30 * time.Second))
	client, err := smtp.NewClient(conn, s.SMTPHost)
	if err != nil {
		conn.Close()
		return fmt.Errorf("SMTP : %v", err)
	}
	defer client.Close()
	if !s.SMTPSSL {
		if ok, _ := client.Extension("STARTTLS"); ok {
			if err := client.StartTLS(&tls.Config{ServerName: s.SMTPHost}); err != nil {
				return fmt.Errorf("SMTP STARTTLS : %v", err)
			}
		}
	}
	if s.SMTPUser != "" {
		if err := client.Auth(smtp.PlainAuth("", s.SMTPUser, s.SMTPPass, s.SMTPHost)); err != nil {
			return fmt.Errorf("authentification SMTP refusée (pour Gmail, utilisez un « mot de passe d'application ») : %v", err)
		}
	}
	if err := client.Mail(extractAddress(from)); err != nil {
		return fmt.Errorf("SMTP expéditeur refusé : %v", err)
	}
	if err := client.Rcpt(to); err != nil {
		return fmt.Errorf("SMTP destinataire refusé : %v", err)
	}
	w, err := client.Data()
	if err != nil {
		return fmt.Errorf("SMTP : %v", err)
	}
	if _, err := w.Write([]byte(b.String())); err != nil {
		return fmt.Errorf("SMTP : %v", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("SMTP : %v", err)
	}
	return client.Quit()
}

// extractAddress : « MOTUYA Fx <no-reply@x.cd> » → no-reply@x.cd.
func extractAddress(from string) string {
	if i, j := strings.LastIndex(from, "<"), strings.LastIndex(from, ">"); i >= 0 && j > i {
		return from[i+1 : j]
	}
	return strings.TrimSpace(from)
}
