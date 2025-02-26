package auth

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestCreateValidateJWT(t *testing.T) {
	uuid := uuid.New()
	tokenSecret := "secret"
	tokenString, err := MakeJWT(uuid, tokenSecret, time.Duration(1)*time.Hour)
	if err != nil {
		t.Fatal(err)
	}

	parsedUUID, err := ValidateJWT(tokenString, tokenSecret)
	if err != nil {
		t.Fatal(err)
	}
	if parsedUUID != uuid {
		t.Fatalf("expected %s, got %s", uuid, parsedUUID)
	}
}

func TestExpiredJWT(t *testing.T) {
	uuid := uuid.New()
	tokenSecret := "secret"
	tokenString, err := MakeJWT(uuid, tokenSecret, time.Duration(1)*time.Nanosecond)
	if err != nil {
		t.Fatal(err)
	}
	// Sleep for 2 seconds to ensure the token is expired
	time.Sleep(2 * time.Second)

	_, err = ValidateJWT(tokenString, tokenSecret)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestBadSecret(t *testing.T) {
	uuid := uuid.New()
	tokenSecret := "secret"
	tokenString, err := MakeJWT(uuid, tokenSecret, time.Duration(1)*time.Hour)
	if err != nil {
		t.Fatal(err)
	}

	_, err = ValidateJWT(tokenString, "badsecret")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}
