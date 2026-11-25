package docstore

import (
	"regexp"
	"testing"
)

func TestNewItemIDShape(t *testing.T) {
	re := regexp.MustCompile(`^[0-9A-Za-z]{10}$`)
	seen := make(map[string]bool, 1000)
	for i := 0; i < 1000; i++ {
		id := newItemID()
		if !re.MatchString(id) {
			t.Fatalf("id %q does not match shape", id)
		}
		if seen[id] {
			t.Fatalf("duplicate id %q", id)
		}
		seen[id] = true
	}
}

func TestValidID(t *testing.T) {
	for _, id := range []string{"abc", "series-1399"} {
		if !validID(id) {
			t.Errorf("validID(%q) = false, want true", id)
		}
	}
	for _, id := range []string{"", "a/b", "__x__"} {
		if validID(id) {
			t.Errorf("validID(%q) = true, want false", id)
		}
	}
}

func TestExportedValidIDMatches(t *testing.T) {
	for _, id := range []string{"abc", "series-1399", "", "a/b", "__x__"} {
		if ValidID(id) != validID(id) {
			t.Errorf("ValidID(%q) = %v, want %v", id, ValidID(id), validID(id))
		}
	}
}
