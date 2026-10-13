// Package auth holds password hashing, TOTP two-factor codes and login
// throttling for the Ike control panel. Standard library only.
package auth

import (
	"crypto/hmac"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Passwords are hashed with PBKDF2-HMAC-SHA256 (OWASP 2023: 600,000 iterations).
// Format: pbkdf2-sha256$<iterations>$<salt b64>$<hash b64>
const iterations = 600_000

func HashPassword(pw string) (string, error) {
	if err := CheckPasswordStrength(pw); err != nil {
		return "", err
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key, err := pbkdf2.Key(sha256.New, pw, salt, iterations, 32)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("pbkdf2-sha256$%d$%s$%s", iterations,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key)), nil
}

func VerifyPassword(hash, pw string) bool {
	parts := strings.Split(hash, "$")
	if len(parts) != 4 || parts[0] != "pbkdf2-sha256" {
		return false
	}
	n, err := strconv.Atoi(parts[1])
	if err != nil || n < 100_000 {
		return false
	}
	salt, err1 := base64.RawStdEncoding.DecodeString(parts[2])
	want, err2 := base64.RawStdEncoding.DecodeString(parts[3])
	if err1 != nil || err2 != nil {
		return false
	}
	got, err := pbkdf2.Key(sha256.New, pw, salt, n, len(want))
	return err == nil && subtle.ConstantTimeCompare(got, want) == 1
}

// CheckPasswordStrength enforces a sane minimum without silly composition rules.
func CheckPasswordStrength(pw string) error {
	if len(pw) < 10 {
		return errors.New("use a password of at least 10 characters")
	}
	if len(pw) > 256 {
		return errors.New("password is too long")
	}
	common := []string{"password12", "1234567890", "qwertyuiop", "administrator", "password123"}
	for _, c := range common {
		if strings.EqualFold(pw, c) {
			return errors.New("that password is too common")
		}
	}
	return nil
}

// ------------------------------------------------------------------ TOTP --

// NewTOTPSecret returns a random base32 secret (160 bits, as RFC 4226 recommends).
func NewTOTPSecret() string {
	b := make([]byte, 20)
	_, _ = rand.Read(b)
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b)
}

// TOTPURL is what authenticator apps scan (shown as a QR code).
func TOTPURL(issuer, account, secret string) string {
	v := url.Values{}
	v.Set("secret", secret)
	v.Set("issuer", issuer)
	v.Set("algorithm", "SHA1")
	v.Set("digits", "6")
	v.Set("period", "30")
	return "otpauth://totp/" + url.PathEscape(issuer+":"+account) + "?" + v.Encode()
}

func totpAt(secret string, counter uint64) (string, error) {
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.ToUpper(strings.ReplaceAll(secret, " ", "")))
	if err != nil {
		return "", err
	}
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], counter)
	h := hmac.New(sha1.New, key)
	h.Write(msg[:])
	sum := h.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	code := (binary.BigEndian.Uint32(sum[off:off+4]) & 0x7fffffff) % 1_000_000
	return fmt.Sprintf("%06d", code), nil
}

// TOTPCode returns the current code (tests and setup screens).
func TOTPCode(secret string, t time.Time) string {
	c, _ := totpAt(secret, uint64(t.Unix()/30))
	return c
}

// VerifyTOTP accepts the code for now and one step either side (clock drift).
// lastUsed prevents replaying a code: pass the counter of the last accepted
// code and store the returned counter.
func VerifyTOTP(secret, code string, now time.Time, lastUsed uint64) (uint64, bool) {
	code = strings.TrimSpace(code)
	if len(code) != 6 {
		return 0, false
	}
	cur := uint64(now.Unix() / 30)
	for _, c := range []uint64{cur - 1, cur, cur + 1} {
		if c <= lastUsed {
			continue
		}
		want, err := totpAt(secret, c)
		if err == nil && subtle.ConstantTimeCompare([]byte(want), []byte(code)) == 1 {
			return c, true
		}
	}
	return 0, false
}

// NewRecoveryCodes returns n one-time codes like "k3f9-2m7q".
func NewRecoveryCodes(n int) []string {
	const alpha = "abcdefghjkmnpqrstuvwxyz23456789"
	out := make([]string, n)
	for i := range out {
		b := make([]byte, 8)
		_, _ = rand.Read(b)
		for j := range b {
			b[j] = alpha[int(b[j])%len(alpha)]
		}
		out[i] = string(b[:4]) + "-" + string(b[4:])
	}
	return out
}

// HashToken is a fast hash for random secrets (session tokens, recovery codes).
func HashToken(t string) string {
	h := sha256.Sum256([]byte(t))
	return base64.RawStdEncoding.EncodeToString(h[:])
}

// RandomToken returns a URL-safe random string with n bytes of entropy.
func RandomToken(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// -------------------------------------------------------------- throttle --

// Throttle slows down password guessing: after Max failures within Window for
// a key (username or IP), logins for that key are refused until Window passes.
type Throttle struct {
	Max    int
	Window time.Duration
	mu     sync.Mutex
	fails  map[string][]time.Time
}

func NewThrottle(max int, window time.Duration) *Throttle {
	return &Throttle{Max: max, Window: window, fails: map[string][]time.Time{}}
}

func (t *Throttle) prune(key string, now time.Time) []time.Time {
	var keep []time.Time
	for _, f := range t.fails[key] {
		if now.Sub(f) < t.Window {
			keep = append(keep, f)
		}
	}
	if len(keep) == 0 {
		delete(t.fails, key)
	} else {
		t.fails[key] = keep
	}
	return keep
}

// Blocked reports whether any key is over the limit, and for how long.
func (t *Throttle) Blocked(now time.Time, keys ...string) (bool, time.Duration) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, k := range keys {
		f := t.prune(k, now)
		if len(f) >= t.Max {
			return true, t.Window - now.Sub(f[0])
		}
	}
	return false, 0
}

func (t *Throttle) Fail(now time.Time, keys ...string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, k := range keys {
		t.fails[k] = append(t.prune(k, now), now)
	}
}

func (t *Throttle) Reset(keys ...string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, k := range keys {
		delete(t.fails, k)
	}
}
