package cloudformation

import (
	"encoding/json"
	"testing"
)

// template_intrinsics_test.go — intrinsic functions whose argument is itself an
// intrinsic, and the intrinsics that were never implemented.
//
// The list-valued case is what broke CDK: a nested stack's TemplateURL is built
// as Fn::Select[n, Fn::Split["||", <asset key>]], and Fn::Select bailed out
// because its second argument was a map rather than a list. The key resolved
// empty, the URL collapsed to "/<bucket>/", S3 answered a ListObjects XML with
// 200, and the nested stack failed with "cannot unmarshal string into
// cloudformation.Template" — three layers away from the actual cause.

// resolveJSON resolves the intrinsics in a JSON fragment, so tests read as the
// template text they represent.
func resolveJSON(t *testing.T, fragment string, ctx *resolveContext) any {
	t.Helper()
	var v any
	if err := json.Unmarshal([]byte(fragment), &v); err != nil {
		t.Fatalf("bad fragment %s: %v", fragment, err)
	}
	return resolveIntrinsics(v, ctx)
}

func testCtx() *resolveContext {
	return &resolveContext{
		Region:    "us-east-1",
		AccountID: "000000000000",
		StackName: "s",
		Params:    map[string]string{"V": "assets/abc.json||"},
		Mappings: map[string]any{
			"RegionMap": map[string]any{
				"us-east-1": map[string]any{"ami": "ami-east"},
			},
		},
	}
}

// A list argument that is itself an intrinsic must be resolved before it is
// indexed or joined.
func TestResolveIntrinsics_listArgumentMayBeAnIntrinsic(t *testing.T) {
	for _, tc := range []struct {
		name     string
		fragment string
		want     any
	}{
		{
			name:     "Select over Split — the CDK nested-stack asset key",
			fragment: `{"Fn::Select": [0, {"Fn::Split": ["||", {"Ref": "V"}]}]}`,
			want:     "assets/abc.json",
		},
		{
			name:     "Select over Split, second element",
			fragment: `{"Fn::Select": [1, {"Fn::Split": ["||", "a||b||c"]}]}`,
			want:     "b",
		},
		{
			name:     "Join over Split",
			fragment: `{"Fn::Join": [",", {"Fn::Split": ["||", "a||b||c"]}]}`,
			want:     "a,b,c",
		},
		{
			name:     "Select over GetAZs — the subnet availability-zone idiom",
			fragment: `{"Fn::Select": [0, {"Fn::GetAZs": ""}]}`,
			want:     "us-east-1a",
		},
		{
			name: "the whole CDK TemplateURL shape",
			fragment: `{"Fn::Join": ["", ["https://s3.us-east-1.amazonaws.com/", "bkt", "/",
				{"Fn::Select": [0, {"Fn::Split": ["||", {"Ref": "V"}]}]},
				{"Fn::Select": [1, {"Fn::Split": ["||", {"Ref": "V"}]}]}]]}`,
			want: "https://s3.us-east-1.amazonaws.com/bkt/assets/abc.json",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveJSON(t, tc.fragment, testCtx()); got != tc.want {
				t.Errorf("= %#v, want %#v", got, tc.want)
			}
		})
	}
}

// Fn::FindInMap was never implemented, and ctx.Mappings was populated but never
// read. Region-keyed maps are one of the oldest CloudFormation idioms.
func TestResolveIntrinsics_findInMap(t *testing.T) {
	if got := resolveJSON(t, `{"Fn::FindInMap": ["RegionMap", "us-east-1", "ami"]}`, testCtx()); got != "ami-east" {
		t.Errorf("Fn::FindInMap = %#v, want \"ami-east\"", got)
	}
	// Keys may themselves be intrinsics — the usual form is a Ref to AWS::Region.
	if got := resolveJSON(t, `{"Fn::FindInMap": ["RegionMap", {"Ref": "AWS::Region"}, "ami"]}`, testCtx()); got != "ami-east" {
		t.Errorf("Fn::FindInMap with a Ref key = %#v, want \"ami-east\"", got)
	}
}

// A missing entry must not resolve to the raw map, which would be silently
// embedded in a property as Go's map formatting.
func TestResolveIntrinsics_findInMapMissingEntry(t *testing.T) {
	for _, fragment := range []string{
		`{"Fn::FindInMap": ["NoSuchMap", "us-east-1", "ami"]}`,
		`{"Fn::FindInMap": ["RegionMap", "eu-west-1", "ami"]}`,
		`{"Fn::FindInMap": ["RegionMap", "us-east-1", "nope"]}`,
	} {
		if got := resolveJSON(t, fragment, testCtx()); got != "" {
			t.Errorf("%s = %#v, want an empty string", fragment, got)
		}
	}
}

func TestResolveIntrinsics_base64(t *testing.T) {
	if got := resolveJSON(t, `{"Fn::Base64": "hello"}`, testCtx()); got != "aGVsbG8=" {
		t.Errorf("Fn::Base64 = %#v, want \"aGVsbG8=\"", got)
	}
	// The value is commonly a Sub — EC2 UserData almost always is.
	if got := resolveJSON(t, `{"Fn::Base64": {"Fn::Sub": "r=${AWS::Region}"}}`, testCtx()); got != "cj11cy1lYXN0LTE=" {
		t.Errorf("Fn::Base64 over Fn::Sub = %#v, want \"cj11cy1lYXN0LTE=\"", got)
	}
}

// Fn::Cidr splits a block into equally sized subnets, as AWS documents.
func TestResolveIntrinsics_cidr(t *testing.T) {
	got, ok := resolveJSON(t, `{"Fn::Cidr": ["10.0.0.0/16", 2, 8]}`, testCtx()).([]any)
	if !ok {
		t.Fatalf("Fn::Cidr did not return a list: %#v", got)
	}
	want := []any{"10.0.0.0/24", "10.0.1.0/24"}
	if len(got) != len(want) {
		t.Fatalf("Fn::Cidr = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Fn::Cidr[%d] = %#v, want %#v", i, got[i], want[i])
		}
	}
	// The usual shape is a Select over it.
	if v := resolveJSON(t, `{"Fn::Select": [1, {"Fn::Cidr": ["10.0.0.0/16", 2, 8]}]}`, testCtx()); v != "10.0.1.0/24" {
		t.Errorf("Select over Cidr = %#v, want \"10.0.1.0/24\"", v)
	}
}
