package cognito

import "testing"

func TestExpandTemplate(t *testing.T) {
	tests := []struct {
		name     string
		tmpl     string
		username string
		code     string
		want     string
	}{
		{
			name:     "both placeholders",
			tmpl:     "Hi {username}, your code is {####}",
			username: "alice",
			code:     "123456",
			want:     "Hi alice, your code is 123456",
		},
		{
			name:     "code only",
			tmpl:     "Your verification code is {####}.",
			username: "bob",
			code:     "999888",
			want:     "Your verification code is 999888.",
		},
		{
			name:     "username only",
			tmpl:     "Welcome {username}!",
			username: "charlie",
			code:     "000",
			want:     "Welcome charlie!",
		},
		{
			name:     "no placeholders",
			tmpl:     "Static message",
			username: "dave",
			code:     "111",
			want:     "Static message",
		},
		{
			name:     "multiple occurrences",
			tmpl:     "{username} ({username}): {####}",
			username: "eve",
			code:     "42",
			want:     "eve (eve): 42",
		},
		{
			name:     "empty template",
			tmpl:     "",
			username: "frank",
			code:     "555",
			want:     "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := expandTemplate(tt.tmpl, tt.username, tt.code)
			if got != tt.want {
				t.Errorf("expandTemplate(%q, %q, %q) = %q, want %q",
					tt.tmpl, tt.username, tt.code, got, tt.want)
			}
		})
	}
}
