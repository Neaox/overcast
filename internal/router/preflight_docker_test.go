package router

import (
	"strings"
	"testing"

	"github.com/overcast-sh/overcast/internal/docker"
)

func TestDockerUnavailableWarning_emptyWhenEverythingConnected(t *testing.T) {
	configs := []docker.ServiceConfig{
		{Name: "rds", Socket: "/var/run/docker.sock"},
		{Name: "ecs", Socket: "/var/run/docker.sock"},
	}
	results := []docker.ServiceResult{
		{Name: "rds"},
		{Name: "ecs"},
	}
	if got := dockerUnavailableWarning(configs, results); got != "" {
		t.Errorf("expected no warning when every configured service connected, got %q", got)
	}
}

func TestDockerUnavailableWarning_emptyWhenNoServicesConfigured(t *testing.T) {
	if got := dockerUnavailableWarning(nil, nil); got != "" {
		t.Errorf("expected no warning with no configured services, got %q", got)
	}
}

func TestDockerUnavailableWarning_namesEveryUnconnectedService(t *testing.T) {
	configs := []docker.ServiceConfig{
		{Name: "rds", Socket: "/var/run/docker.sock"},
		{Name: "ecs", Socket: "/var/run/docker.sock"},
		{Name: "lambda", Socket: "/var/run/docker.sock"},
	}
	// Only lambda connected — rds and ecs both failed to probe.
	results := []docker.ServiceResult{
		{Name: "lambda"},
	}

	got := dockerUnavailableWarning(configs, results)
	if got == "" {
		t.Fatal("expected a warning naming the unconnected services, got none")
	}
	for _, want := range []string{"rds", "ecs", "/var/run/docker.sock"} {
		if !strings.Contains(got, want) {
			t.Errorf("warning = %q, expected it to mention %q", got, want)
		}
	}
	if strings.Contains(got, "lambda") {
		t.Errorf("warning = %q, must not name lambda — it connected successfully", got)
	}
}

func TestDockerUnavailableWarning_groupsByDistinctSocket(t *testing.T) {
	configs := []docker.ServiceConfig{
		{Name: "rds", Socket: "/var/run/docker.sock"},
		{Name: "eks", Socket: "/custom/eks.sock"},
	}
	// Nothing connected on either socket.
	got := dockerUnavailableWarning(configs, nil)
	if got == "" {
		t.Fatal("expected a warning, got none")
	}
	for _, want := range []string{"rds", "eks", "/var/run/docker.sock", "/custom/eks.sock"} {
		if !strings.Contains(got, want) {
			t.Errorf("warning = %q, expected it to mention %q", got, want)
		}
	}
}

// TestDockerUnavailableWarning_deterministicAcrossRuns pins ordering: two
// calls with the configs built in a different order must still produce
// byte-identical text, since a message that reshuffles service names between
// otherwise-identical startups would be a confusing thing to diff in logs.
func TestDockerUnavailableWarning_deterministicAcrossRuns(t *testing.T) {
	a := []docker.ServiceConfig{
		{Name: "rds", Socket: "/var/run/docker.sock"},
		{Name: "ecs", Socket: "/var/run/docker.sock"},
		{Name: "eks", Socket: "/custom/eks.sock"},
	}
	b := []docker.ServiceConfig{
		{Name: "eks", Socket: "/custom/eks.sock"},
		{Name: "ecs", Socket: "/var/run/docker.sock"},
		{Name: "rds", Socket: "/var/run/docker.sock"},
	}

	gotA := dockerUnavailableWarning(a, nil)
	gotB := dockerUnavailableWarning(b, nil)
	if gotA != gotB {
		t.Errorf("expected identical output regardless of config order:\nA: %q\nB: %q", gotA, gotB)
	}
}
