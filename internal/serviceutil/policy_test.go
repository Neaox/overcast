package serviceutil_test

import (
	"testing"

	"github.com/overcast-sh/overcast/internal/serviceutil"
)

func TestStatementGrantsPublicAccess(t *testing.T) {
	tests := []struct {
		name      string
		effect    string
		principal any
		condition any
		want      bool
	}{
		{
			name:      "bare wildcard principal",
			effect:    "Allow",
			principal: "*",
			want:      true,
		},
		{
			name:      "AWS wildcard principal",
			effect:    "Allow",
			principal: map[string]any{"AWS": "*"},
			want:      true,
		},
		{
			name:      "list containing a bare wildcard",
			effect:    "Allow",
			principal: []any{"arn:aws:iam::000000000000:root", "*"},
			want:      true,
		},
		{
			name:      "list containing an AWS wildcard",
			effect:    "Allow",
			principal: []any{map[string]any{"AWS": "arn:aws:iam::000000000000:root"}, map[string]any{"AWS": "*"}},
			want:      true,
		},
		{
			name:      "Service principal is not public",
			effect:    "Allow",
			principal: map[string]any{"Service": "s3.amazonaws.com"},
			want:      false,
		},
		{
			name:      "named principal is not public",
			effect:    "Allow",
			principal: map[string]any{"AWS": "arn:aws:iam::000000000000:root"},
			want:      false,
		},
		{
			name:      "Deny is never public, even with a wildcard principal",
			effect:    "Deny",
			principal: "*",
			want:      false,
		},
		{
			name:      "omitted Condition narrows nothing",
			effect:    "Allow",
			principal: "*",
			condition: nil,
			want:      true,
		},
		{
			name:      "empty Condition narrows nothing",
			effect:    "Allow",
			principal: "*",
			condition: map[string]any{},
			want:      true,
		},
		{
			name:      "a populated Condition narrows the grant",
			effect:    "Allow",
			principal: "*",
			condition: map[string]any{"StringEquals": map[string]any{"aws:PrincipalOrgID": "o-abc"}},
			want:      false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := serviceutil.StatementGrantsPublicAccess(tc.effect, tc.principal, tc.condition); got != tc.want {
				t.Fatalf("StatementGrantsPublicAccess(%q, %#v, %#v) = %v, want %v",
					tc.effect, tc.principal, tc.condition, got, tc.want)
			}
		})
	}
}
