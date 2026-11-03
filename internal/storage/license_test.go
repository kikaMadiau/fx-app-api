package storage

import "testing"

func TestNormalizeBCCLicense(t *testing.T) {
	t.Setenv("BCC_LICENSE_PATTERN", "")
	if got, err := NormalizeBCCLicense("  bcc-Ab  "); err != nil || got != "BCC-AB" {
		t.Fatalf("NormalizeBCCLicense without pattern = %q, %v", got, err)
	}
	if _, err := NormalizeBCCLicense(" \t "); err == nil {
		t.Fatal("empty license accepted")
	}
	t.Setenv("BCC_LICENSE_PATTERN", `^BCC-[A-Z0-9]+$`)
	if got, err := NormalizeBCCLicense(" bcc-12 "); err != nil || got != "BCC-12" {
		t.Fatalf("valid configured license = %q, %v", got, err)
	}
	if _, err := NormalizeBCCLicense("OTHER"); err == nil {
		t.Fatal("license outside configured pattern accepted")
	}
	t.Setenv("BCC_LICENSE_PATTERN", "[")
	if _, err := NormalizeBCCLicense("VALID"); err == nil {
		t.Fatal("invalid configured pattern accepted")
	}
}
