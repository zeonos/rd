package auth

import (
	"testing"
	"time"
)

func TestPassword(t *testing.T) {
	h, err := HashPassword("correct horse")
	if err != nil {
		t.Fatal(err)
	}
	if ok, _ := CheckPassword(h, "correct horse"); !ok {
		t.Fatal("right password rejected")
	}
	if ok, _ := CheckPassword(h, "wrong"); ok {
		t.Fatal("wrong password accepted")
	}
	if CheckPasswordOrDummy("", "x") {
		t.Fatal("empty hash accepted")
	}
}

// RFC 6238 appendix B test vector (SHA-1, 8 digits truncated to 6).
func TestTOTPVector(t *testing.T) {
	// secret "12345678901234567890" in base32
	secret := "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ"
	code, err := TOTPCodeAt(secret, time.Unix(59, 0))
	if err != nil {
		t.Fatal(err)
	}
	if code != "287082" { // 94287082 -> last 6 digits
		t.Fatalf("got %s", code)
	}
	code, _ = TOTPCodeAt(secret, time.Unix(1111111109, 0))
	if code != "081804" { // 07081804
		t.Fatalf("got %s", code)
	}
}

func TestVerifyTOTPWindow(t *testing.T) {
	s := NewTOTPSecret()
	now := time.Unix(1_800_000_000, 0)
	prev, _ := TOTPCodeAt(s, now.Add(-30*time.Second))
	if step, ok := VerifyTOTP(s, prev, now); !ok || step != now.Unix()/30-1 {
		t.Fatal("previous step should be accepted")
	}
	old, _ := TOTPCodeAt(s, now.Add(-90*time.Second))
	if _, ok := VerifyTOTP(s, old, now); ok {
		t.Fatal("code from 3 steps ago accepted")
	}
	if _, ok := VerifyTOTP(s, "12345", now); ok {
		t.Fatal("short code accepted")
	}
}

func TestLimiter(t *testing.T) {
	l := NewLimiter(3, time.Minute, 10*time.Minute)
	now := time.Unix(1000, 0)
	l.Fail("ip", now)
	l.Fail("ip", now)
	if locked, _ := l.Locked("ip", now); locked {
		t.Fatal("locked too early")
	}
	if !l.Fail("ip", now) {
		t.Fatal("third failure should lock")
	}
	if locked, _ := l.Locked("ip", now.Add(5*time.Minute)); !locked {
		t.Fatal("should still be locked")
	}
	if locked, _ := l.Locked("ip", now.Add(11*time.Minute)); locked {
		t.Fatal("lock should expire")
	}
	// failures outside the window do not add up
	l.Fail("u", now)
	l.Fail("u", now.Add(2*time.Minute))
	if l.Fail("u", now.Add(4*time.Minute)) {
		t.Fatal("spread-out failures should not lock")
	}
}

func TestTokens(t *testing.T) {
	tok, h := NewToken("rde_")
	if HashToken(tok) != h || len(tok) < 40 {
		t.Fatal("token hash mismatch")
	}
	if p := RandomPassword(24); len(p) != 24 {
		t.Fatal("bad random password length")
	}
}
