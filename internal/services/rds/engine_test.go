package rds

import "testing"

// A real EngineVersion is precise ("8.0.39"); engineImages advertises families.
// Without a match no container starts at all — and then the endpoint name the
// API already handed out resolves nowhere, while the instance still reports
// "available".
func TestResolveEngineImage(t *testing.T) {
	tests := []struct {
		engine, version string
		wantImage       string
		wantMatched     string
		wantOK          bool
	}{
		{"mysql", "8.0", "mysql:8.0", "8.0", true},
		{"mysql", "8.0.39", "mysql:8.0", "8.0", true},
		{"mysql", "8.4.3", "mysql:8.4", "8.4", true},
		{"mysql", "8.3.0", "mysql:8.0", "8.0", true},
		{"mysql", "5.7.44", "mysql:5.7", "5.7", true},
		{"postgres", "16.3", "postgres:16", "16.1", true},
		{"postgres", "14.11", "postgres:14", "14.11", true},
		{"postgres", "14.12", "postgres:14", "14.11", true},
		{"mariadb", "11.4.2", "mariadb:11", "11.4", true},
		{"aurora-mysql", "3.04.1", "mysql:8.0", "3.04", true},
		{"aurora-mysql", "8.0.mysql_aurora.3.04.0", "mysql:8.0", "3.04", true},
		{"aurora-mysql", "8.4.mysql_aurora.4.0.0", "mysql:8.4", "4.0", true},
		{"aurora-mysql", "5.7.mysql_aurora.2.11.5", "mysql:5.7", "2.11", true},
		// No 9.x image exists. The engine's default is a working database,
		// which beats no container at all.
		{"mysql", "9.1.0", "mysql:8.0", "8.0", true},
		{"oracle-ee", "19", "", "", false},
	}
	for _, tc := range tests {
		image, matched, ok := resolveEngineImage(tc.engine, tc.version)
		if image != tc.wantImage || matched != tc.wantMatched || ok != tc.wantOK {
			t.Errorf("resolveEngineImage(%q, %q) = (%q, %q, %v), want (%q, %q, %v)",
				tc.engine, tc.version, image, matched, ok, tc.wantImage, tc.wantMatched, tc.wantOK)
		}
	}
}
