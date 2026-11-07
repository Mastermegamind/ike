package billing

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Mastermegamind/ike/agent/internal/panel/store"
)

var inv = &store.Invoice{ID: "INV-000007", Account: "naijabites", Description: "Business plan, October", Currency: "NGN", Amount: 1500000}
var cust = Customer{Email: "ada@naijabites.ng", Name: "Ada"}

func TestAmounts(t *testing.T) {
	if Decimal(1500000, "NGN") != "15000.00" || Decimal(5, "USD") != "0.05" || Decimal(2500, "XOF") != "2500" {
		t.Error("Decimal")
	}
	for in, want := range map[string]int64{"15000.00": 1500000, "15000": 1500000, "0.5": 50, "19.999": 1999} {
		if got, _ := ParseDecimal(in, "NGN"); got != want {
			t.Errorf("ParseDecimal(%s)=%d", in, got)
		}
	}
}

func TestBachs(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/checkout-sessions" || r.Header.Get("Authorization") != "Bearer sk_sandbox_x" {
			w.WriteHeader(400)
			return
		}
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.WriteHeader(201)
		w.Write([]byte(`{"checkout_id":"chk_123","checkout_url":"https://pay.bachs.io/chk_123","status":"open"}`))
	}))
	defer srv.Close()
	now := time.Unix(1_800_000_000, 0)
	b := Bachs{SecretKey: "sk_sandbox_x", WebhookSecret: "whsec_test", BaseURL: srv.URL, Now: func() time.Time { return now }}
	c, err := b.CreateCheckout(context.Background(), inv, cust, "https://panel.example/billing")
	if err != nil || c.Ref != "chk_123" || !strings.Contains(c.URL, "chk_123") {
		t.Fatalf("%v %+v", err, c)
	}
	pr := got["pricing"].(map[string]any)
	if pr["amount"] != "15000.00" || pr["currency"] != "NGN" || got["reference"] != "INV-000007" {
		t.Errorf("request body %+v", got)
	}

	body := []byte(`{"id":"evt_1","type":"collection.succeeded","data":{"checkout_id":"chk_123","status":"SUCCEEDED","amount":"15000.00","currency":"NGN"}}`)
	ts := strconv.FormatInt(now.Unix(), 10)
	mac := hmac.New(sha256.New, []byte("whsec_test"))
	mac.Write([]byte(ts + "."))
	mac.Write(body)
	sig := hex.EncodeToString(mac.Sum(nil))

	h := http.Header{}
	h.Set("X-Bachs-Signature-V2", "t="+ts+",v1=deadbeef,v1="+sig)
	p, err := b.ParseWebhook(context.Background(), h, body)
	if err != nil || p.Ref != "chk_123" || p.Amount != 1500000 || p.EventID != "bachs:evt_1" {
		t.Fatalf("%v %+v", err, p)
	}
	h2 := http.Header{}
	h2.Set("X-Bachs-Timestamp", ts)
	h2.Set("X-Bachs-Signature", sig)
	if _, err := b.ParseWebhook(context.Background(), h2, body); err != nil {
		t.Errorf("v1 header rejected: %v", err)
	}
	h2.Set("X-Bachs-Signature", strings.Repeat("0", 64))
	if _, err := b.ParseWebhook(context.Background(), h2, body); err == nil {
		t.Error("bad signature accepted")
	}
	b.Now = func() time.Time { return now.Add(10 * time.Minute) }
	if _, err := b.ParseWebhook(context.Background(), h, body); err == nil {
		t.Error("stale delivery accepted")
	}
	b.Now = func() time.Time { return now }
	other := []byte(`{"id":"evt_2","type":"refund.created","data":{}}`)
	mac = hmac.New(sha256.New, []byte("whsec_test"))
	mac.Write([]byte(ts + "."))
	mac.Write(other)
	h.Set("X-Bachs-Signature-V2", "t="+ts+",v1="+hex.EncodeToString(mac.Sum(nil)))
	if p, err := b.ParseWebhook(context.Background(), h, other); err != nil || p != nil {
		t.Error("non-payment event should be ignored")
	}
}

func TestPaystack(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/transaction/initialize":
			b, _ := io.ReadAll(r.Body)
			if !strings.Contains(string(b), `"amount":1500000`) {
				w.WriteHeader(400)
				return
			}
			w.Write([]byte(`{"status":true,"data":{"authorization_url":"https://checkout.paystack.com/abc","reference":"INV-000007-x"}}`))
		case strings.HasPrefix(r.URL.Path, "/transaction/verify/INV-000007-x"):
			w.Write([]byte(`{"status":true,"data":{"status":"success","amount":1500000,"currency":"NGN"}}`))
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	p := Paystack{SecretKey: "sk_test_abc", BaseURL: srv.URL}
	c, err := p.CreateCheckout(context.Background(), inv, cust, "https://panel.example/billing")
	if err != nil || c.Ref != "INV-000007-x" {
		t.Fatalf("%v %+v", err, c)
	}
	body := []byte(`{"event":"charge.success","data":{"id":4099,"reference":"INV-000007-x","status":"success","amount":1}}`)
	mac := hmac.New(sha512.New, []byte("sk_test_abc"))
	mac.Write(body)
	h := http.Header{}
	h.Set("x-paystack-signature", hex.EncodeToString(mac.Sum(nil)))
	pay, err := p.ParseWebhook(context.Background(), h, body)
	if err != nil || pay.Amount != 1500000 || pay.Ref != "INV-000007-x" {
		t.Fatalf("verified amount must come from the API: %v %+v", err, pay)
	}
	h.Set("x-paystack-signature", "00")
	if _, err := p.ParseWebhook(context.Background(), h, body); err == nil {
		t.Error("bad signature accepted")
	}
}

func TestFlutterwave(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v3/payments":
			w.Write([]byte(`{"status":"success","data":{"link":"https://checkout.flutterwave.com/v3/hosted/pay/xyz"}}`))
		case r.URL.Path == "/v3/transactions/777/verify":
			w.Write([]byte(`{"status":"success","data":{"status":"successful","tx_ref":"INV-000007-y","amount":15000,"currency":"NGN"}}`))
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	f := Flutterwave{SecretKey: "FLWSECK_TEST-x", SecretHash: "my-hash", BaseURL: srv.URL}
	if _, err := f.CreateCheckout(context.Background(), inv, cust, "https://panel.example/billing"); err != nil {
		t.Fatal(err)
	}
	body := []byte(`{"event":"charge.completed","data":{"id":777,"tx_ref":"INV-000007-y","status":"successful"}}`)
	h := http.Header{}
	h.Set("verif-hash", "my-hash")
	pay, err := f.ParseWebhook(context.Background(), h, body)
	if err != nil || pay.Amount != 1500000 {
		t.Fatalf("%v %+v", err, pay)
	}
	h.Set("verif-hash", "nope")
	if _, err := f.ParseWebhook(context.Background(), h, body); err == nil {
		t.Error("bad hash accepted")
	}
}
