package secretsmanager

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/overcast-sh/overcast/internal/clock"
	"github.com/overcast-sh/overcast/internal/config"
	"github.com/overcast-sh/overcast/internal/events"
	"github.com/overcast-sh/overcast/internal/serviceutil"
	"github.com/overcast-sh/overcast/internal/state"
)

// ─── Fixtures ────────────────────────────────────────────────────────────────

const fixtureLambdaARN = "arn:aws:lambda:us-east-1:000000000000:function:rotate-fn"

func newRotationTestHandler(t *testing.T) (*Handler, *clock.Mock) {
	t.Helper()
	mock := clock.NewMock()
	mock.Set(time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC))
	cfg := &config.Config{Region: "us-east-1", AccountID: "000000000000"}
	h := newHandler(cfg, state.NewMemoryStore(), serviceutil.NewServiceLogger(zap.NewNop(), serviceName), mock)
	return h, mock
}

// fakeRotationFn stands in for a rotation Lambda built from AWS's blueprint: it
// runs the blueprint's staging guard before every step, records each step it is
// handed, and performs the same Secrets Manager calls the blueprint makes —
// PutSecretValue staged AWSPENDING on createSecret, and
// UpdateSecretVersionStage on finishSecret.
type fakeRotationFn struct {
	h *Handler

	steps    []string
	tokens   []string
	secretID string
	// stagesAt records what DescribeSecret reported for the rotation's own
	// token at the moment each step began, so a test can assert on what the
	// function saw rather than on the state left behind once rotation ended.
	stagesAt map[string][]string

	failOn         string // step at which the function reports an error
	unreachable    bool   // the function cannot be invoked at all
	skipFinishMove bool   // finishSecret returns without moving AWSCURRENT
}

// blueprintGuard is the boilerplate that opens every AWS-published rotation
// function. The generic SecretsManagerRotationTemplate and the RDS, MySQL,
// PostgreSQL and Redshift blueprints all run it in lambda_handler *before*
// dispatching to any step — createSecret included — so a rotation function
// built from AWS's own template asserts that Secrets Manager has already made
// the ClientRequestToken a version of the secret staged AWSPENDING by the time
// it is first called. Reproducing it here is the point of this fixture: without
// it the tests pass whether or not Overcast stages the token, which is exactly
// how the gap this guard exists to catch went unnoticed.
//
// skip mirrors the blueprint's early `return` for a token that already carries
// AWSCURRENT; a non-empty msg is the ValueError it raises, verbatim.
func (f *fakeRotationFn) blueprintGuard(ctx context.Context, arn, token string) (skip bool, msg string) {
	desc, aerr := f.h.describeSecretTyped(ctx, &secretIDRequest{SecretId: arn})
	if aerr != nil {
		return false, "DescribeSecret failed: " + aerr.Message
	}
	if !desc.RotationEnabled {
		return false, fmt.Sprintf("Secret %s is not enabled for rotation", arn)
	}
	stages, staged := desc.VersionIdsToStages[token]
	if !staged {
		return false, fmt.Sprintf("Secret version %s has no stage for rotation of secret %s.", token, arn)
	}
	if containsString(stages, stageAWSCurrent) {
		return true, ""
	}
	if !containsString(stages, stageAWSPending) {
		return false, fmt.Sprintf("Secret version %s not set as AWSPENDING for rotation of secret %s.", token, arn)
	}
	return false, ""
}

// raise is the fixture's equivalent of the blueprint raising a ValueError: an
// unhandled Lambda error carrying the message.
func (f *fakeRotationFn) raise(msg string) *events.InvokeOutcome {
	return &events.InvokeOutcome{
		FunctionError: "Unhandled",
		Payload:       []byte(`{"errorMessage":` + strconv.Quote(msg) + `}`),
	}
}

// recordStages snapshots the token's staging labels as the step starts.
func (f *fakeRotationFn) recordStages(ctx context.Context, step, arn, token string) {
	if f.stagesAt == nil {
		f.stagesAt = map[string][]string{}
	}
	desc, aerr := f.h.describeSecretTyped(ctx, &secretIDRequest{SecretId: arn})
	if aerr != nil {
		return
	}
	f.stagesAt[step] = desc.VersionIdsToStages[token]
}

func (f *fakeRotationFn) Invoke(ctx context.Context, _ string, payload []byte) (*events.InvokeOutcome, error) {
	var req rotationEvent
	if err := json.Unmarshal(payload, &req); err != nil {
		return &events.InvokeOutcome{FunctionError: "Unhandled"}, nil
	}
	f.steps = append(f.steps, req.Step)
	f.tokens = append(f.tokens, req.ClientRequestToken)
	f.secretID = req.SecretId

	if f.unreachable {
		return nil, nil
	}
	f.recordStages(ctx, req.Step, req.SecretId, req.ClientRequestToken)
	skip, msg := f.blueprintGuard(ctx, req.SecretId, req.ClientRequestToken)
	if msg != "" {
		return f.raise(msg), nil
	}
	if skip {
		return &events.InvokeOutcome{Payload: []byte("null")}, nil
	}
	if req.Step == f.failOn {
		return &events.InvokeOutcome{
			FunctionError: "Unhandled",
			Payload:       []byte(`{"errorMessage":"step failed on purpose"}`),
		}, nil
	}

	switch req.Step {
	case stepCreateSecret:
		// The blueprint checks AWSCURRENT is readable, then tries to read the
		// version it was handed. Secrets Manager stages that version before the
		// call but puts no value in it, so the read comes back
		// ResourceNotFoundException and the function generates the new value
		// and puts it under the same token — which is what makes the step
		// idempotent across a retry.
		if _, aerr := f.h.getSecretValueTyped(ctx, &getSecretValueRequest{
			SecretId: req.SecretId, VersionStage: stageAWSCurrent,
		}); aerr != nil {
			return f.raise("createSecret: AWSCURRENT is unreadable: " + aerr.Message), nil
		}
		_, aerr := f.h.getSecretValueTyped(ctx, &getSecretValueRequest{
			SecretId: req.SecretId, VersionId: req.ClientRequestToken, VersionStage: stageAWSPending,
		})
		switch {
		case aerr == nil:
			// The pending version already holds a value; leave it alone.
		case aerr.Code != "ResourceNotFoundException":
			return f.raise("createSecret: " + aerr.Message), nil
		default:
			if _, aerr := f.h.putSecretValueTyped(ctx, &putSecretValueRequest{
				SecretId:           req.SecretId,
				SecretString:       "rotated-value",
				ClientRequestToken: req.ClientRequestToken,
				VersionStages:      []string{stageAWSPending},
			}); aerr != nil {
				return f.raise("createSecret: PutSecretValue: " + aerr.Message), nil
			}
		}
	case stepFinishSecret:
		if f.skipFinishMove {
			break
		}
		desc, aerr := f.h.describeSecretTyped(ctx, &secretIDRequest{SecretId: req.SecretId})
		if aerr != nil {
			return &events.InvokeOutcome{FunctionError: "Unhandled"}, nil
		}
		current := ""
		for id, stages := range desc.VersionIdsToStages {
			for _, s := range stages {
				if s == stageAWSCurrent {
					current = id
				}
			}
		}
		if _, aerr := f.h.updateSecretVersionStageTyped(ctx, &updateSecretVersionStageRequest{
			SecretId:            req.SecretId,
			VersionStage:        stageAWSCurrent,
			MoveToVersionId:     req.ClientRequestToken,
			RemoveFromVersionId: current,
		}); aerr != nil {
			return &events.InvokeOutcome{FunctionError: "Unhandled"}, nil
		}
	}
	return &events.InvokeOutcome{Payload: []byte("null")}, nil
}

// seedSecret creates a secret with a single AWSCURRENT version.
func seedSecret(t *testing.T, h *Handler, name, value string) *Secret {
	t.Helper()
	ctx := context.Background()
	if _, aerr := h.createSecretTyped(ctx, &createSecretRequest{Name: name, SecretString: value}); aerr != nil {
		t.Fatalf("seed %s: %v", name, aerr)
	}
	sec, aerr := h.store.resolveSecret(ctx, name)
	if aerr != nil {
		t.Fatalf("resolve %s: %v", name, aerr)
	}
	return sec
}

func currentValue(t *testing.T, h *Handler, name string) string {
	t.Helper()
	out, aerr := h.getSecretValueTyped(context.Background(), &getSecretValueRequest{SecretId: name})
	if aerr != nil {
		t.Fatalf("GetSecretValue %s: %v", name, aerr)
	}
	return out.SecretString
}

// ─── The four-step protocol ──────────────────────────────────────────────────

func TestRotateSecret_runsAllFourSteps(t *testing.T) {
	// Given: a secret and a rotation function that behaves like AWS's blueprint
	h, clk := newRotationTestHandler(t)
	sec := seedSecret(t, h, "db-password", "original")
	fake := &fakeRotationFn{h: h}
	h.InitLambdaInvoker(fake)
	originalVersion := sec.CurrentVersionId

	// When: rotation is requested immediately
	ctx := context.Background()
	out, aerr := h.rotateSecretTyped(ctx, &rotateSecretRequest{
		SecretId:          "db-password",
		RotationLambdaARN: fixtureLambdaARN,
		RotationRules:     &RotationRules{AutomaticallyAfterDays: 30},
	})
	if aerr != nil {
		t.Fatalf("RotateSecret: %v", aerr)
	}

	// Then: all four steps ran, in AWS's order, under one ClientRequestToken
	want := []string{stepCreateSecret, stepSetSecret, stepTestSecret, stepFinishSecret}
	if strings.Join(fake.steps, ",") != strings.Join(want, ",") {
		t.Fatalf("steps = %v, want %v", fake.steps, want)
	}
	for i, tok := range fake.tokens {
		if tok == "" || tok != fake.tokens[0] {
			t.Fatalf("token[%d] = %q, want one non-empty token for every step (%v)", i, tok, fake.tokens)
		}
	}
	if fake.secretID != sec.ARN {
		t.Errorf("SecretId in payload = %q, want the secret ARN %q", fake.secretID, sec.ARN)
	}
	if out.VersionId != fake.tokens[0] {
		t.Errorf("RotateSecret VersionId = %q, want the new version %q", out.VersionId, fake.tokens[0])
	}

	// And: AWSCURRENT holds the rotated value
	if got := currentValue(t, h, "db-password"); got != "rotated-value" {
		t.Fatalf("AWSCURRENT value = %q, want %q", got, "rotated-value")
	}

	// And: the stages settled — new version AWSCURRENT, old AWSPREVIOUS, no AWSPENDING left
	rotated, aerr := h.store.resolveSecret(ctx, "db-password")
	if aerr != nil {
		t.Fatalf("resolve: %v", aerr)
	}
	stages := map[string][]string{}
	for _, v := range rotated.Versions {
		stages[v.VersionId] = v.Stages
	}
	if got := stages[fake.tokens[0]]; len(got) != 1 || got[0] != stageAWSCurrent {
		t.Errorf("new version stages = %v, want [%s]", got, stageAWSCurrent)
	}
	if got := stages[originalVersion]; len(got) != 1 || got[0] != stageAWSPrevious {
		t.Errorf("previous version stages = %v, want [%s]", got, stageAWSPrevious)
	}

	// And: the rotation metadata reflects reality
	if rotated.LastRotatedDate == 0 {
		t.Error("LastRotatedDate = 0, want the rotation time")
	}
	wantNext := float64(clk.Now().AddDate(0, 0, 30).Unix())
	if rotated.NextRotationDate != wantNext {
		t.Errorf("NextRotationDate = %v, want %v (30 days on)", rotated.NextRotationDate, wantNext)
	}
	if rotated.LastRotationAttempt == nil || rotated.LastRotationAttempt.Status != rotationSucceeded {
		t.Errorf("LastRotationAttempt = %+v, want a Succeeded record", rotated.LastRotationAttempt)
	}
}

func TestRotateSecret_stagesPendingVersionBeforeCreateSecret(t *testing.T) {
	// Given: a secret and a rotation function that runs AWS's blueprint guard
	h, _ := newRotationTestHandler(t)
	seedSecret(t, h, "db-password", "original")
	fake := &fakeRotationFn{h: h}
	h.InitLambdaInvoker(fake)

	// When: rotation runs under a token the test chose
	const token = "11111111-1111-1111-1111-111111111111"
	ctx := context.Background()
	if _, aerr := h.rotateSecretTyped(ctx, &rotateSecretRequest{
		SecretId:           "db-password",
		RotationLambdaARN:  fixtureLambdaARN,
		ClientRequestToken: token,
	}); aerr != nil {
		t.Fatalf("RotateSecret: %v", aerr)
	}

	// Then: by the time createSecret was invoked, the token was already a
	// version of the secret carrying AWSPENDING. Secrets Manager stages it
	// before it calls the function, and every AWS rotation blueprint refuses to
	// run a step — createSecret included — for a token that is not staged.
	got, seen := fake.stagesAt[stepCreateSecret]
	if !seen || !containsString(got, stageAWSPending) {
		t.Fatalf("stages on version %s at %s = %v, want %s to be attached already",
			token, stepCreateSecret, got, stageAWSPending)
	}

	// And: the staged version held no value until createSecret put one there,
	// which is the ResourceNotFoundException the blueprint branches on.
	if got := currentValue(t, h, "db-password"); got != "rotated-value" {
		t.Fatalf("AWSCURRENT value = %q, want %q", got, "rotated-value")
	}
}

func TestGetSecretValue_stagedVersionWithNoValueIsNotFound(t *testing.T) {
	// Given: a secret whose rotation staged a version but whose rotation
	// function never got as far as putting a value in it
	h, _ := newRotationTestHandler(t)
	sec := seedSecret(t, h, "half-rotated", "original")
	ctx := context.Background()
	const token = "22222222-2222-2222-2222-222222222222"
	if aerr := h.stagePendingRotationVersion(ctx, sec, token); aerr != nil {
		t.Fatalf("stage pending version: %v", aerr)
	}

	// When: the version is read the way a rotation function reads it
	_, aerr := h.getSecretValueTyped(ctx, &getSecretValueRequest{
		SecretId: "half-rotated", VersionId: token, VersionStage: stageAWSPending,
	})

	// Then: AWS reports it as not found — a value-less version is what
	// createSecret's "do I still have a password to generate?" test turns on
	if aerr == nil {
		t.Fatal("GetSecretValue succeeded, want ResourceNotFoundException for a version with no value")
	}
	if aerr.Code != "ResourceNotFoundException" {
		t.Errorf("error code = %q, want ResourceNotFoundException", aerr.Code)
	}

	// And: DescribeSecret still lists it, staged AWSPENDING — the version
	// exists, it just has nothing in it yet
	desc, aerr := h.describeSecretTyped(ctx, &secretIDRequest{SecretId: "half-rotated"})
	if aerr != nil {
		t.Fatalf("DescribeSecret: %v", aerr)
	}
	if stages := desc.VersionIdsToStages[token]; !containsString(stages, stageAWSPending) {
		t.Errorf("VersionIdsToStages[%s] = %v, want %s", token, stages, stageAWSPending)
	}
}

func TestPutSecretValue_fillsTheVersionRotationStaged(t *testing.T) {
	// Given: a secret with a rotation-staged, value-less AWSPENDING version
	h, _ := newRotationTestHandler(t)
	sec := seedSecret(t, h, "fill-pending", "original")
	ctx := context.Background()
	const token = "33333333-3333-3333-3333-333333333333"
	if aerr := h.stagePendingRotationVersion(ctx, sec, token); aerr != nil {
		t.Fatalf("stage pending version: %v", aerr)
	}

	// When: createSecret puts the new value under the same token, as the
	// blueprint does
	out, aerr := h.putSecretValueTyped(ctx, &putSecretValueRequest{
		SecretId:           "fill-pending",
		SecretString:       "generated",
		ClientRequestToken: token,
		VersionStages:      []string{stageAWSPending},
	})

	// Then: it fills the staged version rather than colliding with it
	if aerr != nil {
		t.Fatalf("PutSecretValue: %v", aerr)
	}
	if out.VersionId != token {
		t.Errorf("VersionId = %q, want the ClientRequestToken %q", out.VersionId, token)
	}
	got, aerr := h.getSecretValueTyped(ctx, &getSecretValueRequest{
		SecretId: "fill-pending", VersionId: token,
	})
	if aerr != nil {
		t.Fatalf("GetSecretValue: %v", aerr)
	}
	if got.SecretString != "generated" {
		t.Errorf("SecretString = %q, want %q", got.SecretString, "generated")
	}

	// And: a genuine token collision — same token, different content — is
	// still ResourceExistsException
	if _, aerr := h.putSecretValueTyped(ctx, &putSecretValueRequest{
		SecretId:           "fill-pending",
		SecretString:       "different",
		ClientRequestToken: token,
		VersionStages:      []string{stageAWSPending},
	}); aerr == nil || aerr.Code != "ResourceExistsException" {
		t.Errorf("re-put with different content = %v, want ResourceExistsException", aerr)
	}
}

func TestRotateSecret_stepFailureIsReported(t *testing.T) {
	// Given: a rotation function that fails at one of the four steps
	for _, step := range []string{stepCreateSecret, stepSetSecret, stepTestSecret, stepFinishSecret} {
		t.Run(step, func(t *testing.T) {
			h, _ := newRotationTestHandler(t)
			seedSecret(t, h, "db-password", "original")
			fake := &fakeRotationFn{h: h, failOn: step}
			h.InitLambdaInvoker(fake)

			// When: rotation runs
			ctx := context.Background()
			_, aerr := h.rotateSecretTyped(ctx, &rotateSecretRequest{
				SecretId:          "db-password",
				RotationLambdaARN: fixtureLambdaARN,
			})

			// Then: the failure surfaces, naming the step that failed
			if aerr == nil {
				t.Fatal("RotateSecret succeeded, want a failure")
			}
			if aerr.Code != "InvalidRequestException" {
				t.Errorf("error code = %q, want InvalidRequestException", aerr.Code)
			}
			if !strings.Contains(aerr.Message, step) {
				t.Errorf("error message = %q, want it to name the %q step", aerr.Message, step)
			}

			// And: it stopped there rather than running the rest
			if last := fake.steps[len(fake.steps)-1]; last != step {
				t.Errorf("last step attempted = %q, want %q", last, step)
			}

			// And: AWSCURRENT still resolves to the original value
			if got := currentValue(t, h, "db-password"); got != "original" {
				t.Errorf("AWSCURRENT value = %q, want the untouched %q", got, "original")
			}

			// And: the attempt is recorded so the failure is visible after the fact
			sec, _ := h.store.resolveSecret(ctx, "db-password")
			if sec.LastRotationAttempt == nil {
				t.Fatal("LastRotationAttempt is nil, want the failed attempt recorded")
			}
			if sec.LastRotationAttempt.Status != rotationFailed {
				t.Errorf("attempt status = %q, want %q", sec.LastRotationAttempt.Status, rotationFailed)
			}
			if sec.LastRotationAttempt.Step != step {
				t.Errorf("attempt step = %q, want %q", sec.LastRotationAttempt.Step, step)
			}
		})
	}
}

func TestRotateSecret_finishDidNotMoveCurrent(t *testing.T) {
	// Given: a rotation function whose finishSecret forgets to move AWSCURRENT
	h, _ := newRotationTestHandler(t)
	seedSecret(t, h, "db-password", "original")
	fake := &fakeRotationFn{h: h, skipFinishMove: true}
	h.InitLambdaInvoker(fake)

	// When: rotation runs
	_, aerr := h.rotateSecretTyped(context.Background(), &rotateSecretRequest{
		SecretId:          "db-password",
		RotationLambdaARN: fixtureLambdaARN,
	})

	// Then: reporting success would be a lie — the call fails instead
	if aerr == nil {
		t.Fatal("RotateSecret succeeded, want a failure when AWSCURRENT never moved")
	}
	if !strings.Contains(aerr.Message, stepFinishSecret) {
		t.Errorf("error message = %q, want it to name %q", aerr.Message, stepFinishSecret)
	}
	if got := currentValue(t, h, "db-password"); got != "original" {
		t.Errorf("AWSCURRENT value = %q, want %q", got, "original")
	}
}

func TestRotateSecret_functionNotInvokable(t *testing.T) {
	// Given: a rotation function ARN that resolves to nothing
	h, _ := newRotationTestHandler(t)
	seedSecret(t, h, "db-password", "original")
	fake := &fakeRotationFn{h: h, unreachable: true}
	h.InitLambdaInvoker(fake)

	// When: rotation runs
	_, aerr := h.rotateSecretTyped(context.Background(), &rotateSecretRequest{
		SecretId:          "db-password",
		RotationLambdaARN: fixtureLambdaARN,
	})

	// Then: the caller is told, rather than getting a 200 that rotated nothing
	if aerr == nil {
		t.Fatal("RotateSecret succeeded, want a failure when the function cannot be invoked")
	}
	if aerr.Code != "InvalidRequestException" {
		t.Errorf("error code = %q, want InvalidRequestException", aerr.Code)
	}
}

func TestRotateSecret_scheduleOnlyDoesNotInvoke(t *testing.T) {
	// Given: a secret and a rotation function
	h, clk := newRotationTestHandler(t)
	seedSecret(t, h, "db-password", "original")
	fake := &fakeRotationFn{h: h}
	h.InitLambdaInvoker(fake)

	// When: rotation is configured with RotateImmediately=false
	no := false
	ctx := context.Background()
	if _, aerr := h.rotateSecretTyped(ctx, &rotateSecretRequest{
		SecretId:          "db-password",
		RotationLambdaARN: fixtureLambdaARN,
		RotationRules:     &RotationRules{AutomaticallyAfterDays: 10},
		RotateImmediately: &no,
	}); aerr != nil {
		t.Fatalf("RotateSecret: %v", aerr)
	}

	// Then: nothing was invoked, but the schedule is recorded
	if len(fake.steps) != 0 {
		t.Fatalf("steps = %v, want none", fake.steps)
	}
	sec, _ := h.store.resolveSecret(ctx, "db-password")
	if !sec.RotationEnabled {
		t.Error("RotationEnabled = false, want true")
	}
	if want := float64(clk.Now().AddDate(0, 0, 10).Unix()); sec.NextRotationDate != want {
		t.Errorf("NextRotationDate = %v, want %v", sec.NextRotationDate, want)
	}
	if sec.LastRotatedDate != 0 {
		t.Errorf("LastRotatedDate = %v, want 0", sec.LastRotatedDate)
	}
}

// ─── Scheduled rotation ──────────────────────────────────────────────────────

func TestRotateDueSecrets_onlyRotatesWhatIsDue(t *testing.T) {
	// Given: two scheduled secrets, one due and one not
	h, clk := newRotationTestHandler(t)
	seedSecret(t, h, "due", "original")
	seedSecret(t, h, "not-due", "original")
	fake := &fakeRotationFn{h: h}
	h.InitLambdaInvoker(fake)
	no := false
	ctx := context.Background()
	for _, name := range []string{"due", "not-due"} {
		if _, aerr := h.rotateSecretTyped(ctx, &rotateSecretRequest{
			SecretId:          name,
			RotationLambdaARN: fixtureLambdaARN,
			RotationRules:     &RotationRules{AutomaticallyAfterDays: 1},
			RotateImmediately: &no,
		}); aerr != nil {
			t.Fatalf("RotateSecret %s: %v", name, aerr)
		}
	}
	// Push "not-due" out to a week away.
	notDue, _ := h.store.resolveSecret(ctx, "not-due")
	notDue.NextRotationDate = float64(clk.Now().AddDate(0, 0, 7).Unix())
	if aerr := h.store.putSecret(ctx, notDue); aerr != nil {
		t.Fatalf("putSecret: %v", aerr)
	}

	// When: the clock passes the first secret's next rotation and the sweep runs
	clk.Add(25 * time.Hour)
	rotated := h.rotateDueSecrets(ctx)

	// Then: exactly the due secret rotated
	if rotated != 1 {
		t.Fatalf("rotated = %d, want 1", rotated)
	}
	if got := currentValue(t, h, "due"); got != "rotated-value" {
		t.Errorf("due AWSCURRENT = %q, want %q", got, "rotated-value")
	}
	if got := currentValue(t, h, "not-due"); got != "original" {
		t.Errorf("not-due AWSCURRENT = %q, want %q", got, "original")
	}

	// And: the rotated secret's schedule moved forward, so it does not re-fire
	after, _ := h.store.resolveSecret(ctx, "due")
	if want := float64(clk.Now().AddDate(0, 0, 1).Unix()); after.NextRotationDate != want {
		t.Errorf("NextRotationDate = %v, want %v", after.NextRotationDate, want)
	}
	if again := h.rotateDueSecrets(ctx); again != 0 {
		t.Errorf("second sweep rotated %d, want 0", again)
	}
}

func TestRotateDueSecrets_skipsCancelledRotation(t *testing.T) {
	// Given: a scheduled secret whose rotation was then cancelled
	h, clk := newRotationTestHandler(t)
	seedSecret(t, h, "cancelled", "original")
	fake := &fakeRotationFn{h: h}
	h.InitLambdaInvoker(fake)
	no := false
	ctx := context.Background()
	if _, aerr := h.rotateSecretTyped(ctx, &rotateSecretRequest{
		SecretId:          "cancelled",
		RotationLambdaARN: fixtureLambdaARN,
		RotationRules:     &RotationRules{AutomaticallyAfterDays: 1},
		RotateImmediately: &no,
	}); aerr != nil {
		t.Fatalf("RotateSecret: %v", aerr)
	}
	if _, aerr := h.cancelRotateSecretTyped(ctx, &secretIDRequest{SecretId: "cancelled"}); aerr != nil {
		t.Fatalf("CancelRotateSecret: %v", aerr)
	}

	// When: time passes well beyond what would have been the next rotation
	clk.Add(72 * time.Hour)

	// Then: nothing rotates
	if rotated := h.rotateDueSecrets(ctx); rotated != 0 {
		t.Fatalf("rotated = %d, want 0", rotated)
	}
	if got := currentValue(t, h, "cancelled"); got != "original" {
		t.Errorf("AWSCURRENT = %q, want %q", got, "original")
	}
}

func TestRotateDueSecrets_failedRotationWaitsForTheNextWindow(t *testing.T) {
	// Given: a due secret whose rotation function always fails
	h, clk := newRotationTestHandler(t)
	seedSecret(t, h, "broken", "original")
	fake := &fakeRotationFn{h: h, failOn: stepCreateSecret}
	h.InitLambdaInvoker(fake)
	no := false
	ctx := context.Background()
	if _, aerr := h.rotateSecretTyped(ctx, &rotateSecretRequest{
		SecretId:          "broken",
		RotationLambdaARN: fixtureLambdaARN,
		RotationRules:     &RotationRules{AutomaticallyAfterDays: 1},
		RotateImmediately: &no,
	}); aerr != nil {
		t.Fatalf("RotateSecret: %v", aerr)
	}

	// When: the schedule comes due and the sweep runs
	clk.Add(25 * time.Hour)
	if rotated := h.rotateDueSecrets(ctx); rotated != 0 {
		t.Fatalf("rotated = %d, want 0", rotated)
	}

	// Then: the secret is not left permanently overdue — which is what would
	// spin the engine — and the failure is on the record
	sec, _ := h.store.resolveSecret(ctx, "broken")
	if want := float64(clk.Now().AddDate(0, 0, 1).Unix()); sec.NextRotationDate != want {
		t.Errorf("NextRotationDate = %v, want %v (the next window)", sec.NextRotationDate, want)
	}
	if sec.LastRotationAttempt == nil || sec.LastRotationAttempt.Status != rotationFailed {
		t.Errorf("LastRotationAttempt = %+v, want a Failed record", sec.LastRotationAttempt)
	}
	if again := h.rotateDueSecrets(ctx); again != 0 {
		t.Errorf("second sweep rotated %d, want 0 — the schedule should have moved on", again)
	}
}

func TestRotationEngine_wakesAndRotatesDueSecret(t *testing.T) {
	// Given: a running engine and a secret whose rotation is already due
	cfg := &config.Config{Region: "us-east-1", AccountID: "000000000000"}
	clk := clock.NewMock()
	clk.Set(time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC))
	svc := New(cfg, state.NewMemoryStore(), zap.NewNop(), clk)
	h := svc.handler
	svc.InitLambdaInvoker(&fakeRotationFn{h: h})

	seedSecret(t, h, "engine-due", "original")
	no := false
	ctx := context.Background()
	if _, aerr := h.rotateSecretTyped(ctx, &rotateSecretRequest{
		SecretId:          "engine-due",
		RotationLambdaARN: fixtureLambdaARN,
		RotationRules:     &RotationRules{AutomaticallyAfterDays: 1},
		RotateImmediately: &no,
	}); aerr != nil {
		t.Fatalf("RotateSecret: %v", aerr)
	}
	clk.Add(25 * time.Hour)

	// When: the engine starts and is woken
	svc.startRotationEngine()
	t.Cleanup(func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		svc.Stop(stopCtx)
	})
	h.wakeRotationEngine()

	// Then: the due secret rotates without the test advancing the clock again.
	// The wait here is a synchronisation barrier for the engine goroutine, not
	// a time dependency — the schedule is already overdue.
	deadline := time.Now().Add(5 * time.Second)
	for {
		if currentValue(t, h, "engine-due") == "rotated-value" {
			return
		}
		if time.Now().After(deadline) {
			// fake.steps is written on the engine goroutine, so it is not read
			// here — the store is the safe place to look.
			t.Fatal("engine did not rotate the due secret within the deadline")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestRotationEngine_stopDrains(t *testing.T) {
	// Given: a service whose rotation engine is running
	cfg := &config.Config{Region: "us-east-1", AccountID: "000000000000"}
	svc := New(cfg, state.NewMemoryStore(), zap.NewNop(), clock.NewMock())
	svc.startRotationEngine()

	// When: the service is stopped
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	svc.Stop(ctx)

	// Then: the goroutine is gone and a second Stop is harmless
	svc.Stop(ctx)
	if ctx.Err() != nil {
		t.Fatalf("Stop did not return before the deadline: %v", ctx.Err())
	}
}
