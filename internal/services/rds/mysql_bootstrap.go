package rds

import (
	"archive/tar"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

const mysqlCredentialInitScript = "10-overcast-credentials.sh"

// mysql8BootstrapPassword returns an entrypoint-safe temporary root password.
// The official mysql:8.0 entrypoint inserts MYSQL_ROOT_PASSWORD into an SQL
// string without escaping it, then authenticates with the original value. An
// AWS-valid password containing a backslash can therefore be stored differently
// from the password used by the next entrypoint command. Hex has no SQL or
// option-file metacharacters, and deriving it keeps container rebuilds stable.
func mysql8BootstrapPassword(masterPassword string) string {
	sum := sha256.Sum256([]byte(masterPassword))
	return hex.EncodeToString(sum[:])
}

func usesMySQL8CredentialBootstrap(engine, image string) bool {
	return (engine == "mysql" || engine == "aurora-mysql") && strings.HasPrefix(image, "mysql:8.0")
}

// mysql8CredentialArchive builds a non-executable shell fragment. The MySQL
// image sources non-executable .sh init files into its entrypoint, which lets
// the fragment update MYSQL_ROOT_PASSWORD after installing the real password;
// the entrypoint's subsequent mysqladmin shutdown therefore uses the new one.
func mysql8CredentialArchive(masterUser, masterPassword, dbName string) (*bytes.Reader, error) {
	var sql strings.Builder
	fmt.Fprintf(&sql, "CREATE USER IF NOT EXISTS %s@'%%' IDENTIFIED WITH caching_sha2_password BY %s;\n",
		mysqlString("root"), mysqlString(masterPassword))
	fmt.Fprintf(&sql, "GRANT ALL ON *.* TO %s@'%%' WITH GRANT OPTION;\n", mysqlString("root"))
	fmt.Fprintf(&sql, "ALTER USER %s@'localhost' IDENTIFIED WITH caching_sha2_password BY %s;\n",
		mysqlString("root"), mysqlString(masterPassword))
	if masterUser != "" && masterUser != "root" {
		fmt.Fprintf(&sql, "CREATE USER IF NOT EXISTS %s@'%%' IDENTIFIED WITH caching_sha2_password BY %s;\n",
			mysqlString(masterUser), mysqlString(masterPassword))
		fmt.Fprintf(&sql, "GRANT ALL ON %s.* TO %s@'%%';\n",
			mysqlIdentifier(dbName), mysqlString(masterUser))
	}

	script := "docker_process_sql --database=mysql <<'OVERCAST_SQL'\n" +
		sql.String() +
		"OVERCAST_SQL\n" +
		"export MYSQL_ROOT_PASSWORD=" + shellSingleQuoted(masterPassword) + "\n"

	var archive bytes.Buffer
	tw := tar.NewWriter(&archive)
	if err := tw.WriteHeader(&tar.Header{
		Name: mysqlCredentialInitScript,
		Mode: 0o644, // readable by mysql, non-executable so the entrypoint sources it
		Size: int64(len(script)),
	}); err != nil {
		return nil, err
	}
	if _, err := tw.Write([]byte(script)); err != nil {
		return nil, err
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	return bytes.NewReader(archive.Bytes()), nil
}

func mysqlIdentifier(value string) string {
	return "`" + strings.ReplaceAll(value, "`", "``") + "`"
}

func shellSingleQuoted(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}
