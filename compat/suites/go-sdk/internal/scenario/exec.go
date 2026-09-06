package scenario

import (
	"context"
	"errors"
	"fmt"

	"github.com/overcast-sh/overcast-compat-go-sdk/internal/harness"
)

// Running a generated group (compat/model/README.md § The scenario file).
//
// A group is setup → tests → teardown. Setup runs every step in order and a
// failure reports every test in the group as skip with "setup failed: <the six
// fields>" — which harness.RunGroup does for us, from the error returned here.
// Teardown runs afterwards **even when setup failed**, with every step wrapped
// individually: a setup that failed on its third step has already created what
// its first two made, and no test will run to remove it.

// A Group is one generated registry group. The emitted file declares one per
// group and hangs its setup, tests and teardown off it, so the group name and
// the scenario file — failure-message fields 1 and 6 — are written once.
type Group struct {
	// Name is the registry group name (sqs-gen-queue).
	Name string
	// File is the scenario file this group was generated from, repository
	// relative: failure-message field 6's first half.
	File string
}

// bagKey is where a group's context bag lives on the harness TestContext. The
// harness creates one TestContext per group run and hands the same one to
// setup, every test and teardown, so the bag has exactly the lifetime the IR
// gives a group's context.
const bagKey = "scenario_context"

// bagFor returns the group's context bag, creating it on first use. The
// create-if-absent is atomic because a parallel group's tests share one
// TestContext and reach this concurrently — see harness.TestContext.LoadOrStore.
func bagFor(t *harness.TestContext) *contextBag {
	v := t.LoadOrStore(bagKey, func() any { return newContextBag() })
	if c, ok := v.(*contextBag); ok {
		return c
	}
	// Something else claimed the key. Hand back a private bag rather than
	// panicking: a probe group never reads it, and a lifecycle group would
	// fail loudly on the first unresolvable reference.
	return newContextBag()
}

// RunTest runs one generated test: the primary call, then every clause in
// order.
func (g Group) RunTest(ctx context.Context, t *harness.TestContext, name string, tc Test) error {
	e := g.newExecution(t, name)
	wantErr := errorCodeClause(tc.Assert)

	obs, sdkErr, err := e.callRaw(ctx, &tc.Call, "call")
	if err != nil {
		return err
	}
	switch {
	case wantErr != nil:
		// A test carrying an errorCode clause expects its primary call to
		// fail; the generator refuses such a test any clause that would read
		// the primary response, so every other clause makes a call of its own.
		if sdkErr == nil {
			return e.fail(obs, "call", KindErrorCode, "", acceptedCodes(wantErr), "<no error>")
		}
		if !matchesError(sdkErr, wantErr) {
			return e.fail(obs, "call", KindErrorCode, "", acceptedCodes(wantErr), quote(sdkErr.Error()))
		}
	case sdkErr != nil:
		return e.failedCall(obs, "call", sdkErr)
	default:
		if err := e.applyExports(&tc.Call, obs, "call"); err != nil {
			return err
		}
	}

	for i := range tc.Assert {
		a := &tc.Assert[i]
		if a.Kind == KindErrorCode {
			continue // already checked against the primary call
		}
		if err := e.assert(ctx, a, obs, fmt.Sprintf("assert[%d]", i)); err != nil {
			return err
		}
	}
	return nil
}

// RunSetup runs a group's setup steps in order, stopping at the first failure.
//
// The failure is returned to the harness, which reports every test in the
// group as skip with "setup failed: <message>" and still runs teardown. An
// empty list is a no-op, not a missing phase: a probe group has nothing to set
// up and still registers the hook, so "a probe creates nothing" is visible in
// the emitted source rather than being a convention to remember.
func (g Group) RunSetup(ctx context.Context, t *harness.TestContext, calls ...Call) error {
	e := g.newExecution(t, "setup")
	for i := range calls {
		if _, err := e.invoke(ctx, &calls[i], fmt.Sprintf("setup[%d]", i)); err != nil {
			return err
		}
	}
	return nil
}

// RunTeardown runs a group's teardown steps, each wrapped individually: an
// error, or an unresolvable ref, skips that step and the rest still run. Each
// skip is logged to stderr — which the Go runner multiplexes into its own log
// — and none of them fails the group, which is this suite's existing teardown
// convention and compat/AGENTS.md's "teardown must not throw".
//
// Returning an error instead would report a teardown failure on every clean
// run of a lifecycle group: the delete test has already removed the resource
// the teardown step names, so a "not found" there is the expected outcome, not
// a leak. Proof that nothing leaked is the orphan sweep — a {runId} search
// after the run — not the teardown's own exit status.
func (g Group) RunTeardown(ctx context.Context, t *harness.TestContext, calls ...Call) error {
	e := g.newExecution(t, "teardown")
	for i := range calls {
		step := fmt.Sprintf("teardown[%d]", i)
		if _, err := e.invoke(ctx, &calls[i], step); err != nil {
			t.Log(fmt.Sprintf("%s: skipped %s: %v", g.Name, step, err))
		}
	}
	return nil
}

// execution is one group-scoped run of one test, setup or teardown.
type execution struct {
	group Group
	tc    *harness.TestContext
	bag   *contextBag
	// test is failure-message field 1's second half: the test name, or
	// "setup"/"teardown" for a group hook.
	test string
}

func (g Group) newExecution(t *harness.TestContext, test string) *execution {
	return &execution{group: g, tc: t, bag: bagFor(t), test: test}
}

// binder returns a fresh Binder for one call, so a value that failed to bind
// in one call cannot suppress the next one's assignments.
func (e *execution) binder() *Binder {
	return &Binder{runID: e.tc.RunID, group: e.group.Name, bag: e.bag}
}

// callRaw builds a call's input and sends it, keeping the SDK's own error
// separate from this package's.
//
// sdkErr is the SDK's error, unwrapped, for the two clauses that must inspect
// it (errorCode, and absent's error form). err is everything attributable to
// the scenario before anything was sent — an unresolvable ref, a value that
// does not fit the input field — and is already a *failure.
//
// The returned observed carries the exact params JSON sent, so every failure
// downstream of it quotes what went on the wire.
func (e *execution) callRaw(ctx context.Context, c *Call, step string) (obs observed, sdkErr, err error) {
	obs = observed{op: c.Op}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return obs, nil, fmt.Errorf("%s/%s: %s: %w", e.group.Name, e.test, c.Op, ctxErr)
	}

	b := e.binder()
	in := c.Build(b)
	if b.err != nil {
		// Nothing was sent, so field 3 shows the params as the scenario file
		// writes them rather than a half-built input that never existed.
		obs.params = c.Params
		var ref *refError
		if errors.As(b.err, &ref) {
			return obs, nil, e.fail(obs, step, "params", ref.path, "the context path to be set", "<unset>")
		}
		return obs, nil, e.fail(obs, step, "params", b.member, "a value the input member accepts", quote(b.err.Error()))
	}

	sent, encErr := renderParams(in)
	if encErr != nil {
		obs.params = c.Params
		return obs, nil, e.fail(obs, step, "params", "", "params that encode as JSON", quote(encErr.Error()))
	}
	obs.params = sent

	out, runErr := c.Send(ctx, in)
	if runErr != nil {
		return obs, runErr, nil
	}
	body, _ := toDocument(out)
	obs.body, obs.ok = body, true
	return obs, nil, nil
}

// renderParams is failure-message field 3: the built input struct as the JSON
// it will be serialized from. It is the document form rather than the wire
// body, which is what makes it comparable with the same field in the three
// interpreters' messages — they print the params document too.
func renderParams(in any) (string, error) {
	doc, ok := toDocument(in)
	if !ok {
		doc = map[string]any{}
	}
	return canonicalJSON(doc)
}

// call is callRaw with the SDK's error turned into a failure — what every
// clause that simply needs the call to succeed wants.
func (e *execution) call(ctx context.Context, c *Call, step string) (observed, error) {
	obs, sdkErr, err := e.callRaw(ctx, c, step)
	if err != nil {
		return obs, err
	}
	if sdkErr != nil {
		return obs, e.failedCall(obs, step, sdkErr)
	}
	return obs, nil
}

// invoke is call plus its exports, for a setup or teardown step, whose whole
// purpose is the context values it leaves behind.
func (e *execution) invoke(ctx context.Context, c *Call, step string) (observed, error) {
	obs, err := e.call(ctx, c, step)
	if err != nil {
		return obs, err
	}
	return obs, e.applyExports(c, obs, step)
}

// applyExports writes a call's response paths into the context bag. An export
// path that does not resolve is an error for the step that carries it: the
// value a later step will reference is not there, and failing here names the
// path instead of failing later with an unresolvable reference.
func (e *execution) applyExports(c *Call, obs observed, step string) error {
	for _, path := range sortedKeys(c.Export) {
		respPath := c.Export[path]
		v, ok, err := resolvePath(obs.body, respPath)
		if err != nil {
			return e.fail(obs, step, "export", respPath, "a well-formed response path", quote(err.Error()))
		}
		if !ok {
			return e.fail(obs, step, "export", respPath, fmt.Sprintf("a value to export into %q", path), missingValue)
		}
		e.bag.set(path, v)
	}
	return nil
}
