package lambda

import "testing"

func stringPointer(value string) *string { return &value }

func TestNormalizeLoggingConfig_presenceAndDefaults(t *testing.T) {
	tests := []struct {
		name string
		req  *loggingConfigRequest
		want loggingConfig
	}{
		{
			name: "json defaults both levels to info",
			req:  &loggingConfigRequest{LogFormat: stringPointer("JSON")},
			want: loggingConfig{LogGroup: "/aws/lambda/fn", LogFormat: "JSON", ApplicationLogLevel: "INFO", SystemLogLevel: "INFO"},
		},
		{
			name: "explicit text custom group",
			req:  &loggingConfigRequest{LogGroup: stringPointer("/custom/group"), LogFormat: stringPointer("Text")},
			want: loggingConfig{LogGroup: "/custom/group", LogFormat: "Text"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, aerr := normalizeLoggingConfig(tc.req, "fn")
			if aerr != nil {
				t.Fatalf("normalize: %v", aerr)
			}
			if *got != tc.want {
				t.Fatalf("config = %#v, want %#v", *got, tc.want)
			}
		})
	}
}
