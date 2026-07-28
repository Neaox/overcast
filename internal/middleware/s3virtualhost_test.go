package middleware

import "testing"

func TestExtractS3BucketFromHost(t *testing.T) {
	tests := []struct {
		host string
		want string
	}{
		// Path-style: no subdomain → no rewrite.
		{"localhost", ""},
		{"localhost:4566", ""},
		{"127.0.0.1", ""},
		{"127.0.0.1:4566", ""},
		{"[::1]:4566", ""},

		// Virtual-hosted: {bucket}.localhost
		{"mybucket.localhost", "mybucket"},
		{"mybucket.localhost:4566", "mybucket"},
		{"cdk-hnb659fds-assets-050972292291-ap-southeast-2.localhost", "cdk-hnb659fds-assets-050972292291-ap-southeast-2"},
		{"cdk-hnb659fds-assets-050972292291-ap-southeast-2.localhost:4566", "cdk-hnb659fds-assets-050972292291-ap-southeast-2"},

		// Virtual-hosted: {bucket}.s3.localhost
		{"mybucket.s3.localhost", "mybucket"},
		{"mybucket.s3.localhost:4566", "mybucket"},
		// Plain ".s3.localhost" suffix with no configured extra base — proves
		// the ".s3." match rule is suffix-agnostic. Browsers and
		// systemd-resolved/Windows resolve *.localhost to loopback natively,
		// so "{bucket}.s3.localhost[:port]" is a common way real users hit
		// the endpoint without any OVERCAST_HOSTNAME configuration.
		{"my-bucket.s3.localhost", "my-bucket"},
		{"my-bucket.s3.localhost:4566", "my-bucket"},

		// Virtual-hosted: {bucket}.s3.{region}.localhost
		{"mybucket.s3.us-east-1.localhost", "mybucket"},
		{"mybucket.s3.ap-southeast-2.localhost:4566", "mybucket"},

		// Virtual-hosted: {bucket}.s3.amazonaws.com (standard AWS)
		{"mybucket.s3.amazonaws.com", "mybucket"},
		{"mybucket.s3.us-west-2.amazonaws.com", "mybucket"},

		// S3 service endpoint — NOT a virtual-hosted bucket request.
		// s3.localhost is the S3 service subdomain, not {bucket}.localhost.
		{"s3.localhost", ""},
		{"s3.localhost:4566", ""},
		{"s3.us-east-1.localhost", ""},
		{"s3.ap-southeast-2.localhost:4566", ""},

		// Legacy dash dialect: {bucket}.s3-{region}.{base} (pre-2019 regions,
		// e.g. mybucket.s3-us-west-2.amazonaws.com). The dash separator is
		// used instead of a dot before the region label.
		{"mybucket.s3-us-west-2.amazonaws.com", "mybucket"},
		{"mybucket.s3-external-1.amazonaws.com", "mybucket"},
		{"my.dotted.bucket.s3-us-west-2.amazonaws.com", "my.dotted.bucket"},
		// The bare regional dash-style S3 endpoint itself (no bucket
		// subdomain) must stay path-style.
		{"s3-us-west-2.amazonaws.com", ""},

		// Bucket names may contain dots — the bucket is everything before the
		// FIRST ".s3." separator.
		{"my.dotted.bucket.s3.localhost", "my.dotted.bucket"},

		// A host containing ".s3x." (not ".s3.") must NOT be mistaken for the
		// virtual-hosted separator — "s3x" is not "s3" in label position.
		{"weird.s3x.host", ""},
	}
	for _, tt := range tests {
		t.Run(tt.host, func(t *testing.T) {
			got := extractS3BucketFromHost(tt.host)
			if got != tt.want {
				t.Errorf("extractS3BucketFromHost(%q) = %q, want %q", tt.host, got, tt.want)
			}
		})
	}
}

// TestExtractS3BucketFromHost_builtInDefaultBases covers the wildcard-DNS
// domains recognised out of the box, with no OVERCAST_HOSTNAME configured:
// "localhost.overcast.sh" (our own domain) and "localhost.localstack.cloud"
// (what a user migrating from LocalStack already has in their config). Both
// resolve to 127.0.0.1 for every subdomain, so CDK/SDK-generated bucket
// hostnames reach the emulator without any hosts-file changes.
func TestExtractS3BucketFromHost_builtInDefaultBases(t *testing.T) {
	tests := []struct {
		host string
		want string
	}{
		// The base domains themselves are not buckets.
		{"localhost.overcast.sh", ""},
		{"localhost.overcast.sh:4566", ""},
		{"localhost.localstack.cloud", ""},
		{"localhost.localstack.cloud:4566", ""},

		// {bucket}.localhost.overcast.sh
		{"mybucket.localhost.overcast.sh", "mybucket"},
		{"mybucket.localhost.overcast.sh:4566", "mybucket"},

		// {bucket}.localhost.localstack.cloud — the reported CDK failure.
		{"mybucket.localhost.localstack.cloud", "mybucket"},
		{"cdk-hnb659fds-assets-000000000000-ap-southeast-2.localhost.localstack.cloud:4566", "cdk-hnb659fds-assets-000000000000-ap-southeast-2"},
		{"cdk-hnb659fds-assets-000000000000-ap-southeast-2.localhost.overcast.sh:4566", "cdk-hnb659fds-assets-000000000000-ap-southeast-2"},

		// Bucket names may contain dots — the whole prefix before the base is
		// the bucket. A prefix carrying a reserved host label at segment index
		// >= 1 ("abc.execute-api.us-east-1") is the one exception: that is a
		// host-routed address, not a bucket. See hostaddressing.go tier B.
		{"my.dotted.bucket.localhost.overcast.sh", "my.dotted.bucket"},
		{"my.dotted.bucket.localhost.localstack.cloud:4566", "my.dotted.bucket"},

		// Bare S3 service endpoints on the default bases are NOT buckets.
		{"s3.localhost.overcast.sh", ""},
		{"s3.localhost.localstack.cloud:4566", ""},
		{"s3.us-east-1.localhost.overcast.sh", ""},

		// The pre-existing "localhost" default must not be displaced.
		{"mybucket.localhost", "mybucket"},
		{"mybucket.localhost:4566", "mybucket"},
		{"s3.localhost", ""},

		// IPs still pass through untouched.
		{"127.0.0.1:4566", ""},
		{"[::1]:4566", ""},
	}
	for _, tt := range tests {
		t.Run(tt.host, func(t *testing.T) {
			got := extractS3BucketFromHost(tt.host)
			if got != tt.want {
				t.Errorf("extractS3BucketFromHost(%q) = %q, want %q", tt.host, got, tt.want)
			}
		})
	}
}

// TestExtractS3BucketFromHost_configuredHostnameAddsToDefaults proves a
// configured OVERCAST_HOSTNAME is additive: the built-in defaults keep working
// alongside it, and configuring a hostname that is already a default does not
// break (duplicate) handling.
func TestExtractS3BucketFromHost_configuredHostnameAddsToDefaults(t *testing.T) {
	tests := []struct {
		name      string
		extraBase string
		host      string
		want      string
	}{
		{"custom base recognised", "dev.example.test", "mybucket.dev.example.test", "mybucket"},
		{"custom base with port", "dev.example.test", "mybucket.dev.example.test:4566", "mybucket"},
		{"custom base itself is not a bucket", "dev.example.test", "dev.example.test:4566", ""},
		{"custom base s3 endpoint is not a bucket", "dev.example.test", "s3.dev.example.test", ""},

		// Defaults still recognised while a custom base is configured.
		{"localhost default survives custom base", "dev.example.test", "mybucket.localhost", "mybucket"},
		{"overcast.sh default survives custom base", "dev.example.test", "mybucket.localhost.overcast.sh", "mybucket"},
		{"localstack.cloud default survives custom base", "dev.example.test", "mybucket.localhost.localstack.cloud", "mybucket"},

		// Configuring a hostname that is already a built-in default must be a
		// no-op, not a duplicate-entry bug.
		{"configured hostname duplicates a default", "localhost.overcast.sh", "mybucket.localhost.overcast.sh", "mybucket"},
		{"configured hostname duplicates localhost", "localhost", "mybucket.localhost", "mybucket"},
		{"configured hostname duplicates localstack default", "localhost.localstack.cloud", "mybucket.localhost.localstack.cloud:4566", "mybucket"},

		// A configured parent domain must not shadow a longer default that also
		// matches — "x.localhost.overcast.sh" is bucket "x", not "x.localhost".
		{"longer default wins over configured parent domain", "overcast.sh", "mybucket.localhost.overcast.sh", "mybucket"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractS3BucketFromHost(tt.host, tt.extraBase)
			if got != tt.want {
				t.Errorf("extractS3BucketFromHost(%q, %q) = %q, want %q", tt.host, tt.extraBase, got, tt.want)
			}
		})
	}
}

// TestExtractS3BucketFromHost_withExtraBase covers wildcard-DNS hostnames set
// via OVERCAST_HOSTNAME (e.g. "localhost.localstack.cloud"). CDK's asset
// publisher constructs URLs like:
//
//	cdk-hnb659fds-assets-<account>-<region>.localhost.localstack.cloud:<port>
//
// which must be recognised and rewritten to path-style so the bucket is found.
func TestExtractS3BucketFromHost_withExtraBase(t *testing.T) {
	const base = "localhost.localstack.cloud"

	tests := []struct {
		host string
		want string
	}{
		// The base domain itself is not a bucket.
		{base, ""},
		{base + ":4566", ""},

		// {bucket}.{base} — plain virtual-host (SDK v3 / CDK asset publisher)
		{"mybucket." + base, "mybucket"},
		{"mybucket." + base + ":4566", "mybucket"},

		// CDK bootstrap bucket hostname — the real-world trigger for this fix
		{"cdk-hnb659fds-assets-000000000000-ap-southeast-2." + base, "cdk-hnb659fds-assets-000000000000-ap-southeast-2"},
		{"cdk-hnb659fds-assets-000000000000-ap-southeast-2." + base + ":4566", "cdk-hnb659fds-assets-000000000000-ap-southeast-2"},

		// {bucket}.s3.{base} — s3-subdomain style still handled by .s3. rule
		{"mybucket.s3." + base, "mybucket"},
		{"mybucket.s3." + base + ":4566", "mybucket"},

		// {bucket}.s3.{region}.{base}
		{"mybucket.s3.us-east-1." + base, "mybucket"},
		{"mybucket.s3.ap-southeast-2." + base + ":4566", "mybucket"},

		// {bucket}.s3-{region}.{base} — legacy dash dialect with a configured base.
		{"mybucket.s3-us-west-2." + base, "mybucket"},

		// Existing localhost patterns still work when an extra base is present.
		{"mybucket.localhost", "mybucket"},
		{"mybucket.localhost:4566", "mybucket"},

		// S3 service endpoint with extra base — NOT a virtual-hosted bucket.
		{"s3." + base, ""},
		{"s3." + base + ":4566", ""},
		{"s3.us-east-1." + base, ""},
		{"s3.ap-southeast-2." + base + ":4566", ""},

		// IPs still pass through untouched.
		{"127.0.0.1:4566", ""},
		{"[::1]:4566", ""},
	}
	for _, tt := range tests {
		t.Run(tt.host, func(t *testing.T) {
			got := extractS3BucketFromHost(tt.host, base)
			if got != tt.want {
				t.Errorf("extractS3BucketFromHost(%q, %q) = %q, want %q", tt.host, base, got, tt.want)
			}
		})
	}
}
