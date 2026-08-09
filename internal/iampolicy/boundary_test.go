package iampolicy

import "testing"

// boundaryFrom compiles a boundary document, failing the test if it does not
// parse — the tests that want an unparseable one say so explicitly.
func boundaryFrom(t *testing.T, raw string) *Boundary {
	t.Helper()
	boundary, err := NewBoundary(raw, SourceRef{ID: "boundary", Type: SourceTypeIAMPolicy})
	if err != nil {
		t.Fatalf("NewBoundary: unexpected error: %v", err)
	}
	return boundary
}

func getObject() Request {
	return Request{Action: "s3:GetObject", Resource: "arn:aws:s3:::b/k"}
}

func TestEvaluate_boundaryAllowsIdentityAllow_isAllowed(t *testing.T) {
	// Given: an identity allow and a boundary covering the same action
	identity := identityFrom(t, `{"Statement":[{"Effect":"Allow","Action":"s3:*","Resource":"*"}]}`)
	boundary := boundaryFrom(t, `{"Statement":[{"Effect":"Allow","Action":"s3:GetObject","Resource":"*"}]}`)

	// When: the request is evaluated
	res := Evaluate(Input{Request: getObject(), Identity: identity, Boundary: boundary})

	// Then: the intersection allows it, and the boundary verdict is reported
	if res.Decision != DecisionAllowed {
		t.Fatalf("Decision = %q, want %q", res.Decision, DecisionAllowed)
	}
	if !res.BoundaryApplied || !res.AllowedByBoundary {
		t.Fatalf("BoundaryApplied/AllowedByBoundary = %v/%v, want true/true",
			res.BoundaryApplied, res.AllowedByBoundary)
	}
}

func TestEvaluate_boundaryDoesNotCoverAction_isImplicitDeny(t *testing.T) {
	// Given: an identity allow and a boundary that covers a different service
	identity := identityFrom(t, `{"Statement":[{"Effect":"Allow","Action":"s3:*","Resource":"*"}]}`)
	boundary := boundaryFrom(t, `{"Statement":[{"Effect":"Allow","Action":"sqs:*","Resource":"*"}]}`)

	// When: the request is evaluated
	res := Evaluate(Input{Request: getObject(), Identity: identity, Boundary: boundary})

	// Then: the boundary caps the allow. AWS reports this as an implicit deny
	// because the boundary grants nothing of its own, so no statement decided it
	if res.Decision != DecisionImplicitDeny {
		t.Fatalf("Decision = %q, want %q", res.Decision, DecisionImplicitDeny)
	}
	if res.AllowedByBoundary {
		t.Fatal("AllowedByBoundary = true, want false")
	}
}

func TestEvaluate_boundaryExplicitDeny_isExplicitDeny(t *testing.T) {
	// Given: an identity allow and a boundary denying the action outright
	identity := identityFrom(t, `{"Statement":[{"Effect":"Allow","Action":"s3:*","Resource":"*"}]}`)
	boundary := boundaryFrom(t, `{"Statement":[{"Effect":"Deny","Action":"s3:GetObject","Resource":"*"}]}`)

	// When: the request is evaluated
	res := Evaluate(Input{Request: getObject(), Identity: identity, Boundary: boundary})

	// Then: an explicit deny in either half is final
	if res.Decision != DecisionExplicitDeny {
		t.Fatalf("Decision = %q, want %q", res.Decision, DecisionExplicitDeny)
	}
}

func TestEvaluate_boundaryAllowsButIdentityDoesNot_isImplicitDeny(t *testing.T) {
	// Given: a boundary allowing everything and no identity policy
	boundary := boundaryFrom(t, `{"Statement":[{"Effect":"Allow","Action":"*","Resource":"*"}]}`)

	// When: the request is evaluated
	res := Evaluate(Input{Request: getObject(), Boundary: boundary})

	// Then: a boundary grants nothing on its own
	if res.Decision != DecisionImplicitDeny {
		t.Fatalf("Decision = %q, want %q", res.Decision, DecisionImplicitDeny)
	}
	if !res.AllowedByBoundary {
		t.Fatal("AllowedByBoundary = false, want true — the boundary itself does allow the action")
	}
}

func TestEvaluate_emptyBoundary_capsEverything(t *testing.T) {
	// Given: an identity allow and a boundary carrying no statements — which is
	// what an unresolvable boundary compiles to
	identity := identityFrom(t, `{"Statement":[{"Effect":"Allow","Action":"s3:*","Resource":"*"}]}`)

	// When: the request is evaluated
	res := Evaluate(Input{Request: getObject(), Identity: identity, Boundary: &Boundary{}})

	// Then: an empty boundary is not the same as no boundary
	if res.Decision != DecisionImplicitDeny {
		t.Fatalf("Decision = %q, want %q", res.Decision, DecisionImplicitDeny)
	}
	if !res.BoundaryApplied {
		t.Fatal("BoundaryApplied = false, want true")
	}
}

func TestEvaluate_noBoundary_reportsNoBoundaryDecision(t *testing.T) {
	// Given: an identity allow and no boundary
	identity := identityFrom(t, `{"Statement":[{"Effect":"Allow","Action":"s3:*","Resource":"*"}]}`)

	// When: the request is evaluated
	res := Evaluate(Input{Request: getObject(), Identity: identity})

	// Then: nothing is reported about a boundary that does not exist
	if res.Decision != DecisionAllowed || res.BoundaryApplied {
		t.Fatalf("Decision/BoundaryApplied = %q/%v, want allowed/false", res.Decision, res.BoundaryApplied)
	}
}

func TestEvaluate_boundaryUnsupportedConstruct_isReported(t *testing.T) {
	// Given: an identity allow and a boundary using a condition operator the
	// evaluator does not implement
	identity := identityFrom(t, `{"Statement":[{"Effect":"Allow","Action":"s3:*","Resource":"*"}]}`)
	boundary := boundaryFrom(t, `{"Statement":[{"Effect":"Allow","Action":"s3:*","Resource":"*",
		"Condition":{"StringPalindrome":{"aws:username":"bob"}}}]}`)

	// When: the request is evaluated, carrying the key the condition names
	req := getObject()
	req.Context = map[string]string{"aws:username": "bob"}
	res := Evaluate(Input{Request: req, Identity: identity, Boundary: boundary})

	// Then: the gap surfaces rather than being resolved to an allow or a deny
	if len(res.Unsupported) == 0 {
		t.Fatal("Unsupported = none, want the boundary's unimplemented operator")
	}
}

func TestNewBoundary_malformedDocument_allowsNothing(t *testing.T) {
	// Given: a boundary document that is not valid JSON
	// When: it is compiled
	boundary, err := NewBoundary("{not json", SourceRef{ID: "boundary", Type: SourceTypeIAMPolicy})

	// Then: the error is reported for logging, and the boundary that comes back
	// allows nothing rather than being mistaken for no boundary at all
	if err == nil {
		t.Fatal("NewBoundary: expected an error for a malformed document")
	}
	if boundary == nil || len(boundary.Statements) != 0 {
		t.Fatalf("Boundary = %#v, want an empty boundary", boundary)
	}
	identity := identityFrom(t, `{"Statement":[{"Effect":"Allow","Action":"s3:*","Resource":"*"}]}`)
	res := Evaluate(Input{Request: getObject(), Identity: identity, Boundary: boundary})
	if res.Decision != DecisionImplicitDeny {
		t.Fatalf("Decision = %q, want %q", res.Decision, DecisionImplicitDeny)
	}
}
