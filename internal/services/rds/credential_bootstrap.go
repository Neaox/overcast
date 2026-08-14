package rds

import (
	"archive/tar"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

const (
	credentialBootstrapMarker     = "/tmp/.overcast-rds-bootstrap-complete"
	credentialBootstrapMarkerFile = "99-overcast-bootstrap-complete.sh"
)

// credentialBootstrapPassword returns an entrypoint-safe temporary password.
// Hex has no SQL or option-file metacharacters, and deriving it keeps container
// rebuilds stable without persisting a second credential.
func credentialBootstrapPassword(masterPassword string) string {
	sum := sha256.Sum256([]byte(masterPassword))
	return hex.EncodeToString(sum[:])
}

func credentialArchiveForInstance(inst *DBInstance, image string) (*bytes.Reader, error) {
	switch inst.Engine {
	case "mysql", "aurora-mysql":
		spec := mysqlMasterAccountFor(inst.Engine, inst.EngineVersion)
		if usesMySQL8CredentialBootstrap(inst.Engine, image) {
			return mysql8CredentialArchive(inst.MasterUsername, inst.MasterUserPassword, spec)
		}
		return mysqlCredentialArchive(inst.MasterUsername, inst.MasterUserPassword, spec)
	case "mariadb":
		return mysqlCredentialArchive(inst.MasterUsername, inst.MasterUserPassword,
			mysqlMasterAccountFor(inst.Engine, inst.EngineVersion))
	case "postgres", "aurora-postgresql":
		return postgresCredentialArchive(inst.MasterUsername, inst.MasterUserPassword, inst.DBName)
	default:
		return nil, fmt.Errorf("engine %q has no credential initializer", inst.Engine)
	}
}

func credentialInitArchive(name, contents string) (*bytes.Reader, error) {
	var archive bytes.Buffer
	tw := tar.NewWriter(&archive)
	if err := writeCredentialArchiveFile(tw, name, contents); err != nil {
		return nil, err
	}
	if err := writeCredentialArchiveFile(tw, credentialBootstrapMarkerFile,
		"touch "+credentialBootstrapMarker+"\n"); err != nil {
		return nil, err
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	return bytes.NewReader(archive.Bytes()), nil
}

func writeCredentialArchiveFile(tw *tar.Writer, name, contents string) error {
	if err := tw.WriteHeader(&tar.Header{
		Name: name,
		Mode: 0o644, // readable by the engine; non-executable shell files are sourced
		Size: int64(len(contents)),
	}); err != nil {
		return err
	}
	_, err := tw.Write([]byte(contents))
	return err
}
