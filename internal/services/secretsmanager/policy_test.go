package secretsmanager

import "testing"

func TestParsePolicyDocument(t *testing.T) {
	const wellFormed = `{"Version":"2012-10-17","Statement":[{"Effect":"Allow",` +
		`"Principal":{"AWS":"arn:aws:iam::000000000000:root"},` +
		`"Action":"secretsmanager:GetSecretValue","Resource":"*"}]}`

	cases := []struct {
		name        string
		policy      string
		wantFinding bool
	}{
		{name: "well-formed array of statements", policy: wellFormed},
		{
			// AWS accepts a bare statement object as well as an array. Failing
			// it would reject a policy real AWS takes.
			name: "single statement object",
			policy: `{"Version":"2012-10-17","Statement":{"Effect":"Allow",` +
				`"Principal":"*","Action":"secretsmanager:GetSecretValue","Resource":"*"}}`,
		},
		{name: "not JSON", policy: "{not json", wantFinding: true},
		{name: "no Version", policy: `{"Statement":[]}`, wantFinding: true},
		{name: "no Statement", policy: `{"Version":"2012-10-17"}`, wantFinding: true},
		{
			name:        "statement with no Effect",
			policy:      `{"Version":"2012-10-17","Statement":[{"Principal":"*","Action":"a"}]}`,
			wantFinding: true,
		},
		{
			name:        "Effect that is neither Allow nor Deny",
			policy:      `{"Version":"2012-10-17","Statement":[{"Effect":"Maybe","Principal":"*","Action":"a"}]}`,
			wantFinding: true,
		},
		{
			name:        "statement with no Principal",
			policy:      `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"a"}]}`,
			wantFinding: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// When: the document is parsed
			_, findings := parsePolicyDocument(tc.policy)

			// Then: findings appear only where the document is unusable
			if tc.wantFinding && len(findings) == 0 {
				t.Fatalf("no findings, want at least one")
			}
			if !tc.wantFinding && len(findings) > 0 {
				t.Fatalf("findings = %+v, want none", findings)
			}
			for _, f := range findings {
				if f.CheckName == "" || f.ErrorMessage == "" {
					t.Errorf("finding %+v has an empty CheckName or ErrorMessage", f)
				}
			}
		})
	}
}

func TestPolicyDocument_grantsPublicAccess(t *testing.T) {
	cases := []struct {
		name   string
		policy string
		public bool
	}{
		{
			name:   "bare wildcard principal",
			policy: `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":"*","Action":"a","Resource":"*"}]}`,
			public: true,
		},
		{
			name:   "AWS wildcard principal",
			policy: `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":{"AWS":"*"},"Action":"a","Resource":"*"}]}`,
			public: true,
		},
		{
			// A Condition narrows the grant, so BlockPublicPolicy allows it.
			name: "wildcard principal narrowed by a Condition",
			policy: `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":"*","Action":"a","Resource":"*",` +
				`"Condition":{"StringEquals":{"aws:PrincipalOrgID":"o-abc"}}}]}`,
		},
		{
			name:   "named principal",
			policy: `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":{"AWS":"arn:aws:iam::000000000000:root"},"Action":"a","Resource":"*"}]}`,
		},
		{
			// A wildcard Deny is not a public grant.
			name:   "wildcard principal on a Deny",
			policy: `{"Version":"2012-10-17","Statement":[{"Effect":"Deny","Principal":"*","Action":"a","Resource":"*"}]}`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			doc, findings := parsePolicyDocument(tc.policy)
			if len(findings) > 0 {
				t.Fatalf("fixture policy is not valid: %+v", findings)
			}
			if got := doc.grantsPublicAccess(); got != tc.public {
				t.Fatalf("grantsPublicAccess = %v, want %v", got, tc.public)
			}
		})
	}
}
