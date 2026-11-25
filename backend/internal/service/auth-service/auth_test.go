package auth

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"testing"
	"time"
)

func hashOf(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func status(err error) int {
	var e *Error
	if errors.As(err, &e) {
		return e.Status
	}
	return 0
}

func TestLogin(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	a := New("admin", " "+hashOf("pw")+" ", 10*time.Minute)
	a.now = func() time.Time { return now }

	if _, err := a.Login("admin", "nope", "1.1.1.1"); status(err) != http.StatusUnauthorized {
		t.Fatalf("wrong password: %v", err)
	}
	if _, err := a.Login("root", "pw", "1.1.1.1"); status(err) != http.StatusUnauthorized {
		t.Fatalf("wrong user: %v", err)
	}
	token, err := a.Login("admin", "pw", "1.1.1.1")
	if err != nil || len(token) != 64 {
		t.Fatalf("login = %q, %v", token, err)
	}
	other, _ := a.Login("admin", "pw", "1.1.1.1")
	if other == token || !a.Check(token) || !a.Check(other) || a.Check("") || a.Check("x") {
		t.Fatal("each login must get its own valid token")
	}

	// 요청이 있으면 만료가 늘어나고, idle 동안 없으면 만료된다.
	now = now.Add(9 * time.Minute)
	if !a.Check(token) {
		t.Fatal("token expired too early")
	}
	now = now.Add(9 * time.Minute)
	if !a.Check(token) || a.Check(other) {
		t.Fatal("sliding expiry")
	}
	a.Logout(token)
	if a.Check(token) {
		t.Fatal("logout")
	}
}

func TestLoginLockout(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	a := New("admin", hashOf("pw"), time.Hour)
	a.now = func() time.Time { return now }

	for i := 0; i < maxFailures; i++ {
		a.Login("admin", "x", "1.1.1.1")
	}
	if _, err := a.Login("admin", "pw", "1.1.1.1"); status(err) != http.StatusTooManyRequests {
		t.Fatalf("locked: %v", err)
	}
	if _, err := a.Login("admin", "pw", "2.2.2.2"); err != nil {
		t.Fatalf("other ip: %v", err)
	}
	now = now.Add(lockDuration)
	if _, err := a.Login("admin", "pw", "1.1.1.1"); err != nil {
		t.Fatalf("after lock: %v", err)
	}
	// 잠금이 풀린 뒤에는 다시 처음부터 센다.
	a.Login("admin", "x", "1.1.1.1")
	if _, err := a.Login("admin", "pw", "1.1.1.1"); err != nil {
		t.Fatalf("count reset: %v", err)
	}
}

func TestNotConfigured(t *testing.T) {
	for _, a := range []*Auth{New("", hashOf("pw"), time.Hour), New("admin", "", time.Hour), New("admin", "pw", time.Hour), New("admin", hashOf("pw")[:63]+"z", time.Hour)} {
		if a.Configured() {
			t.Errorf("%+v must not be configured", a)
		}
		if _, err := a.Login(a.user, "pw", "1.1.1.1"); status(err) != http.StatusServiceUnavailable {
			t.Errorf("login without config: %v", err)
		}
	}
	if !New("admin", hashOf("pw"), time.Hour).Configured() {
		t.Error("valid config")
	}
}

func TestTicket(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	a := New("admin", hashOf("pw"), time.Hour)
	a.now = func() time.Time { return now }

	ticket := a.Ticket()
	if !a.UseTicket(ticket) || a.UseTicket(ticket) || a.UseTicket("") {
		t.Fatal("ticket must be usable once")
	}
	late := a.Ticket()
	now = now.Add(ticketTTL)
	if a.UseTicket(late) {
		t.Fatal("expired ticket")
	}
}
