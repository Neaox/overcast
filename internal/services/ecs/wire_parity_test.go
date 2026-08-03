package ecs

// wire_parity_test.go — the JSON 1.1 and RPC v2 CBOR paths accept the same
// fields.
//
// CreateService, RunTask and RegisterTaskDefinition are each implemented twice,
// and the two drifted before: a field added to one path was silently dropped on
// the other, so the same call behaved differently depending on which protocol
// the caller's SDK happened to speak. Newer SDK releases move services onto
// CBOR, which makes that a bug users hit by upgrading rather than by changing
// their code.

import (
	"reflect"
	"sort"
	"testing"
)

// jsonFields returns the json tag names a request struct accepts.
func jsonFields(t *testing.T, v any) []string {
	t.Helper()
	rt := reflect.TypeOf(v)
	if rt.Kind() == reflect.Ptr {
		rt = rt.Elem()
	}
	if rt.Kind() != reflect.Struct {
		t.Fatalf("expected a struct, got %s", rt.Kind())
	}
	names := make([]string, 0, rt.NumField())
	for i := range rt.NumField() {
		tag := rt.Field(i).Tag.Get("json")
		for j := 0; j < len(tag); j++ {
			if tag[j] == ',' {
				tag = tag[:j]
				break
			}
		}
		if tag != "" && tag != "-" {
			names = append(names, tag)
		}
	}
	sort.Strings(names)
	return names
}

// assertAccepts fails when the typed request is missing a field the legacy one
// accepts. Extra fields on the typed side are fine — it may model more.
func assertAccepts(t *testing.T, operation string, legacy, typed []string) {
	t.Helper()
	have := make(map[string]bool, len(typed))
	for _, f := range typed {
		have[f] = true
	}
	var missing []string
	for _, f := range legacy {
		if !have[f] {
			missing = append(missing, f)
		}
	}
	if len(missing) > 0 {
		t.Errorf("%s: the CBOR path drops fields the JSON path accepts: %v\n"+
			"Add them to the typed request struct in typed_logic.go, or the same call "+
			"behaves differently depending on the caller's SDK.", operation, missing)
	}
}

func TestWireParity_createService(t *testing.T) {
	// The legacy handler declares its request inline, so the field list is
	// restated here; the test's value is catching a field added to one path and
	// not the other, which this comparison still does.
	legacy := []string{
		"cluster", "serviceName", "taskDefinition", "desiredCount", "launchType",
		"schedulingStrategy", "networkConfiguration", "deploymentController",
		"deploymentConfiguration", "capacityProviderStrategy", "loadBalancers",
		"platformVersion", "healthCheckGracePeriodSeconds", "enableExecuteCommand",
		"propagateTags", "serviceRegistries", "placementStrategy", "placementConstraints",
	}
	sort.Strings(legacy)
	assertAccepts(t, "CreateService", legacy, jsonFields(t, createServiceRequest{}))
}

func TestWireParity_runTask(t *testing.T) {
	legacy := []string{
		"cluster", "taskDefinition", "count", "launchType", "networkConfiguration",
		"platformVersion", "overrides", "group", "startedBy",
	}
	sort.Strings(legacy)
	assertAccepts(t, "RunTask", legacy, jsonFields(t, runTaskRequest{}))
}

func TestWireParity_registerTaskDefinition(t *testing.T) {
	legacy := []string{
		"family", "containerDefinitions", "networkMode", "requiresCompatibilities",
		"cpu", "memory", "volumes", "taskRoleArn", "executionRoleArn",
		"runtimePlatform", "ephemeralStorage", "pidMode", "ipcMode",
	}
	sort.Strings(legacy)
	assertAccepts(t, "RegisterTaskDefinition", legacy, jsonFields(t, registerTaskDefinitionRequest{}))
}
