package rds

import (
	"bytes"
	"fmt"
	"strings"
)

// RDS exposes an AWS-managed rdsadmin role for maintenance. The stock image
// needs an equivalent login so ModifyDBInstance can restore the requested
// master's password even after that user changed it directly with SQL. Its
// generated password is never returned and connections are made only by
// docker exec inside the engine container.
const postgresBootstrapUser = "rdsadmin"

// postgresCredentialArchive creates the requested AWS-visible master account
// without making it a PostgreSQL SUPERUSER. RDS grants CREATEDB, CREATEROLE,
// and membership in its managed rds_superuser role instead; the stock image's
// bootstrap superuser is retained as the AWS-shaped rdsadmin maintenance
// account; its generated credential is not exposed to callers.
func postgresCredentialArchive(masterUser, masterPassword, dbName string) (*bytes.Reader, error) {
	var sql strings.Builder
	fmt.Fprintf(&sql, "CREATE ROLE %s NOLOGIN NOSUPERUSER CREATEDB CREATEROLE INHERIT;\n",
		pgIdentifier("rds_superuser"))
	fmt.Fprintf(&sql, "CREATE ROLE %s NOLOGIN NOSUPERUSER NOREPLICATION;\n", pgIdentifier("rds_password"))
	fmt.Fprintf(&sql, "CREATE ROLE %s NOLOGIN NOSUPERUSER REPLICATION;\n", pgIdentifier("rds_replication"))
	fmt.Fprintf(&sql, "GRANT %s, %s TO %s WITH ADMIN OPTION;\n",
		pgIdentifier("rds_password"), pgIdentifier("rds_replication"), pgIdentifier("rds_superuser"))
	sql.WriteString("DO $overcast$\n")
	sql.WriteString("DECLARE managed_role text;\n")
	sql.WriteString("BEGIN\n")
	sql.WriteString("  FOREACH managed_role IN ARRAY ARRAY['pg_monitor', 'pg_signal_backend', 'pg_checkpoint', 'pg_use_reserved_connections'] LOOP\n")
	sql.WriteString("    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = managed_role) THEN\n")
	sql.WriteString("      EXECUTE format('GRANT %I TO rds_superuser WITH ADMIN OPTION', managed_role);\n")
	sql.WriteString("    END IF;\n")
	sql.WriteString("  END LOOP;\n")
	sql.WriteString("END\n")
	sql.WriteString("$overcast$;\n")
	fmt.Fprintf(&sql, "CREATE ROLE %s WITH LOGIN NOSUPERUSER CREATEDB CREATEROLE INHERIT PASSWORD %s;\n",
		pgIdentifier(masterUser), pgString(masterPassword))
	fmt.Fprintf(&sql, "GRANT %s TO %s WITH ADMIN OPTION;\n",
		pgIdentifier("rds_superuser"), pgIdentifier(masterUser))
	fmt.Fprintf(&sql, "ALTER DATABASE %s OWNER TO %s;\n", pgIdentifier("postgres"), pgIdentifier(masterUser))
	if dbName != "" && dbName != "postgres" {
		fmt.Fprintf(&sql, "CREATE DATABASE %s OWNER %s;\n", pgIdentifier(dbName), pgIdentifier(masterUser))
	}
	return credentialInitArchive("10-overcast-credentials.sql", sql.String())
}
