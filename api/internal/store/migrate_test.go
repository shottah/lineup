package store

import (
	"errors"
	"io/fs"
	"testing"
)

// TestMigrationsParse verifies the embedded migration source loads without
// hitting a real database, and that it exposes exactly one ordered migration
// (0001) with both an up and a down file.
func TestMigrationsParse(t *testing.T) {
	src, err := migrationSource()
	if err != nil {
		t.Fatalf("migrationSource() error = %v", err)
	}
	defer src.Close()

	first, err := src.First()
	if err != nil {
		t.Fatalf("First() error = %v", err)
	}
	if first != 1 {
		t.Fatalf("First() version = %d, want 1", first)
	}

	if _, _, err := src.ReadUp(first); err != nil {
		t.Fatalf("ReadUp(%d) error = %v", first, err)
	}
	if _, _, err := src.ReadDown(first); err != nil {
		t.Fatalf("ReadDown(%d) error = %v", first, err)
	}

	// Only one migration exists, so asking for the next one must report
	// "not exist" rather than silently returning a bogus version.
	if _, err := src.Next(first); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("Next(%d) error = %v, want fs.ErrNotExist", first, err)
	}
}
