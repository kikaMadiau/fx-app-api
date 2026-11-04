package profile

import (
	"encoding/base32"
	"strings"
	"testing"
	"time"
)

// Vecteurs officiels de la RFC 6238 (SHA1, secret ASCII "12345678901234567890"),
// tronqués aux 6 derniers chiffres.
func TestTOTPCodeMatchesRFC6238Vectors(t *testing.T) {
	secret := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString([]byte("12345678901234567890"))
	vectors := map[int64]string{
		59:          "287082",
		1111111109:  "081804",
		1111111111:  "050471",
		1234567890:  "005924",
		2000000000:  "279037",
		20000000000: "353130",
	}
	for unix, want := range vectors {
		got, err := TOTPCode(secret, TOTPStep(time.Unix(unix, 0)))
		if err != nil {
			t.Fatalf("TOTPCode(%d): %v", unix, err)
		}
		if got != want {
			t.Errorf("TOTPCode(%d) = %s, want %s", unix, got, want)
		}
	}
}

func TestVerifyTOTPAcceptsSmallClockSkewAndRejectsReplay(t *testing.T) {
	secret, err := GenerateTOTPSecret()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_800_000_000, 0)
	previous, _ := TOTPCode(secret, TOTPStep(now)-1)

	step, ok := VerifyTOTP(secret, previous, now, 0)
	if !ok || step != TOTPStep(now)-1 {
		t.Fatalf("code of previous step should be accepted, got ok=%v step=%d", ok, step)
	}
	if _, ok := VerifyTOTP(secret, previous, now, step); ok {
		t.Fatal("a code already used must be rejected")
	}
}

func TestVerifyTOTPRejectsWrongAndOldCodes(t *testing.T) {
	secret, _ := GenerateTOTPSecret()
	now := time.Unix(1_800_000_000, 0)
	old, _ := TOTPCode(secret, TOTPStep(now)-5)
	if _, ok := VerifyTOTP(secret, old, now, 0); ok {
		t.Fatal("a code from 2.5 minutes ago must be rejected")
	}
	if _, ok := VerifyTOTP(secret, "12345", now, 0); ok {
		t.Fatal("a 5-digit code must be rejected")
	}
	current, _ := TOTPCode(secret, TOTPStep(now))
	if _, ok := VerifyTOTP(secret, current[:3]+" "+current[3:], now, 0); !ok {
		t.Fatal("a code typed with a space must be accepted")
	}
}

func TestOTPAuthURL(t *testing.T) {
	got := OTPAuthURL("FX Change", "jean@fx.local", "ABCDEF")
	for _, part := range []string{"otpauth://totp/FX%20Change:jean@fx.local?", "secret=ABCDEF", "issuer=FX+Change", "digits=6", "period=30"} {
		if !strings.Contains(got, part) {
			t.Errorf("OTPAuthURL = %s, missing %s", got, part)
		}
	}
}

func TestValidatePreferences(t *testing.T) {
	if err := ValidatePreferences(DefaultPreferences()); err != nil {
		t.Fatalf("default preferences must be valid: %v", err)
	}
	for _, bad := range []Preferences{
		{Currency: "CDF", Timezone: "Africa/Kinshasa", DateFormat: "DD/MM/YYYY", Theme: "light"},
		{Currency: "USD", Timezone: "Europe/Paris", DateFormat: "DD/MM/YYYY", Theme: "light"},
		{Currency: "USD", Timezone: "Africa/Kinshasa", DateFormat: "MM/DD/YYYY", Theme: "light"},
		{Currency: "USD", Timezone: "Africa/Kinshasa", DateFormat: "DD/MM/YYYY", Theme: "blue"},
	} {
		if err := ValidatePreferences(bad); err == nil {
			t.Errorf("preferences %+v must be rejected", bad)
		}
	}
}
