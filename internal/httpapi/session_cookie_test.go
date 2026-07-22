package httpapi

import (
	"bytes"
	"strings"
	"testing"
)

func TestSessionCookieRoundTripAndTamper(t *testing.T) {
	secret := bytes.Repeat([]byte("s"), 32)
	cookie, wantHash, err := newSessionToken(secret)
	if err != nil {
		t.Fatal(err)
	}
	gotHash, err := verifySessionToken(cookie, secret)
	if err != nil || !bytes.Equal(gotHash, wantHash) {
		t.Fatalf("verify = %x, %v", gotHash, err)
	}
	parts := strings.Split(cookie, ".")
	parts[0] = "A" + parts[0][1:]
	if _, err := verifySessionToken(strings.Join(parts, "."), secret); err == nil {
		t.Fatal("tampered cookie was accepted")
	}
}
