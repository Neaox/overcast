package main

import (
	"strings"
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/overcast-sh/overcast/internal/config"
)

// TestLogLocalStackAliases_namesEachAliasUsed verifies the #1190 second-PR
// startup log line: one Info line per entry in LocalStackAliasesUsed, naming
// both the Overcast variable and the LocalStack alias that supplied or
// confirmed it, in deterministic (sorted) order regardless of map iteration
// order.
func TestLogLocalStackAliases_namesEachAliasUsed(t *testing.T) {
	core, logs := observer.New(zapcore.DebugLevel)
	logger := zap.New(core)
	cfg := &config.Config{
		LocalStackAliasesUsed: map[string]string{
			"OVERCAST_PORT":   "EDGE_PORT",
			"OVERCAST_LISTEN": "GATEWAY_LISTEN",
		},
	}

	logLocalStackAliases(logger, cfg)

	entries := logs.FilterMessageSnippet("LocalStack-compatibility alias").All()
	if len(entries) != 2 {
		t.Fatalf("expected exactly 2 alias log lines, got %d: %v", len(entries), logs.All())
	}
	if !strings.Contains(entries[0].Message, "OVERCAST_LISTEN") || !strings.Contains(entries[0].Message, "GATEWAY_LISTEN") {
		t.Errorf("first log line %q should name OVERCAST_LISTEN/GATEWAY_LISTEN (sorted order)", entries[0].Message)
	}
	if !strings.Contains(entries[1].Message, "OVERCAST_PORT") || !strings.Contains(entries[1].Message, "EDGE_PORT") {
		t.Errorf("second log line %q should name OVERCAST_PORT/EDGE_PORT (sorted order)", entries[1].Message)
	}
	if got := entries[1].ContextMap()["localStackAliasSource"]; got != "EDGE_PORT" {
		t.Errorf("localStackAliasSource field: expected EDGE_PORT, got %v", got)
	}
}

// TestLogLocalStackAliases_namesEachIgnoredVar verifies one Info line per
// recognised-but-inert LocalStack variable, each naming why it has no
// effect.
func TestLogLocalStackAliases_namesEachIgnoredVar(t *testing.T) {
	core, logs := observer.New(zapcore.DebugLevel)
	logger := zap.New(core)
	cfg := &config.Config{
		IgnoredLocalStackVars: []string{"SERVICES", "LOCALSTACK_API_KEY"},
	}

	logLocalStackAliases(logger, cfg)

	entries := logs.FilterMessageSnippet("no Overcast effect").All()
	if len(entries) != 2 {
		t.Fatalf("expected exactly 2 ignored-var log lines, got %d: %v", len(entries), logs.All())
	}
	if !strings.Contains(entries[0].Message, "LOCALSTACK_API_KEY") {
		t.Errorf("first log line %q should name LOCALSTACK_API_KEY (sorted order)", entries[0].Message)
	}
	if !strings.Contains(entries[1].Message, "SERVICES") || !strings.Contains(entries[1].Message, "runs every service") {
		t.Errorf("second log line %q should name SERVICES and its reason", entries[1].Message)
	}
}

// TestLogLocalStackAliases_silentWhenNothingToReport verifies no log lines
// are emitted when no alias fired and no ignored variable was seen.
func TestLogLocalStackAliases_silentWhenNothingToReport(t *testing.T) {
	core, logs := observer.New(zapcore.DebugLevel)
	logger := zap.New(core)

	logLocalStackAliases(logger, &config.Config{})

	if n := logs.Len(); n != 0 {
		t.Errorf("expected no log lines, got %d: %v", n, logs.All())
	}
}
