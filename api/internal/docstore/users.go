package docstore

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"cloud.google.com/go/firestore"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func isNotFound(err error) bool { return status.Code(err) == codes.NotFound }

// User mirrors a users/{uid} document. The ID is the Firebase UID.
type User struct {
	ID            string          `json:"id"`
	Email         string          `json:"email"`
	DisplayName   string          `json:"display_name"`
	Region        string          `json:"region"`
	SchedulePrefs json.RawMessage `json:"schedule_prefs"`
}

type userDoc struct {
	Email         string         `firestore:"email"`
	DisplayName   string         `firestore:"displayName"`
	Region        string         `firestore:"region"`
	SchedulePrefs map[string]any `firestore:"schedulePrefs"`
	CreatedAt     time.Time      `firestore:"createdAt"`
	UpdatedAt     time.Time      `firestore:"updatedAt"`
}

func (d *userDoc) user(id string) (*User, error) {
	prefs := d.SchedulePrefs
	if prefs == nil {
		prefs = map[string]any{}
	}
	raw, err := json.Marshal(prefs)
	if err != nil {
		return nil, err
	}
	return &User{ID: id, Email: d.Email, DisplayName: d.DisplayName, Region: d.Region, SchedulePrefs: raw}, nil
}

// prefsMap converts a JSON object into a Firestore map; empty or null input
// yields an empty map.
func prefsMap(raw json.RawMessage) (map[string]any, error) {
	m := map[string]any{}
	if len(raw) == 0 {
		return m, nil
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	if m == nil {
		m = map[string]any{}
	}
	return m, nil
}

// UpsertUserByFirebaseUID creates users/{uid} on first sight (applying
// defaultPrefs) or refreshes email/displayName on subsequent sign-ins. Empty
// email or displayName values never overwrite previously stored ones.
func (s *Store) UpsertUserByFirebaseUID(ctx context.Context, uid, email, displayName string, defaultPrefs json.RawMessage) (*User, error) {
	if !validID(uid) {
		return nil, fmt.Errorf("docstore: upsert user: invalid uid %q", uid)
	}
	prefs, err := prefsMap(defaultPrefs)
	if err != nil {
		return nil, fmt.Errorf("docstore: upsert user: default prefs: %w", err)
	}
	ref := s.c.Collection("users").Doc(uid)
	// Fast path: this runs on every authenticated request, so avoid a
	// transaction when the stored doc needs no refresh.
	if snap, gerr := ref.Get(ctx); gerr == nil {
		var d userDoc
		if err := snap.DataTo(&d); err != nil {
			return nil, fmt.Errorf("docstore: upsert user: %w", err)
		}
		if (email == "" || email == d.Email) && (displayName == "" || displayName == d.DisplayName) {
			u, err := d.user(uid)
			if err != nil {
				return nil, fmt.Errorf("docstore: upsert user: %w", err)
			}
			return u, nil
		}
	} else if !isNotFound(gerr) {
		return nil, fmt.Errorf("docstore: upsert user: %w", gerr)
	}
	var out *User
	err = s.c.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
		snap, err := tx.Get(ref)
		if err != nil && !isNotFound(err) {
			return err
		}
		now := now()
		var d userDoc
		if err != nil {
			d = userDoc{Email: email, DisplayName: displayName, Region: "US", SchedulePrefs: prefs, CreatedAt: now, UpdatedAt: now}
			if err := tx.Create(ref, &d); err != nil {
				return err
			}
		} else {
			if err := snap.DataTo(&d); err != nil {
				return err
			}
			var ups []firestore.Update
			if email != "" && email != d.Email {
				d.Email = email
				ups = append(ups, firestore.Update{Path: "email", Value: email})
			}
			if displayName != "" && displayName != d.DisplayName {
				d.DisplayName = displayName
				ups = append(ups, firestore.Update{Path: "displayName", Value: displayName})
			}
			// Only genuine refreshes write, so concurrent unchanged logins
			// do not contend on the same document.
			if len(ups) > 0 {
				d.UpdatedAt = now
				ups = append(ups, firestore.Update{Path: "updatedAt", Value: now})
				if err := tx.Update(ref, ups); err != nil {
					return err
				}
			}
		}
		u, err := d.user(uid)
		out = u
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("docstore: upsert user: %w", err)
	}
	return out, nil
}

// UpdateUserPrefs updates region and/or schedulePrefs; nil/empty arguments
// leave the corresponding field untouched. It fails if the user is missing.
func (s *Store) UpdateUserPrefs(ctx context.Context, userID string, region *string, prefsJSON json.RawMessage) (*User, error) {
	if !validID(userID) {
		return nil, fmt.Errorf("docstore: update user prefs: invalid user id %q", userID)
	}
	var prefs map[string]any
	if len(prefsJSON) > 0 {
		var err error
		if prefs, err = prefsMap(prefsJSON); err != nil {
			return nil, fmt.Errorf("docstore: update user prefs: prefs: %w", err)
		}
	}
	ref := s.c.Collection("users").Doc(userID)
	var out *User
	err := s.c.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
		snap, err := tx.Get(ref)
		if err != nil {
			return err
		}
		var d userDoc
		if err := snap.DataTo(&d); err != nil {
			return err
		}
		now := now()
		d.UpdatedAt = now
		ups := []firestore.Update{{Path: "updatedAt", Value: now}}
		if region != nil {
			d.Region = *region
			ups = append(ups, firestore.Update{Path: "region", Value: *region})
		}
		if prefs != nil {
			d.SchedulePrefs = prefs
			ups = append(ups, firestore.Update{Path: "schedulePrefs", Value: prefs})
		}
		if err := tx.Update(ref, ups); err != nil {
			return err
		}
		u, err := d.user(userID)
		out = u
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("docstore: update user prefs: %w", err)
	}
	return out, nil
}
