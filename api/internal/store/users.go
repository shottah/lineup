package store

import (
	"context"
	"encoding/json"
	"fmt"
)

// User mirrors a row of the users table. FirebaseUID is intentionally
// excluded from JSON responses.
type User struct {
	ID            int64           `json:"id"`
	FirebaseUID   string          `json:"-"`
	Email         string          `json:"email"`
	DisplayName   string          `json:"display_name"`
	Region        string          `json:"region"`
	SchedulePrefs json.RawMessage `json:"schedule_prefs"`
}

const userReturning = `id, firebase_uid, email, display_name, region, schedule_prefs`

// UpsertUserByFirebaseUID inserts the user on first sight (applying
// defaultPrefs) or refreshes email/display_name on subsequent sign-ins.
// An empty displayName never overwrites a previously stored one.
func (s *Store) UpsertUserByFirebaseUID(ctx context.Context, firebaseUID, email, displayName string, defaultPrefs json.RawMessage) (*User, error) {
	const q = `
INSERT INTO users (firebase_uid, email, display_name, schedule_prefs)
VALUES ($1, $2, $3, $4)
ON CONFLICT (firebase_uid) DO UPDATE SET
  email = EXCLUDED.email,
  display_name = CASE WHEN EXCLUDED.display_name <> '' THEN EXCLUDED.display_name ELSE users.display_name END,
  updated_at = now()
RETURNING ` + userReturning
	u := &User{}
	err := s.Pool.QueryRow(ctx, q, firebaseUID, email, displayName, defaultPrefs).
		Scan(&u.ID, &u.FirebaseUID, &u.Email, &u.DisplayName, &u.Region, &u.SchedulePrefs)
	if err != nil {
		return nil, fmt.Errorf("store: upsert user: %w", err)
	}
	return u, nil
}

// UpdateUserPrefs updates region and/or schedule_prefs; nil arguments leave
// the corresponding column untouched.
func (s *Store) UpdateUserPrefs(ctx context.Context, userID int64, region *string, prefsJSON json.RawMessage) (*User, error) {
	const q = `
UPDATE users SET
  region = COALESCE($2, region),
  schedule_prefs = COALESCE($3, schedule_prefs),
  updated_at = now()
WHERE id = $1
RETURNING ` + userReturning
	u := &User{}
	err := s.Pool.QueryRow(ctx, q, userID, region, prefsJSON).
		Scan(&u.ID, &u.FirebaseUID, &u.Email, &u.DisplayName, &u.Region, &u.SchedulePrefs)
	if err != nil {
		return nil, fmt.Errorf("store: update user prefs: %w", err)
	}
	return u, nil
}
