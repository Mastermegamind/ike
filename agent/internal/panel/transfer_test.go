package panel

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Mastermegamind/ike/agent/internal/panel/billing"
	"github.com/Mastermegamind/ike/agent/internal/panel/store"
)

// Pay by bank transfer, then open a card checkout and abandon it: the
// transfer still settles the invoice.
func TestBankTransfer(t *testing.T) {
	e := setup(t)
	charges := 0
	var chargeBody map[string]any
	paystackAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/charge":
			charges++
			b, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(b, &chargeBody)
			w.Write([]byte(`{"status":true,"message":"Charge attempted","data":{"reference":"TRF-1","status":"pending_bank_transfer",
				"display_text":"Please make a transfer to the account specified","account_name":"PAYSTACK-TITAN","account_number":"9912345678",
				"bank":{"name":"Paystack-Titan","slug":"titan"},"account_expires_at":"2099-01-01T08:00:00Z"}}`))
		case r.URL.Path == "/transaction/initialize":
			w.Write([]byte(`{"status":true,"data":{"authorization_url":"https://checkout.paystack.com/y","reference":"CARD-1"}}`))
		case strings.HasPrefix(r.URL.Path, "/transaction/verify/TRF-1"):
			w.Write([]byte(`{"data":{"status":"success","amount":750000,"currency":"NGN"}}`))
		}
	}))
	defer paystackAPI.Close()
	e.srv.Providers = func() map[string]billing.Provider {
		return map[string]billing.Provider{"paystack": billing.Paystack{SecretKey: "sk_test_k", BaseURL: paystackAPI.URL}}
	}
	admin := e.browser(e.admin)
	admin.login("marvellous", "admin-password-1", "")
	admin.do("POST", "/api/v1/accounts", map[string]string{"name": "adaeze"}, nil)
	var inv store.Invoice
	admin.do("POST", "/api/v1/billing/invoices", map[string]any{"account": "adaeze", "description": "Starter", "amount": "7500.00", "due_in_days": 3}, &inv)

	var bt store.BankTransfer
	if code := admin.do("POST", "/api/v1/billing/invoices/"+inv.ID+"/transfer", map[string]string{"provider": "paystack"}, &bt); code != 200 {
		t.Fatalf("transfer: %d", code)
	}
	if bt.AccountNumber != "9912345678" || bt.Bank != "Paystack-Titan" || bt.Amount != 750000 || bt.Expires.Year() != 2099 {
		t.Fatalf("%+v", bt)
	}
	if chargeBody["amount"].(float64) != 750000 || chargeBody["bank_transfer"] == nil {
		t.Fatalf("charge request %+v", chargeBody)
	}
	// asking again shows the same account
	admin.do("POST", "/api/v1/billing/invoices/"+inv.ID+"/transfer", map[string]string{"provider": "paystack"}, &bt)
	if charges != 1 {
		t.Fatalf("%d charges", charges)
	}
	// a card checkout afterwards doesn't orphan the transfer
	admin.do("POST", "/api/v1/billing/invoices/"+inv.ID+"/checkout", map[string]string{"provider": "paystack"}, nil)
	body := []byte(`{"event":"charge.success","data":{"id":4412,"reference":"TRF-1","status":"success"}}`)
	mac := hmac.New(sha512.New, []byte("sk_test_k"))
	mac.Write(body)
	req, _ := http.NewRequest("POST", e.panel.URL+"/webhooks/paystack", bytes.NewReader(body))
	req.Header.Set("x-paystack-signature", hex.EncodeToString(mac.Sum(nil)))
	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("webhook: %v %v", resp, err)
	}
	var invs []store.Invoice
	admin.do("GET", "/api/v1/billing/invoices", nil, &invs)
	if invs[0].Status != "paid" {
		t.Fatalf("not paid: %+v", invs[0])
	}
	// no transfer for a paid invoice, or without a provider that supports it
	if code := admin.do("POST", "/api/v1/billing/invoices/"+inv.ID+"/transfer", map[string]string{"provider": "paystack"}, nil); code != 400 {
		t.Fatalf("paid invoice: %d", code)
	}
}
