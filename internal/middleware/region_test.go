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

		// A hostname is case-insensitive, so neither the service label nor the
		// region segment may be matched or returned case-sensitively. This one
		// bites harder than a routing miss: the region returned here becomes
		// the store's key prefix (serviceutil.RegionKey), so an upper-case
		// region silently partitions state — resources created under
		// "US-East-1" are invisible to every request that says "us-east-1".
		{"d038ecd84a.EXECUTE-API.ap-southeast-2.amazonaws.com", "ap-southeast-2"},
		{"d038ecd84a.execute-api.AP-SOUTHEAST-2.amazonaws.com", "ap-southeast-2"},
		{"D038ECD84A.Execute-Api.EU-West-1.localhost:4566", "eu-west-1"},
		{"MyBucket.S3.US-East-1.amazonaws.com", "us-east-1"},
	}
	for _, tc := range cases {
		if got := regionFromHost(tc.host); got != tc.want {
			t.Errorf("regionFromHost(%q) = %q; want %q", tc.host, got, tc.want)
		}
	}
}
