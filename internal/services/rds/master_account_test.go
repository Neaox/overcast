package rds

import (
	"slices"
	"testing"
)

func TestMySQLMasterAccountSpec_engineVersionSemantics(t *testing.T) {
	tests := []struct {
		name             string
		engine           string
		version          string
		wantRole         bool
		wantAuthPlugin   string
		wantPrivileges   []string
		absentPrivileges []string
	}{
		{
			name: "RDS MySQL before role model", engine: "mysql", version: "8.0.35",
			wantAuthPlugin: "mysql_native_password", absentPrivileges: []string{"APPLICATION_PASSWORD_ADMIN"},
		},
		{
			name: "RDS MySQL role model", engine: "mysql", version: "8.0.36",
			wantRole: true, wantAuthPlugin: "mysql_native_password", wantPrivileges: []string{"APPLICATION_PASSWORD_ADMIN", "XA_RECOVER_ADMIN"},
		},
		{
			name: "RDS MySQL 8.4 authentication", engine: "mysql", version: "8.4.3",
			wantRole: true, wantAuthPlugin: "caching_sha2_password",
			wantPrivileges:   []string{"ROLE_ADMIN", "FLUSH_PRIVILEGES", "SENSITIVE_VARIABLES_OBSERVER", "SET_ANY_DEFINER", "SHOW_ROUTINE"},
			absentPrivileges: []string{"SET_USER_ID", "ALLOW_NONEXISTENT_DEFINER"},
		},
		{
			name: "MariaDB before 11.4", engine: "mariadb", version: "10.11.8",
			absentPrivileges: []string{"SHOW CREATE ROUTINE"},
		},
		{
			name: "MariaDB 11.4", engine: "mariadb", version: "11.4.2",
			wantPrivileges: []string{"SHOW CREATE ROUTINE"},
		},
		{
			name: "Aurora MySQL v2 full version", engine: "aurora-mysql", version: "5.7.mysql_aurora.2.11.5",
			absentPrivileges: []string{"CONNECTION_ADMIN", "SHOW_ROUTINE"},
		},
		{
			name: "Aurora MySQL 3.03", engine: "aurora-mysql", version: "8.0.mysql_aurora.3.03.1",
			wantRole: true, wantAuthPlugin: "mysql_native_password", wantPrivileges: []string{"CONNECTION_ADMIN"},
			absentPrivileges: []string{"SHOW_ROUTINE", "FLUSH_TABLES"},
		},
		{
			name: "Aurora MySQL 3.04", engine: "aurora-mysql", version: "8.0.mysql_aurora.3.04.0",
			wantRole: true, wantAuthPlugin: "mysql_native_password", wantPrivileges: []string{"CONNECTION_ADMIN", "SHOW_ROUTINE"},
			absentPrivileges: []string{"FLUSH_TABLES"},
		},
		{
			name: "Aurora MySQL 3.09", engine: "aurora-mysql", version: "3.09.0",
			wantRole: true, wantAuthPlugin: "mysql_native_password",
			wantPrivileges: []string{"SHOW_ROUTINE", "FLUSH_OPTIMIZER_COSTS", "FLUSH_STATUS", "FLUSH_TABLES", "FLUSH_USER_RESOURCES"},
		},
		{
			name: "Aurora MySQL 4 authentication", engine: "aurora-mysql", version: "8.4.mysql_aurora.4.0.0",
			wantRole: true, wantAuthPlugin: "caching_sha2_password",
			wantPrivileges:   []string{"CONNECTION_ADMIN", "ALLOW_NONEXISTENT_DEFINER", "FLUSH_PRIVILEGES", "OPTIMIZE_LOCAL_TABLE", "SET_ANY_DEFINER"},
			absentPrivileges: []string{"SET_USER_ID", "SENSITIVE_VARIABLES_OBSERVER"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Given/When: a supported MySQL-family engine version is resolved.
			spec := mysqlMasterAccountFor(tc.engine, tc.version)

			// Then: the observable account model follows that AWS engine version.
			if got := spec.RoleName != ""; got != tc.wantRole {
				t.Errorf("role model = %v, want %v (role %q)", got, tc.wantRole, spec.RoleName)
			}
			if spec.AuthPlugin != tc.wantAuthPlugin {
				t.Errorf("AuthPlugin = %q, want %q", spec.AuthPlugin, tc.wantAuthPlugin)
			}
			for _, privilege := range tc.wantPrivileges {
				if !slices.Contains(spec.Privileges, privilege) {
					t.Errorf("Privileges missing %q: %v", privilege, spec.Privileges)
				}
			}
			for _, privilege := range tc.absentPrivileges {
				if slices.Contains(spec.Privileges, privilege) {
					t.Errorf("Privileges unexpectedly contain %q: %v", privilege, spec.Privileges)
				}
			}
		})
	}
}

func TestAuroraMySQLTrackVersion(t *testing.T) {
	tests := map[string]string{
		"3.04":                    "3.04",
		"3.09.0":                  "3.09.0",
		"8.0.mysql_aurora.3.04.0": "3.04.0",
		"5.7.mysql_aurora.2.11.5": "2.11.5",
		"8.4.mysql_aurora.4.0.0":  "4.0.0",
	}
	for input, want := range tests {
		if got := auroraMySQLTrackVersion(input); got != want {
			t.Errorf("auroraMySQLTrackVersion(%q) = %q, want %q", input, got, want)
		}
	}
}
