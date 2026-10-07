package config

import (
	"net/url"
	"testing"
)

func TestDSNDefaults(t *testing.T) {
	// Clear POSTGRES_* so the defaults apply.
	for _, k := range []string{
		"POSTGRES_USER", "POSTGRES_PASSWORD", "POSTGRES_HOST",
		"POSTGRES_PORT", "POSTGRES_DB", "POSTGRES_SSLMODE",
	} {
		t.Setenv(k, "")
	}
	want := "postgres://homepoll:homepoll@localhost:5432/homepoll?sslmode=disable"
	if got := Load().DSN; got != want {
		t.Errorf("DSN = %q, want %q", got, want)
	}
}

func TestDSNEscapesCredentials(t *testing.T) {
	// Credentials with URL-special characters must be escaped.
	t.Setenv("POSTGRES_PASSWORD", "p@ss:w/rd")
	u, err := url.Parse(Load().DSN)
	if err != nil {
		t.Fatalf("DSN is not a valid URL: %v", err)
	}
	if pw, _ := u.User.Password(); pw != "p@ss:w/rd" {
		t.Errorf("password round-trip = %q, want %q", pw, "p@ss:w/rd")
	}
}
