// Package docstore persists Lineup data in Cloud Firestore.
package docstore

import (
	"errors"
	"regexp"
	"strings"

	"cloud.google.com/go/firestore"
)

var (
	ErrTitleNotFound = errors.New("docstore: title not found")
	ErrGuideNotFound = errors.New("docstore: guide not found")
	ErrPastMove      = errors.New("docstore: cannot move a past slot")
	ErrPastTarget    = errors.New("docstore: cannot move a slot into the past")
)

// Store is a Firestore-backed data store.
type Store struct{ c *firestore.Client }

// New returns a Store using the given Firestore client.
func New(c *firestore.Client) *Store { return &Store{c: c} }

var reservedID = regexp.MustCompile(`^__.*__$`)

// validID reports whether id is usable as a Firestore document ID.
func validID(id string) bool {
	return id != "" && !strings.Contains(id, "/") && !reservedID.MatchString(id)
}

// ValidID reports whether id is usable as a Firestore document ID. Handlers
// use it to reject path IDs before they reach the store.
func ValidID(id string) bool { return validID(id) }
