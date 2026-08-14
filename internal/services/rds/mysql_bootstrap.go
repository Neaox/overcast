package rds

import (
	"bytes"
	"fmt"
	"strings"
)

const (
	mysqlCredentialInitScript = "10-overcast-credentials.sh"
)

func usesMySQL8CredentialBootstrap(engine, image string) bool {
	return (engine == "mysql" || engine == "aurora-mysql") && strings.HasPrefix(image, "mysql:8.")
}

// mysql8CredentialArchive builds a non-executable shell fragment. The MySQL
// image sources non-executable .sh init files into its entrypoint, which lets
// the fragment update MYSQL_ROOT_PASSWORD after installing the real password;
// the entrypoint's subsequent mysqladmin shutdown therefore uses the new one.
// This avoids the image interpolating an AWS-valid backslash differently in
// its own bootstrap SQL and the authentication attempt that follows.
func mysql8CredentialArchive(masterUser, masterPassword string, spec masterAccountSpec) (*bytes.Reader, error) {
	var sql strings.Builder
	fmt.Fprintf(&sql, "ALTER USER %s@'localhost' IDENTIFIED WITH %s BY %s;\n",
		mysqlString("root"), spec.AuthPlugin, mysqlString(masterPassword))
	writeMySQLMasterUser(&sql, masterUser, masterPassword, spec)
	writeRemoveRemoteRoot(&sql, masterUser)

	script := "docker_process_sql --database=mysql <<'OVERCAST_SQL'\n" +
		sql.String() +
		"OVERCAST_SQL\n" +
		"export MYSQL_ROOT_PASSWORD=" + shellSingleQuoted(masterPassword) + "\n"

	return credentialInitArchive(mysqlCredentialInitScript, script)
}

func mysqlCredentialArchive(masterUser, masterPassword string, spec masterAccountSpec) (*bytes.Reader, error) {
	var sql strings.Builder
	writeMySQLMasterUser(&sql, masterUser, masterPassword, spec)
	writeRemoveRemoteRoot(&sql, masterUser)
	return credentialInitArchive("10-overcast-credentials.sql", sql.String())
}

func writeMySQLMasterUser(sql *strings.Builder, masterUser, masterPassword string, spec masterAccountSpec) {
	if masterUser == "" {
		return
	}
	identifiedBy := " IDENTIFIED BY "
	if spec.AuthPlugin != "" {
		identifiedBy = " IDENTIFIED WITH " + spec.AuthPlugin + " BY "
	}
	fmt.Fprintf(sql, "CREATE USER IF NOT EXISTS %s@'%%'%s%s;\n",
		mysqlString(masterUser), identifiedBy, mysqlString(masterPassword))
	privileges := strings.Join(spec.Privileges, ", ")
	if spec.RoleName == "" {
		fmt.Fprintf(sql, "GRANT %s ON *.* TO %s@'%%' WITH GRANT OPTION;\n",
			privileges, mysqlString(masterUser))
		return
	}
	role := mysqlString(spec.RoleName)
	fmt.Fprintf(sql, "CREATE ROLE IF NOT EXISTS %s@'%%';\n", role)
	fmt.Fprintf(sql, "GRANT %s ON *.* TO %s@'%%' WITH GRANT OPTION;\n", privileges, role)
	fmt.Fprintf(sql, "GRANT %s@'%%' TO %s@'%%';\n", role, mysqlString(masterUser))
	fmt.Fprintf(sql, "SET DEFAULT ROLE %s@'%%' TO %s@'%%';\n", role, mysqlString(masterUser))
}

func writeRemoveRemoteRoot(sql *strings.Builder, masterUser string) {
	if masterUser != "root" {
		fmt.Fprintf(sql, "DROP USER IF EXISTS %s@'%%';\n", mysqlString("root"))
	}
}

func shellSingleQuoted(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}
