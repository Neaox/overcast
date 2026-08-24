package cloudformation

import (
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func TestCheckInlineTemplateSize_boundary(t *testing.T) {
	if err := checkInlineTemplateSize(strings.Repeat("x", maxInlineTemplateBytes)); err != nil {
		t.Errorf("exactly %d bytes rejected: %v", maxInlineTemplateBytes, err)
	}
	err := checkInlineTemplateSize(strings.Repeat("x", maxInlineTemplateBytes+1))
	if err == nil {
		t.Fatalf("%d bytes accepted, want rejection", maxInlineTemplateBytes+1)
	}
	if !strings.Contains(err.Error(), strconv.Itoa(maxInlineTemplateBytes)) {
		t.Errorf("message = %q, want it to name the limit", err)
	}
	if !strings.Contains(err.Error(), "templateBody") {
		t.Errorf("message = %q, want it to name the member", err)
	}
	if strings.Contains(err.Error(), "xxxxxxxxxx") {
		t.Errorf("message echoes the oversized value back to the client: %q", err)
	}
}

func TestCheckResolvedTemplateSize_boundary(t *testing.T) {
	if err := checkResolvedTemplateSize(strings.Repeat("x", maxResolvedTemplateBytes)); err != nil {
		t.Errorf("exactly %d bytes rejected: %v", maxResolvedTemplateBytes, err)
	}
	err := checkResolvedTemplateSize(strings.Repeat("x", maxResolvedTemplateBytes+1))
	if err == nil {
		t.Fatalf("%d bytes accepted, want rejection", maxResolvedTemplateBytes+1)
	}
	// AWS's wording, verbatim — the number in it is why the limit is decimal.
	if err.Error() != "Template may not exceed 1000000 bytes in size." {
		t.Errorf("message = %q, want AWS's wording", err)
	}
}

// TestResolvedLimitIsADecimalMegabyte guards the unit, which the quotas table
// states only as "1 MB". Reading it as 1 MiB would accept templates AWS
// refuses — the divergence direction that fails in the account rather than
// locally.
func TestResolvedLimitIsADecimalMegabyte(t *testing.T) {
	if maxResolvedTemplateBytes != 1_000_000 {
		t.Fatalf("maxResolvedTemplateBytes = %d, want 1000000", maxResolvedTemplateBytes)
	}
	if err := checkResolvedTemplateSize(strings.Repeat("x", 1<<20)); err == nil {
		t.Error("1 MiB accepted: the limit has been widened to a mebibyte")
	}
}

func TestCheckInlineTemplateSize_emptyIsNotAnError(t *testing.T) {
	// An absent TemplateBody means "use TemplateURL", or on UpdateStack "reuse
	// the stack's own". The size check must stay out of that decision.
	if err := checkInlineTemplateSize(""); err != nil {
		t.Errorf("empty TemplateBody rejected: %v", err)
	}
}

// TestEveryTypedTemplateOperationChecksInlineSize is a coverage guard rather
// than a behaviour test. The 51,200-byte cap is a property of the TemplateBody
// parameter, so it applies to every operation that accepts one — and two of
// them read the field directly instead of going through
// resolveTypedTemplateBody, which is exactly how such a check gets missed.
//
// It walks the typed operation registry for request types carrying a
// TemplateBody field and fails if a new one appears that this package's tests
// do not exercise, so the omission surfaces here rather than in a bug report.
func TestEveryTypedTemplateOperationChecksInlineSize(t *testing.T) {
	// Operations covered by the integration tests in
	// tests/integration/cloudformation/template_size_test.go.
	covered := map[string]bool{
		"CreateStack":        true,
		"UpdateStack":        true,
		"CreateChangeSet":    true,
		"ValidateTemplate":   true,
		"GetTemplateSummary": true,
	}

	h := &Handler{}
	seen := 0
	for name, operation := range h.typedOps() {
		in, ok := typedRequestType(operation)
		if !ok {
			continue
		}
		if _, hasTemplate := in.FieldByName("TemplateBody"); !hasTemplate {
			continue
		}
		seen++
		if !covered[name] {
			t.Errorf("%s accepts an inline TemplateBody but is not covered by an inline-size test; "+
				"add it to tests/integration/cloudformation/template_size_test.go and to this list", name)
		}
	}
	if seen != len(covered) {
		t.Errorf("found %d typed operations taking a TemplateBody, want %d — "+
			"an entry in the covered list no longer names a real operation", seen, len(covered))
	}
}

// typedRequestType recovers an operation's decoded request struct.
//
// op.NewTyped returns the Operation interface, which erases its type
// parameters, and Go reflection cannot read them back off the concrete type.
// The typed function it stores can: its first non-context parameter is a
// pointer to the request struct. The field is unexported, but a Type needs no
// access to be read.
func typedRequestType(operation any) (reflect.Type, bool) {
	v := reflect.TypeOf(operation)
	for v != nil && v.Kind() == reflect.Pointer {
		v = v.Elem()
	}
	if v == nil || v.Kind() != reflect.Struct {
		return nil, false
	}
	fn, ok := v.FieldByName("fn")
	if !ok || fn.Type.Kind() != reflect.Func || fn.Type.NumIn() != 2 {
		return nil, false
	}
	in := fn.Type.In(1)
	if in.Kind() != reflect.Pointer || in.Elem().Kind() != reflect.Struct {
		return nil, false
	}
	return in.Elem(), true
}
