package httpapi

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strings"
)

const sessionCookieName = "securebrain_session"

func newSessionToken(secret []byte) (cookieValue string, tokenHash []byte, err error) {
	raw := make([]byte, 32)
	if _, err = rand.Read(raw); err != nil {
		return "", nil, err
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(token))
	signature := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	hash := sha256.Sum256([]byte(token))
	return token + "." + signature, hash[:], nil
}

func verifySessionToken(value string, secret []byte) ([]byte, error) {
	if len(secret) < 32 {
		return nil, errors.New("session secret is too short")
	}
	token, signature, ok := strings.Cut(value, ".")
	if !ok || token == "" || signature == "" || strings.Contains(signature, ".") {
		return nil, errors.New("invalid session cookie")
	}
	supplied, err := base64.RawURLEncoding.DecodeString(signature)
	if err != nil {
		return nil, errors.New("invalid session cookie")
	}
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(token))
	if !hmac.Equal(supplied, mac.Sum(nil)) {
		return nil, errors.New("invalid session cookie")
	}
	decoded, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(decoded) != 32 {
		return nil, errors.New("invalid session cookie")
	}
	hash := sha256.Sum256([]byte(token))
	return hash[:], nil
}
