package notification

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Message : un e-mail ou un SMS adressé à un client.
type Message struct {
	Channel       string
	Template      string
	To            string
	Subject       string
	Body          string
	TransactionID int
	CreatedAt     time.Time
}

// Sender fait partir les messages. En mode test, OutboxSender les écrit dans des fichiers ;
// un vrai fournisseur (SMTP, passerelle SMS) implémentera la même interface.
type Sender interface {
	Mode() string
	Send(msg Message) (location string, err error)
}

// NewSenderFromEnv choisit l'envoi. Une fois la base branchée (UseSettings), il suit les
// réglages de l'administrateur (test, réel par Twilio/SMTP, ou désactivé) ; sinon NOTIFY_MODE :
// "test" (par défaut) écrit dans NOTIFY_OUTBOX_DIR (./outbox), "off" désactive les notifications.
func NewSenderFromEnv() Sender {
	outbox := &OutboxSender{Dir: envOrDefault("NOTIFY_OUTBOX_DIR", "outbox")}
	settingsMu.RLock()
	configured := settingsSource != nil
	settingsMu.RUnlock()
	if configured {
		return &configuredSender{outbox: outbox}
	}
	switch strings.ToLower(envOrDefault("NOTIFY_MODE", "test")) {
	case "off", "none", "false":
		return nil
	default:
		return outbox
	}
}

// Active : faux si les notifications sont coupées (pas d'envoi, rien dans le journal).
func Active(s Sender) bool { return s != nil && s.Mode() != ModeOff }

// OutboxSender n'envoie rien : chaque message devient un fichier, rangé par jour,
// pour voir exactement ce que le client recevrait.
type OutboxSender struct {
	Dir string
}

func (s *OutboxSender) Mode() string { return "test" }

var unsafeFileChars = regexp.MustCompile(`[^A-Za-z0-9_-]+`)

func (s *OutboxSender) Send(msg Message) (string, error) {
	at := msg.CreatedAt
	if at.IsZero() {
		at = time.Now()
	}
	dir := filepath.Join(s.Dir, at.Format("2006-01-02"))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create outbox: %w", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return "", fmt.Errorf("secure outbox directory: %w", err)
	}

	ext, content := ".txt", fmt.Sprintf("À : %s\n\n%s\n", msg.To, msg.Body)
	if msg.Channel == ChannelEmail {
		ext = ".html"
		content = fmt.Sprintf("<!-- À : %s | Objet : %s -->\n%s", escapeComment(msg.To), escapeComment(msg.Subject), msg.Body)
	}
	nonce := make([]byte, 8)
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("generate outbox filename: %w", err)
	}
	name := fmt.Sprintf("%s_op%06d_%s_%s_%s%s", at.Format("150405.000"), msg.TransactionID, msg.Template, strings.ToLower(msg.Channel), hex.EncodeToString(nonce), ext)
	name = unsafeFileChars.ReplaceAllString(strings.TrimSuffix(name, ext), "-") + ext
	path := filepath.Join(dir, name)
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", fmt.Errorf("write outbox message: %w", err)
	}
	if _, err := file.WriteString(content); err != nil {
		_ = file.Close()
		return "", fmt.Errorf("write outbox message: %w", err)
	}
	if err := file.Close(); err != nil {
		return "", fmt.Errorf("close outbox message: %w", err)
	}
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	return path, nil
}

func escapeComment(s string) string {
	return strings.ReplaceAll(s, "--", "- -")
}
