package config_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// dockerfileEnvAssignment matches one NAME=value token inside an ENV
// instruction, capturing the name. Values never contain whitespace in this
// Dockerfile (paths and bare words), so token-splitting the instruction is
// sufficient.
var dockerfileEnvAssignment = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_]*)=`)

// localstackAliasVars are the LocalStack-documented variables internal/config
// reads as compatibility aliases or recognised no-ops (see
// localstack_aliases.go and resolveHostnameAlias). Baking any of them into
// the image would be even worse than baking the Overcast variable it
// aliases: it would look like user intent on the LocalStack side of every
// conflict check.
var localstackAliasVars = []string{
	"EDGE_PORT", "DEFAULT_REGION", "DATA_DIR", "GATEWAY_LISTEN", "DEBUG",
	"PERSISTENCE", "LOCALSTACK_HOST", "HOSTNAME_EXTERNAL",
	"LAMBDA_RUNTIME_ENVIRONMENT_TIMEOUT",
	"SERVICES", "LOCALSTACK_API_KEY", "LOCALSTACK_AUTH_TOKEN",
}

// TestDockerfileBakesNoConfigDefaults pins the set of configuration
// variables the Docker image bakes as ENV to exactly OVERCAST_DATA_DIR plus
// its provenance marker. Anything baked as ENV is indistinguishable at
// runtime from a value the user passed with `docker run -e`, so Load treats
// it as an explicit setting — and an explicit setting turns every
// LocalStack-compatibility alias that disagrees with it into a startup
// conflict. The image used to bake OVERCAST_PORT, OVERCAST_LISTEN,
// OVERCAST_LOG_LEVEL, OVERCAST_DEFAULT_REGION, OVERCAST_ACCOUNT_ID and
// OVERCAST_DEBUG (each merely restating the binary's own default), which
// made `docker run -e DEFAULT_REGION=eu-west-1` — the documented drop-in
// LocalStack migration path — fail startup. The binary's defaults make the
// baked copies unnecessary; this test keeps them from creeping back.
//
// OVERCAST_DATA_DIR is the one genuinely image-specific default, and it is
// allowed only together with OVERCAST_DATA_DIR_SOURCE=image — the marker
// that tells Load it is a default (aliases override it, and
// OVERCAST_STATE=auto does not read it as a persistence signal).
func TestDockerfileBakesNoConfigDefaults(t *testing.T) {
	baked := parseDockerfileEnv(t)

	// Every OVERCAST_* variable, and every LocalStack alias, is off-limits —
	// except the data-dir pair.
	allowed := map[string]bool{
		"OVERCAST_DATA_DIR":        true,
		"OVERCAST_DATA_DIR_SOURCE": true,
	}
	for name := range baked {
		if allowed[name] {
			continue
		}
		if strings.HasPrefix(name, "OVERCAST_") {
			t.Errorf("Dockerfile bakes ENV %s=%q: a baked OVERCAST_* value reads as an explicit setting and conflicts with any disagreeing LocalStack alias at startup — rely on the binary's default instead (see the comment on the ENV block)", name, baked[name])
		}
		for _, alias := range localstackAliasVars {
			if name == alias {
				t.Errorf("Dockerfile bakes ENV %s=%q, a LocalStack-documented variable internal/config reads — the image must not pre-set it", name, baked[name])
			}
		}
	}

	// And the pair that is allowed must stay intact: the data dir without its
	// marker would read as user intent (flipping OVERCAST_STATE=auto to
	// hybrid on every volume-less `docker run`), and the marker without the
	// data dir would mark nothing.
	if got := baked["OVERCAST_DATA_DIR"]; got != "/data" {
		t.Errorf("Dockerfile ENV OVERCAST_DATA_DIR: expected the baked /data default, got %q", got)
	}
	if got := baked["OVERCAST_DATA_DIR_SOURCE"]; got != "image" {
		t.Errorf("Dockerfile ENV OVERCAST_DATA_DIR_SOURCE: expected \"image\" alongside the baked OVERCAST_DATA_DIR, got %q", got)
	}
}

// parseDockerfileEnv reads the repository Dockerfile and returns every
// NAME=value assignment made by an ENV instruction (across all stages),
// following backslash line continuations.
func parseDockerfileEnv(t *testing.T) map[string]string {
	t.Helper()

	path := filepath.Join("..", "..", "Dockerfile")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}

	baked := map[string]string{}
	inEnv := false
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimRight(line, "\r")
		trimmed := strings.TrimSpace(line)
		continues := strings.HasSuffix(trimmed, "\\")

		var body string
		switch {
		case inEnv:
			body = trimmed
		case strings.HasPrefix(trimmed, "ENV "):
			body = strings.TrimSpace(strings.TrimPrefix(trimmed, "ENV "))
		default:
			continue
		}
		inEnv = continues
		body = strings.TrimSuffix(body, "\\")

		for _, token := range strings.Fields(body) {
			m := dockerfileEnvAssignment.FindStringSubmatch(token)
			if m == nil {
				continue
			}
			baked[m[1]] = strings.Trim(token[len(m[1])+1:], `"`)
		}
	}
	if len(baked) == 0 {
		t.Fatalf("found no ENV assignments in %s — the parser or the Dockerfile changed shape", path)
	}
	return baked
}
