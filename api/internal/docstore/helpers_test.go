package docstore

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"cloud.google.com/go/firestore"
)

var nonProjectChars = regexp.MustCompile(`[^a-z0-9-]+`)

// testStore returns a Store on its own emulator project, so tests never share
// data. It skips when FIRESTORE_EMULATOR_HOST is unset.
func testStore(t *testing.T) *Store {
	t.Helper()
	if os.Getenv("FIRESTORE_EMULATOR_HOST") == "" {
		t.Skip("FIRESTORE_EMULATOR_HOST not set")
	}
	name := nonProjectChars.ReplaceAllString(strings.ToLower(t.Name()), "-")
	suffix := fmt.Sprintf("-%d", time.Now().UnixNano()%1_000_000_000)
	if max := 30 - len("demo-") - len(suffix); len(name) > max {
		name = name[:max]
	}
	c, err := firestore.NewClient(context.Background(), "demo-"+name+suffix)
	if err != nil {
		t.Fatalf("firestore client: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return New(c)
}

func uniqueUID(t *testing.T) string {
	t.Helper()
	return "test-" + strings.ReplaceAll(t.Name(), "/", "-") + "-" + time.Now().Format("150405.000000000")
}
