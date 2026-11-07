// Package billing connects invoices to payment gateways popular with African
// hosting customers: Bachs (bachs.io), Paystack and Flutterwave.
//
// Every provider does two things: create a hosted checkout for an invoice, and
// verify an incoming webhook. A webhook is only trusted after its signature
// checks out; Paystack and Flutterwave payments are also re-verified with the
// provider's API before an invoice is marked paid.
package billing

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Mastermegamind/ike/agent/internal/panel/store"
)

// Checkout is a hosted payment page for one invoice.
type Checkout struct {
	URL string // send the customer here
	Ref string // provider reference, matched against webhooks
}

// Payment is a verified "this was paid" event.
type Payment struct {
	EventID  string // for de-duplication
	Ref      string // matches Checkout.Ref
	Amount   int64  // minor units
	Currency string
}

// Customer is who pays.
type Customer struct {
	Email string
	Name  string
}

type Provider interface {
	Name() string
	CreateCheckout(ctx context.Context, inv *store.Invoice, c Customer, returnURL string) (*Checkout, error)
	// ParseWebhook verifies the request and returns a payment, nil for events
	// that aren't successful payments, or an error for bad signatures.
	ParseWebhook(ctx context.Context, header http.Header, body []byte) (*Payment, error)
}

// Transfer is a temporary bank account to pay one invoice into. The
// provider's usual webhook confirms the payment against Ref.
type Transfer struct {
	Ref           string
	Bank          string
	AccountNumber string
	AccountName   string
	Amount        int64 // minor units; the customer sends exactly this
	Currency      string
	Expires       time.Time
	Note          string
}

// TransferProvider can give an account number for a bank transfer (Paystack
// "Pay with Transfer", Flutterwave bank transfer).
type TransferProvider interface {
	CreateTransfer(ctx context.Context, inv *store.Invoice, c Customer) (*Transfer, error)
}

var ErrBadSignature = errors.New("webhook signature is invalid")

// Minor-unit exponent per currency (ISO 4217). Default 2.
var exponent = map[string]int{"XOF": 0, "XAF": 0, "UGX": 0, "RWF": 0, "JPY": 0, "KRW": 0}

func exp(cur string) int {
	if e, ok := exponent[strings.ToUpper(cur)]; ok {
		return e
	}
	return 2
}

// Decimal formats minor units as "5000.00".
func Decimal(amount int64, cur string) string {
	e := exp(cur)
	if e == 0 {
		return strconv.FormatInt(amount, 10)
	}
	d := int64(math.Pow10(e))
	return fmt.Sprintf("%d.%0*d", amount/d, e, amount%d)
}

// ParseDecimal turns "5000.00" (or a JSON number) into minor units.
func ParseDecimal(v any, cur string) (int64, error) {
	var s string
	switch x := v.(type) {
	case string:
		s = x
	case float64:
		s = strconv.FormatFloat(x, 'f', -1, 64)
	case json.Number:
		s = x.String()
	default:
		return 0, fmt.Errorf("unexpected amount %v", v)
	}
	e := exp(cur)
	whole, frac, _ := strings.Cut(strings.TrimSpace(s), ".")
	for len(frac) < e {
		frac += "0"
	}
	if len(frac) > e {
		frac = frac[:e]
	}
	n, err := strconv.ParseInt(whole+frac, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("bad amount %q", s)
	}
	return n, nil
}

type httpDoer struct {
	client *http.Client
}

func (h httpDoer) do(ctx context.Context, method, url, bearer string, body any, out any) error {
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+bearer)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	c := h.client
	if c == nil {
		c = &http.Client{Timeout: 30 * time.Second}
	}
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 300 {
		msg := strings.TrimSpace(string(data))
		if len(msg) > 300 {
			msg = msg[:300]
		}
		return fmt.Errorf("payment provider returned HTTP %d: %s", resp.StatusCode, msg)
	}
	if out != nil {
		d := json.NewDecoder(bytes.NewReader(data))
		d.UseNumber()
		return d.Decode(out)
	}
	return nil
}

// ---------------------------------------------------------------- Bachs ----

// Bachs: POST /v1/checkout-sessions with ad-hoc pricing; webhooks signed with
// HMAC-SHA256 over "{timestamp}.{raw body}" (hex), header X-Bachs-Signature-V2
// "t=…,v1=…" or X-Bachs-Signature + X-Bachs-Timestamp. Deliveries older than
// five minutes are rejected as replays.
type Bachs struct {
	SecretKey     string
	WebhookSecret string
	BaseURL       string // https://api.bachs.io or https://sandbox-api.bachs.io
	HTTP          *http.Client
	Now           func() time.Time
}

func (Bachs) Name() string { return "bachs" }

func (b Bachs) base() string {
	if b.BaseURL != "" {
		return strings.TrimRight(b.BaseURL, "/")
	}
	if strings.HasPrefix(b.SecretKey, "sk_sandbox_") {
		return "https://sandbox-api.bachs.io"
	}
	return "https://api.bachs.io"
}

func (b Bachs) CreateCheckout(ctx context.Context, inv *store.Invoice, c Customer, returnURL string) (*Checkout, error) {
	body := map[string]any{
		"pricing":            map[string]any{"currency": inv.Currency, "amount": Decimal(inv.Amount, inv.Currency)},
		"customer":           map[string]any{"email": c.Email, "name": c.Name},
		"success_url":        returnURL,
		"cancel_url":         returnURL,
		"reference":          inv.ID,
		"metadata":           map[string]string{"invoice_id": inv.ID, "account": inv.Account},
		"expires_in_minutes": 1440,
	}
	if c.Name == "" {
		delete(body["customer"].(map[string]any), "name")
	}
	var out struct {
		CheckoutID  string `json:"checkout_id"`
		CheckoutURL string `json:"checkout_url"`
	}
	if err := (httpDoer{b.HTTP}).do(ctx, "POST", b.base()+"/v1/checkout-sessions", b.SecretKey, body, &out); err != nil {
		return nil, err
	}
	if out.CheckoutURL == "" {
		return nil, errors.New("Bachs didn't return a checkout link")
	}
	return &Checkout{URL: out.CheckoutURL, Ref: out.CheckoutID}, nil
}

func (b Bachs) ParseWebhook(_ context.Context, h http.Header, body []byte) (*Payment, error) {
	now := time.Now
	if b.Now != nil {
		now = b.Now
	}
	ts := h.Get("X-Bachs-Timestamp")
	var sigs []string
	if v2 := h.Get("X-Bachs-Signature-V2"); v2 != "" {
		for _, part := range strings.Split(v2, ",") {
			k, v, _ := strings.Cut(strings.TrimSpace(part), "=")
			switch k {
			case "t":
				ts = v
			case "v1":
				sigs = append(sigs, v)
			}
		}
	} else if s := h.Get("X-Bachs-Signature"); s != "" {
		sigs = []string{s}
	}
	t, err := strconv.ParseInt(ts, 10, 64)
	if err != nil || len(sigs) == 0 {
		return nil, ErrBadSignature
	}
	if d := now().Unix() - t; d > 300 || d < -300 {
		return nil, fmt.Errorf("%w: delivery is older than 5 minutes", ErrBadSignature)
	}
	mac := hmac.New(sha256.New, []byte(b.WebhookSecret))
	mac.Write([]byte(ts + "."))
	mac.Write(body)
	want := hex.EncodeToString(mac.Sum(nil))
	okSig := false
	for _, s := range sigs {
		if subtle.ConstantTimeCompare([]byte(strings.ToLower(s)), []byte(want)) == 1 {
			okSig = true
		}
	}
	if !okSig {
		return nil, ErrBadSignature
	}
	var ev struct {
		ID   string `json:"id"`
		Type string `json:"type"`
		Data struct {
			CheckoutID string `json:"checkout_id"`
			Status     string `json:"status"`
			Amount     any    `json:"amount"`
			Currency   string `json:"currency"`
		} `json:"data"`
	}
	d := json.NewDecoder(bytes.NewReader(body))
	d.UseNumber()
	if err := d.Decode(&ev); err != nil {
		return nil, err
	}
	// checkout.completed and collection.succeeded can arrive in either order; both mean paid.
	if ev.Type != "collection.succeeded" && ev.Type != "checkout.completed" {
		return nil, nil
	}
	if ev.Type == "collection.succeeded" && !strings.EqualFold(ev.Data.Status, "succeeded") {
		return nil, nil
	}
	p := &Payment{EventID: "bachs:" + ev.ID, Ref: ev.Data.CheckoutID, Currency: strings.ToUpper(ev.Data.Currency)}
	if ev.Data.Amount != nil && p.Currency != "" {
		p.Amount, _ = ParseDecimal(ev.Data.Amount, p.Currency)
	} else {
		p.Amount = -1 // amount not in this event; matched by checkout id alone
	}
	return p, nil
}

// ------------------------------------------------------------- Paystack ----

// Paystack: POST /transaction/initialize (amount in minor units); webhooks
// signed with HMAC-SHA512 of the raw body using the secret key, header
// x-paystack-signature. Payments are re-verified with /transaction/verify.
type Paystack struct {
	SecretKey string
	BaseURL   string // https://api.paystack.co
	HTTP      *http.Client
}

func (Paystack) Name() string { return "paystack" }

func (p Paystack) base() string {
	if p.BaseURL != "" {
		return strings.TrimRight(p.BaseURL, "/")
	}
	return "https://api.paystack.co"
}

func (p Paystack) CreateCheckout(ctx context.Context, inv *store.Invoice, c Customer, returnURL string) (*Checkout, error) {
	ref := inv.ID + "-" + strconv.FormatInt(time.Now().Unix(), 36)
	var out struct {
		Status bool `json:"status"`
		Data   struct {
			AuthorizationURL string `json:"authorization_url"`
			Reference        string `json:"reference"`
		} `json:"data"`
		Message string `json:"message"`
	}
	body := map[string]any{"email": c.Email, "amount": inv.Amount, "currency": inv.Currency, "reference": ref,
		"callback_url": returnURL, "metadata": map[string]string{"invoice_id": inv.ID, "account": inv.Account}}
	if err := (httpDoer{p.HTTP}).do(ctx, "POST", p.base()+"/transaction/initialize", p.SecretKey, body, &out); err != nil {
		return nil, err
	}
	if !out.Status || out.Data.AuthorizationURL == "" {
		return nil, fmt.Errorf("Paystack: %s", out.Message)
	}
	return &Checkout{URL: out.Data.AuthorizationURL, Ref: out.Data.Reference}, nil
}

// CreateTransfer asks Paystack for a temporary account (valid 8 hours).
func (p Paystack) CreateTransfer(ctx context.Context, inv *store.Invoice, c Customer) (*Transfer, error) {
	ref := inv.ID + "-t" + strconv.FormatInt(time.Now().Unix(), 36)
	expires := time.Now().Add(8 * time.Hour).UTC()
	var out struct {
		Status  bool   `json:"status"`
		Message string `json:"message"`
		Data    struct {
			Reference     string `json:"reference"`
			Status        string `json:"status"`
			DisplayText   string `json:"display_text"`
			AccountName   string `json:"account_name"`
			AccountNumber string `json:"account_number"`
			Bank          struct {
				Name string `json:"name"`
			} `json:"bank"`
			ExpiresAt string `json:"account_expires_at"`
		} `json:"data"`
	}
	body := map[string]any{"email": c.Email, "amount": inv.Amount, "currency": inv.Currency, "reference": ref,
		"bank_transfer": map[string]string{"account_expires_at": expires.Format(time.RFC3339)},
		"metadata":      map[string]string{"invoice_id": inv.ID, "account": inv.Account}}
	if err := (httpDoer{p.HTTP}).do(ctx, "POST", p.base()+"/charge", p.SecretKey, body, &out); err != nil {
		return nil, err
	}
	if !out.Status || out.Data.AccountNumber == "" {
		return nil, fmt.Errorf("Paystack: %s", out.Message)
	}
	t := &Transfer{Ref: out.Data.Reference, Bank: out.Data.Bank.Name, AccountNumber: out.Data.AccountNumber, AccountName: out.Data.AccountName,
		Amount: inv.Amount, Currency: inv.Currency, Expires: expires, Note: out.Data.DisplayText}
	if t.Ref == "" {
		t.Ref = ref
	}
	if e, err := time.Parse(time.RFC3339, out.Data.ExpiresAt); err == nil {
		t.Expires = e
	}
	return t, nil
}

func (p Paystack) ParseWebhook(ctx context.Context, h http.Header, body []byte) (*Payment, error) {
	mac := hmac.New(sha512.New, []byte(p.SecretKey))
	mac.Write(body)
	want := hex.EncodeToString(mac.Sum(nil))
	if subtle.ConstantTimeCompare([]byte(strings.ToLower(h.Get("x-paystack-signature"))), []byte(want)) != 1 {
		return nil, ErrBadSignature
	}
	var ev struct {
		Event string `json:"event"`
		Data  struct {
			ID        json.Number `json:"id"`
			Reference string      `json:"reference"`
			Status    string      `json:"status"`
		} `json:"data"`
	}
	d := json.NewDecoder(bytes.NewReader(body))
	d.UseNumber()
	if err := d.Decode(&ev); err != nil {
		return nil, err
	}
	if ev.Event != "charge.success" {
		return nil, nil
	}
	// Ask Paystack directly rather than trusting the payload's amount.
	var v struct {
		Data struct {
			Status   string `json:"status"`
			Amount   int64  `json:"amount"`
			Currency string `json:"currency"`
		} `json:"data"`
	}
	if err := (httpDoer{p.HTTP}).do(ctx, "GET", p.base()+"/transaction/verify/"+ev.Data.Reference, p.SecretKey, nil, &v); err != nil {
		return nil, err
	}
	if v.Data.Status != "success" {
		return nil, nil
	}
	return &Payment{EventID: "paystack:" + ev.Data.ID.String(), Ref: ev.Data.Reference, Amount: v.Data.Amount, Currency: strings.ToUpper(v.Data.Currency)}, nil
}

// ---------------------------------------------------------- Flutterwave ----

// Flutterwave (v3): POST /v3/payments returns a hosted link; webhooks carry the
// "secret hash" you set in the dashboard in the verif-hash header. Payments are
// re-verified with /v3/transactions/{id}/verify.
type Flutterwave struct {
	SecretKey  string
	SecretHash string
	BaseURL    string // https://api.flutterwave.com
	HTTP       *http.Client
}

func (Flutterwave) Name() string { return "flutterwave" }

func (f Flutterwave) base() string {
	if f.BaseURL != "" {
		return strings.TrimRight(f.BaseURL, "/")
	}
	return "https://api.flutterwave.com"
}

func (f Flutterwave) CreateCheckout(ctx context.Context, inv *store.Invoice, c Customer, returnURL string) (*Checkout, error) {
	ref := inv.ID + "-" + strconv.FormatInt(time.Now().Unix(), 36)
	amt, _ := strconv.ParseFloat(Decimal(inv.Amount, inv.Currency), 64)
	body := map[string]any{
		"tx_ref": ref, "amount": amt, "currency": inv.Currency, "redirect_url": returnURL,
		"customer":       map[string]string{"email": c.Email, "name": c.Name},
		"customizations": map[string]string{"title": "Hosting invoice " + inv.ID, "description": inv.Description},
		"meta":           map[string]string{"invoice_id": inv.ID, "account": inv.Account},
	}
	var out struct {
		Status  string `json:"status"`
		Message string `json:"message"`
		Data    struct {
			Link string `json:"link"`
		} `json:"data"`
	}
	if err := (httpDoer{f.HTTP}).do(ctx, "POST", f.base()+"/v3/payments", f.SecretKey, body, &out); err != nil {
		return nil, err
	}
	if out.Data.Link == "" {
		return nil, fmt.Errorf("Flutterwave: %s", out.Message)
	}
	return &Checkout{URL: out.Data.Link, Ref: ref}, nil
}

// CreateTransfer asks Flutterwave for a one-time account number.
func (f Flutterwave) CreateTransfer(ctx context.Context, inv *store.Invoice, c Customer) (*Transfer, error) {
	ref := inv.ID + "-t" + strconv.FormatInt(time.Now().Unix(), 36)
	amt, _ := strconv.ParseFloat(Decimal(inv.Amount, inv.Currency), 64)
	body := map[string]any{"tx_ref": ref, "amount": amt, "currency": inv.Currency, "email": c.Email, "fullname": c.Name,
		"is_permanent": false, "narration": "Hosting invoice " + inv.ID, "meta": map[string]string{"invoice_id": inv.ID, "account": inv.Account}}
	var out struct {
		Status  string `json:"status"`
		Message string `json:"message"`
		Meta    struct {
			Authorization struct {
				Account    string `json:"transfer_account"`
				Bank       string `json:"transfer_bank"`
				Expiration string `json:"account_expiration"`
				Note       string `json:"transfer_note"`
				Amount     any    `json:"transfer_amount"`
			} `json:"authorization"`
		} `json:"meta"`
	}
	if err := (httpDoer{f.HTTP}).do(ctx, "POST", f.base()+"/v3/charges?type=bank_transfer", f.SecretKey, body, &out); err != nil {
		return nil, err
	}
	a := out.Meta.Authorization
	if a.Account == "" {
		return nil, fmt.Errorf("Flutterwave: %s", out.Message)
	}
	t := &Transfer{Ref: ref, Bank: a.Bank, AccountNumber: a.Account, AccountName: "Flutterwave",
		Amount: inv.Amount, Currency: inv.Currency, Expires: time.Now().Add(time.Hour).UTC(), Note: a.Note}
	if n, err := ParseDecimal(a.Amount, inv.Currency); err == nil && n > 0 {
		t.Amount = n // Flutterwave may add its fee
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02 15:04:05"} {
		if e, err := time.Parse(layout, a.Expiration); err == nil {
			t.Expires = e.UTC()
			break
		}
	}
	return t, nil
}

func (f Flutterwave) ParseWebhook(ctx context.Context, h http.Header, body []byte) (*Payment, error) {
	if f.SecretHash == "" || subtle.ConstantTimeCompare([]byte(h.Get("verif-hash")), []byte(f.SecretHash)) != 1 {
		return nil, ErrBadSignature
	}
	var ev struct {
		Event string `json:"event"`
		Data  struct {
			ID     json.Number `json:"id"`
			TxRef  string      `json:"tx_ref"`
			Status string      `json:"status"`
		} `json:"data"`
	}
	d := json.NewDecoder(bytes.NewReader(body))
	d.UseNumber()
	if err := d.Decode(&ev); err != nil {
		return nil, err
	}
	if ev.Event != "charge.completed" || ev.Data.Status != "successful" {
		return nil, nil
	}
	var v struct {
		Data struct {
			Status   string `json:"status"`
			TxRef    string `json:"tx_ref"`
			Amount   any    `json:"amount"`
			Currency string `json:"currency"`
		} `json:"data"`
	}
	if err := (httpDoer{f.HTTP}).do(ctx, "GET", f.base()+"/v3/transactions/"+ev.Data.ID.String()+"/verify", f.SecretKey, nil, &v); err != nil {
		return nil, err
	}
	if v.Data.Status != "successful" || v.Data.TxRef != ev.Data.TxRef {
		return nil, nil
	}
	amt, err := ParseDecimal(v.Data.Amount, v.Data.Currency)
	if err != nil {
		return nil, err
	}
	return &Payment{EventID: "flutterwave:" + ev.Data.ID.String(), Ref: v.Data.TxRef, Amount: amt, Currency: strings.ToUpper(v.Data.Currency)}, nil
}

// FromSettings builds the enabled providers.
func FromSettings(s store.BillingSettings) map[string]Provider {
	out := map[string]Provider{}
	if p, ok := s.Providers["bachs"]; ok && p.Enabled && p.SecretKey != "" {
		b := Bachs{SecretKey: p.SecretKey, WebhookSecret: p.WebhookSecret}
		if p.Sandbox {
			b.BaseURL = "https://sandbox-api.bachs.io"
		}
		out["bachs"] = b
	}
	if p, ok := s.Providers["paystack"]; ok && p.Enabled && p.SecretKey != "" {
		out["paystack"] = Paystack{SecretKey: p.SecretKey}
	}
	if p, ok := s.Providers["flutterwave"]; ok && p.Enabled && p.SecretKey != "" {
		out["flutterwave"] = Flutterwave{SecretKey: p.SecretKey, SecretHash: p.WebhookSecret}
	}
	return out
}
