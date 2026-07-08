package fbauth

import (
	"context"
	"errors"
)

// ErrUnknownToken is returned by Fake for tokens it has no entry for.
var ErrUnknownToken = errors.New("fbauth: unknown token")

// Fake is a TokenVerifier for tests: it resolves raw token strings from a
// fixed map and never talks to Firebase.
type Fake struct {
	Tokens map[string]Identity
}

func (f *Fake) VerifyIDToken(_ context.Context, rawToken string) (Identity, error) {
	id, ok := f.Tokens[rawToken]
	if !ok {
		return Identity{}, ErrUnknownToken
	}
	return id, nil
}
