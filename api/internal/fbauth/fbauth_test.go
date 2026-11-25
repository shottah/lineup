package fbauth

import (
	"context"
	"testing"

	firebase "firebase.google.com/go/v4"
)

// TestNewUsesSharedApp: New builds its Auth client from the caller's app, so
// main can share one *firebase.App between Auth and Firestore.
func TestNewUsesSharedApp(t *testing.T) {
	t.Setenv("FIREBASE_AUTH_EMULATOR_HOST", "127.0.0.1:9099") // no network or creds needed
	ctx := context.Background()
	app, err := firebase.NewApp(ctx, &firebase.Config{ProjectID: "demo-fbauth"})
	if err != nil {
		t.Fatalf("NewApp: %v", err)
	}
	v, err := New(ctx, app)
	if err != nil || v == nil {
		t.Fatalf("New(app) = %v, %v", v, err)
	}
}

func TestNewRejectsNilApp(t *testing.T) {
	if _, err := New(context.Background(), nil); err == nil {
		t.Fatal("New(nil) error = nil, want error")
	}
}
