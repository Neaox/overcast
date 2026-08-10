package ecr

// registry_credentials_test.go — the shared registry's htpasswd file.
//
// It used to reach the container as a bind mount of a path under DataDir. A
// bind source is resolved by the Docker daemon, so that only worked when
// Overcast's filesystem was the daemon's: run the published image and the
// daemon found no such path, created an empty directory in its place, and the
// registry came up with nothing to authenticate against — every `docker push`
// to the emulated ECR refused, with no error anywhere saying why. The file is
// copied into the container instead, so it is built here rather than written.

import (
	"archive/tar"
	"bytes"
	"io"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

func TestHTPasswdArchive_carriesTheCredentialTheTokenGrants(t *testing.T) {
	// Given: the password GetAuthorizationToken hands out.
	const password = "s3cret-registry-password"

	// When: the credential archive is built.
	archive, err := htpasswdArchive(password)
	if err != nil {
		t.Fatalf("htpasswdArchive: %v", err)
	}

	// Then: it holds one entry, at the path the registry is configured to read,
	// named relative to the root it is unpacked into.
	tr := tar.NewReader(bytes.NewReader(archive))
	hdr, err := tr.Next()
	if err != nil {
		t.Fatalf("read archive: %v", err)
	}
	if hdr.Name != "auth/htpasswd" {
		t.Errorf("entry name = %q, want auth/htpasswd", hdr.Name)
	}
	if hdr.Mode&0o004 == 0 {
		t.Errorf("mode = %o, want world-readable: the registry image runs as its own user", hdr.Mode)
	}

	// Then: the entry authenticates the token's user with the token's password.
	body, err := io.ReadAll(tr)
	if err != nil {
		t.Fatalf("read entry: %v", err)
	}
	user, hash, ok := bytes.Cut(bytes.TrimSpace(body), []byte(":"))
	if !ok {
		t.Fatalf("entry is not an htpasswd line: %q", body)
	}
	if string(user) != ecrRegistryUser {
		t.Errorf("user = %q, want %q", user, ecrRegistryUser)
	}
	if err := bcrypt.CompareHashAndPassword(hash, []byte(password)); err != nil {
		t.Errorf("hash does not match the password the token carries: %v", err)
	}

	if _, err := tr.Next(); err != io.EOF {
		t.Errorf("archive holds more than the credentials file (err=%v)", err)
	}
}
