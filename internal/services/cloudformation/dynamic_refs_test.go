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

	got, ok := expandDynamicRefs("AWS::RDS::DBInstance", props, ctx).(map[string]any)
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

	got := expandDynamicRefs("AWS::RDS::DBInstance", "{{resolve:ssm:/outer}}", ctx)

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

	got := expandDynamicRefs("AWS::RDS::DBInstance", map[string]any{"MasterUsername": arnRef}, ctx).(map[string]any)

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

	expandDynamicRefs("AWS::RDS::DBInstance", []any{"{{resolve:ssm:/first}}", "{{resolve:ssm:/second}}"}, ctx)

	err := ctx.takeDynamicRefErr()
	if err == nil || !strings.Contains(err.Error(), "/first") {
		t.Errorf("error = %v, want the first failure", err)
	}
}

// Without a resolver — a template resolved outside provisioning — a reference
// must be reported rather than silently left in a property value.
func TestExpandDynamicRefs_missingResolverIsAnError(t *testing.T) {
	ctx := &resolveContext{}

	got := expandDynamicRefs("AWS::RDS::DBInstance", "{{resolve:ssm:/app/tier}}", ctx)

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
		if got := expandDynamicRefs("AWS::RDS::DBInstance", s, ctx); got != s {
			t.Errorf("expandDynamicRefs(%q) = %v, want it unchanged", s, got)
		}
	}
	if err := ctx.takeDynamicRefErr(); err != nil {
		t.Errorf("unexpected error for reference-free strings: %v", err)
	}
}

// The two forms of a resource's properties, and why they differ.
//
// resolveAllProperties resolves intrinsics and stops: what it returns is what
// gets hashed and stored, and it must still carry the literal reference so a
// rotated secret does not read as a changed property. Expansion is the separate
// step, and it resolves a reference that Fn::Sub assembled — which is the point
// of expanding after the intrinsics rather than inside them.
func TestResolveAllProperties_leavesDynamicReferencesForTheSeparateExpansionPass(t *testing.T) {
	const ref = "{{resolve:secretsmanager:db-creds:SecretString:username}}"
	ctx := stubRefCtx(map[string]string{ref: "appuser"})
	ctx.Params = map[string]string{"SecretName": "db-creds"}

	recorded := resolveAllProperties(map[string]any{
		"MasterUsername": map[string]any{
			"Fn::Sub": "{{resolve:secretsmanager:${SecretName}:SecretString:username}}",
		},
	}, ctx)

	if err := ctx.takeDynamicRefErr(); err != nil {
		t.Fatalf("unexpected resolution failure: %v", err)
	}
	// Fn::Sub has been applied; the reference has not been resolved.
	if recorded["MasterUsername"] != ref {
		t.Errorf("recorded MasterUsername = %v, want the assembled reference left literal (%q)",
			recorded["MasterUsername"], ref)
	}

	expanded, _ := expandDynamicRefs("AWS::RDS::DBInstance", recorded, ctx).(map[string]any)
	if err := ctx.takeDynamicRefErr(); err != nil {
		t.Fatalf("unexpected expansion failure: %v", err)
	}
	if expanded["MasterUsername"] != "appuser" {
		t.Errorf("expanded MasterUsername = %v, want the secret value", expanded["MasterUsername"])
	}
	// Expansion must not have written through to the recorded form — that is
	// what keeps the resolved secret out of the store.
	if recorded["MasterUsername"] != ref {
		t.Error("expansion mutated the recorded properties, so the resolved secret would be persisted")
	}
}

// ── Custom resource restriction on secure references (issue #606) ─────────
//
// AWS: "Dynamic references can't be used for secure values (like those stored
// in Parameter Store or Secrets Manager) in custom resources."
// https://docs.aws.amazon.com/AWSCloudFormation/latest/UserGuide/dynamic-references.html#dynamic-references-considerations

func TestFindSecureDynamicRef(t *testing.T) {
	for _, tc := range []struct {
		name    string
		props   map[string]any
		wantOK  bool
		wantSvc string
	}{
		{
			name:    "secretsmanager is secure",
			props:   map[string]any{"Password": "{{resolve:secretsmanager:db-creds:SecretString:password}}"},
			wantOK:  true,
			wantSvc: "secretsmanager",
		},
		{
			name:    "ssm-secure is secure",
			props:   map[string]any{"ApiKey": "{{resolve:ssm-secure:/app/api-key}}"},
			wantOK:  true,
			wantSvc: "ssm-secure",
		},
		{
			name:   "plain ssm is not secure",
			props:  map[string]any{"Tier": "{{resolve:ssm:/app/tier}}"},
			wantOK: false,
		},
		{
			name:   "no reference at all",
			props:  map[string]any{"Name": "plain-value"},
			wantOK: false,
		},
		{
			name: "secure reference nested inside a list of maps",
			props: map[string]any{
				"Tags": []any{
					map[string]any{"Key": "db", "Value": "{{resolve:ssm:/db/name}}"},
					map[string]any{"Key": "secret", "Value": "{{resolve:secretsmanager:db-creds}}"},
				},
			},
			wantOK:  true,
			wantSvc: "secretsmanager",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ref, ok := findSecureDynamicRef(tc.props)
			if ok != tc.wantOK {
				t.Fatalf("findSecureDynamicRef ok = %v, want %v", ok, tc.wantOK)
			}
			if !tc.wantOK {
				return
			}
			if ref.Service != tc.wantSvc {
				t.Errorf("service = %q, want %q", ref.Service, tc.wantSvc)
			}
		})
	}
}

func TestRejectSecureCustomResourceRefs(t *testing.T) {
	t.Run("secure reference fails, naming the restriction", func(t *testing.T) {
		err := rejectSecureCustomResourceRefs(map[string]any{
			"Password": "{{resolve:secretsmanager:db-creds:SecretString:password}}",
		})
		if err == nil {
			t.Fatal("expected an error")
		}
		if !strings.Contains(err.Error(), "secretsmanager") || !strings.Contains(err.Error(), "not supported") {
			t.Errorf("error = %v, want it to name the service and the restriction", err)
		}
	})
	t.Run("plain ssm is unaffected", func(t *testing.T) {
		if err := rejectSecureCustomResourceRefs(map[string]any{
			"Tier": "{{resolve:ssm:/app/tier}}",
		}); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})
	t.Run("nil properties", func(t *testing.T) {
		if err := rejectSecureCustomResourceRefs(nil); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})
}

// expandCustomResourceProperties rejects a secure reference without resolving
// it (the resolver is never even asked), and otherwise behaves exactly like
// expandRecordedProperties.
func TestExpandCustomResourceProperties(t *testing.T) {
	t.Run("secure reference rejected, resolver never asked", func(t *testing.T) {
		ctx := &resolveContext{
			DynamicRef: func(ref dynamicRef) (string, error) {
				t.Fatalf("resolver invoked for %s — a secure reference must be rejected, not resolved", ref.Raw)
				return "", nil
			},
		}
		_, err := expandCustomResourceProperties(map[string]any{
			"Password": "{{resolve:secretsmanager:db-creds:SecretString:password}}",
		}, ctx)
		if err == nil {
			t.Fatal("expected an error")
		}
	})
	t.Run("plain ssm still resolves", func(t *testing.T) {
		ctx := stubRefCtx(map[string]string{"{{resolve:ssm:/app/tier}}": "prod"})
		got, err := expandCustomResourceProperties(map[string]any{
			"Tier": "{{resolve:ssm:/app/tier}}",
		}, ctx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got["Tier"] != "prod" {
			t.Errorf("Tier = %v, want %q", got["Tier"], "prod")
		}
	})
}

// expandResourceProperties routes a custom resource type through the
// reject-secure/resolve-plain rule and leaves every other resource type on
// the ordinary expansion path, where a secure reference still resolves —
// e.g. an RDS MasterUsername from Secrets Manager, which is exactly the
// reference AWS's own examples use.
func TestExpandResourceProperties_routesByResourceType(t *testing.T) {
	secureRef := "{{resolve:secretsmanager:db-creds:SecretString:password}}"
	ctx := stubRefCtx(map[string]string{secureRef: "s3cr3t"})
	recorded := map[string]any{"Password": secureRef}

	t.Run("custom resource type rejects", func(t *testing.T) {
		for _, resType := range []string{"Custom::TestResource", "AWS::CloudFormation::CustomResource"} {
			if _, err := expandResourceProperties(resType, recorded, ctx); err == nil {
				t.Errorf("%s: expected the secure reference to be rejected", resType)
			}
		}
	})
	t.Run("ordinary resource type resolves", func(t *testing.T) {
		got, err := expandResourceProperties("AWS::RDS::DBInstance", recorded, ctx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got["Password"] != "s3cr3t" {
			t.Errorf("Password = %v, want the resolved secret", got["Password"])
		}
	})
}

// ── ssm-secure property allowlist (issue #605) ────────────────────────────
//
// AWS: "Secure strings can only be used for resource properties that support
// the ssm-secure dynamic reference pattern."
// https://docs.aws.amazon.com/AWSCloudFormation/latest/UserGuide/dynamic-references-ssm-secure-strings.html

func TestPropertyPathIs(t *testing.T) {
	for _, tc := range []struct {
		path []string
		want string
		ok   bool
	}{
		{[]string{"MasterUserPassword"}, "MasterUserPassword", true},
		{[]string{"LoginProfile", "Password"}, "LoginProfile/Password", true},
		{[]string{"RdsDbInstances", "DbPassword"}, "RdsDbInstances/DbPassword", true},
		// A prefix of a segment is not the segment.
		{[]string{"MasterUserPasswordX"}, "MasterUserPassword", false},
		{[]string{"MasterUser"}, "MasterUserPassword", false},
		// Depth has to match in both directions.
		{[]string{"LoginProfile"}, "LoginProfile/Password", false},
		{[]string{"LoginProfile", "Password", "Extra"}, "LoginProfile/Password", false},
		{[]string{"Password"}, "LoginProfile/Password", false},
		// A value that is not inside any property matches nothing.
		{nil, "Password", false},
	} {
		if got := propertyPathIs(tc.path, tc.want); got != tc.ok {
			t.Errorf("propertyPathIs(%q, %q) = %v, want %v", tc.path, tc.want, got, tc.ok)
		}
	}
}

func TestExpandDynamicRefs_ssmSecureIsRefusedOutsideTheAllowlist(t *testing.T) {
	// Given: a resolver that fails the test if it is asked for anything — a
	// refused reference must not decrypt the parameter it names.
	ctx := &resolveContext{
		DynamicRef: func(ref dynamicRef) (string, error) {
			t.Fatalf("resolver invoked for %s — a refused reference must not be read", ref.Raw)
			return "", nil
		},
	}
	const ref = "{{resolve:ssm-secure:/app/queue-name}}"

	// When: the reference sits in a property AWS does not allow it in.
	got := expandDynamicRefs("AWS::SQS::Queue", map[string]any{"QueueName": ref}, ctx).(map[string]any)

	// Then: the reference is left in place and the failure names it, the
	// property path, and AWS's own wording.
	if got["QueueName"] != ref {
		t.Errorf("QueueName = %v, want the reference left in place for the failed resource", got["QueueName"])
	}
	err := ctx.takeDynamicRefErr()
	if err == nil {
		t.Fatal("no error recorded for an ssm-secure reference outside the allowlist")
	}
	for _, want := range []string{ref, "SSM Secure reference is not supported in: [AWS::SQS::Queue/Properties/QueueName]"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %v, want it to contain %q", err, want)
		}
	}
}

// Every row of AWS's table, at the exact path it names.
func TestExpandDynamicRefs_ssmSecureResolvesInEveryAllowedProperty(t *testing.T) {
	const ref = "{{resolve:ssm-secure:/app/secret}}"
	for _, tc := range []struct {
		resType string
		props   map[string]any
		at      []string
	}{
		{"AWS::DirectoryService::MicrosoftAD", map[string]any{"Password": ref}, []string{"Password"}},
		{"AWS::DirectoryService::SimpleAD", map[string]any{"Password": ref}, []string{"Password"}},
		{"AWS::ElastiCache::ReplicationGroup", map[string]any{"AuthToken": ref}, []string{"AuthToken"}},
		{"AWS::IAM::User", map[string]any{"LoginProfile": map[string]any{"Password": ref}}, []string{"LoginProfile", "Password"}},
		{"AWS::KinesisFirehose::DeliveryStream",
			map[string]any{"RedshiftDestinationConfiguration": map[string]any{"Password": ref}},
			[]string{"RedshiftDestinationConfiguration", "Password"}},
		{"AWS::OpsWorks::App", map[string]any{"Source": map[string]any{"Password": ref}}, []string{"Source", "Password"}},
		{"AWS::OpsWorks::Stack",
			map[string]any{"CustomCookbooksSource": map[string]any{"Password": ref}},
			[]string{"CustomCookbooksSource", "Password"}},
		// A list property: every element is the same property, so the index is
		// not part of the path.
		{"AWS::OpsWorks::Stack",
			map[string]any{"RdsDbInstances": []any{
				map[string]any{"DbUser": "appuser"},
				map[string]any{"DbPassword": ref},
			}},
			[]string{"RdsDbInstances", "DbPassword"}},
		{"AWS::RDS::DBCluster", map[string]any{"MasterUserPassword": ref}, []string{"MasterUserPassword"}},
		{"AWS::RDS::DBInstance", map[string]any{"MasterUserPassword": ref}, []string{"MasterUserPassword"}},
		{"AWS::Redshift::Cluster", map[string]any{"MasterUserPassword": ref}, []string{"MasterUserPassword"}},
	} {
		t.Run(tc.resType+"/"+strings.Join(tc.at, "/"), func(t *testing.T) {
			if !ssmSecureAllowedAt(tc.resType, tc.at) {
				t.Fatalf("%s is not allowed at %q", tc.resType, tc.at)
			}
			ctx := stubRefCtx(map[string]string{ref: "s3cr3t"})
			expandDynamicRefs(tc.resType, tc.props, ctx)
			if err := ctx.takeDynamicRefErr(); err != nil {
				t.Errorf("unexpected failure for an allowed property: %v", err)
			}
		})
	}
}

// The same property name on the wrong resource type, and the right name at the
// wrong depth, are both outside the allowlist.
func TestExpandDynamicRefs_ssmSecureAllowlistIsKeyedByTypeAndPath(t *testing.T) {
	const ref = "{{resolve:ssm-secure:/app/secret}}"
	for _, tc := range []struct {
		name    string
		resType string
		props   map[string]any
	}{
		{"allowed property name, wrong resource type", "AWS::SQS::Queue",
			map[string]any{"MasterUserPassword": ref}},
		{"allowed nested name, but at the top level", "AWS::IAM::User",
			map[string]any{"Password": ref}},
		{"allowed nested name, but under the wrong property", "AWS::IAM::User",
			map[string]any{"Tags": []any{map[string]any{"Password": ref}}}},
		{"unregistered resource type", "AWS::Unknown::Thing",
			map[string]any{"Password": ref}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := stubRefCtx(map[string]string{ref: "s3cr3t"})
			expandDynamicRefs(tc.resType, tc.props, ctx)
			if err := ctx.takeDynamicRefErr(); err == nil {
				t.Error("no error recorded for an ssm-secure reference outside the allowlist")
			}
		})
	}
}

// The restriction is ssm-secure's alone. AWS: "The secretsmanager dynamic
// reference can be used in all resource properties", and a plain ssm parameter
// is plaintext.
func TestExpandDynamicRefs_otherSchemesStayUnrestricted(t *testing.T) {
	const (
		secretRef = "{{resolve:secretsmanager:queue-name-secret}}"
		ssmRef    = "{{resolve:ssm:/app/queue-name}}"
	)
	ctx := stubRefCtx(map[string]string{secretRef: "from-secret", ssmRef: "from-parameter"})

	// The same property an ssm-secure reference is refused in.
	got := expandDynamicRefs("AWS::SQS::Queue", map[string]any{
		"QueueName": secretRef,
		"Tags":      []any{map[string]any{"Key": "name", "Value": ssmRef}},
	}, ctx).(map[string]any)

	if err := ctx.takeDynamicRefErr(); err != nil {
		t.Fatalf("unexpected failure: %v", err)
	}
	if got["QueueName"] != "from-secret" {
		t.Errorf("QueueName = %v, want the resolved secret", got["QueueName"])
	}
	if tag := got["Tags"].([]any)[0].(map[string]any); tag["Value"] != "from-parameter" {
		t.Errorf("Tags[0].Value = %v, want the resolved parameter", tag["Value"])
	}
}
