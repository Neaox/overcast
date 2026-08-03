package cloudformation

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// dynamic_refs_test.go — parsing and expansion of {{resolve:...}} references.
//
// The reference that prompted this is the one CDK emits for an RDS instance's
// master username: a full secret ARN, itself six colons deep, followed by the
// SecretString selector, a JSON key, and two empty trailing fields. Splitting
// it naively on ":" tears the ARN apart, so the ARN-awareness below is the
// whole difficulty of parsing these.

const arnRef = "{{resolve:secretsmanager:arn:aws:secretsmanager:ap-southeast-2:000000000000:" +
	"secret:sm-l-ase2-bc-new-owners-website-database-TvhpC3:SecretString:username::}}"

func TestParseDynamicRef(t *testing.T) {
	for _, tc := range []struct {
		name        string
		raw         string
		wantOK      bool
		wantService string
		wantFields  []string
	}{
		{
			name:        "secret ARN keeps its own colons",
			raw:         arnRef,
			wantOK:      true,
			wantService: "secretsmanager",
			wantFields: []string{
				"arn:aws:secretsmanager:ap-southeast-2:000000000000:secret:sm-l-ase2-bc-new-owners-website-database-TvhpC3",
				"SecretString", "username", "", "",
			},
		},
		{
			name:        "secret by name",
			raw:         "{{resolve:secretsmanager:db-creds:SecretString:password}}",
			wantOK:      true,
			wantService: "secretsmanager",
			wantFields:  []string{"db-creds", "SecretString", "password"},
		},
		{
			name:        "secret with no selector at all",
			raw:         "{{resolve:secretsmanager:db-creds}}",
			wantOK:      true,
			wantService: "secretsmanager",
			wantFields:  []string{"db-creds"},
		},
		{
			name:        "secret with a version stage",
			raw:         "{{resolve:secretsmanager:db-creds:SecretString:password:AWSPREVIOUS}}",
			wantOK:      true,
			wantService: "secretsmanager",
			wantFields:  []string{"db-creds", "SecretString", "password", "AWSPREVIOUS"},
		},
		{
			name:        "ssm parameter",
			raw:         "{{resolve:ssm:/app/tier}}",
			wantOK:      true,
			wantService: "ssm",
			wantFields:  []string{"/app/tier"},
		},
		{
			name:        "ssm-secure with a version",
			raw:         "{{resolve:ssm-secure:/app/api-key:3}}",
			wantOK:      true,
			wantService: "ssm-secure",
			wantFields:  []string{"/app/api-key", "3"},
		},
		{name: "not a reference", raw: "plain-value"},
		{name: "not a resolve reference", raw: "{{something:else}}"},
		{name: "no service", raw: "{{resolve:}}"},
		{name: "service with no payload", raw: "{{resolve:ssm}}"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ref, ok := parseDynamicRef(tc.raw)
			if ok != tc.wantOK {
				t.Fatalf("parseDynamicRef(%q) ok = %v, want %v", tc.raw, ok, tc.wantOK)
			}
			if !tc.wantOK {
				return
			}
			if ref.Service != tc.wantService {
				t.Errorf("service = %q, want %q", ref.Service, tc.wantService)
			}
			if len(ref.Fields) != len(tc.wantFields) {
				t.Fatalf("fields = %#v, want %#v", ref.Fields, tc.wantFields)
			}
			for i, want := range tc.wantFields {
				if ref.Fields[i] != want {
					t.Errorf("field %d = %q, want %q", i, ref.Fields[i], want)
				}
			}
			if ref.Raw != tc.raw {
				t.Errorf("Raw = %q, want the reference as written", ref.Raw)
			}
		})
	}
}

// field() must read an omitted trailing field as empty rather than panicking —
// "…:username::" and "…:username" both mean "no version stage, no version ID".
func TestDynamicRef_fieldBeyondTheEndIsEmpty(t *testing.T) {
	ref, ok := parseDynamicRef("{{resolve:secretsmanager:db-creds}}")
	if !ok {
		t.Fatal("parseDynamicRef failed on a valid reference")
	}
	for _, i := range []int{1, 2, 3, 4, 99} {
		if got := ref.field(i); got != "" {
			t.Errorf("field(%d) = %q, want empty", i, got)
		}
	}
	if got := ref.field(-1); got != "" {
		t.Errorf("field(-1) = %q, want empty", got)
	}
}

// stubRefCtx returns a resolve context whose resolver answers from a map.
func stubRefCtx(values map[string]string) *resolveContext {
	return &resolveContext{
		Region:    "us-east-1",
		AccountID: "000000000000",
		StackName: "s",
		DynamicRef: func(ref dynamicRef) (string, error) {
			v, ok := values[ref.Raw]
			if !ok {
				return "", fmt.Errorf("no stub value for %s", ref.Raw)
			}
			return v, nil
		},
	}
}

// The property tree is what the services actually receive, so expansion has to
// reach strings at every depth — not just top-level scalars.
func TestExpandDynamicRefs_replacesReferencesThroughoutAPropertiesTree(t *testing.T) {
	ctx := stubRefCtx(map[string]string{
		arnRef:                       "appuser",
		"{{resolve:ssm:/db/name}}":   "newowners",
		"{{resolve:ssm:/db/engine}}": "mysql",
	})

	props := map[string]any{
		"MasterUsername":   arnRef,
		"Engine":           "{{resolve:ssm:/db/engine}}",
		"AllocatedStorage": float64(20),
		"Tags": []any{
			map[string]any{"Key": "db", "Value": "{{resolve:ssm:/db/name}}"},
		},
		"Description": "database {{resolve:ssm:/db/name}} for {{resolve:ssm:/db/engine}}",
	}

	got, ok := expandDynamicRefs(props, ctx).(map[string]any)
	if !ok {
		t.Fatal("expandDynamicRefs did not return a map")
	}
	if err := ctx.takeDynamicRefErr(); err != nil {
		t.Fatalf("unexpected resolution failure: %v", err)
	}

	if got["MasterUsername"] != "appuser" {
		t.Errorf("MasterUsername = %v, want %q", got["MasterUsername"], "appuser")
	}
	if got["Engine"] != "mysql" {
		t.Errorf("Engine = %v, want %q", got["Engine"], "mysql")
	}
	if got["AllocatedStorage"] != float64(20) {
		t.Errorf("AllocatedStorage = %v, want it untouched", got["AllocatedStorage"])
	}
	// Nested inside a list of maps.
	tag := got["Tags"].([]any)[0].(map[string]any)
	if tag["Value"] != "newowners" {
		t.Errorf("Tags[0].Value = %v, want %q", tag["Value"], "newowners")
	}
	// Two references embedded in surrounding text.
	if want := "database newowners for mysql"; got["Description"] != want {
		t.Errorf("Description = %v, want %q", got["Description"], want)
	}
}

// A resolved value is never rescanned. Expansion runs as its own pass after the
// intrinsics precisely so a secret whose content happens to look like a
// reference cannot reach back into the resolver.
func TestExpandDynamicRefs_resolvedValueIsNotRescanned(t *testing.T) {
	ctx := stubRefCtx(map[string]string{
		"{{resolve:ssm:/outer}}": "{{resolve:ssm:/inner}}",
	})

	got := expandDynamicRefs("{{resolve:ssm:/outer}}", ctx)

	if got != "{{resolve:ssm:/inner}}" {
		t.Errorf("expandDynamicRefs = %v, want the resolved value left alone", got)
	}
	if err := ctx.takeDynamicRefErr(); err != nil {
		t.Errorf("resolver was invoked for the resolved value: %v", err)
	}
}

// An unresolvable reference must be recorded, not swallowed: the provisioner
// turns the recorded error into a resource failure, which is what stops the
// literal reference text reaching a service as if it were a value.
func TestExpandDynamicRefs_unresolvableReferenceIsRecorded(t *testing.T) {
	ctx := stubRefCtx(nil)

	got := expandDynamicRefs(map[string]any{"MasterUsername": arnRef}, ctx).(map[string]any)

	if got["MasterUsername"] != arnRef {
		t.Errorf("MasterUsername = %v, want the reference left in place for the failed resource", got["MasterUsername"])
	}
	err := ctx.takeDynamicRefErr()
	if err == nil {
		t.Fatal("no error recorded for an unresolvable reference")
	}
	if !strings.Contains(err.Error(), "resolve {{resolve:secretsmanager:") {
		t.Errorf("error = %v, want it to name the reference that failed", err)
	}
	// And taking it clears it, so the next resource is not blamed for it.
	if again := ctx.takeDynamicRefErr(); again != nil {
		t.Errorf("takeDynamicRefErr left %v behind", again)
	}
}

// Only the first failure is kept — later references in the same resource are
// usually collateral and burying the first one helps nobody.
func TestExpandDynamicRefs_firstFailureIsTheOneReported(t *testing.T) {
	ctx := &resolveContext{
		DynamicRef: func(ref dynamicRef) (string, error) {
			return "", errors.New("boom " + ref.field(0))
		},
	}

	expandDynamicRefs([]any{"{{resolve:ssm:/first}}", "{{resolve:ssm:/second}}"}, ctx)

	err := ctx.takeDynamicRefErr()
	if err == nil || !strings.Contains(err.Error(), "/first") {
		t.Errorf("error = %v, want the first failure", err)
	}
}

// Without a resolver — a template resolved outside provisioning — a reference
// must be reported rather than silently left in a property value.
func TestExpandDynamicRefs_missingResolverIsAnError(t *testing.T) {
	ctx := &resolveContext{}

	got := expandDynamicRefs("{{resolve:ssm:/app/tier}}", ctx)

	if got != "{{resolve:ssm:/app/tier}}" {
		t.Errorf("expandDynamicRefs = %v, want the reference unchanged", got)
	}
	if err := ctx.takeDynamicRefErr(); err == nil {
		t.Error("no error recorded when no resolver was installed")
	}
}

// Strings with no reference in them must not be touched, and must not cost a
// regexp scan — every property value in every template goes through here.
func TestExpandDynamicRefs_leavesOrdinaryStringsAlone(t *testing.T) {
	ctx := stubRefCtx(nil)
	for _, s := range []string{"", "admin", "{{notresolve}}", "arn:aws:s3:::bucket"} {
		if got := expandDynamicRefs(s, ctx); got != s {
			t.Errorf("expandDynamicRefs(%q) = %v, want it unchanged", s, got)
		}
	}
	if err := ctx.takeDynamicRefErr(); err != nil {
		t.Errorf("unexpected error for reference-free strings: %v", err)
	}
}

// resolveAllProperties is the seam the provisioner actually calls; prove the
// expansion happens there rather than only in the helper under test.
func TestResolveAllProperties_expandsDynamicReferencesAfterIntrinsics(t *testing.T) {
	ctx := stubRefCtx(map[string]string{"{{resolve:secretsmanager:db-creds:SecretString:username}}": "appuser"})
	ctx.Params = map[string]string{"SecretName": "db-creds"}

	props := resolveAllProperties(map[string]any{
		"MasterUsername": map[string]any{
			"Fn::Sub": "{{resolve:secretsmanager:${SecretName}:SecretString:username}}",
		},
	}, ctx)

	if err := ctx.takeDynamicRefErr(); err != nil {
		t.Fatalf("unexpected resolution failure: %v", err)
	}
	if props["MasterUsername"] != "appuser" {
		t.Errorf("MasterUsername = %v, want the reference resolved after Fn::Sub built it", props["MasterUsername"])
	}
}
