package middleware

import "testing"

func TestRegionFromHost(t *testing.T) {
	cases := []struct {
		host, want string
	}{
		{"d038ecd84a.execute-api.ap-southeast-2.amazonaws.com", "ap-southeast-2"},
		{"d038ecd84a.execute-api.us-east-1.localhost.localstack.cloud:4566", "us-east-1"},
		{"d038ecd84a.execute-api.eu-west-1.localhost:4566", "eu-west-1"},
		{"localhost:4566", ""},
		{"", ""},
		{"foo.bar", ""},
		{"d038ecd84a.unknown-svc.ap-southeast-2.amazonaws.com", ""},
	}
	for _, tc := range cases {
		if got := regionFromHost(tc.host); got != tc.want {
			t.Errorf("regionFromHost(%q) = %q; want %q", tc.host, got, tc.want)
		}
	}
}
