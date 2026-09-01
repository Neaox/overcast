package main

import (
	"strings"
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/overcast-sh/overcast/internal/config"
)

// TestLogHostnameAlias_namesTheAliasWhenUsed verifies the #1190 startup log
// line: when LOCALSTACK_HOST supplied or confirmed OVERCAST_HOSTNAME,
// logHostnameAlias names both the alias variable and the resulting hostname,
// so an operator migrating a LocalStack compose file sees their setting was
// recognised.
func TestLogHostnameAlias_namesTheAliasWhenUsed(t *testing.T) {
	core, logs := observer.New(zapcore.DebugLevel)
	logger := zap.New(core)
	cfg := &config.Config{
		Hostname:            "localhost.localstack.cloud",
		HostnameAliasSource: "LOCALSTACK_HOST",
	}

	logHostnameAlias(logger, cfg)

	entries := logs.FilterMessageSnippet("LocalStack-compatibility alias").All()
	if len(entries) != 1 {
		t.Fatalf("expected exactly 1 hostname-alias log line, got %d", len(entries))
	}
	entry := entries[0]
	if !strings.Contains(entry.Message, "LOCALSTACK_HOST") || !strings.Contains(entry.Message, "localhost.localstack.cloud") {
		t.Errorf("log message %q does not name both the alias variable and the hostname", entry.Message)
	}
	if got := entry.ContextMap()["hostnameAliasSource"]; got != "LOCALSTACK_HOST" {
		t.Errorf("hostnameAliasSource field: expected LOCALSTACK_HOST, got %v", got)
	}
	if got := entry.ContextMap()["hostname"]; got != "localhost.localstack.cloud" {
		t.Errorf("hostname field: expected localhost.localstack.cloud, got %v", got)
	}
}

// TestLogHostnameAlias_silentWhenNoAlias verifies no log line is emitted
// when OVERCAST_HOSTNAME was set without any LocalStack alias, or when
// neither was set — HostnameAliasSource is empty either way, and there is
// nothing for the operator to be told about an alias that was never used.
func TestLogHostnameAlias_silentWhenNoAlias(t *testing.T) {
	for _, tc := range []struct {
		name string
		cfg  *config.Config
	}{
		{name: "OVERCAST_HOSTNAME set alone", cfg: &config.Config{Hostname: "overcast.internal"}},
		{name: "neither set", cfg: &config.Config{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			core, logs := observer.New(zapcore.DebugLevel)
			logger := zap.New(core)

			logHostnameAlias(logger, tc.cfg)

			if n := logs.Len(); n != 0 {
				t.Errorf("expected no log lines, got %d: %v", n, logs.All())
			}
		})
	}
}
