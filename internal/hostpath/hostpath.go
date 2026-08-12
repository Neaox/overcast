// Package hostpath normalizes developer-supplied host paths for bind mounts,
// and explains the mount failures those paths cause.
//
// Two compute services take a host path from the user and hand it to the
// Docker daemon — Lambda's hot reload and ECS's — and both face the same two
// problems. A path written the way Windows writes it is not a path the daemon
// accepts, and a bind that the daemon refuses fails with a message that names
// neither the path nor what to do about it. Solving those twice, in two
// packages, is how the two features drift apart.
package hostpath

import (
	"fmt"
	"path"
	"strings"
	"unicode"
)

// Normalize converts a user-supplied absolute host path into the form the
// Docker daemon accepts, and rejects anything that is not absolute.
//
// A relative path is refused rather than resolved against the process's
// working directory: Overcast's working directory is not the user's, and
// silently binding the wrong tree is worse than saying no.
func Normalize(raw string) (string, error) {
	p := strings.TrimSpace(raw)
	if p == "" {
		return "", fmt.Errorf("path is empty")
	}

	// Convert Windows drive-letter paths to Docker-compatible mount paths.
	// Example: C:\Users\dev\app -> /c/Users/dev/app
	if len(p) >= 3 && unicode.IsLetter(rune(p[0])) && p[1] == ':' && (p[2] == '\\' || p[2] == '/') {
		drive := strings.ToLower(string(p[0]))
		rest := strings.ReplaceAll(p[2:], "\\", "/")
		p = "/" + drive + rest
	}

	p = strings.ReplaceAll(p, "\\", "/")
	p = path.Clean(p)
	if !strings.HasPrefix(p, "/") {
		return "", fmt.Errorf("%q is not absolute", raw)
	}
	return p, nil
}

// DecorateMountError adds the missing half of a Docker bind-mount failure: the
// path that failed, and the setting that most often explains it.
//
// Docker Desktop shares only a configured set of host directories with its VM,
// and a bind outside that set fails with a message that names neither the path
// nor the setting. Anything not recognisably a mount failure is returned
// unchanged rather than dressed up as one.
func DecorateMountError(err error, hostPath string) error {
	if err == nil || hostPath == "" {
		return err
	}
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "mounts denied") ||
		strings.Contains(msg, "invalid mount config") ||
		strings.Contains(msg, "bind source path does not exist") {
		return fmt.Errorf("bind mount failed for %q: %w; if using Docker Desktop, allow this path in File Sharing settings: https://docs.docker.com/desktop/settings-and-maintenance/settings/#file-sharing", hostPath, err)
	}
	return err
}
