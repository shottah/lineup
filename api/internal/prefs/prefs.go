// Package prefs owns the schedule_prefs JSON shape: the canonical default
// and validation for PATCH input. The shape is fixed by issue #8:
// {"windows":{"mon":{"enabled":true,"start":"19:00","end":"23:00"},...}}
// with exactly the seven lowercase day keys and zero-padded 24h HH:MM.
package prefs

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
)

var dayKeys = []string{"mon", "tue", "wed", "thu", "fri", "sat", "sun"}

var timeRe = regexp.MustCompile(`^([01][0-9]|2[0-3]):[0-5][0-9]$`)

type window struct {
	Enabled bool   `json:"enabled"`
	Start   string `json:"start"`
	End     string `json:"end"`
}

type prefsDoc struct {
	Windows map[string]window `json:"windows"`
}

// Default returns the canonical default prefs: every day enabled
// 19:00-23:00. The result is freshly marshaled each call, so callers may
// not mutate shared state through it.
func Default() json.RawMessage {
	w := make(map[string]window, len(dayKeys))
	for _, d := range dayKeys {
		w[d] = window{Enabled: true, Start: "19:00", End: "23:00"}
	}
	raw, err := json.Marshal(prefsDoc{Windows: w})
	if err != nil {
		panic(fmt.Sprintf("prefs: marshal default: %v", err)) // static input; cannot fail
	}
	return raw
}

// Validate reports whether raw is a well-formed prefs document: exactly the
// seven day keys, HH:MM times, start strictly before end. nil means valid.
func Validate(raw json.RawMessage) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var doc prefsDoc
	if err := dec.Decode(&doc); err != nil {
		return fmt.Errorf("prefs: %w", err)
	}
	if dec.More() {
		return fmt.Errorf("prefs: trailing data after document")
	}
	if len(doc.Windows) != len(dayKeys) {
		return fmt.Errorf("prefs: windows must contain exactly the seven day keys")
	}
	for _, d := range dayKeys {
		w, ok := doc.Windows[d]
		if !ok {
			return fmt.Errorf("prefs: missing day %q", d)
		}
		if !timeRe.MatchString(w.Start) || !timeRe.MatchString(w.End) {
			return fmt.Errorf("prefs: %s: times must be zero-padded 24h HH:MM", d)
		}
		if w.Start >= w.End { // zero-padded HH:MM compares correctly as strings
			return fmt.Errorf("prefs: %s: start must be before end", d)
		}
	}
	return nil
}

// ParsedWindow is a decoded viewing window in minutes from midnight.
type ParsedWindow struct {
	Enabled  bool
	StartMin int
	EndMin   int
}

// Windows decodes a schedule_prefs document into per-weekday windows for
// guide generation. The legacy empty document `{}` (the schema default
// before first sign-in) and empty input yield an empty map, not an error.
func Windows(raw json.RawMessage) (map[string]ParsedWindow, error) {
	trimmed := string(bytes.TrimSpace(raw))
	if trimmed == "" || trimmed == "{}" {
		return map[string]ParsedWindow{}, nil
	}
	if err := Validate(raw); err != nil {
		return nil, err
	}
	var doc prefsDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("prefs: %w", err) // unreachable after Validate
	}
	out := make(map[string]ParsedWindow, len(doc.Windows))
	for day, w := range doc.Windows {
		out[day] = ParsedWindow{Enabled: w.Enabled, StartMin: hhmm(w.Start), EndMin: hhmm(w.End)}
	}
	return out, nil
}

// hhmm converts a Validate-checked "HH:MM" to minutes from midnight.
func hhmm(s string) int {
	h, _ := strconv.Atoi(s[:2])
	m, _ := strconv.Atoi(s[3:])
	return h*60 + m
}
