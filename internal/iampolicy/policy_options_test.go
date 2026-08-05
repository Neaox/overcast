package iampolicy

import "testing"

func TestParseDocumentWithOptions_allowsKMSIneffectiveStatement(t *testing.T) {
	// Given: a resource-policy statement with no Action
	raw := `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":{"AWS":"arn:aws:iam::000000000000:root"},"Resource":"*"}]}`
	src := SourceRef{ID: "key", Type: SourceTypeResourcePolicy}

	// Then: the ordinary IAM grammar still rejects it
	if _, err := ParseDocument(raw, src); err == nil {
		t.Fatal("ParseDocument accepted a statement with no Action")
	}

	// But: KMS can preserve AWS's accepted-but-ineffective statement behavior
	statements, err := ParseDocumentWithOptions(raw, src, ParseOptions{
		AllowMissingAction:  true,
		RequireVersion:      true,
		RequireStatements:   true,
		RequirePrincipal:    true,
		RejectEmptyElements: true,
	})
	if err != nil {
		t.Fatalf("ParseDocumentWithOptions returned error: %v", err)
	}
	if len(statements) != 1 || len(statements[0].Action) != 0 {
		t.Fatalf("statements = %#v, want one ineffective statement", statements)
	}
}

func TestParseDocumentWithOptions_requiresKMSPolicyStructure(t *testing.T) {
	opts := ParseOptions{
		AllowMissingAction:  true,
		RequireVersion:      true,
		RequireStatements:   true,
		RequirePrincipal:    true,
		RejectEmptyElements: true,
	}
	for _, tc := range []struct {
		name string
		raw  string
	}{
		{name: "version", raw: `{"Statement":[{"Effect":"Allow","Principal":"*","Action":"kms:*"}]}`},
		{name: "statements", raw: `{"Version":"2012-10-17","Statement":[]}`},
		{name: "principal", raw: `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"kms:*"}]}`},
		{name: "action values", raw: `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":"*","Action":[]}]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ParseDocumentWithOptions(tc.raw, SourceRef{ID: "key"}, opts); err == nil {
				t.Fatalf("accepted policy missing required %s", tc.name)
			}
		})
	}
}
