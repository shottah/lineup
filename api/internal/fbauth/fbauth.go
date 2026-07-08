// Package fbauth isolates Firebase ID-token verification behind the
// TokenVerifier interface so handlers and middleware can be tested without
// Firebase. The real Verifier honors FIREBASE_AUTH_EMULATOR_HOST, so the
// same code path verifies against the local Auth emulator in development
// and against live Firebase in production.
package fbauth

import (
	"context"
	"fmt"

	firebase "firebase.google.com/go/v4"
	"firebase.google.com/go/v4/auth"
)

// Identity is what a verified ID token yields. Email is always present for
// the providers Lineup supports (users.email is NOT NULL); DisplayName may
// be empty.
type Identity struct {
	UID         string
	Email       string
	DisplayName string
}

// TokenVerifier verifies a raw Firebase ID token and returns the caller's
// identity claims.
type TokenVerifier interface {
	VerifyIDToken(ctx context.Context, rawToken string) (Identity, error)
}

// Verifier is the production TokenVerifier backed by firebase-admin-go.
type Verifier struct {
	client *auth.Client
}

// New builds a Verifier for projectID. Verification needs no service
// account credentials: signature checks use Google's public certs, and the
// emulator (FIREBASE_AUTH_EMULATOR_HOST) skips signatures entirely.
func New(ctx context.Context, projectID string) (*Verifier, error) {
	app, err := firebase.NewApp(ctx, &firebase.Config{ProjectID: projectID})
	if err != nil {
		return nil, fmt.Errorf("fbauth: new app: %w", err)
	}
	client, err := app.Auth(ctx)
	if err != nil {
		return nil, fmt.Errorf("fbauth: auth client: %w", err)
	}
	return &Verifier{client: client}, nil
}

func (v *Verifier) VerifyIDToken(ctx context.Context, rawToken string) (Identity, error) {
	tok, err := v.client.VerifyIDToken(ctx, rawToken)
	if err != nil {
		return Identity{}, fmt.Errorf("fbauth: verify: %w", err)
	}
	id := Identity{UID: tok.UID}
	if e, ok := tok.Claims["email"].(string); ok {
		id.Email = e
	}
	if n, ok := tok.Claims["name"].(string); ok {
		id.DisplayName = n
	}
	return id, nil
}
