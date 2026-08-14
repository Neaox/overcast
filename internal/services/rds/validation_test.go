package rds

import (
	"strings"
	"testing"
)

func TestValidateMasterUsername(t *testing.T) {
	tests := []struct {
		name, engine, username string
		cluster                bool
		wantError              bool
	}{
		{name: "RDS username", engine: "mysql", username: "app_admin"},
		{name: "RDS starts with digit", engine: "mysql", username: "1admin", wantError: true},
		{name: "RDS punctuation", engine: "postgres", username: "app-admin", wantError: true},
		{name: "RDS too long", engine: "postgres", username: strings.Repeat("a", 17), wantError: true},
		{name: "Aurora MySQL alphanumeric", engine: "aurora-mysql", username: "clusteradmin", cluster: true},
		{name: "Aurora MySQL underscore", engine: "aurora-mysql", username: "cluster_admin", cluster: true, wantError: true},
		{name: "Aurora PostgreSQL 16", engine: "aurora-postgresql", username: "a" + strings.Repeat("1", 15), cluster: true},
		{name: "Aurora PostgreSQL 17", engine: "aurora-postgresql", username: "a" + strings.Repeat("1", 16), cluster: true, wantError: true},
		{name: "reserved RDS account", engine: "mysql", username: "rdsadmin", wantError: true},
		{name: "reserved Aurora role", engine: "aurora-mysql", username: "rds_superuser_role", cluster: true, wantError: true},
		{name: "reserved PostgreSQL role", engine: "postgres", username: "rds_superuser", wantError: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Given/When: a master username is validated for its resource kind.
			err := validateMasterUsername(tc.engine, tc.username, tc.cluster)

			// Then: its documented engine-specific shape is enforced.
			if (err != nil) != tc.wantError {
				t.Errorf("validateMasterUsername(%q, %q, %v) error = %v, wantError %v", tc.engine, tc.username, tc.cluster, err, tc.wantError)
			}
		})
	}
}

func TestValidateInitialDatabaseName(t *testing.T) {
	tests := []struct {
		name, engine, database string
		wantError              bool
	}{
		{name: "omitted", engine: "mysql"},
		{name: "MySQL name", engine: "mysql", database: "app_01"},
		{name: "MySQL starts with digit", engine: "mysql", database: "1app", wantError: true},
		{name: "MySQL too long", engine: "mysql", database: "a" + strings.Repeat("1", 64), wantError: true},
		{name: "PostgreSQL 63", engine: "postgres", database: "a" + strings.Repeat("1", 62)},
		{name: "PostgreSQL 64", engine: "postgres", database: "a" + strings.Repeat("1", 63), wantError: true},
		{name: "punctuation", engine: "aurora-postgresql", database: "app-db", wantError: true},
		{name: "reserved word", engine: "aurora-mysql", database: "select", wantError: true},
		{name: "MySQL system schema", engine: "mariadb", database: "mysql", wantError: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Given/When: an initial database name is validated.
			err := validateInitialDatabaseName(tc.engine, tc.database)

			// Then: invalid names fail before a container is launched.
			if (err != nil) != tc.wantError {
				t.Errorf("validateInitialDatabaseName(%q, %q) error = %v, wantError %v", tc.engine, tc.database, err, tc.wantError)
			}
		})
	}
}
