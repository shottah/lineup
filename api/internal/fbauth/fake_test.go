package fbauth

import (
	"context"
	"errors"
	"testing"
)

func TestFakeVerifyIDToken(t *testing.T) {
	f := &Fake{Tokens: map[string]Identity{
		"good-token": {UID: "u1", Email: "u1@example.com", DisplayName: "U One"},
	}}

	id, err := f.VerifyIDToken(context.Background(), "good-token")
	if err != nil {
		t.Fatalf("VerifyIDToken(good-token) error = %v", err)
	}
	if id.UID != "u1" || id.Email != "u1@example.com" || id.DisplayName != "U One" {
		t.Fatalf("VerifyIDToken(good-token) = %+v, want u1 identity", id)
	}

	if _, err := f.VerifyIDToken(context.Background(), "bad-token"); !errors.Is(err, ErrUnknownToken) {
		t.Fatalf("VerifyIDToken(bad-token) error = %v, want ErrUnknownToken", err)
	}
}
