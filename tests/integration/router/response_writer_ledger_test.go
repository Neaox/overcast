package router_test

// response_writer_ledger_test.go — no AWS operation may encode its own success
// response.
//
// The request-ID header is set by the protocol write helpers and by nothing
// else: middleware.RequestID deliberately sets none, because the header's name
// depends on the service's protocol. So a handler that writes
// `json.NewEncoder(w).Encode(...)` straight onto the ResponseWriter silently
// opts its operation out of a header every real AWS response carries.
//
// That is not a hypothetical. Lambda's entire success surface — 38 operations
// across eight files — did exactly this, and no test caught it for the whole
// life of the service, because every request-ID assertion in the suite happened
// to be on an *error* response, and errors do go through a helper. ECS's
// account-settings and container-instance handlers had the same hole while the
// rest of their own service wrote through protocol.WriteAWSJSON.
//
// A per-operation assertion cannot close this: it only covers the operations
// somebody remembered to list, which is the failure mode all over again. So the
// ledger below is the check, in the same shape as the model-binding ledger in
// internal/router — a scan of the shipping source with a named, reasoned
// exception list.

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// directEncode is the call that bypasses the protocol writers.
const directEncode = "json.NewEncoder(w).Encode("

// emulatorOnlyWriters are the files allowed to encode straight onto the
// ResponseWriter, because what they serve is not an AWS operation and so has no
// AWS response contract to honour. Each entry says which surface it is.
//
// Adding a file here is a claim that a real AWS client can never reach it. If
// that is not true, the fix is protocol.WriteRESTJSON / WriteAWSJSON, not an
// entry in this map.
var emulatorOnlyWriters = map[string]string{
	// Endpoints under /_<service>/… , served to the web console only.
	"cloudformation/handler_emulator.go": "/_overcast/cloudformation deploy diagnostics for the console",
	"ec2/handler_debug.go":               "/_ec2 debug endpoints",
	"ecs/handler_emulator.go":            "/_ecs task inspection for the console",
	"lambda/handler_instances.go":        "/_overcast/lambda/instances for the console",
	"rds/handler_emulator.go":            "/_rds diagnostics for the console",
	"secretsmanager/service.go":          "/_secretsmanager console admin routes",
	"ses/service.go":                     "/_ses console admin routes",
	"sns/service.go":                     "/_sns console admin routes",
	"lambda/handler_source.go":           "the console's source editor, on the Lambda path prefix but not an AWS operation",
	"lambda/handler_layers.go":           "GetLayerVersionMetadata, a /_lambda extension-discovery route",
	"lambda/handler_functions.go":        "ListRuntimes and the saved test-event routes, all emulator-only",
	"lambda/handler_metrics.go":          "/_overcast/lambda/functions/{name}/metrics, the web Monitor tab's read-through (service-metrics-platform.md phase 3)",
	"sqs/handler_metrics.go":             "/_overcast/sqs/queues/{name}/metrics, the web Monitor section's read-through (service-metrics-platform.md phase 3)",
	"sns/handler_metrics.go":             "/_overcast/sns/topics/{topicName}/metrics, the web Monitor tab's read-through (service-metrics-platform.md phase 4)",
	"dynamodb/handler_metrics.go":        "/_overcast/dynamodb/tables/{name}/metrics, the web Monitor tab's read-through (service-metrics-platform.md phase 4)",
	"cognito/handler_managed_login.go":   "the hosted-UI OAuth endpoints, which answer as an OIDC provider rather than as an AWS API",
	"cognito/jwt.go":                     "the pool's JWKS document, fetched by JWT verifiers at /.well-known/jwks.json",

	// Not an AWS API at all: the Runtime and Extensions APIs Lambda serves to
	// the inside of a function container.
	"lambda/runtime_api.go": "the in-container Lambda Runtime/Extensions API",
}

func TestServiceHandlers_writeSuccessResponsesThroughTheProtocolHelpers(t *testing.T) {
	// Given: the shipping source of every service package.
	root := servicesRoot(t)

	// When: it is scanned for handlers encoding their own responses.
	var offenders []string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		rel = filepath.ToSlash(rel)
		if _, allowed := emulatorOnlyWriters[rel]; allowed {
			return nil
		}
		source, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if strings.Contains(string(source), directEncode) {
			offenders = append(offenders, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	sort.Strings(offenders)

	// Then: none does. A response written this way carries no request ID, which
	// an SDK surfaces as empty ResponseMetadata and a trace cannot correlate on.
	if len(offenders) > 0 {
		t.Errorf("these files encode a response onto the ResponseWriter directly, so it carries no request-ID header:\n  %s\n"+
			"Use protocol.WriteRESTJSON (application/json) or protocol.WriteAWSJSON (an x-amz-json target protocol) instead.\n"+
			"If the endpoint is emulator-only and no AWS client can reach it, add it to emulatorOnlyWriters with the surface it serves.",
			strings.Join(offenders, "\n  "))
	}

	// And: no exception outlives the file it describes, so a deleted or
	// converted handler cannot leave a stale excuse behind.
	var stale []string
	for rel := range emulatorOnlyWriters {
		source, readErr := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if readErr != nil {
			stale = append(stale, rel+" (no such file)")
			continue
		}
		if !strings.Contains(string(source), directEncode) {
			stale = append(stale, rel+" (no longer encodes directly)")
		}
	}
	sort.Strings(stale)
	if len(stale) > 0 {
		t.Errorf("emulatorOnlyWriters has entries that no longer describe anything: %s", strings.Join(stale, ", "))
	}
}

// servicesRoot locates internal/services relative to this test file's package.
func servicesRoot(t *testing.T) string {
	t.Helper()
	// tests/integration/router → repository root.
	root := filepath.Join("..", "..", "..", "internal", "services")
	if _, err := os.Stat(root); err != nil {
		t.Fatalf("cannot find internal/services from the test's working directory: %v", err)
	}
	return root
}
