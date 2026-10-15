package auth

import (
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"nodepilot/internal/config"
)

func TestParseJWTRequiresHS256(t *testing.T) {
	old := config.JWTSecret
	config.JWTSecret = []byte("test-secret")
	defer func() { config.JWTSecret = old }()

	token, err := jwt.NewWithClaims(jwt.SigningMethodHS512, jwt.MapClaims{
		"sub": "admin", "ver": 0, "exp": time.Now().Add(time.Hour).Unix(),
	}).SignedString(config.JWTSecret)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseJWT(token); err == nil {
		t.Fatal("HS512 token should be rejected")
	}
}

func TestParseJWTAcceptsValidToken(t *testing.T) {
	old := config.JWTSecret
	config.JWTSecret = []byte("test-secret")
	defer func() { config.JWTSecret = old }()

	token, err := IssueJWT("admin", 2)
	if err != nil {
		t.Fatal(err)
	}
	claims, err := ParseJWT(token)
	if err != nil {
		t.Fatal(err)
	}
	if claims["sub"] != "admin" {
		t.Fatalf("sub = %v, want admin", claims["sub"])
	}
}
