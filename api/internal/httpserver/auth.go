package httpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/shottah/lineup/api/internal/docstore"
	"github.com/shottah/lineup/api/internal/fbauth"
	"github.com/shottah/lineup/api/internal/prefs"
)

// UserStore is the slice of *docstore.Store the auth layer needs; narrowed to
// an interface so handler tests run without Firestore.
type UserStore interface {
	UpsertUserByFirebaseUID(ctx context.Context, firebaseUID, email, displayName string, defaultPrefs json.RawMessage) (*docstore.User, error)
	UpdateUserPrefs(ctx context.Context, userID string, upd docstore.UserPrefsUpdate) (*docstore.User, error)
}

type ctxKey int

const userKey ctxKey = 0

// userFrom returns the authenticated user placed on the context by
// requireAuth, or nil outside an authenticated request.
func userFrom(ctx context.Context) *docstore.User {
	u, _ := ctx.Value(userKey).(*docstore.User)
	return u
}

func writeJSONError(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	fmt.Fprintf(w, `{"error":%q}`, code)
}

// requireAuth verifies the Bearer token, upserts the user row (first sign-in
// gets default schedule prefs), and stashes the user on the context.
func requireAuth(v fbauth.TokenVerifier, users UserStore) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			const bearer = "Bearer "
			h := r.Header.Get("Authorization")
			if !strings.HasPrefix(h, bearer) {
				writeJSONError(w, http.StatusUnauthorized, "unauthorized")
				return
			}
			ident, err := v.VerifyIDToken(r.Context(), strings.TrimPrefix(h, bearer))
			if err != nil {
				writeJSONError(w, http.StatusUnauthorized, "unauthorized")
				return
			}
			u, err := users.UpsertUserByFirebaseUID(r.Context(), ident.UID, ident.Email, ident.DisplayName, prefs.Default())
			if err != nil {
				writeJSONError(w, http.StatusInternalServerError, "internal")
				return
			}
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), userKey, u)))
		})
	}
}
