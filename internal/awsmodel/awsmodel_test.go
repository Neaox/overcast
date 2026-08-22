package awsmodel

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestParseModel_extractsDirectServiceOperationsAndProtocols(t *testing.T) {
	// Given: a minimal official-style Smithy JSON AST with a JSON service that
	// carries more than one protocol trait, and a REST service.
	path := filepath.Join(t.TempDir(), "queue.json")
	writeAWSModel(t, path, `{
  "smithy":"2.0",
  "shapes":{
    "example.queue#Queue_20260101":{"type":"service","version":"2026-01-01","operations":[{"target":"example.queue#CreateQueue"}],"traits":{"aws.api#service":{"sdkId":"Queue","endpointPrefix":"queue"},"aws.protocols#awsJson1_0":{},"smithy.protocols#rpcv2Cbor":{}}},
    "example.queue#CreateQueue":{"type":"operation"}
  }
}`)

	// When: the model is parsed.
	got, err := ParseModel(path)
	if err != nil {
		t.Fatal(err)
	}

	// Then: the single operation carries every field a consumer needs to route
	// and identify it, including the full additive protocol set.
	want := []Operation{{
		Service: "queue", ServiceShape: "Queue_20260101", SDKID: "Queue", APIVersion: "2026-01-01",
		Name: "CreateQueue", Protocol: "AWSJSON10", Protocols: []string{"AWSJSON10", "RPCV2CBOR"},
		TargetPrefix: "Queue_20260101.",
	}}
	assertOperationsEqual(t, got, want)
}

func TestParseModel_extractsRESTHTTPBinding(t *testing.T) {
	path := filepath.Join(t.TempDir(), "widget.json")
	writeAWSModel(t, path, `{
  "smithy":"2.0",
  "shapes":{
    "example.widget#Widget":{"type":"service","version":"2026-02-02","operations":[{"target":"example.widget#GetWidget"}],"traits":{"aws.api#service":{"sdkId":"Widget","endpointPrefix":"widget"},"aws.protocols#restJson1":{}}},
    "example.widget#GetWidget":{"type":"operation","traits":{"smithy.api#http":{"method":"GET","uri":"/widgets/{id}"}}}
  }
}`)

	got, err := ParseModel(path)
	if err != nil {
		t.Fatal(err)
	}

	want := []Operation{{
		Service: "widget", ServiceShape: "Widget", SDKID: "Widget", APIVersion: "2026-02-02",
		Name: "GetWidget", Protocol: "RESTJSON", Protocols: []string{"RESTJSON"},
		HTTPMethod: "GET", URI: "/widgets/{id}",
	}}
	assertOperationsEqual(t, got, want)
}

func TestParseModel_resolvesResourceLifecycleAndNestedResourceOperations(t *testing.T) {
	// Given: a service whose operation is reached only through a resource
	// binding, mirroring how most AWS services declare CRUD operations.
	path := filepath.Join(t.TempDir(), "resource.json")
	writeAWSModel(t, path, `{
  "smithy":"2.0",
  "shapes":{
    "example.resource#ResourceService":{"type":"service","version":"2026-03-03","resources":[{"target":"example.resource#Widget"}],"traits":{"aws.api#service":{"sdkId":"Resource Service","endpointPrefix":"resource"},"aws.protocols#restJson1":{}}},
    "example.resource#Widget":{"type":"resource","operations":[{"target":"example.resource#GetWidget"}],"resources":[{"target":"example.resource#Part"}]},
    "example.resource#GetWidget":{"type":"operation","traits":{"smithy.api#http":{"method":"GET","uri":"/widgets/{id}"}}},
    "example.resource#Part":{"type":"resource","create":{"target":"example.resource#CreatePart"}},
    "example.resource#CreatePart":{"type":"operation","traits":{"smithy.api#http":{"method":"POST","uri":"/parts"}}}
  }
}`)

	// When: the model is parsed.
	got, err := ParseModel(path)
	if err != nil {
		t.Fatal(err)
	}

	// Then: both the resource's own operation and its nested resource's
	// lifecycle operation are resolved, and the service SDK ID with a space is
	// lowercased and hyphenated for the operation's Service field.
	want := []Operation{
		{
			Service: "resource-service", ServiceShape: "ResourceService", SDKID: "Resource Service", APIVersion: "2026-03-03",
			Name: "GetWidget", Protocol: "RESTJSON", Protocols: []string{"RESTJSON"},
			HTTPMethod: "GET", URI: "/widgets/{id}",
		},
		{
			Service: "resource-service", ServiceShape: "ResourceService", SDKID: "Resource Service", APIVersion: "2026-03-03",
			Name: "CreatePart", Protocol: "RESTJSON", Protocols: []string{"RESTJSON"},
			HTTPMethod: "POST", URI: "/parts",
		},
	}
	assertOperationsEqual(t, got, want)
}

func TestParseModel_extractsQueryProtocolAndSigningName(t *testing.T) {
	path := filepath.Join(t.TempDir(), "query.json")
	writeAWSModel(t, path, `{
  "smithy":"2.0",
  "shapes":{
    "example.query#Query":{"type":"service","version":"2026-04-04","operations":[{"target":"example.query#DescribeWidgets"}],"traits":{"aws.api#service":{"sdkId":"Query","endpointPrefix":"query"},"aws.protocols#ec2Query":{},"aws.auth#sigv4":{"name":"query-signing"}}},
    "example.query#DescribeWidgets":{"type":"operation"}
  }
}`)

	got, err := ParseModel(path)
	if err != nil {
		t.Fatal(err)
	}

	want := []Operation{{
		Service: "query", ServiceShape: "Query", SDKID: "Query", APIVersion: "2026-04-04",
		Name: "DescribeWidgets", Protocol: "EC2Query", Protocols: []string{"EC2Query"},
		SigningName: "query-signing",
	}}
	assertOperationsEqual(t, got, want)
}

func TestParseModel_errorsOnServiceMissingServiceTrait(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.json")
	writeAWSModel(t, path, `{"shapes":{"example#Bad":{"type":"service","version":"2026-01-01"}}}`)

	if _, err := ParseModel(path); err == nil {
		t.Fatal("ParseModel() unexpectedly succeeded for a service with no aws.api#service trait")
	}
}

func TestParseModel_errorsOnMissingOperationReference(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.json")
	writeAWSModel(t, path, `{
  "shapes":{
    "example#Bad":{"type":"service","version":"2026-01-01","operations":[{"target":"example#Missing"}],"traits":{"aws.api#service":{"sdkId":"Bad"}}}
  }
}`)

	if _, err := ParseModel(path); err == nil {
		t.Fatal("ParseModel() unexpectedly succeeded referencing a missing operation")
	}
}

func TestParseModel_errorsOnMissingResource(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.json")
	writeAWSModel(t, path, `{
  "shapes":{
    "example#Bad":{"type":"service","version":"2026-01-01","resources":[{"target":"example#Missing"}],"traits":{"aws.api#service":{"sdkId":"Bad"}}}
  }
}`)

	if _, err := ParseModel(path); err == nil {
		t.Fatal("ParseModel() unexpectedly succeeded referencing a missing resource")
	}
}

func TestLoadOperations_walksDirectoryAndSortsDeterministically(t *testing.T) {
	// Given: two model files whose service and operation names sort in the
	// opposite order to the filenames that declare them.
	dir := t.TempDir()
	writeAWSModel(t, filepath.Join(dir, "z-first-on-disk.json"), `{
  "shapes":{
    "example.zeta#Zeta":{"type":"service","version":"2026-01-01","operations":[{"target":"example.zeta#DoThing"}],"traits":{"aws.api#service":{"sdkId":"Zeta"},"aws.protocols#restJson1":{}}},
    "example.zeta#DoThing":{"type":"operation"}
  }
}`)
	writeAWSModel(t, filepath.Join(dir, "a-second-on-disk.json"), `{
  "shapes":{
    "example.alpha#Alpha":{"type":"service","version":"2026-01-01","operations":[{"target":"example.alpha#DoThing"}],"traits":{"aws.api#service":{"sdkId":"Alpha"},"aws.protocols#restJson1":{}}},
    "example.alpha#DoThing":{"type":"operation"}
  }
}`)
	// A non-JSON file in the same tree must be ignored rather than failing the walk.
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("not a model"), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := LoadOperations(dir)
	if err != nil {
		t.Fatal(err)
	}

	if len(got) != 2 {
		t.Fatalf("LoadOperations() returned %d operations, want 2: %+v", len(got), got)
	}
	if got[0].Service != "alpha" || got[1].Service != "zeta" {
		t.Fatalf("LoadOperations() = %+v, want alpha before zeta regardless of file walk order", got)
	}
}

func TestLoadOperations_rejectsEmptyModelDirectory(t *testing.T) {
	if _, err := LoadOperations(t.TempDir()); err == nil {
		t.Fatal("LoadOperations() unexpectedly succeeded for a directory with no service models")
	}
}

func TestVerifyRevision_acceptsMatchingCheckoutHead(t *testing.T) {
	requireGit(t)
	repo := t.TempDir()
	runGit(t, repo, "init", "-q")
	runGit(t, repo, "-c", "user.email=test@example.com", "-c", "user.name=test", "commit", "--allow-empty", "-q", "-m", "initial")
	head := runGit(t, repo, "rev-parse", "HEAD")
	modelsDir := filepath.Join(repo, "models")
	if err := os.Mkdir(modelsDir, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := VerifyRevision(modelsDir, head); err != nil {
		t.Fatalf("VerifyRevision() = %v, want nil for the checkout's own HEAD", err)
	}
}

func TestVerifyRevision_rejectsMismatchedRevision(t *testing.T) {
	requireGit(t)
	repo := t.TempDir()
	runGit(t, repo, "init", "-q")
	runGit(t, repo, "-c", "user.email=test@example.com", "-c", "user.name=test", "commit", "--allow-empty", "-q", "-m", "initial")
	modelsDir := filepath.Join(repo, "models")
	if err := os.Mkdir(modelsDir, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := VerifyRevision(modelsDir, "0000000000000000000000000000000000000000"); err == nil {
		t.Fatal("VerifyRevision() unexpectedly succeeded for a revision that does not match HEAD")
	}
}

func writeAWSModel(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}

func assertOperationsEqual(t *testing.T, got, want []Operation) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %d operations, want %d:\ngot:  %+v\nwant: %+v", len(got), len(want), got, want)
	}
	for i := range want {
		if got[i].Service != want[i].Service || got[i].ServiceShape != want[i].ServiceShape ||
			got[i].SDKID != want[i].SDKID || got[i].APIVersion != want[i].APIVersion ||
			got[i].Name != want[i].Name || got[i].Protocol != want[i].Protocol ||
			got[i].TargetPrefix != want[i].TargetPrefix || got[i].SigningName != want[i].SigningName ||
			got[i].HTTPMethod != want[i].HTTPMethod || got[i].URI != want[i].URI {
			t.Fatalf("operation %d = %+v, want %+v", i, got[i], want[i])
		}
		if len(got[i].Protocols) != len(want[i].Protocols) {
			t.Fatalf("operation %d Protocols = %v, want %v", i, got[i].Protocols, want[i].Protocols)
		}
		for p := range want[i].Protocols {
			if got[i].Protocols[p] != want[i].Protocols[p] {
				t.Fatalf("operation %d Protocols = %v, want %v", i, got[i].Protocols, want[i].Protocols)
			}
		}
	}
}

func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available on PATH")
	}
}

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return trimTrailingNewline(string(out))
}

func trimTrailingNewline(s string) string {
	for len(s) > 0 && (s[len(s)-1] == '\n' || s[len(s)-1] == '\r') {
		s = s[:len(s)-1]
	}
	return s
}
