package config

import "testing"

// The value is joined to a port and handed to every Lambda container, so a bad
// one is not caught until a function fails to initialise — and "host:port" is
// the easy mistake, because the docs and the startup log both render the result
// with its port attached. net.JoinHostPort would turn that into
// "host.docker.internal:9001:9001", advertised to every container, baked into
// AWS_ENDPOINT_URL, and reported by /_overcast/health as a healthy listener.
func TestValidateRuntimeAPIHost(t *testing.T) {
	valid := []string{
		"",                     // auto, normalised away before this runs
		"host.docker.internal", // the value that fixes a Docker Desktop host
		"192.168.1.20",         // an IP, right for a remote or DinD daemon
		"::1",                  // an IPv6 literal, whose colons are its own
		"fd00::1",              //
		"overcast.internal.",   // a fully-qualified name
		"a-b.c-d.example",      //
	}
	for _, host := range valid {
		if err := validateRuntimeAPIHost(host); err != nil {
			t.Errorf("validateRuntimeAPIHost(%q) = %v, want accepted", host, err)
		}
	}

	invalid := map[string]string{
		"a port":            "host.docker.internal:9001",
		"an IP with a port": "192.168.1.20:9001",
		"a URL":             "http://host.docker.internal:9001",
		"a path":            "host.docker.internal/runtime",
		"whitespace":        "host.docker.internal ",
		"an inner space":    "host docker internal",
		"an empty label":    "host..internal",
		"a leading hyphen":  "-host.internal",
		"a trailing hyphen": "host-.internal",
		"an underscore":     "host_docker_internal",
	}
	for name, host := range invalid {
		t.Run(name, func(t *testing.T) {
			err := validateRuntimeAPIHost(host)
			if err == nil {
				t.Fatalf("validateRuntimeAPIHost(%q) = nil, want rejected", host)
			}
			// The message has to say what to write instead — this is read by
			// somebody whose Lambda just stopped working.
			if !contains(err.Error(), "LAMBDA_RUNTIME_API_HOST") || !contains(err.Error(), "auto") {
				t.Errorf("error does not name the variable and the fix: %v", err)
			}
		})
	}
}

func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
