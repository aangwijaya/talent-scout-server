package store

import (
	"strings"
	"testing"
)

func TestNormalizeDSN(t *testing.T) {
	tests := []struct {
		name       string
		dsn        string
		wantSimple bool
		wantQuery  string
	}{
		{
			name:       "session pooler",
			dsn:        "postgres://user:pass@host:5432/postgres?sslmode=require",
			wantSimple: false,
			wantQuery:  "sslmode=require",
		},
		{
			name:       "transaction pooler port",
			dsn:        "postgres://user:pass@host:6543/postgres?sslmode=require",
			wantSimple: true,
			wantQuery:  "sslmode=require",
		},
		{
			name:       "pgbouncer flag",
			dsn:        "postgres://user:pass@host:5432/postgres?pgbouncer=true&sslmode=require",
			wantSimple: true,
			wantQuery:  "sslmode=require",
		},
		{
			name:       "keyword form",
			dsn:        "host=localhost port=5432 dbname=postgres",
			wantSimple: false,
			wantQuery:  "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dsn, simple := normalizeDSN(tc.dsn)
			if simple != tc.wantSimple {
				t.Fatalf("simple = %v, want %v", simple, tc.wantSimple)
			}
			if strings.Contains(dsn, "pgbouncer") {
				t.Fatalf("normalized DSN still contains pgbouncer: %q", dsn)
			}
			if tc.wantQuery != "" && !strings.Contains(dsn, tc.wantQuery) {
				t.Fatalf("normalized DSN %q does not contain %q", dsn, tc.wantQuery)
			}
		})
	}
}
