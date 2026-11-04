package notification

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeStore struct {
	mu      sync.Mutex
	ctx     TransactionContext
	ops     []DayOperation
	sent    bool
	records []Record
}

func (f *fakeStore) LoadTransaction(int) (*TransactionContext, error) {
	c := f.ctx
	return &c, nil
}
func (f *fakeStore) DayOperations(int, time.Time) ([]DayOperation, error) { return f.ops, nil }
func (f *fakeStore) SentSince(int, string, time.Time) (bool, error)       { return f.sent, nil }
func (f *fakeStore) LoadCustomer(int, int) (*TransactionContext, error) {
	c := f.ctx
	c.TransactionID = 0
	return &c, nil
}
func (f *fakeStore) Save(rec *Record) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.records = append(f.records, *rec)
	return nil
}

func (f *fakeStore) find(template, channel string) *Record {
	for i := range f.records {
		if f.records[i].Template == template && f.records[i].Channel == channel {
			return &f.records[i]
		}
	}
	return nil
}

func newTestService(t *testing.T, store *fakeStore) *Service {
	t.Helper()
	t.Setenv("APP_TIMEZONE", "Africa/Kinshasa")
	t.Setenv("NOTIFY_TEMPLATES_DIR", t.TempDir())
	s := NewService(store, &OutboxSender{Dir: t.TempDir()})
	s.now = func() time.Time { return time.Date(2026, 11, 3, 10, 24, 0, 0, time.UTC) }
	return s
}

func buyContext() TransactionContext {
	return TransactionContext{
		TransactionID: 42, Amount: 50, Currency: "USD", TargetCurrency: "CDF", Type: "BUY", Rate: 2261.56,
		ConvertedAmount: 113078, Status: "COMPLETED", CreatedAt: time.Date(2026, 11, 3, 9, 24, 0, 0, time.UTC),
		CustomerID: 7, CustomerName: "Patrick Mbuyi", Phone: "+243810000001", Email: "patrick@example.com",
		IDType: "PASSPORT", IDNumber: "OP123", IDExpiryDate: "2030-01-01", HasIDFront: true, HasIDBack: true,
		TraderFirstName: "Jean", TraderLastName: "Kabeya", ExchangeOffice: "Bureau Gombe", StoreLicense: "CH/0457",
	}
}

func TestConfirmationIsSentBySMSAndEmail(t *testing.T) {
	store := &fakeStore{ctx: buyContext(), ops: []DayOperation{{ID: 42, Amount: 50, Currency: "USD"}}}
	newTestService(t, store).notifyNow(42)

	sms := store.find(TemplateConfirmation, ChannelSMS)
	if sms == nil || sms.Status != StatusSent {
		t.Fatalf("SMS not sent: %+v", store.records)
	}
	want := "MOTUYA Fx.: Bonjour Patrick Mbuyi, vous avez changé 50,00 USD en 113 078 CDF au taux de 2 261,56 auprès du cambiste Jean Kabeya. Réf 000042. Merci. Service BCC"
	if sms.Body != want {
		t.Fatalf("SMS body\n got %q\nwant %q", sms.Body, want)
	}
	// Le kit l'annonce : environ 2 SMS avec des valeurs réelles, en GSM-7.
	if chars, gsm, segments := SMSInfo(sms.Body); !gsm || segments > 2 {
		t.Fatalf("SMS should fit in one GSM-7 message, got %d chars gsm=%v segments=%d", chars, gsm, segments)
	}
	if _, err := os.Stat(sms.Detail); err != nil {
		t.Fatalf("outbox file missing: %v", err)
	}

	email := store.find(TemplateConfirmation, ChannelEmail)
	if email == nil || email.Status != StatusSent || email.Subject != "Votre opération de change est confirmée" {
		t.Fatalf("email not sent: %+v", email)
	}
	for _, part := range []string{"Patrick Mbuyi", "<strong>50,00 USD</strong>", "<strong>113 078 CDF</strong>", "CH/0457", "Jean Kabeya", "50 / 5 000 USD", "/branding/motuya-mark.png"} {
		if !strings.Contains(email.Body, part) {
			t.Fatalf("email body misses %q", part)
		}
	}
	if store.find(TemplateDailyLimit, ChannelSMS) != nil {
		t.Fatal("daily limit must not be sent below the limit")
	}
}

func TestSaleSwapsWhatTheClientGivesAndGets(t *testing.T) {
	ctx := buyContext()
	ctx.Type = "SELL"
	store := &fakeStore{ctx: ctx}
	newTestService(t, store).notifyNow(42)
	if sms := store.find(TemplateConfirmation, ChannelSMS); !strings.Contains(sms.Body, "vous avez changé 113 078 CDF en 50,00 USD") {
		t.Fatalf("sale SMS: %q", sms.Body)
	}
}

func TestEmailIsSkippedWithoutAddress(t *testing.T) {
	ctx := buyContext()
	ctx.Email = ""
	store := &fakeStore{ctx: ctx}
	newTestService(t, store).notifyNow(42)
	if email := store.find(TemplateConfirmation, ChannelEmail); email == nil || email.Status != StatusSkipped {
		t.Fatalf("email should be skipped: %+v", email)
	}
}

func TestDailyLimitOnlyWhenCrossed(t *testing.T) {
	t.Setenv("DAILY_LIMIT_USD", "5000")
	ctx := buyContext()
	ctx.Amount = 1500
	store := &fakeStore{ctx: ctx, ops: []DayOperation{{ID: 40, Amount: 4000, Currency: "USD"}, {ID: 42, Amount: 1500, Currency: "USD"}}}
	newTestService(t, store).notifyNow(42)
	sms := store.find(TemplateDailyLimit, ChannelSMS)
	if sms == nil || !strings.Contains(sms.Body, "limite de change du jour (5 000 USD)") || !strings.Contains(sms.Body, "demain, 4 novembre 2026 à 00h00") {
		t.Fatalf("daily limit SMS: %+v", sms)
	}

	// Déjà au-dessus du plafond avant l'opération : pas de nouveau message.
	store2 := &fakeStore{ctx: ctx, ops: []DayOperation{{ID: 40, Amount: 6000, Currency: "USD"}, {ID: 42, Amount: 1500, Currency: "USD"}}}
	newTestService(t, store2).notifyNow(42)
	if store2.find(TemplateDailyLimit, ChannelSMS) != nil {
		t.Fatal("limit already crossed earlier: no second message")
	}
}

func TestRefusedOperationWarnsTheCustomerOncePerDay(t *testing.T) {
	t.Setenv("DAILY_LIMIT_USD", "5000")
	store := &fakeStore{ctx: buyContext()}
	s := newTestService(t, store)
	s.DailyLimitReached(7, 3, 4700)
	deadline := time.Now().Add(2 * time.Second)
	for {
		store.mu.Lock()
		n := len(store.records)
		store.mu.Unlock()
		if n >= 2 || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	sms := store.find(TemplateDailyLimit, ChannelSMS)
	if sms == nil || sms.TransactionID != nil || !strings.Contains(sms.Body, "limite de change du jour (5 000 USD)") {
		t.Fatalf("refusal SMS: %+v", store.records)
	}
	email := store.find(TemplateDailyLimit, ChannelEmail)
	if email == nil || !strings.Contains(email.Body, "4 700 / 5 000 USD") {
		t.Fatal("refusal e-mail should show the day total")
	}

	already := &fakeStore{ctx: buyContext(), sent: true}
	s2 := newTestService(t, already)
	s2.DailyLimitReached(7, 3, 4700)
	time.Sleep(100 * time.Millisecond)
	if len(already.records) != 0 {
		t.Fatal("already warned today: no second message")
	}
}

func TestDossierReminder(t *testing.T) {
	ctx := buyContext()
	ctx.HasIDBack = false
	store := &fakeStore{ctx: ctx}
	s := newTestService(t, store)
	s.notifyDossier(42)
	sms := store.find(TemplateDossier, ChannelSMS)
	if sms == nil || !strings.Contains(sms.Body, "la photo du verso de votre pièce d'identité") {
		t.Fatalf("dossier SMS: %+v", store.records)
	}

	complete := &fakeStore{ctx: buyContext()}
	newTestService(t, complete).notifyDossier(42)
	if len(complete.records) != 0 {
		t.Fatal("complete file: no reminder")
	}

	recent := &fakeStore{ctx: ctx, sent: true}
	newTestService(t, recent).notifyDossier(42)
	if len(recent.records) != 0 {
		t.Fatal("reminder already sent this week: no new message")
	}
}

func TestMissingDocument(t *testing.T) {
	now := time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC)
	ctx := buyContext()
	ctx.IDNumber = ""
	if got := MissingDocument(&ctx, now); got != "votre pièce d'identité" {
		t.Fatalf("no ID: %q", got)
	}
	ctx = buyContext()
	ctx.IDExpiryDate = "2025-01-01"
	if got := MissingDocument(&ctx, now); !strings.Contains(got, "en cours de validité") {
		t.Fatalf("expired: %q", got)
	}
}

func TestPendingTransactionIsNotConfirmed(t *testing.T) {
	ctx := buyContext()
	ctx.Status = "PENDING"
	store := &fakeStore{ctx: ctx}
	newTestService(t, store).notifyNow(42)
	if len(store.records) != 0 {
		t.Fatalf("pending: no message, got %+v", store.records)
	}
}

func TestTemplateOverrideFromKitFolder(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "sms"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sms", "confirmation-change.txt"), []byte("Bonjour {{ .Civility }} {{ .ClientName }}, réf {{ .Reference }} {{ .Unknown }}"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("NOTIFY_TEMPLATES_DIR", dir)
	r, err := Render(TemplateConfirmation, Data{"ClientName": "Patrick", "Reference": "000042"})
	if err != nil {
		t.Fatal(err)
	}
	if r.SMS != "Bonjour Patrick, réf 000042" {
		t.Fatalf("override: %q", r.SMS)
	}
}

func TestFormatting(t *testing.T) {
	cases := map[string]string{
		FormatAmount(113078.4, "CDF"):  "113 078",
		FormatAmount(1234567.5, "USD"): "1 234 567,50",
		FormatRate(2261.56):            "2 261,56",
		FormatRate(0.000361):           "0,000361",
		FormatReference(42):            "000042",
	}
	for got, want := range cases {
		if got != want {
			t.Errorf("got %q want %q", got, want)
		}
	}
	if _, err := NormalizeEmail("pas-une-adresse"); err == nil {
		t.Error("invalid e-mail accepted")
	}
	if got, err := NormalizeEmail(" Patrick@Example.com "); err != nil || got != "patrick@example.com" {
		t.Errorf("e-mail: %q %v", got, err)
	}
}
