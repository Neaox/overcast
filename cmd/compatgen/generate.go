//go:build dev

package main

import (
	"fmt"
	"sort"
	"strings"

	"github.com/overcast-sh/overcast/internal/awsapi"
)

// The emitter — docs/plans/compat-coverage-modelgen.md §3.3–§3.5.
//
// One recipe plus one shape snapshot plus the capability table become one
// scenario file: a lifecycle group per resource (create → read/list → update
// → tag → authored → delete, with setup and teardown for what it requires)
// and one probe group for the operations the emulator does not implement.
// Anything that cannot be expressed is refused into gaps, never guessed.
//
// Two classes of problem are kept apart on purpose. A recipe or values entry
// that contradicts the model (an unknown member, a literal of the wrong kind,
// a path that resolves to nothing) is an error: the curated file is wrong and
// generation stops. A recipe that simply does not cover an operation is a
// refusal: the operation is recorded in gaps.json and generation continues.

// Tag literals used by every generated tag/untag test. Chosen to satisfy
// every AWS tag key/value pattern seen so far (letters only).
const (
	compatTagKey   = "compat"
	compatTagValue = "scenario"
)

// generation is the result of generating one service.
type generation struct {
	scenario *scenario
	gaps     []gap
	auto     []autoBinding
	uses     []valueUse
	// covered maps an operation to the group/test names that exercise it as
	// the primary call.
	covered map[string][]string
	// folded records reads and lists that did not get a test of their own
	// because the group already had one for the operation.
	folded []string
	// noTeardown lists resources that create something and declare no delete.
	noTeardown []string
	caps       capabilityTable
	model      *serviceModel
	recipe     recipe
}

type generator struct {
	model   *serviceModel
	recipe  recipe
	service string
	caps    capabilityTable
	binder  *binder
	out     *generation
}

// generate builds one service's scenario. client is the §7.3 naming header,
// looked up by the caller (clientInfoFor) so a test can supply one for a
// fixture service the routing manifest does not know.
func generate(model *serviceModel, r recipe, values *valuesTable, caps capabilityTable, client clientInfo) (*generation, error) {
	g := &generator{
		model:   model,
		recipe:  r,
		service: r.Service,
		caps:    caps,
		binder:  &binder{model: model, service: r.Service, values: values},
		out: &generation{
			scenario: &scenario{Version: scenarioVersion, Service: r.Service, Client: client, Groups: []group{}},
			covered:  make(map[string][]string),
			caps:     caps,
			model:    model,
			recipe:   r,
		},
	}
	if err := g.checkRecipeAgainstModel(); err != nil {
		return nil, err
	}
	for _, res := range r.Resources {
		if res.SetupOnly {
			continue
		}
		if err := g.lifecycleGroup(res); err != nil {
			return nil, err
		}
	}
	if err := g.probeGroup(); err != nil {
		return nil, err
	}
	g.uncoveredImplemented()
	g.out.auto = g.binder.auto
	g.out.uses = g.binder.uses
	sortGaps(g.out.gaps)
	if err := validateScenario(g.out.scenario); err != nil {
		return nil, fmt.Errorf("generated scenario for %s is malformed: %w", r.Service, err)
	}
	return g.out, nil
}

// clientInfoFor assembles the §7.3 naming header from the manifest (the
// router's own view of the service) and the snapshot's service traits.
func clientInfoFor(model *serviceModel, service string) (clientInfo, error) {
	ops := model.Operations()
	if len(ops) == 0 {
		return clientInfo{}, fmt.Errorf("%s models no operations", service)
	}
	entries := awsapi.Operations(service, ops[0])
	if len(entries) == 0 {
		return clientInfo{}, fmt.Errorf("the routing manifest has no entry for %s/%s; is the capability key right?", service, ops[0])
	}
	op := entries[0]
	return clientInfo{
		SDKID:          op.SDKID,
		EndpointPrefix: model.EndpointPrefix,
		SigningName:    model.SigningName,
		Protocol:       string(op.Protocol),
		APIVersion:     op.APIVersion,
		TargetPrefix:   strings.TrimSuffix(op.TargetPrefix, "."),
	}, nil
}

// checkRecipeAgainstModel verifies every operation the recipe names exists
// and every read/list path resolves, before any group is built, so a typo is
// reported once with its location rather than as a refusal in three groups.
func (g *generator) checkRecipeAgainstModel() error {
	for _, res := range g.recipe.Resources {
		ops := res.setupOps()
		for _, rd := range res.allReads() {
			ops = append(ops, rd.Op)
		}
		if res.List != nil {
			ops = append(ops, res.List.Op)
		}
		for _, m := range res.Mutable {
			ops = append(ops, m.Op)
		}
		if res.Tags != nil {
			ops = append(ops, res.Tags.Tag.Op, res.Tags.Untag.Op, res.Tags.List.Op)
		}
		if res.Delete != nil {
			ops = append(ops, res.Delete.Op)
		}
		for _, a := range res.Operations {
			ops = append(ops, a.Op)
		}
		for _, op := range ops {
			if !g.model.HasOperation(op) {
				return fmt.Errorf("resource %q names operation %q, which %s does not model", res.ID, op, g.recipe.modelService())
			}
		}
		if res.NotFound != nil && !g.model.IsErrorShape(res.NotFound.Error) {
			return fmt.Errorf("resource %q: notFound error %q is not an error shape in the model", res.ID, res.NotFound.Error)
		}
	}
	return nil
}

// groupName follows §3.3: <service>-gen-<resource>, <service>-gen-probe.
func (g *generator) groupName(suffix string) string {
	return g.service + "-gen-" + suffix
}

func (g *generator) refuseOp(groupName, op string, r *refusal) {
	g.out.gaps = append(g.out.gaps, gap{Service: g.service, Operation: op, Group: groupName, Reason: r.Reason, Detail: r.Detail})
}

// ---------------------------------------------------------------------------
// Group builder
// ---------------------------------------------------------------------------

type groupBuilder struct {
	g         *generator
	group     group
	scope     []resource
	exports   exportKinds
	producers map[string]string
	names     map[string]string
	// owner is the resource whose call is being bound; its binds take
	// precedence over every other in-scope resource's. nil for probes.
	owner *resource
}

func (g *generator) newGroupBuilder(name, kind string, scope []resource) *groupBuilder {
	return &groupBuilder{
		g:         g,
		group:     group{Name: name, Kind: kind, Setup: []call{}, Tests: []test{}, Teardown: []call{}},
		scope:     scope,
		exports:   make(exportKinds),
		producers: make(map[string]string),
		names:     make(map[string]string),
	}
}

func (gb *groupBuilder) bindScope() bindScope {
	if gb.owner == nil {
		return bindScope{resources: gb.scope, exports: gb.exports}
	}
	resources := []resource{*gb.owner}
	for _, res := range gb.scope {
		if res.ID != gb.owner.ID {
			resources = append(resources, res)
		}
	}
	return bindScope{resources: resources, exports: gb.exports}
}

// forResource makes res the owner of the calls bound next.
func (gb *groupBuilder) forResource(res resource) {
	owner := res
	gb.owner = &owner
}

// bindCall binds a recipe call for this group.
func (gb *groupBuilder) bindCall(op string, explicit map[string]any) (call, *refusal, error) {
	params, ref, err := gb.g.binder.bind(gb.group.Name, op, explicit, gb.bindScope())
	if err != nil || ref != nil {
		return call{}, ref, err
	}
	return call{Op: op, Params: params}, nil, nil
}

// registerExports resolves each export path against the operation's output
// and records the context path, its kind and its producer.
func (gb *groupBuilder) registerExports(c *call, exports map[string]string, producer string) error {
	if len(exports) == 0 {
		return nil
	}
	output := gb.g.model.OutputShape(c.Op)
	if output == "" {
		return fmt.Errorf("%s returns nothing, so it cannot export %s", c.Op, sortedStringKeys(exports))
	}
	if c.Export == nil {
		c.Export = make(map[string]string, len(exports))
	}
	for _, ctx := range sortedStringKeys(exports) {
		path := mustPath(exports[ctx])
		target, err := gb.g.model.ResolvePath(output, path)
		if err != nil {
			return fmt.Errorf("%s export %s: %w", c.Op, ctx, err)
		}
		c.Export[ctx] = exports[ctx]
		gb.exports[ctx] = gb.g.model.Kind(target)
		gb.producers[ctx] = producer
	}
	return nil
}

// checkNames refuses two resources naming themselves with the same suffix
// inside one group, which would be one AWS resource pretending to be two.
func (gb *groupBuilder) checkNames(res resource, params map[string]any) error {
	for _, suffix := range namesIn(params) {
		if owner, taken := gb.names[suffix]; taken && owner != res.ID {
			return fmt.Errorf("group %s: resources %q and %q both use $name %q", gb.group.Name, owner, res.ID, suffix)
		}
		gb.names[suffix] = res.ID
	}
	return nil
}

// instantiate emits a resource's create and derived calls into setup — or,
// for a pre-existing resource, its read, so its exports are in scope.
func (gb *groupBuilder) instantiate(res resource) (*refusal, error) {
	gb.forResource(res)
	if res.Create == nil {
		rd := res.Read
		c, ref, err := gb.bindCall(rd.Op, rd.Params)
		if err != nil || ref != nil {
			return ref, err
		}
		if err := gb.registerExports(&c, prefixed(res.ID, rd.Exports), ""); err != nil {
			return nil, err
		}
		gb.group.Setup = append(gb.group.Setup, c)
		return nil, nil
	}
	params := cloneValue(res.Create.Params).(map[string]any)
	if err := applyMutableFrom(params, res); err != nil {
		return nil, fmt.Errorf("resource %q: %w", res.ID, err)
	}
	if err := gb.checkNames(res, params); err != nil {
		return nil, err
	}
	c, ref, err := gb.bindCall(res.Create.Op, params)
	if err != nil || ref != nil {
		return ref, err
	}
	if err := gb.registerExports(&c, prefixed(res.ID, res.Exports), ""); err != nil {
		return nil, err
	}
	gb.group.Setup = append(gb.group.Setup, c)
	for _, d := range res.Derived {
		dc, ref, err := gb.bindCall(d.Op, d.Params)
		if err != nil || ref != nil {
			return ref, err
		}
		if err := gb.registerExports(&dc, map[string]string{res.ID + "." + d.Export: d.Path}, ""); err != nil {
			return nil, err
		}
		gb.group.Setup = append(gb.group.Setup, dc)
	}
	return nil, nil
}

// teardown emits delete calls for the given resources, in the order given.
func (gb *groupBuilder) teardown(resources []resource) error {
	for _, res := range resources {
		if res.Delete == nil {
			continue
		}
		gb.forResource(res)
		c, ref, err := gb.bindCall(res.Delete.Op, res.Delete.Params)
		if err != nil {
			return err
		}
		if ref != nil {
			return fmt.Errorf("group %s: teardown of %q cannot be bound: %s", gb.group.Name, res.ID, ref.Detail)
		}
		gb.group.Teardown = append(gb.group.Teardown, c)
	}
	return nil
}

// addTest appends a test, computing its dependencies from the exports it
// consumes and recording coverage.
func (gb *groupBuilder) addTest(t test) error {
	if _, taken := gb.hasTest(t.Name); taken {
		return fmt.Errorf("group %s: test %q would be declared twice", gb.group.Name, t.Name)
	}
	depends := make(map[string]struct{})
	for _, ref := range refsInTest(t) {
		producer, known := gb.producers[ref]
		if !known {
			return fmt.Errorf("group %s: test %s refers to %s, which nothing exports before it", gb.group.Name, t.Name, ref)
		}
		if producer != "" && producer != t.Name {
			depends[producer] = struct{}{}
		}
	}
	t.Depends = sortedSet(depends)
	if len(t.Depends) == 0 {
		t.Depends = nil
	}
	gb.group.Tests = append(gb.group.Tests, t)
	key := gb.group.Name + "/" + t.Name
	gb.g.out.covered[t.Op] = append(gb.g.out.covered[t.Op], key)
	return nil
}

func (gb *groupBuilder) hasTest(name string) (test, bool) {
	for _, t := range gb.group.Tests {
		if t.Name == name {
			return t, true
		}
	}
	return test{}, false
}

// hasTestForOp reports whether an operation is already some test's primary
// call in this group.
func (gb *groupBuilder) hasTestForOp(op string) bool {
	for _, t := range gb.group.Tests {
		if t.Op == op {
			return true
		}
	}
	return false
}

// refsInTest collects every $ref a test consumes, in its call and assertions.
func refsInTest(t test) []string {
	set := make(map[string]struct{})
	add := func(v any) {
		for _, ref := range refsIn(v) {
			set[ref] = struct{}{}
		}
	}
	add(t.Call.Params)
	var walk func(a assertion)
	walk = func(a assertion) {
		if a.Call != nil {
			add(a.Call.Params)
		}
		for _, c := range a.Checks {
			if c.Equals != nil {
				add(c.Equals)
			}
		}
		for _, v := range a.Where {
			add(v)
		}
		if a.Assert != nil {
			walk(*a.Assert)
		}
	}
	for _, a := range t.Assert {
		walk(a)
	}
	return sortedSet(set)
}

// wrap applies the resource's async retry to a read-back style clause.
func wrap(a assertion, res resource) assertion {
	if res.Async == nil {
		return a
	}
	return eventually(a, *res.Async)
}

// ---------------------------------------------------------------------------
// Assertion completion (authored clauses and derived ones alike)
// ---------------------------------------------------------------------------

// completeAssertion binds the clause's call (if any), resolves every path it
// names against the model, and fills in error codes. ownOutput is the shape
// of the test's own response, for clauses without a call.
func (gb *groupBuilder) completeAssertion(a *assertion, ownOp, producer string) (*refusal, error) {
	output := ""
	if ownOp != "" {
		output = gb.g.model.OutputShape(ownOp)
	}
	if a.Call != nil {
		c, ref, err := gb.bindCall(a.Call.Op, a.Call.Params)
		if err != nil || ref != nil {
			return ref, err
		}
		if err := gb.registerExports(&c, a.Call.Export, producer); err != nil {
			return nil, err
		}
		*a.Call = c
		output = gb.g.model.OutputShape(c.Op)
	}
	switch a.Kind {
	case assertResponseField, assertReadback:
		for _, path := range sortedCheckPaths(a.Checks) {
			if err := gb.checkCheck(output, path, a.Checks[path]); err != nil {
				return nil, err
			}
		}
	case assertListContains, assertAbsent:
		if a.Error != nil {
			if err := gb.fillError(a.Call.Op, a.Error); err != nil {
				return nil, err
			}
			break
		}
		if output == "" {
			subject := ownOp
			if a.Call != nil {
				subject = a.Call.Op
			}
			return nil, fmt.Errorf("%s returns nothing, so there is no %s to search", subject, a.ItemsPath)
		}
		items, err := gb.g.model.ResolvePath(output, mustPath(a.ItemsPath))
		if err != nil {
			return nil, err
		}
		if gb.g.model.Kind(items) != "list" {
			return nil, fmt.Errorf("%s resolves to %s, which is not a list", a.ItemsPath, items)
		}
		item := gb.g.model.Shapes[items].Member
		for _, path := range sortedValueKeys(a.Where) {
			target, err := gb.g.model.ResolvePath(item, mustPath(path))
			if err != nil {
				return nil, fmt.Errorf("where %w", err)
			}
			if err := gb.g.binder.checkValue(a.Where[path], target, gb.exports, "where "+path, gb.group.Name); err != nil {
				return nil, err
			}
		}
	case assertErrorCode:
		if err := gb.fillError(ownOp, a.Error); err != nil {
			return nil, err
		}
	case assertEventually:
		return gb.completeAssertion(a.Assert, ownOp, producer)
	}
	return nil, nil
}

func (gb *groupBuilder) checkCheck(output, path string, c check) error {
	if output == "" {
		return fmt.Errorf("check on %s: the operation returns nothing", path)
	}
	target, err := gb.g.model.ResolvePath(output, mustPath(path))
	if err != nil {
		return err
	}
	if c.Equals != nil {
		return gb.g.binder.checkValue(c.Equals, target, gb.exports, "check "+path, gb.group.Name)
	}
	return nil
}

// fillError resolves an error spec: the shape must be one the operation
// declares, and the code is what the model says the wire carries.
func (gb *groupBuilder) fillError(op string, spec *errorSpec) error {
	declared := gb.g.model.OperationErrors(op)
	if i := sort.SearchStrings(declared, spec.Shape); i >= len(declared) || declared[i] != spec.Shape {
		return fmt.Errorf("%s does not declare error %s (it declares %s)", op, spec.Shape, strings.Join(declared, ", "))
	}
	spec.Code = gb.g.model.ErrorCode(spec.Shape)
	return nil
}

// ---------------------------------------------------------------------------
// Lifecycle groups
// ---------------------------------------------------------------------------

func (g *generator) lifecycleGroup(res resource) error {
	name := g.groupName(res.ID)
	closure := g.recipe.closure(res.ID)
	// Nearest first for binding: the resource itself, then what it requires,
	// most immediate first.
	scope := make([]resource, 0, len(closure))
	for i := len(closure) - 1; i >= 0; i-- {
		scope = append(scope, closure[i])
	}
	gb := g.newGroupBuilder(name, groupLifecycle, scope)
	for _, required := range closure[:len(closure)-1] {
		ref, err := gb.instantiate(required)
		if err != nil {
			return err
		}
		if ref != nil {
			g.refuseOp(name, res.primaryOp(), refuse(reasonSetupRefused+":"+required.ID,
				fmt.Sprintf("required resource %q cannot be created: %s", required.ID, ref.Detail)))
			return nil
		}
	}

	created := res.Create == nil
	if !created {
		if err := gb.createTest(res); err != nil {
			return err
		}
		created = gb.hasTestForOp(res.Create.Op)
	}
	if created {
		for _, rd := range res.allReads() {
			if err := gb.readTest(res, rd); err != nil {
				return err
			}
		}
		if err := gb.mutableTests(res); err != nil {
			return err
		}
		if err := gb.tagTests(res); err != nil {
			return err
		}
		for _, a := range res.Operations {
			if err := gb.authoredTest(res, a); err != nil {
				return err
			}
		}
		// The list test comes last before delete so that an authored
		// operation that changes what a listing shows (a visibility change
		// on a message in flight) has run.
		if err := gb.listTest(res); err != nil {
			return err
		}
		if err := gb.deleteTest(res); err != nil {
			return err
		}
	}
	if err := gb.teardown(reversed(closure)); err != nil {
		return err
	}
	if res.Delete == nil && res.Create != nil {
		g.out.noTeardown = append(g.out.noTeardown, res.ID)
	}
	if len(gb.group.Tests) > 0 {
		g.out.scenario.Groups = append(g.out.scenario.Groups, gb.group)
	}
	return nil
}

func reversed(resources []resource) []resource {
	out := make([]resource, 0, len(resources))
	for i := len(resources) - 1; i >= 0; i-- {
		out = append(out, resources[i])
	}
	return out
}

// applyMutableFrom seeds the create params with every mutation's `from`, so
// the update that follows is a real change and the create read-back can
// assert the initial value.
func applyMutableFrom(params map[string]any, res resource) error {
	for _, m := range res.Mutable {
		if m.From == nil {
			continue
		}
		if err := setMemberPath(params, m.Member, cloneValue(m.From)); err != nil {
			return err
		}
	}
	return nil
}

func prefixed(id string, exports map[string]string) map[string]string {
	out := make(map[string]string, len(exports))
	for name, path := range exports {
		out[id+"."+name] = path
	}
	return out
}

// identityCheck is the check a read applies to its identity path: equal to
// the export it names, else shaped as the model says.
func (gb *groupBuilder) identityCheck(res resource, rd readSpec) check {
	if rd.Identity == "" {
		return gb.shapeCheck(gb.g.model.OutputShape(rd.Op), rd.IdentityPath)
	}
	return equals(map[string]any{"$ref": res.ID + "." + rd.Identity})
}

// shapeCheck is the strongest check the model supports on a response field
// whose value nothing else pins down: it matches the shape's pattern when
// the model declares one RE2 can express, else it is merely present.
func (gb *groupBuilder) shapeCheck(output, path string) check {
	if output == "" {
		return nonEmpty()
	}
	target, err := gb.g.model.ResolvePath(output, mustPath(path))
	if err != nil {
		return nonEmpty()
	}
	if c := gb.g.model.Constraints(target); c.Pattern != "" && gb.g.model.Kind(target) == "string" {
		if _, verifiable := patternMatches(c.Pattern, ""); verifiable {
			return matches(c.Pattern)
		}
	}
	return nonEmpty()
}

// readCall binds the resource's non-consuming read for use as a read-back.
func (gb *groupBuilder) readCall(res resource) (call, *refusal, error) {
	if res.Read == nil || res.Read.Consuming {
		return call{}, nil, nil
	}
	return gb.bindCall(res.Read.Op, res.Read.Params)
}

func (gb *groupBuilder) createTest(res resource) error {
	op := res.Create.Op
	gb.forResource(res)
	params := cloneValue(res.Create.Params).(map[string]any)
	if err := applyMutableFrom(params, res); err != nil {
		return fmt.Errorf("resource %q: %w", res.ID, err)
	}
	if err := gb.checkNames(res, params); err != nil {
		return err
	}
	c, ref, err := gb.bindCall(op, params)
	if err != nil {
		return err
	}
	if ref != nil {
		gb.g.refuseOp(gb.group.Name, op, ref)
		return nil
	}
	if err := gb.registerExports(&c, prefixed(res.ID, res.Exports), op); err != nil {
		return err
	}
	var clauses []assertion
	if len(res.Exports) > 0 {
		fields := make(map[string]check, len(res.Exports))
		for _, path := range sortedStringValues(res.Exports) {
			fields[path] = gb.shapeCheck(gb.g.model.OutputShape(op), path)
		}
		clauses = append(clauses, responseField(fields))
	}
	for _, d := range res.Derived {
		dc, ref, err := gb.bindCall(d.Op, d.Params)
		if err != nil {
			return err
		}
		if ref != nil {
			gb.g.refuseOp(gb.group.Name, op, refuse(ref.Reason, "derived export "+d.Export+": "+ref.Detail))
			return nil
		}
		if err := gb.registerExports(&dc, map[string]string{res.ID + "." + d.Export: d.Path}, op); err != nil {
			return err
		}
		clauses = append(clauses, wrap(readback(dc, checks(d.Path, gb.shapeCheck(gb.g.model.OutputShape(d.Op), d.Path))), res))
	}
	// An authored create assertion takes full responsibility for verifying
	// the create; the derived read-back and list-membership clauses are for
	// resources whose read and list can simply be replayed.
	readbacks := 0
	if len(res.Create.Assert) == 0 {
		ref, err := gb.derivedCreateClauses(res, op, &clauses, &readbacks)
		if err != nil {
			return err
		}
		if ref != nil {
			gb.g.refuseOp(gb.group.Name, op, ref)
			return nil
		}
	}
	for _, authored := range res.Create.Assert {
		a := cloneAssertion(authored)
		ref, err := gb.completeAssertion(&a, op, op)
		if err != nil {
			return err
		}
		if ref != nil {
			gb.g.refuseOp(gb.group.Name, op, ref)
			return nil
		}
		clauses = append(clauses, a)
		readbacks++
	}
	if readbacks == 0 && len(res.Derived) == 0 {
		gb.g.refuseOp(gb.group.Name, op, refuse(reasonNoReadbackPath,
			fmt.Sprintf("resource %q declares no read, no list and no authored create assertion, so the create cannot be verified", res.ID)))
		return nil
	}
	if len(clauses) == 0 {
		return fmt.Errorf("internal: create test for %s has no clauses", op)
	}
	return gb.addTest(newTest(op, op, c, clauses[0], clauses[1:]...))
}

// derivedCreateClauses appends the create test's read-back (via `read`) and
// list-membership (via `list`) clauses.
func (gb *groupBuilder) derivedCreateClauses(res resource, op string, clauses *[]assertion, readbacks *int) (*refusal, error) {
	rc, ref, err := gb.readCall(res)
	if err != nil {
		return nil, err
	}
	if ref != nil {
		return refuse(ref.Reason, "read-back via "+res.Read.Op+": "+ref.Detail), nil
	}
	if rc.Op != "" {
		fields := checks(res.Read.IdentityPath, gb.identityCheck(res, *res.Read))
		for _, m := range res.Mutable {
			if m.From != nil {
				fields[m.ReadPath] = equals(cloneValue(m.From))
			}
		}
		a := readback(rc, fields)
		if ref, err := gb.completeAssertion(&a, "", op); err != nil || ref != nil {
			return ref, err
		}
		*clauses = append(*clauses, wrap(a, res))
		*readbacks++
	}
	if res.List != nil {
		lc, ref, err := gb.bindCall(res.List.Op, res.List.Params)
		if err != nil {
			return nil, err
		}
		if ref != nil {
			return refuse(ref.Reason, "list-membership via "+res.List.Op+": "+ref.Detail), nil
		}
		a := listContains(&lc, res.List.ItemsPath, map[string]any{res.List.IdentityPath: map[string]any{"$ref": res.ID + "." + res.List.Identity}})
		if ref, err := gb.completeAssertion(&a, "", op); err != nil || ref != nil {
			return ref, err
		}
		*clauses = append(*clauses, wrap(a, res))
		*readbacks++
	}
	return nil, nil
}

// firstErr turns a completion refusal into a gap and returns the error, if
// any, for the callers that cannot continue.
func firstErr(ref *refusal, err error, gb *groupBuilder, op string) error {
	if err != nil {
		return err
	}
	gb.g.refuseOp(gb.group.Name, op, ref)
	return nil
}

func (gb *groupBuilder) readTest(res resource, rd readSpec) error {
	if gb.hasTestForOp(rd.Op) {
		gb.g.out.folded = append(gb.g.out.folded, gb.group.Name+"/"+rd.Op+" (read)")
		return nil
	}
	gb.forResource(res)
	c, ref, err := gb.bindCall(rd.Op, rd.Params)
	if err != nil {
		return err
	}
	if ref != nil {
		gb.g.refuseOp(gb.group.Name, rd.Op, ref)
		return nil
	}
	if err := gb.registerExports(&c, prefixed(res.ID, rd.Exports), rd.Op); err != nil {
		return err
	}
	a := responseField(checks(rd.IdentityPath, gb.identityCheck(res, rd)))
	if ref, err := gb.completeAssertion(&a, rd.Op, rd.Op); err != nil || ref != nil {
		return firstErr(ref, err, gb, rd.Op)
	}
	return gb.addTest(newTest(rd.Op, rd.Op, c, a))
}

func (gb *groupBuilder) listTest(res resource) error {
	if res.List == nil {
		return nil
	}
	op := res.List.Op
	if gb.hasTestForOp(op) {
		gb.g.out.folded = append(gb.g.out.folded, gb.group.Name+"/"+op+" (list)")
		return nil
	}
	gb.forResource(res)
	c, ref, err := gb.bindCall(op, res.List.Params)
	if err != nil {
		return err
	}
	if ref != nil {
		gb.g.refuseOp(gb.group.Name, op, ref)
		return nil
	}
	a := listContains(nil, res.List.ItemsPath, map[string]any{res.List.IdentityPath: map[string]any{"$ref": res.ID + "." + res.List.Identity}})
	if ref, err := gb.completeAssertion(&a, op, op); err != nil || ref != nil {
		return firstErr(ref, err, gb, op)
	}
	return gb.addTest(newTest(op, op, c, a))
}

func (gb *groupBuilder) mutableTests(res resource) error {
	perOp := make(map[string]int)
	for _, m := range res.Mutable {
		perOp[m.Op]++
	}
	for _, m := range res.Mutable {
		name := m.Op
		if perOp[m.Op] > 1 {
			name = m.Op + pascal(lastSegment(m.Member))
		}
		gb.forResource(res)
		if res.Read == nil || res.Read.Consuming {
			gb.g.refuseOp(gb.group.Name, m.Op, refuse(reasonNoReadbackPath,
				fmt.Sprintf("mutation of %s needs a non-consuming read on resource %q to read the new value back", m.Member, res.ID)))
			continue
		}
		params := cloneValue(m.Params)
		if params == nil {
			params = map[string]any{}
		}
		object := params.(map[string]any)
		if err := setMemberPath(object, m.Member, cloneValue(m.To)); err != nil {
			return fmt.Errorf("resource %q mutable %s: %w", res.ID, m.Member, err)
		}
		c, ref, err := gb.bindCall(m.Op, object)
		if err != nil {
			return err
		}
		if ref != nil {
			gb.g.refuseOp(gb.group.Name, m.Op, ref)
			continue
		}
		rc, ref, err := gb.readCall(res)
		if err != nil {
			return err
		}
		if ref != nil {
			gb.g.refuseOp(gb.group.Name, m.Op, refuse(ref.Reason, "read-back via "+res.Read.Op+": "+ref.Detail))
			continue
		}
		a := readback(rc, checks(m.ReadPath, equals(cloneValue(m.To))))
		if ref, err := gb.completeAssertion(&a, "", name); err != nil || ref != nil {
			if err := firstErr(ref, err, gb, m.Op); err != nil {
				return err
			}
			continue
		}
		if err := gb.addTest(newTest(name, m.Op, c, wrap(a, res))); err != nil {
			return err
		}
	}
	return nil
}

// tagShape says how a service carries tags: a string map, or a list of
// {Key, Value} structures.
type tagShape int

const (
	tagsAsMap tagShape = iota + 1
	tagsAsList
)

func (gb *groupBuilder) detectTagShape(res resource) (tagShape, *refusal, error) {
	tags := res.Tags
	input := gb.g.model.InputShape(tags.Tag.Op)
	target, ok := gb.g.model.MemberTarget(input, tags.Tag.Member)
	if !ok {
		return 0, nil, fmt.Errorf("resource %q: %s has no member %q", res.ID, tags.Tag.Op, tags.Tag.Member)
	}
	untagInput := gb.g.model.InputShape(tags.Untag.Op)
	untagTarget, ok := gb.g.model.MemberTarget(untagInput, tags.Untag.Member)
	if !ok {
		return 0, nil, fmt.Errorf("resource %q: %s has no member %q", res.ID, tags.Untag.Op, tags.Untag.Member)
	}
	if gb.g.model.Kind(untagTarget) != "list" || gb.g.model.Kind(gb.g.model.Shapes[untagTarget].Member) != "string" {
		return 0, refuse(reasonUnsupportedTagShape+":"+untagTarget, fmt.Sprintf("%s.%s is not a list of strings", tags.Untag.Op, tags.Untag.Member)), nil
	}
	switch gb.g.model.Kind(target) {
	case "map":
		shape := gb.g.model.Shapes[target]
		if gb.g.model.Kind(shape.Key) == "string" && gb.g.model.Kind(shape.Value) == "string" {
			return tagsAsMap, nil, nil
		}
	case "list":
		item := gb.g.model.Shapes[target].Member
		key, hasKey := gb.g.model.MemberTarget(item, "Key")
		value, hasValue := gb.g.model.MemberTarget(item, "Value")
		if hasKey && hasValue && gb.g.model.Kind(key) == "string" && gb.g.model.Kind(value) == "string" {
			return tagsAsList, nil, nil
		}
	}
	return 0, refuse(reasonUnsupportedTagShape+":"+target, fmt.Sprintf("%s.%s is neither a string map nor a list of {Key, Value}", tags.Tag.Op, tags.Tag.Member)), nil
}

func (gb *groupBuilder) tagTests(res resource) error {
	if res.Tags == nil {
		return nil
	}
	tags := res.Tags
	gb.forResource(res)
	shape, ref, err := gb.detectTagShape(res)
	if err != nil {
		return err
	}
	if ref != nil {
		for _, op := range []string{tags.Tag.Op, tags.List.Op, tags.Untag.Op} {
			gb.g.refuseOp(gb.group.Name, op, ref)
		}
		return nil
	}
	var tagValue any
	if shape == tagsAsMap {
		tagValue = map[string]any{compatTagKey: compatTagValue}
	} else {
		tagValue = []any{map[string]any{"Key": compatTagKey, "Value": compatTagValue}}
	}
	// The listing call is bound once; every clause below takes its own copy.
	listing, ref, err := gb.bindCall(tags.List.Op, tags.List.Params)
	if err != nil {
		return err
	}
	if ref != nil {
		for _, op := range []string{tags.Tag.Op, tags.List.Op, tags.Untag.Op} {
			gb.g.refuseOp(gb.group.Name, op, refuse(ref.Reason, "tag listing via "+tags.List.Op+": "+ref.Detail))
		}
		return nil
	}
	present := map[string]any{"$.Key": compatTagKey, "$.Value": compatTagValue}

	// Tag: the tag shows up in the listing.
	c, ref, err := gb.bindCall(tags.Tag.Op, map[string]any{tags.Tag.Member: tagValue})
	if err != nil {
		return err
	}
	if ref != nil {
		gb.g.refuseOp(gb.group.Name, tags.Tag.Op, ref)
	} else {
		lc := cloneCall(listing)
		var a assertion
		if shape == tagsAsMap {
			a = readback(lc, checks(joinPath(tags.List.Path, compatTagKey), equals(compatTagValue)))
		} else {
			a = listContains(&lc, tags.List.Path, cloneValue(present).(map[string]any))
		}
		if ref, err := gb.completeAssertion(&a, "", tags.Tag.Op); err != nil || ref != nil {
			if err := firstErr(ref, err, gb, tags.Tag.Op); err != nil {
				return err
			}
		} else if err := gb.addTest(newTest(tags.Tag.Op, tags.Tag.Op, c, wrap(a, res))); err != nil {
			return err
		}
	}

	// List: its own response carries the tag.
	if gb.hasTestForOp(tags.List.Op) {
		gb.g.out.folded = append(gb.g.out.folded, gb.group.Name+"/"+tags.List.Op+" (tags.list)")
	} else {
		lc := cloneCall(listing)
		var a assertion
		if shape == tagsAsMap {
			a = responseField(checks(joinPath(tags.List.Path, compatTagKey), equals(compatTagValue)))
		} else {
			a = listContains(nil, tags.List.Path, cloneValue(present).(map[string]any))
		}
		if ref, err := gb.completeAssertion(&a, tags.List.Op, tags.List.Op); err != nil || ref != nil {
			if err := firstErr(ref, err, gb, tags.List.Op); err != nil {
				return err
			}
		} else if err := gb.addTest(newTest(tags.List.Op, tags.List.Op, lc, a)); err != nil {
			return err
		}
	}

	// Untag: the tag is gone from the listing.
	uc, ref, err := gb.bindCall(tags.Untag.Op, map[string]any{tags.Untag.Member: []any{compatTagKey}})
	if err != nil {
		return err
	}
	if ref != nil {
		gb.g.refuseOp(gb.group.Name, tags.Untag.Op, ref)
		return nil
	}
	lc := cloneCall(listing)
	var a assertion
	if shape == tagsAsMap {
		a = readback(lc, checks(joinPath(tags.List.Path, compatTagKey), missing()))
	} else {
		a = absentFromList(&lc, tags.List.Path, map[string]any{"$.Key": compatTagKey})
	}
	if ref, err := gb.completeAssertion(&a, "", tags.Untag.Op); err != nil || ref != nil {
		return firstErr(ref, err, gb, tags.Untag.Op)
	}
	return gb.addTest(newTest(tags.Untag.Op, tags.Untag.Op, uc, wrap(a, res)))
}

func (gb *groupBuilder) authoredTest(res resource, a authoredOp) error {
	name := a.testName()
	gb.forResource(res)
	c, ref, err := gb.bindCall(a.Op, a.Params)
	if err != nil {
		return err
	}
	if ref != nil {
		gb.g.refuseOp(gb.group.Name, a.Op, ref)
		return nil
	}
	if err := gb.registerExports(&c, a.Export, name); err != nil {
		return err
	}
	clauses := make([]assertion, 0, len(a.Assert))
	for _, authored := range a.Assert {
		clause := cloneAssertion(authored)
		ref, err := gb.completeAssertion(&clause, a.Op, name)
		if err != nil {
			return fmt.Errorf("resource %q operation %s: %w", res.ID, name, err)
		}
		if ref != nil {
			gb.g.refuseOp(gb.group.Name, a.Op, ref)
			return nil
		}
		clauses = append(clauses, clause)
	}
	return gb.addTest(newTest(name, a.Op, c, clauses[0], clauses[1:]...))
}

func (gb *groupBuilder) deleteTest(res resource) error {
	if res.Delete == nil {
		return nil
	}
	op := res.Delete.Op
	gb.forResource(res)
	c, ref, err := gb.bindCall(op, res.Delete.Params)
	if err != nil {
		return err
	}
	if ref != nil {
		gb.g.refuseOp(gb.group.Name, op, ref)
		return nil
	}
	var a assertion
	switch {
	case res.NotFound != nil:
		rc, ref, err := gb.readCall(res)
		if err != nil {
			return err
		}
		if ref != nil {
			gb.g.refuseOp(gb.group.Name, op, refuse(ref.Reason, "absence via "+res.Read.Op+": "+ref.Detail))
			return nil
		}
		if rc.Op == "" {
			gb.g.refuseOp(gb.group.Name, op, refuse(reasonNoReadbackPath,
				fmt.Sprintf("notFound is declared but resource %q has no non-consuming read to raise it", res.ID)))
			return nil
		}
		a = absentByError(rc, errorSpec{Shape: res.NotFound.Error})
	case res.List != nil:
		lc, ref, err := gb.bindCall(res.List.Op, res.List.Params)
		if err != nil {
			return err
		}
		if ref != nil {
			gb.g.refuseOp(gb.group.Name, op, refuse(ref.Reason, "absence via "+res.List.Op+": "+ref.Detail))
			return nil
		}
		a = absentFromList(&lc, res.List.ItemsPath, map[string]any{res.List.IdentityPath: map[string]any{"$ref": res.ID + "." + res.List.Identity}})
	default:
		gb.g.refuseOp(gb.group.Name, op, refuse(reasonNoReadbackPath,
			fmt.Sprintf("resource %q declares neither notFound nor list, so absence after delete cannot be verified", res.ID)))
		return nil
	}
	if ref, err := gb.completeAssertion(&a, "", op); err != nil || ref != nil {
		return firstErr(ref, err, gb, op)
	}
	return gb.addTest(newTest(op, op, c, wrap(a, res)))
}

// ---------------------------------------------------------------------------
// Probe group
// ---------------------------------------------------------------------------

// probeGroup covers every modeled operation the emulator does not implement
// and no lifecycle test exercises: one call with model-valid values, one
// assertion on the modeled output. Against an unimplemented operation the SDK
// raises the 501 and the harness records `unimplemented`; the assertion is
// never reached, and regeneration moves the operation out of this group the
// day it is implemented.
func (g *generator) probeGroup() error {
	name := g.groupName("probe")
	var probes []string
	for _, op := range g.model.Operations() {
		if g.caps.implemented(op) {
			continue
		}
		if _, covered := g.out.covered[op]; covered {
			continue
		}
		probes = append(probes, op)
	}
	if len(probes) == 0 {
		return nil
	}
	// Every resource the emulator can actually set up is in scope — one whose
	// create (or read, for a pre-existing resource) is unimplemented would
	// fail the group's setup and turn every probe into a skip instead of the
	// `unimplemented` the probe exists to record. Only the resources a probe
	// references are instantiated. Bind against a scope where every setup
	// export is assumed available, then instantiate what was used.
	all := g.probeScope()
	assumed := g.newGroupBuilder(name, groupProbe, all)
	for _, res := range all {
		if res.Create == nil {
			for ctx, path := range prefixed(res.ID, res.Read.Exports) {
				if err := assumed.registerExports(&call{Op: res.Read.Op}, map[string]string{ctx: path}, ""); err != nil {
					return err
				}
			}
			continue
		}
		for ctx, path := range prefixed(res.ID, res.Exports) {
			if err := assumed.registerExports(&call{Op: res.Create.Op}, map[string]string{ctx: path}, ""); err != nil {
				return err
			}
		}
		for _, d := range res.Derived {
			if err := assumed.registerExports(&call{Op: d.Op}, map[string]string{res.ID + "." + d.Export: d.Path}, ""); err != nil {
				return err
			}
		}
	}
	var tests []boundProbe
	used := make(map[string]bool)
	for _, op := range probes {
		c, ref, err := assumed.bindCall(op, nil)
		if err != nil {
			return err
		}
		if ref != nil {
			g.refuseOp(name, op, ref)
			continue
		}
		a, ref, err := assumed.probeAssertion(op, c)
		if err != nil {
			return err
		}
		if ref != nil {
			g.refuseOp(name, op, ref)
			continue
		}
		t := newTest(op, op, c, a)
		for _, ref := range refsInTest(t) {
			used[strings.SplitN(ref, ".", 2)[0]] = true
		}
		tests = append(tests, boundProbe{op: op, test: t})
	}
	if len(tests) == 0 {
		return nil
	}
	// Now build the real group: setup for the used resources' closures.
	wanted := make(map[string]bool)
	for id := range used {
		for _, res := range g.recipe.closure(id) {
			wanted[res.ID] = true
		}
	}
	ordered, _ := g.recipe.topological()
	var setup []resource
	for _, res := range ordered {
		if wanted[res.ID] {
			setup = append(setup, res)
		}
	}
	gb := g.newGroupBuilder(name, groupProbe, all)
	for _, res := range setup {
		ref, err := gb.instantiate(res)
		if err != nil {
			return err
		}
		if ref != nil {
			// A resource a probe depends on cannot be created: refuse those
			// probes rather than the whole group.
			for _, b := range tests {
				if usesResource(b.test, res.ID) {
					g.refuseOp(name, b.op, refuse(reasonSetupRefused+":"+res.ID, fmt.Sprintf("required resource %q cannot be created: %s", res.ID, ref.Detail)))
				}
			}
			var kept []boundProbe
			for _, b := range tests {
				if !usesResource(b.test, res.ID) {
					kept = append(kept, b)
				}
			}
			tests = kept
		}
	}
	for _, b := range tests {
		if err := gb.addTest(b.test); err != nil {
			return err
		}
	}
	if err := gb.teardown(reversed(setup)); err != nil {
		return err
	}
	if len(gb.group.Tests) > 0 {
		g.out.scenario.Groups = append(g.out.scenario.Groups, gb.group)
	}
	return nil
}

func usesResource(t test, id string) bool {
	for _, ref := range refsInTest(t) {
		if strings.HasPrefix(ref, id+".") {
			return true
		}
	}
	return false
}

// probeScope is every recipe resource whose setup the emulator implements,
// with everything it requires. Recipe order, so the first resource a recipe
// lists is the one a probe binds to when several bind the same member.
func (g *generator) probeScope() []resource {
	all, _ := g.recipe.topological()
	usable := make(map[string]bool)
	for _, res := range all {
		ok := true
		for _, op := range res.setupOps() {
			if !g.caps.implemented(op) {
				ok = false
			}
		}
		for _, req := range res.Requires {
			if !usable[req] {
				ok = false
			}
		}
		usable[res.ID] = ok
	}
	var scope []resource
	for _, res := range g.recipe.Resources {
		if usable[res.ID] {
			scope = append(scope, res)
		}
	}
	return scope
}

// boundProbe is a probe test whose params bound, held until the resources it
// references are known to be creatable.
type boundProbe struct {
	op   string
	test test
}

// probeAssertion is the one clause a probe carries: the modeled output's
// identity member, or — for an operation that returns nothing — a read-back
// of the resource the call was bound to, proving the call left it intact.
func (gb *groupBuilder) probeAssertion(op string, c call) (assertion, *refusal, error) {
	output := gb.g.model.OutputShape(op)
	if output != "" {
		if member := identityMember(gb.g.model, output); member != "" {
			return responseField(checks("$."+member, nonEmpty())), nil, nil
		}
	}
	for _, ref := range refsIn(c.Params) {
		id := strings.SplitN(ref, ".", 2)[0]
		for _, res := range gb.scope {
			if res.ID != id || res.Read == nil || res.Read.Consuming {
				continue
			}
			rc, refusal, err := gb.bindCall(res.Read.Op, res.Read.Params)
			if err != nil || refusal != nil {
				return assertion{}, refusal, err
			}
			a := readback(rc, checks(res.Read.IdentityPath, gb.identityCheck(res, *res.Read)))
			if refusal, err := gb.completeAssertion(&a, "", op); err != nil || refusal != nil {
				return assertion{}, refusal, err
			}
			return a, nil, nil
		}
	}
	return assertion{}, refuse(reasonNoOutputToAssert,
		fmt.Sprintf("%s returns no identifying member and is bound to no readable resource, so a probe would assert nothing", op)), nil
}

// identityMember picks the output member a probe asserts: the first member,
// in suffix-preference order, that looks like an identifier; else the first
// required member; else the first member at all.
func identityMember(model *serviceModel, output string) string {
	members := model.Members(output)
	if len(members) == 0 {
		return ""
	}
	for _, suffix := range []string{"Arn", "Id", "Url", "Name", "Handle", "Token", "Status", "State"} {
		for _, member := range members {
			if strings.HasSuffix(member, suffix) && isScalar(model, output, member) {
				return member
			}
		}
	}
	if required := model.RequiredMembers(output); len(required) > 0 {
		return required[0]
	}
	return members[0]
}

func isScalar(model *serviceModel, structure, member string) bool {
	target, _ := model.MemberTarget(structure, member)
	switch model.Kind(target) {
	case "string", "enum", "integer", "float", "boolean", "timestamp":
		return true
	}
	return false
}

// ---------------------------------------------------------------------------
// Implemented operations the recipe gives no role
// ---------------------------------------------------------------------------

func (g *generator) uncoveredImplemented() {
	for _, op := range g.model.Operations() {
		if !g.caps.implemented(op) {
			continue
		}
		if _, covered := g.out.covered[op]; covered {
			continue
		}
		if g.refusedSomewhere(op) {
			continue
		}
		if isUpdateFamily(op) {
			g.refuseOp(g.groupName("probe"), op, refuse(reasonUpdateWithoutMutable,
				fmt.Sprintf("%s is implemented (%s) but no recipe resource declares a mutable member or tags for it", op, g.caps.statusLabel(op))))
			continue
		}
		g.refuseOp(g.groupName("probe"), op, refuse(reasonProbeOfImplementedOp,
			fmt.Sprintf("%s is implemented (%s), so it may not be probed, and no recipe resource gives it a role", op, g.caps.statusLabel(op))))
	}
}

func (g *generator) refusedSomewhere(op string) bool {
	for _, gp := range g.out.gaps {
		if gp.Operation == op {
			return true
		}
	}
	return false
}

func isUpdateFamily(op string) bool {
	for _, prefix := range []string{"Update", "Set", "Tag", "Untag"} {
		if strings.HasPrefix(op, prefix) {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Small helpers
// ---------------------------------------------------------------------------

// cloneCall copies a bound call so two clauses never share one params map.
func cloneCall(c call) call {
	out := call{Op: c.Op, Params: cloneValue(c.Params).(map[string]any)}
	if c.Export != nil {
		out.Export = make(map[string]string, len(c.Export))
		for k, v := range c.Export {
			out.Export[k] = v
		}
	}
	return out
}

func cloneAssertion(a assertion) assertion {
	out := a
	out.Comment = ""
	if a.Call != nil {
		c := *a.Call
		c.Params = cloneValue(a.Call.Params).(map[string]any)
		if c.Params == nil {
			c.Params = map[string]any{}
		}
		if a.Call.Export != nil {
			c.Export = make(map[string]string, len(a.Call.Export))
			for k, v := range a.Call.Export {
				c.Export[k] = v
			}
		}
		out.Call = &c
	}
	if a.Checks != nil {
		out.Checks = make(map[string]check, len(a.Checks))
		for k, v := range a.Checks {
			v.Equals = cloneValue(v.Equals)
			out.Checks[k] = v
		}
	}
	if a.Where != nil {
		out.Where = cloneValue(a.Where).(map[string]any)
	}
	if a.Error != nil {
		e := *a.Error
		out.Error = &e
	}
	if a.Assert != nil {
		inner := cloneAssertion(*a.Assert)
		out.Assert = &inner
	}
	return out
}

func sortedStringKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func sortedStringValues(m map[string]string) []string {
	values := make([]string, 0, len(m))
	for _, v := range m {
		values = append(values, v)
	}
	sort.Strings(values)
	return values
}

func sortedCheckPaths(m map[string]check) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func sortedValueKeys(m map[string]any) []string { return sortedKeys(m) }

func lastSegment(memberPath string) string {
	parts := strings.Split(memberPath, ".")
	return parts[len(parts)-1]
}

// pascal upper-cases the first letter so a variant suffix folds into a
// PascalCase test name.
func pascal(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
