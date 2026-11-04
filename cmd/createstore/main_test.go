package main

import (
	"strings"
	"testing"

	traderservices "fx-app-api/internal/domain/trader/traderservices"
	"golang.org/x/crypto/bcrypt"
)

func validOptions() options {
	return options{
		name: "Bureau Gombe", license: " bcc-123 ", firstName: "Kika", lastName: "Madiau",
		email: "sup@example.com", phone: "+243 800 000 000",
	}
}

func TestValidateOptionsReportsEveryMissingRequiredField(t *testing.T) {
	fields := []struct {
		name string
		set  func(*options)
	}{
		{"--name", func(o *options) { o.name = " " }},
		{"--license", func(o *options) { o.license = "" }},
		{"--supervisor-first-name", func(o *options) { o.firstName = "" }},
		{"--supervisor-last-name", func(o *options) { o.lastName = "" }},
		{"--supervisor-email", func(o *options) { o.email = "" }},
		{"--supervisor-phone", func(o *options) { o.phone = "" }},
	}
	for _, field := range fields {
		t.Run(field.name, func(t *testing.T) {
			o := validOptions()
			field.set(&o)
			_, _, _, _, err := validateOptions(o)
			if err == nil || !strings.Contains(err.Error(), field.name) {
				t.Fatalf("expected missing field %s, got %v", field.name, err)
			}
		})
	}
}

func TestValidateOptionsNormalizesAndSetsSupervisorDefaults(t *testing.T) {
	t.Setenv("BCC_LICENSE_PATTERN", `^BCC-[A-Z0-9]+$`)
	store, supervisor, _, _, err := validateOptions(validOptions())
	if err != nil {
		t.Fatal(err)
	}
	if store.BCCLicenseNumber != "BCC-123" {
		t.Fatalf("license not normalized: %q", store.BCCLicenseNumber)
	}
	if supervisor.Phone != "+243800000000" {
		t.Fatalf("phone not normalized: %q", supervisor.Phone)
	}
	if supervisor.Roles != "SUPERVISOR" || supervisor.Status != "ACTIVE" || !supervisor.IsActive || !supervisor.MustChangePassword {
		t.Fatalf("unexpected supervisor defaults: %+v", supervisor)
	}
}

func TestValidateOptionsRejectsDuplicateOrInvalidDates(t *testing.T) {
	tests := []struct {
		name string
		edit func(*options)
		want string
	}{
		{"invalid expiry format", func(o *options) { o.expiresAt = "04/03/2027" }, "YYYY-MM-DD"},
		{"expiry before issue", func(o *options) { o.issuedAt = "2027-03-04"; o.expiresAt = "2027-03-04" }, "après"},
		{"invalid email", func(o *options) { o.email = "not-an-email" }, "email"},
		{"invalid international phone", func(o *options) { o.phone = "0800 000 000" }, "E.164"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o := validOptions()
			tt.edit(&o)
			_, _, _, _, err := validateOptions(o)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("expected error containing %q, got %v", tt.want, err)
			}
		})
	}
}

func TestGeneratedPasswordUsesAuthenticationHash(t *testing.T) {
	password, err := generatedPassword()
	if err != nil {
		t.Fatal(err)
	}
	hash, err := traderservices.HashPassword(password)
	if err != nil {
		t.Fatal(err)
	}
	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)); err != nil {
		t.Fatalf("generated password hash cannot be verified: %v", err)
	}
}
