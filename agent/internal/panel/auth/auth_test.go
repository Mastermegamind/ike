package auth

import (
	"strings"
	"testing"
	"time"
)

func TestPasswords(t *testing.T) {
	h, err := HashPassword("correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h, "pbkdf2-sha256$600000$") {
		t.Errorf("hash %s", h)
	}
	if !VerifyPassword(h, "correct horse battery") || VerifyPassword(h, "wrong horse battery") {
		t.Error("verify failed")
	}
	if VerifyPassword("garbage", "x") || VerifyPassword("pbkdf2-sha256$10$a$b", "x") {
		t.Error("bad hash accepted")
	}
	if _, err := HashPassword("short"); err == nil {
		t.Error("short password accepted")
	}
}

func TestTOTP(t *testing.T) {
	// RFC 6238 test vector: secret "12345678901234567890", T=59 → 94287082 (8 digits) → last 6: 287082
	secret := "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ"
	if c := TOTPCode(secret, time.Unix(59, 0)); c != "287082" {
		t.Fatalf("RFC vector: got %s", c)
	}
	now := time.Unix(1_800_000_000, 0)
	code := TOTPCode(secret, now)
	ctr, ok := VerifyTOTP(secret, code, now, 0)
	if !ok {
		t.Fatal("current code rejected")
	}
	if _, ok := VerifyTOTP(secret, code, now, ctr); ok {
		t.Error("replayed code accepted")
	}
	if _, ok := VerifyTOTP(secret, TOTPCode(secret, now.Add(-30*time.Second)), now, 0); !ok {
		t.Error("previous step should be accepted for clock drift")
	}
	if _, ok := VerifyTOTP(secret, TOTPCode(secret, now.Add(-5*time.Minute)), now, 0); ok {
		t.Error("old code accepted")
	}
	if !strings.HasPrefix(TOTPURL("Ike", "andre", NewTOTPSecret()), "otpauth://totp/Ike:andre?") {
		t.Error("bad otpauth URL")
	}
}

func TestThrottle(t *testing.T) {
	th := NewThrottle(3, time.Minute)
	now := time.Now()
	for i := 0; i < 3; i++ {
		th.Fail(now, "user:andre", "ip:1.2.3.4")
	}
	if b, _ := th.Blocked(now, "user:andre"); !b {
		t.Error("should be blocked")
	}
	if b, _ := th.Blocked(now.Add(2*time.Minute), "user:andre"); b {
		t.Error("should unblock after window")
	}
}
