package prefs

import (
	"encoding/json"
	"testing"
)

func TestDefaultIsValidAndCanonical(t *testing.T) {
	d := Default()
	if err := Validate(d); err != nil {
		t.Fatalf("Validate(Default()) = %v, want nil", err)
	}
	var p struct {
		Windows map[string]struct {
			Enabled bool   `json:"enabled"`
			Start   string `json:"start"`
			End     string `json:"end"`
		} `json:"windows"`
	}
	if err := json.Unmarshal(d, &p); err != nil {
		t.Fatalf("Default() not JSON: %v", err)
	}
	if len(p.Windows) != 7 {
		t.Fatalf("Default() has %d windows, want 7", len(p.Windows))
	}
	mon := p.Windows["mon"]
	if !mon.Enabled || mon.Start != "19:00" || mon.End != "23:00" {
		t.Fatalf("Default() mon = %+v, want enabled 19:00-23:00", mon)
	}
}

func TestValidate(t *testing.T) {
	day := `{"enabled":true,"start":"19:00","end":"23:00"}`
	full := func(overrides map[string]string) string {
		out := `{"windows":{`
		for i, d := range []string{"mon", "tue", "wed", "thu", "fri", "sat", "sun"} {
			v := day
			if o, ok := overrides[d]; ok {
				v = o
			}
			if i > 0 {
				out += ","
			}
			out += `"` + d + `":` + v
		}
		return out + `}}`
	}

	cases := []struct {
		name    string
		raw     string
		wantErr bool
	}{
		{"valid default shape", full(nil), false},
		{"disabled day still needs valid times", full(map[string]string{"tue": `{"enabled":false,"start":"09:00","end":"10:30"}`}), false},
		{"missing day key", `{"windows":{"mon":` + day + `}}`, true},
		{"extra key", `{"windows":{"mon":` + day + `,"tue":` + day + `,"wed":` + day + `,"thu":` + day + `,"fri":` + day + `,"sat":` + day + `,"sun":` + day + `,"xxx":` + day + `}}`, true},
		{"bad time format", full(map[string]string{"fri": `{"enabled":true,"start":"7pm","end":"23:00"}`}), true},
		{"non-zero-padded hour", full(map[string]string{"fri": `{"enabled":true,"start":"9:00","end":"23:00"}`}), true},
		{"start not before end", full(map[string]string{"sat": `{"enabled":true,"start":"23:00","end":"19:00"}`}), true},
		{"start equals end", full(map[string]string{"sat": `{"enabled":true,"start":"19:00","end":"19:00"}`}), true},
		{"hour out of range", full(map[string]string{"sun": `{"enabled":true,"start":"24:00","end":"25:00"}`}), true},
		{"unknown field in window", full(map[string]string{"mon": `{"enabled":true,"start":"19:00","end":"23:00","x":1}`}), true},
		{"top-level unknown field", `{"windows":` + full(nil)[12:] + `,"x":1}`, true},
		{"not json", `{`, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := Validate(json.RawMessage(tc.raw))
			if (err != nil) != tc.wantErr {
				t.Fatalf("Validate() error = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}
