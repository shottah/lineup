package docstore

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"
)

type indexFile struct {
	Indexes []struct {
		CollectionGroup string `json:"collectionGroup"`
		QueryScope      string `json:"queryScope"`
		Fields          []struct {
			FieldPath string `json:"fieldPath"`
			Order     string `json:"order"`
		} `json:"fields"`
	} `json:"indexes"`
}

func TestIndexesCoverShelfQueries(t *testing.T) {
	b, err := os.ReadFile("../../../firestore.indexes.json")
	if err != nil {
		t.Fatal(err)
	}
	var f indexFile
	if err := json.Unmarshal(b, &f); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, ix := range f.Indexes {
		if ix.CollectionGroup != "entries" || ix.QueryScope != "COLLECTION" {
			continue
		}
		var parts []string
		for _, fl := range ix.Fields {
			parts = append(parts, fl.FieldPath+" "+fl.Order)
		}
		got = append(got, strings.Join(parts, ", "))
	}
	want := []string{
		"status ASCENDING, addedAt DESCENDING",
		"favorite ASCENDING, addedAt DESCENDING",
		"rating DESCENDING, addedAt DESCENDING",
	}
	sort.Strings(got)
	sort.Strings(want)
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("entries composite indexes = %v, want %v", got, want)
	}
}
