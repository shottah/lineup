package firestorerules

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

const project = "demo-lineup"

func unsignedJWT(t *testing.T, uid string) string {
	t.Helper()
	enc := base64.RawURLEncoding
	header, _ := json.Marshal(map[string]string{"alg": "none", "typ": "JWT"})
	now := time.Now().Unix()
	claims, err := json.Marshal(map[string]any{
		"sub":      uid,
		"user_id":  uid,
		"aud":      project,
		"iss":      "https://securetoken.google.com/" + project,
		"iat":      now,
		"exp":      now + 3600,
		"firebase": map[string]string{"sign_in_provider": "custom"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return enc.EncodeToString(header) + "." + enc.EncodeToString(claims) + "."
}

func TestRulesDenyClientAccess(t *testing.T) {
	host := os.Getenv("FIRESTORE_EMULATOR_HOST")
	if host == "" {
		t.Skip("FIRESTORE_EMULATOR_HOST not set")
	}
	base := fmt.Sprintf("http://%s/v1/projects/%s/databases/(default)/documents/", host, project)
	token := unsignedJWT(t, "u1")

	cases := []struct {
		name, method, path, body, auth string
	}{
		{"get own user doc", http.MethodGet, "users/u1", "", token},
		{"patch own user doc", http.MethodPatch, "users/u1", `{"fields":{"x":{"stringValue":"y"}}}`, token},
		{"get title", http.MethodGet, "titles/series-1", "", token},
		{"get title unauthenticated", http.MethodGet, "titles/series-1", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req, err := http.NewRequest(tc.method, base+tc.path, strings.NewReader(tc.body))
			if err != nil {
				t.Fatal(err)
			}
			if tc.body != "" {
				req.Header.Set("Content-Type", "application/json")
			}
			if tc.auth != "" {
				req.Header.Set("Authorization", "Bearer "+tc.auth)
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusForbidden {
				t.Fatalf("status = %d, want 403", resp.StatusCode)
			}
		})
	}
}
