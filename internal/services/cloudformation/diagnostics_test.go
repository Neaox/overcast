package cloudformation

// diagnostics_test.go — the deploy-diagnostics journal, exercised from the
// outside: a deploy fails, the rollback destroys everything it can reach, and
// the diagnosis is still readable afterwards.
//
// The fake ECS router below is the load-bearing part of these tests, not
// scaffolding. It stops answering for its stopped tasks and their container
// logs the moment DeleteService lands — which is exactly what a real rollback
// does to them — so a capture moved to after teardown would gather nothing and
// every assertion in TestStackDiagnostics_survivesTheRollbackThatDeletesTheEvidence
// would fail. Ordering is the property under test; the double exists to make
// it observable.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/state"
)

const (
	diagStackName    = "MyStack"
	diagStackID      = "arn:aws:cloudformation:us-east-1:000000000000:stack/MyStack/11111111-2222-3333-4444-555555555555"
	diagServiceARN   = "arn:aws:ecs:us-east-1:000000000000:service/MyStack-Cluster/MyStack-WebService-9f21c4"
	diagTaskARN      = "arn:aws:ecs:us-east-1:000000000000:task/MyStack-Cluster/8f3a1c9d4b2e4f7a9c0d1e2f3a4b5c6d"
	diagContainer    = "app"
	diagAWSSentence  = "(service MyStack-WebService-9f21c4) is unable to consistently start tasks successfully."
	diagContainerLog = "npm ERR! missing script: start\n" +
		"Error: DATABASE_URL is not set\n" +
		"    at loadConfig (/app/src/config.js:14:11)\n"
)

// ── The fake ECS router ────────────────────────────────────────────────────

// fakeECSRouter answers the subset of ECS that the CloudFormation resource
// handler and the diagnostics collector reach for, and nothing else. It is a
// router rather than a stub object on purpose: the collector is required to
// gather its evidence over the emulator router exactly as the provisioner
// already does, so a double that could only be reached by a Go import would
// let that requirement rot silently.
type fakeECSRouter struct {
	mu sync.Mutex
	// serviceDeleted flips when the rollback deletes the service. From that
	// moment the stopped tasks and their retained logs are unreachable, which
	// is what makes capture-order observable.
	serviceDeleted bool
	deleted        []string
	targets        []string
}

func newFakeECSRouter() *fakeECSRouter { return &fakeECSRouter{} }

func (f *fakeECSRouter) deletedServices() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.deleted...)
}

func (f *fakeECSRouter) seenTargets() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.targets...)
}

func (f *fakeECSRouter) torndown() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.serviceDeleted
}

func (f *fakeECSRouter) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if target := r.Header.Get("X-Amz-Target"); target != "" {
		f.mu.Lock()
		f.targets = append(f.targets, target)
		f.mu.Unlock()
		f.serveECSJSON(w, strings.TrimPrefix(target, "AmazonEC2ContainerServiceV20141113."))
		return
	}
	if r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/_overcast/ecs/tasks/") {
		f.serveContainerLogs(w, r)
		return
	}
	http.NotFound(w, r)
}

func (f *fakeECSRouter) serveECSJSON(w http.ResponseWriter, op string) {
	switch op {
	case "CreateService":
		writeFakeJSON(w, http.StatusOK, map[string]any{"service": map[string]any{
			"serviceArn":  diagServiceARN,
			"serviceName": "MyStack-WebService-9f21c4",
			"clusterArn":  "arn:aws:ecs:us-east-1:000000000000:cluster/MyStack-Cluster",
		}})
	case "DescribeServices":
		writeFakeJSON(w, http.StatusOK, map[string]any{"services": f.describedServices()})
	case "ListTasks":
		writeFakeJSON(w, http.StatusOK, map[string]any{"taskArns": f.stoppedTaskARNs()})
	case "DescribeTasks":
		writeFakeJSON(w, http.StatusOK, map[string]any{"tasks": f.describedTasks(), "failures": []any{}})
	case "UpdateService":
		writeFakeJSON(w, http.StatusOK, map[string]any{"service": map[string]any{"serviceArn": diagServiceARN}})
	case "DeleteService":
		f.mu.Lock()
		f.serviceDeleted = true
		f.deleted = append(f.deleted, diagServiceARN)
		f.mu.Unlock()
		writeFakeJSON(w, http.StatusOK, map[string]any{"service": map[string]any{"serviceArn": diagServiceARN}})
	default:
		writeFakeJSON(w, http.StatusBadRequest, map[string]any{"__type": "InvalidAction", "message": op})
	}
}

// describedServices reports a service whose deployment has given up. After the
// rollback deletes it there is no service to describe, as on ECS.
func (f *fakeECSRouter) describedServices() []any {
	if f.torndown() {
		return []any{}
	}
	return []any{map[string]any{
		"serviceName":  "MyStack-WebService-9f21c4",
		"desiredCount": 1,
		"runningCount": 0,
		"deployments": []any{map[string]any{
			"id":                 "ecs-svc/1234",
			"status":             "PRIMARY",
			"desiredCount":       1,
			"runningCount":       0,
			"failedTasks":        3,
			"rolloutState":       "FAILED",
			"rolloutStateReason": "ECS deployment circuit breaker: task failed to start.",
		}},
		"events": []any{
			map[string]any{"id": "e2", "createdAt": 1755230400.0, "message": diagAWSSentence},
			map[string]any{"id": "e1", "createdAt": 1755230394.0, "message": "(service MyStack-WebService-9f21c4) has started 1 tasks: (task 8f3a1c9d)."},
		},
	}}
}

func (f *fakeECSRouter) stoppedTaskARNs() []string {
	if f.torndown() {
		return []string{}
	}
	return []string{diagTaskARN}
}

func (f *fakeECSRouter) describedTasks() []any {
	if f.torndown() {
		return []any{}
	}
	started, stopped := 1755230394.0, 1755230400.0
	return []any{map[string]any{
		"taskArn":       diagTaskARN,
		"clusterArn":    "arn:aws:ecs:us-east-1:000000000000:cluster/MyStack-Cluster",
		"group":         "service:MyStack-WebService-9f21c4",
		"lastStatus":    "STOPPED",
		"desiredStatus": "STOPPED",
		"stoppedReason": "Essential container in task exited",
		"stopCode":      "EssentialContainerExited",
		"startedAt":     started,
		"stoppedAt":     stopped,
		"containers": []any{map[string]any{
			"name":       diagContainer,
			"lastStatus": "STOPPED",
			"exitCode":   1.0,
			"reason":     "",
		}},
	}}
}

func (f *fakeECSRouter) serveContainerLogs(w http.ResponseWriter, r *http.Request) {
	if f.torndown() {
		writeFakeJSON(w, http.StatusNotFound, map[string]any{"error": "task or container not found"})
		return
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/_overcast/ecs/tasks/"), "/")
	if len(parts) != 3 || parts[1] != "logs" {
		writeFakeJSON(w, http.StatusNotFound, map[string]any{"error": "no such endpoint"})
		return
	}
	writeFakeJSON(w, http.StatusOK, map[string]any{
		"taskArn":       diagTaskARN,
		"containerName": parts[2],
		"logs":          diagContainerLog,
	})
}

func writeFakeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// ── Fixture ────────────────────────────────────────────────────────────────

// diagFixture is a CloudFormation service wired to the fake ECS router, plus
// the chi router carrying its emulator-only endpoints.
type diagFixture struct {
	svc    *Service
	ecs    *fakeECSRouter
	routes chi.Router
}

func newDiagFixture(t *testing.T) *diagFixture {
	t.Helper()
	cfg := &config.Config{Region: "us-east-1", AccountID: "000000000000"}
	svc := New(cfg, state.NewMemoryStore(), zap.NewNop(), clock.New())
	ecs := newFakeECSRouter()
	svc.InitRouter(ecs)
	routes := chi.NewRouter()
	svc.RegisterRoutes(routes)
	t.Cleanup(func() { svc.Stop(t.Context()) })
	return &diagFixture{svc: svc, ecs: ecs, routes: routes}
}

// failingECSStack is the stack these tests deploy: one ECS service that cannot
// keep a task alive, which is the failure the whole feature exists for.
func failingECSStack() (*Stack, *Template) {
	stack := &Stack{
		StackName: diagStackName,
		StackID:   diagStackID,
		Region:    "us-east-1",
	}
	tmpl := &Template{Resources: map[string]TemplateResource{
		"WebService": {Type: "AWS::ECS::Service", Properties: map[string]any{
			"Cluster":        "MyStack-Cluster",
			"TaskDefinition": "arn:aws:ecs:us-east-1:000000000000:task-definition/web:1",
			"DesiredCount":   1,
		}},
	}}
	return stack, tmpl
}

// getDiagnostics calls the emulator-only endpoint the console reads.
func (f *diagFixture) getDiagnostics(t *testing.T, stackName string) (int, []byte) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet,
		"/_overcast/cloudformation/stacks/"+stackName+"/diagnostics", nil)
	rec := httptest.NewRecorder()
	f.routes.ServeHTTP(rec, req)
	return rec.Code, rec.Body.Bytes()
}

// ── P0.1 — the whole feature in one test ───────────────────────────────────

// A deploy fails because its ECS service cannot keep a task alive; the
// rollback then deletes the service, which takes the stopped task record and
// the container's retained output with it. The diagnosis has to outlive all of
// that, because the moment a developer wants it is after the rollback has
// finished.
func TestStackDiagnostics_survivesTheRollbackThatDeletesTheEvidence(t *testing.T) {
	// Given: a stack whose only resource is a service that never stabilizes
	f := newDiagFixture(t)
	stack, tmpl := failingECSStack()

	// When: the deploy fails and the rollback runs to completion
	f.svc.provisioner.provisionStackResources(stack, tmpl)

	if stack.Status != StatusRollbackComplete {
		t.Fatalf("stack status = %q, want %q — the test needs a completed rollback", stack.Status, StatusRollbackComplete)
	}
	if !f.ecs.torndown() {
		t.Fatal("the rollback did not delete the ECS service, so this test proves nothing about surviving it")
	}
	// The ordering is the mechanism, so assert it directly as well as through
	// its consequences: the task records must have been read before the call
	// that destroyed them.
	if describedAt, deletedAt := indexOfTarget(f.ecs.seenTargets(), "DescribeTasks"),
		indexOfTarget(f.ecs.seenTargets(), "DeleteService"); describedAt < 0 || describedAt > deletedAt {
		t.Fatalf("ECS calls went %v — DescribeTasks must precede DeleteService, or there is nothing left to describe",
			f.ecs.seenTargets())
	}

	// Then: the endpoint still answers with the stopped task's exit code and
	// the container's own output
	code, body := f.getDiagnostics(t, diagStackName)
	if code != http.StatusOK {
		t.Fatalf("GET diagnostics = %d, want 200; body %s", code, body)
	}
	var got DeployDiagnostics
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode diagnostics: %v; body %s", err, body)
	}

	if got.StackName != diagStackName || got.StackID != diagStackID {
		t.Errorf("journal identifies %q/%q, want %q/%q", got.StackName, got.StackID, diagStackName, diagStackID)
	}
	if got.Operation != deployOperationCreate {
		t.Errorf("operation = %q, want %q", got.Operation, deployOperationCreate)
	}
	if !strings.Contains(got.AWSReason, "unable to consistently start tasks") {
		t.Errorf("awsReason = %q, want the sentence CloudFormation itself recorded", got.AWSReason)
	}
	if len(got.Resources) != 1 {
		t.Fatalf("resources = %+v, want the one failed resource", got.Resources)
	}

	res := got.Resources[0]
	if res.LogicalID != "WebService" || res.Type != "AWS::ECS::Service" {
		t.Errorf("resource = %q/%q, want WebService/AWS::ECS::Service", res.LogicalID, res.Type)
	}

	exitCode := findFact(res.Sections, "Exit code")
	if exitCode == nil {
		t.Fatalf("no exit-code fact in %s", mustJSON(t, res.Sections))
	}
	if exitCode.Value != "1" {
		t.Errorf("exit code = %q, want %q", exitCode.Value, "1")
	}

	logSection := findSectionOfKind(res.Sections, sectionKindLog)
	if logSection == nil {
		t.Fatalf("no container output in %s", mustJSON(t, res.Sections))
	}
	if !strings.Contains(logSection.Log.Text, "DATABASE_URL is not set") {
		t.Errorf("container output = %q, want the container's own error", logSection.Log.Text)
	}
	if logSection.Provenance != provenanceCapture {
		t.Errorf("container output provenance = %q, want %q — real AWS discards it too",
			logSection.Provenance, provenanceCapture)
	}

	if got.Headline == "" {
		t.Error("headline is empty; the tab leads with Overcast's one-sentence reading of the evidence")
	}
	if got.Counterfactual == "" {
		t.Error("counterfactual is empty; it is what keeps the Overcast-only evidence honest")
	}
}

// ── P0.2 — hard rule 1: the AWS surface is unchanged ───────────────────────

// Nothing Overcast authors may enter an AWS-shaped field. The check is
// byte-identity of the three read operations a client uses to find out why a
// deploy failed, taken from one failed stack with the journal present and then
// again with it removed.
//
// Comparing one stack against itself rather than two runs against each other
// is what makes this a real guard: with two runs, a difference in timestamps
// or generated names would have to be normalised away, and a normaliser is
// exactly the thing that would hide an Overcast-authored string appearing in a
// reason field.
func TestStackDiagnostics_leavesTheAWSSurfaceByteIdentical(t *testing.T) {
	// Given: a stack whose deploy failed and was journalled with real evidence
	f := newDiagFixture(t)
	stack, tmpl := failingECSStack()
	f.svc.provisioner.provisionStackResources(stack, tmpl)

	code, body := f.getDiagnostics(t, diagStackName)
	if code != http.StatusOK {
		t.Fatalf("GET diagnostics = %d, want a journal to compare against: %s", code, body)
	}

	actions := []string{"DescribeStacks", "DescribeStackEvents", "ListStackResources"}
	withJournal := map[string]string{}
	for _, action := range actions {
		withJournal[action] = queryCFN(t, f.svc, action, diagStackName)
	}

	// When: the journal is removed entirely
	if err := f.svc.provisioner.store.deleteDeployDiagnostics(t.Context(), diagStackName); err != nil {
		t.Fatalf("deleteDeployDiagnostics: %v", err)
	}
	if code, body := f.getDiagnostics(t, diagStackName); code != http.StatusNotFound {
		t.Fatalf("GET diagnostics after delete = %d, want 404: %s", code, body)
	}

	// Then: every AWS response is byte-for-byte what it was
	for _, action := range actions {
		if got := queryCFN(t, f.svc, action, diagStackName); got != withJournal[action] {
			t.Errorf("%s differs when a journal is present.\nwith:    %s\nwithout: %s",
				action, withJournal[action], got)
		}
	}

	// And no string Overcast authored for the journal appears anywhere on the
	// AWS surface — byte-identity proves the journal changes nothing, this
	// proves the journal's vocabulary was never there to begin with.
	overcastOnly := []string{
		"Diagnostics", "diagnostics", "overcast-capture", "overcast-inference",
		"DATABASE_URL", "Container output", "In real AWS this deploy",
	}
	for _, action := range actions {
		for _, needle := range overcastOnly {
			if strings.Contains(withJournal[action], needle) {
				t.Errorf("%s response contains %q — nothing Overcast authors may enter an AWS-shaped field:\n%s",
					action, needle, withJournal[action])
			}
		}
	}
}

// requestIDElement matches the one field in an AWS response that is per-call
// rather than per-stack. It is normalised away in the byte-identity comparison
// below, and it is the only thing that is: a fresh request ID differs between
// any two calls whatever the journal does, so leaving it in would make the
// comparison vacuous rather than strict.
var requestIDElement = regexp.MustCompile(`<RequestId>[^<]*</RequestId>`)

// queryCFN issues a Query-protocol call against the CloudFormation handler and
// returns the XML body with the per-call request ID neutralised.
func queryCFN(t *testing.T, svc *Service, action, stackName string) string {
	t.Helper()
	form := "Action=" + action + "&Version=2010-05-15&StackName=" + stackName
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(form))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	svc.DispatchQuery(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("%s = %d: %s", action, rec.Code, rec.Body.String())
	}
	return requestIDElement.ReplaceAllString(rec.Body.String(), "<RequestId/>")
}

// ── P0.3 — hard rule 2: no resource outlives its rollback ──────────────────

// Capture reads the evidence; it must never retain the resource that holds it.
// Retaining an ECS service so its logs stayed readable would break the very
// semantics this feature exists to preserve.
func TestStackDiagnostics_captureRetainsNothing(t *testing.T) {
	// Given: a failing deploy of an ECS service, captured and rolled back
	f := newDiagFixture(t)
	stack, tmpl := failingECSStack()

	// When: the rollback runs
	f.svc.provisioner.provisionStackResources(stack, tmpl)

	// Then: the service was deleted exactly once, and nothing is left listed
	// on the stack as still standing
	if deleted := f.ecs.deletedServices(); len(deleted) != 1 || deleted[0] != diagServiceARN {
		t.Errorf("rollback deleted %v, want exactly [%s] — capture must not hold a resource open",
			deleted, diagServiceARN)
	}
	for _, r := range stack.Resources {
		if r.Status != ResourceDeleteComplete {
			t.Errorf("resource %s ended %q, want %q — capture must not turn a delete into a retain",
				r.LogicalID, r.Status, ResourceDeleteComplete)
		}
	}
	// And the journal exists anyway, which is the whole point of doing the
	// reading before the deleting rather than instead of it.
	if code, body := f.getDiagnostics(t, diagStackName); code != http.StatusOK {
		t.Fatalf("GET diagnostics = %d, want 200; body %s", code, body)
	}
}

// ── P0.4 — one entry per stack ─────────────────────────────────────────────

// The question the tab answers is why *this* deploy failed, so a second failed
// deploy of the same stack replaces the entry rather than adding to it. That
// is what bounds the journal without any eviction machinery.
func TestStackDiagnostics_secondFailedDeployReplacesTheEntry(t *testing.T) {
	// Given: a stack that has already failed and been journalled
	f := newDiagFixture(t)
	stack, tmpl := failingECSStack()
	f.svc.provisioner.provisionStackResources(stack, tmpl)

	code, first := f.getDiagnostics(t, diagStackName)
	if code != http.StatusOK {
		t.Fatalf("first deploy: GET diagnostics = %d: %s", code, first)
	}

	// When: the same stack is deployed again and fails again
	f.ecs = newFakeECSRouter()
	f.svc.InitRouter(f.ecs)
	stack2, tmpl2 := failingECSStack()
	stack2.StackID = diagStackID + "-second"
	f.svc.provisioner.provisionStackResources(stack2, tmpl2)

	// Then: there is one entry, and it describes the second deploy
	code, second := f.getDiagnostics(t, diagStackName)
	if code != http.StatusOK {
		t.Fatalf("second deploy: GET diagnostics = %d: %s", code, second)
	}
	var got DeployDiagnostics
	if err := json.Unmarshal(second, &got); err != nil {
		t.Fatalf("decode diagnostics: %v", err)
	}
	if got.StackID != stack2.StackID {
		t.Errorf("journal stackId = %q, want the second deploy's %q — the entry is replaced, not appended to",
			got.StackID, stack2.StackID)
	}
	if len(got.Resources) != 1 {
		t.Errorf("resources = %d, want 1 — a replaced entry does not accumulate the previous deploy's",
			len(got.Resources))
	}
}

// A journal exists if and only if the *most recent* deploy failed. A stack
// that failed, was fixed and now deploys clean must stop answering with the
// old explanation — a failure story on a green stack is worse than none, and
// the console cannot filter it out for itself because the existence of the
// journal is the answer to its request.
func TestStackDiagnostics_successfulDeployClearsTheEntry(t *testing.T) {
	// Given: a stack whose deploy failed and was journalled
	f := newDiagFixture(t)
	stack, tmpl := failingECSStack()
	f.svc.provisioner.provisionStackResources(stack, tmpl)
	if code, body := f.getDiagnostics(t, diagStackName); code != http.StatusOK {
		t.Fatalf("after the failed deploy: GET diagnostics = %d: %s", code, body)
	}

	// When: the same stack is deployed again and this time succeeds. The
	// template is a type with no registered handler, which provisions as a
	// stub — the deploy's outcome is what matters here, not what it built.
	fixed := &Stack{StackName: diagStackName, StackID: diagStackID, Region: "us-east-1"}
	f.svc.provisioner.provisionStackResources(fixed, &Template{Resources: map[string]TemplateResource{
		"Placeholder": {Type: "Test::Deploy::Succeeds"},
	}})
	if fixed.Status != StatusCreateComplete {
		t.Fatalf("stack status = %q, want %q — this test needs a successful deploy", fixed.Status, StatusCreateComplete)
	}

	// Then: there is nothing left to explain
	code, body := f.getDiagnostics(t, diagStackName)
	if code != http.StatusNotFound {
		t.Fatalf("GET diagnostics after a successful deploy = %d, want 404 — the old diagnosis describes nothing now; body %s",
			code, body)
	}
}

// A healthy stack has no journal, and the console hides the tab on that 404
// rather than showing an empty one. It is the ordinary case, so it must not
// read as an error.
func TestStackDiagnostics_absentJournalIs404WithAMessage(t *testing.T) {
	f := newDiagFixture(t)

	code, body := f.getDiagnostics(t, "a-stack-that-never-failed")
	if code != http.StatusNotFound {
		t.Fatalf("GET diagnostics = %d, want 404; body %s", code, body)
	}
	var out struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode 404 body: %v; body %s", err, body)
	}
	if out.Error == "" {
		t.Errorf("404 body = %s, want an {\"error\": …} message", body)
	}
}

// A stopped task belongs to the service that owns it, and to no other. ECS's
// ListTasks is cluster-wide, so the collector filters the described tasks by
// their `group` — and if it cannot name the service it must gather nothing
// rather than sweep the cluster and attribute another stack's failures to this
// one. That case is reachable: a service already gone by the time capture ran
// describes as nothing at all.
func TestStoppedTasksForECSService_attributesTasksToTheirOwnerOnly(t *testing.T) {
	ecs := newFakeECSRouter()
	ctx := t.Context()

	t.Run("another service's stopped tasks are not claimed", func(t *testing.T) {
		tasks, omitted := stoppedTasksForECSService(ctx, ecs, "us-east-1", "MyStack-Cluster", "SomeOtherService")
		if len(tasks) != 0 || omitted != 0 {
			t.Errorf("got %d tasks (%d omitted), want none — the stopped task belongs to another service",
				len(tasks), omitted)
		}
	})

	t.Run("an unnameable service gathers nothing", func(t *testing.T) {
		tasks, _ := stoppedTasksForECSService(ctx, ecs, "us-east-1", "MyStack-Cluster", "")
		if len(tasks) != 0 {
			t.Errorf("got %d tasks, want none — with no service name every task in the cluster would be claimed",
				len(tasks))
		}
	})

	t.Run("its own stopped tasks are found", func(t *testing.T) {
		tasks, _ := stoppedTasksForECSService(ctx, ecs, "us-east-1", "MyStack-Cluster", "MyStack-WebService-9f21c4")
		if len(tasks) != 1 {
			t.Fatalf("got %d tasks, want 1", len(tasks))
		}
	})
}

// The provenance, kind and operation values are the interface between this
// package and the console, which switches on them by literal string. A rename
// here would compile cleanly and break the tab silently, so the wire values
// are pinned rather than left to the constants' own spelling.
func TestStackDiagnostics_contractValuesAreExact(t *testing.T) {
	for _, tc := range []struct{ got, want string }{
		{provenanceAWSAPI, "aws-api"},
		{provenanceCapture, "overcast-capture"},
		{provenanceInference, "overcast-inference"},
		{sectionKindFacts, "facts"},
		{sectionKindEvents, "events"},
		{sectionKindLog, "log"},
		{deployOperationCreate, "CREATE"},
		{deployOperationUpdate, "UPDATE"},
	} {
		if tc.got != tc.want {
			t.Errorf("contract value = %q, want %q", tc.got, tc.want)
		}
	}
}

// ── helpers ────────────────────────────────────────────────────────────────

// indexOfTarget reports where an ECS operation first appears in the call
// order, or -1 if it was never called.
func indexOfTarget(targets []string, op string) int {
	for i, target := range targets {
		if strings.HasSuffix(target, "."+op) {
			return i
		}
	}
	return -1
}

func findFact(sections []DiagnosticSection, label string) *DiagnosticFact {
	for i := range sections {
		for j := range sections[i].Facts {
			if sections[i].Facts[j].Label == label {
				return &sections[i].Facts[j]
			}
		}
	}
	return nil
}

func findSectionOfKind(sections []DiagnosticSection, kind string) *DiagnosticSection {
	for i := range sections {
		if sections[i].Kind == kind {
			return &sections[i]
		}
	}
	return nil
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Sprintf("%+v", v)
	}
	return string(data)
}
