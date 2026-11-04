package notification

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSendTwilioPostsFormAndReadsErrors(t *testing.T) {
	var got struct{ path, user, to, from, body string }
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.path = r.URL.Path
		got.user, _, _ = r.BasicAuth()
		_ = r.ParseForm()
		got.to, got.from, got.body = r.Form.Get("To"), r.Form.Get("From"), r.Form.Get("Body")
		if strings.Contains(got.to, "999") {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"code":21211,"message":"Invalid 'To' Phone Number"}`))
			return
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"sid":"SM123"}`))
	}))
	defer server.Close()
	old := twilioAPI
	twilioAPI = server.URL
	defer func() { twilioAPI = old }()

	s := Settings{TwilioAccountSID: "AC1", TwilioAuthToken: "tok", TwilioFrom: "+15550001", TwilioWhatsAppFrom: "+15550002"}
	id, err := SendTwilio(s, "whatsapp", "+243810000001", "Bonjour")
	if err != nil || id != "SM123" {
		t.Fatalf("id=%q err=%v", id, err)
	}
	if got.path != "/2010-04-01/Accounts/AC1/Messages.json" || got.user != "AC1" ||
		got.to != "whatsapp:+243810000001" || got.from != "whatsapp:+15550002" || got.body != "Bonjour" {
		t.Fatalf("requête inattendue : %+v", got)
	}
	if _, err := SendTwilio(s, "sms", "+243999", "x"); err == nil || !strings.Contains(err.Error(), "21211") {
		t.Fatalf("erreur Twilio attendue, reçu %v", err)
	}
	if _, err := SendTwilio(Settings{}, "sms", "+243810000001", "x"); err == nil {
		t.Fatal("Twilio non configuré : erreur attendue")
	}
}

func TestSendLiveUsesEnabledChannels(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"sid":"SM9"}`))
	}))
	defer server.Close()
	old := twilioAPI
	twilioAPI = server.URL
	defer func() { twilioAPI = old }()

	s := Settings{Mode: ModeLive, SMSEnabled: true, WhatsAppEnabled: true,
		TwilioAccountSID: "AC1", TwilioAuthToken: "tok", TwilioFrom: "+1555", TwilioWhatsAppFrom: "+1556"}
	location, err := sendLive(s, Message{Channel: ChannelSMS, To: "+243810000001", Body: "x"})
	if err != nil || calls != 2 || !strings.Contains(location, "sms:SM9") || !strings.Contains(location, "whatsapp:SM9") {
		t.Fatalf("location=%q err=%v calls=%d", location, err, calls)
	}
	s.SMSEnabled, s.WhatsAppEnabled = false, false
	if _, err := sendLive(s, Message{Channel: ChannelSMS, To: "+243810000001"}); err == nil {
		t.Fatal("canaux désactivés : erreur attendue")
	}
	if _, err := sendLive(s, Message{Channel: ChannelEmail, To: "a@b.cd"}); err == nil {
		t.Fatal("e-mail désactivé : erreur attendue")
	}
}
