package config

import "testing"

// envKeys lists every environment variable Load reads. Each test case sets
// all of them explicitly (to "" when absent from its env map) so cases
// can't leak state from the outer test environment.
var envKeys = []string{"DATABASE_URL", "TMDB_API_KEY", "FIREBASE_PROJECT_ID", "PORT"}

func TestLoad(t *testing.T) {
	tests := []struct {
		name    string
		env     map[string]string
		wantErr bool
		want    Config
	}{
		{
			name:    "missing DATABASE_URL errors",
			env:     map[string]string{},
			wantErr: true,
		},
		{
			name: "DATABASE_URL only, other fields default to empty",
			env: map[string]string{
				"DATABASE_URL": "postgres://localhost/db",
			},
			want: Config{DatabaseURL: "postgres://localhost/db"},
		},
		{
			name: "all fields set",
			env: map[string]string{
				"DATABASE_URL":        "postgres://localhost/db",
				"TMDB_API_KEY":        "tmdb-key",
				"FIREBASE_PROJECT_ID": "proj-id",
				"PORT":                "9090",
			},
			want: Config{
				DatabaseURL:       "postgres://localhost/db",
				TMDBKey:           "tmdb-key",
				FirebaseProjectID: "proj-id",
				Port:              "9090",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, key := range envKeys {
				t.Setenv(key, tt.env[key]) // zero value "" when absent from the map
			}

			got, err := Load()
			if tt.wantErr {
				if err == nil {
					t.Fatalf("Load() error = nil, want error")
				}
				return
			}
			if err != nil {
				t.Fatalf("Load() unexpected error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("Load() = %+v, want %+v", got, tt.want)
			}
		})
	}
}
