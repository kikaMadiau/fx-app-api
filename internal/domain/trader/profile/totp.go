package profile

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// Codes TOTP (RFC 6238) compatibles avec Google Authenticator, Microsoft Authenticator, etc. :
// HMAC-SHA1, 6 chiffres, pas de 30 secondes.
const (
	totpDigits = 6
	totpPeriod = 30
	// Tolérance d'un pas avant/après pour absorber un léger décalage d'horloge du téléphone.
	totpSkew = 1
)

var base32NoPadding = base32.StdEncoding.WithPadding(base32.NoPadding)

// GenerateTOTPSecret renvoie un secret aléatoire de 160 bits encodé en base32.
func GenerateTOTPSecret() (string, error) {
	raw := make([]byte, 20)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate totp secret: %w", err)
	}
	return base32NoPadding.EncodeToString(raw), nil
}

// TOTPCode calcule le code valable pour un pas de temps donné.
func TOTPCode(secret string, step int64) (string, error) {
	key, err := base32NoPadding.DecodeString(strings.ToUpper(strings.TrimSpace(secret)))
	if err != nil {
		return "", fmt.Errorf("invalid totp secret: %w", err)
	}
	var counter [8]byte
	binary.BigEndian.PutUint64(counter[:], uint64(step))
	mac := hmac.New(sha1.New, key)
	mac.Write(counter[:])
	sum := mac.Sum(nil)
	offset := sum[len(sum)-1] & 0x0f
	value := binary.BigEndian.Uint32(sum[offset:offset+4]) & 0x7fffffff
	return fmt.Sprintf("%0*d", totpDigits, value%1000000), nil
}

// TOTPStep renvoie le pas de temps TOTP correspondant à un instant.
func TOTPStep(now time.Time) int64 {
	return now.Unix() / totpPeriod
}

// VerifyTOTP vérifie un code et renvoie le pas de temps qui correspond, pour empêcher
// la réutilisation d'un même code (lastUsedStep). Renvoie ok=false si le code est faux ou déjà utilisé.
func VerifyTOTP(secret, code string, now time.Time, lastUsedStep int64) (step int64, ok bool) {
	code = strings.ReplaceAll(strings.TrimSpace(code), " ", "")
	if len(code) != totpDigits {
		return 0, false
	}
	current := TOTPStep(now)
	for delta := int64(-totpSkew); delta <= totpSkew; delta++ {
		candidate := current + delta
		if candidate <= lastUsedStep {
			continue
		}
		expected, err := TOTPCode(secret, candidate)
		if err != nil {
			return 0, false
		}
		if subtle.ConstantTimeCompare([]byte(expected), []byte(code)) == 1 {
			return candidate, true
		}
	}
	return 0, false
}

// OTPAuthURL construit le lien otpauth:// lu par les applications d'authentification (QR code).
func OTPAuthURL(issuer, account, secret string) string {
	label := url.PathEscape(issuer + ":" + account)
	query := url.Values{}
	query.Set("secret", secret)
	query.Set("issuer", issuer)
	query.Set("algorithm", "SHA1")
	query.Set("digits", fmt.Sprint(totpDigits))
	query.Set("period", fmt.Sprint(totpPeriod))
	return "otpauth://totp/" + label + "?" + query.Encode()
}
