package iampolicy

import (
	"strings"
	"testing"
)

func TestValidateDocument_rejectsWhatAWSRejects(t *testing.T) {
	for _, tc := range []struct {
		name, raw, wantIn string
	}{
		{"unparseable", `{"Statement":[`, "not valid JSON"},
		{"trailing comma", `{"Statement":[{"Effect":"Allow","Action":"s3:*",}]}`, "not valid JSON"},
		{"json array", `[{"Effect":"Allow","Action":"s3:*"}]`, "must be a JSON object"},
		{"json string", `"policy"`, "must be a JSON object"},
		{"json null", `null`, "must be a JSON object"},
		{"empty object", `{}`, "no Statement element"},
		{"statement empty list", `{"Statement":[]}`, "empty Statement list"},
		{"statement wrong type", `{"Statement":"s3:GetObject"}`, "neither a statement nor a list"},
		{"effect missing", `{"Statement":[{"Action":"s3:*"}]}`, "Effect must be exactly"},
		{"effect misspelled", `{"Statement":[{"Effect":"Permit","Action":"s3:*"}]}`, "Effect must be exactly"},
		{"effect lowercase", `{"Statement":[{"Effect":"allow","Action":"s3:*"}]}`, "Effect must be exactly"},
		{"action missing", `{"Statement":[{"Effect":"Allow","Resource":"*"}]}`, "neither Action nor NotAction"},
		{"action and notaction", `{"Statement":[{"Effect":"Allow","Action":"s3:*","NotAction":"s3:Put*"}]}`, "both Action and NotAction"},
		{"resource and notresource", `{"Statement":[{"Effect":"Allow","Action":"s3:*","Resource":"*","NotResource":"*"}]}`, "both Resource and NotResource"},
		{"unsupported version", `{"Version":"2026-01-01","Statement":[{"Effect":"Allow","Action":"s3:*"}]}`, "unsupported Version"},
		{"second statement bad", `{"Statement":[{"Effect":"Allow","Action":"s3:*"},{"Effect":"Allow"}]}`, "statement 2"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateDocument(tc.raw)
			if err == nil {
				t.Fatalf("ValidateDocument accepted %s", tc.raw)
			}
			if !strings.Contains(err.Error(), tc.wantIn) {
				t.Errorf("error = %q, want it to mention %q", err, tc.wantIn)
			}
			// The boundary check has no stored policy to name, so its message
			// must not carry the evaluator's "(unnamed)" placeholder.
			if strings.Contains(err.Error(), "(unnamed)") {
				t.Errorf("error names no source but says %q", err)
			}
		})
	}
}

func TestValidateDocument_acceptsWhatAWSAccepts(t *testing.T) {
	for _, tc := range []struct{ name, raw string }{
		{"minimal", `{"Statement":[{"Effect":"Allow","Action":"s3:*"}]}`},
		{"single object statement", `{"Version":"2012-10-17","Statement":{"Effect":"Allow","Action":"s3:*","Resource":"*"}}`},
		{"legacy version", `{"Version":"2008-10-17","Statement":[{"Effect":"Deny","Action":"*","Resource":"*"}]}`},
		{"action list", `{"Statement":[{"Effect":"Allow","Action":["s3:Get*","s3:List*"],"Resource":["*"]}]}`},
		{"not action", `{"Statement":[{"Effect":"Deny","NotAction":"s3:Get*","Resource":"*"}]}`},
		{"not resource", `{"Statement":[{"Effect":"Allow","Action":"s3:*","NotResource":"arn:aws:s3:::x/*"}]}`},
		{"sid and condition", `{"Statement":[{"Sid":"S1","Effect":"Allow","Action":"s3:*","Resource":"*","Condition":{"Bool":{"aws:SecureTransport":"true"}}}]}`},
		{"trust policy", `{"Statement":[{"Effect":"Allow","Principal":{"Service":"ec2.amazonaws.com"},"Action":"sts:AssumeRole"}]}`},
		{"wildcard principal", `{"Statement":[{"Effect":"Allow","Principal":"*","Action":"sts:AssumeRole"}]}`},
		{"federated principal", `{"Statement":[{"Effect":"Allow","Principal":{"Federated":"example.com"},"Action":"sts:AssumeRoleWithWebIdentity"}]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := ValidateDocument(tc.raw); err != nil {
				t.Fatalf("ValidateDocument rejected a legal document: %v", err)
			}
		})
	}
}

// TestValidateDocument_doesNotTightenEvaluation pins the one place the
// boundary check and the evaluator deliberately differ: a lowercase Effect is
// refused on the way in, but a document already in the store — written before
// the check existed — still evaluates rather than silently granting nothing.
func TestValidateDocument_doesNotTightenEvaluation(t *testing.T) {
	raw := `{"Statement":[{"Effect":"allow","Action":"s3:*","Resource":"*"}]}`
	if err := ValidateDocument(raw); err == nil {
		t.Fatal("ValidateDocument accepted a lowercase Effect")
	}
	stmts, err := ParseDocument(raw, SourceRef{ID: "stored"})
	if err != nil {
		t.Fatalf("ParseDocument rejected an already-stored document: %v", err)
	}
	if len(stmts) != 1 || stmts[0].Effect != "Allow" {
		t.Fatalf("statements = %#v, want one canonicalised Allow", stmts)
	}
}
