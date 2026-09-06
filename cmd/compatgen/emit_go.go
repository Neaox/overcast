//go:build dev

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/format"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

// The go-sdk source emitter — docs/plans/compat-coverage-modelgen.md §3.2 D1,
// phase G3.
//
// The three interpreters execute the scenario IR at run time. The AWS SDK for
// Go v2 has no public dynamic-dispatch API, and the plan rejects reaching into
// smithy-go's marshaller layer to fake one: the whole value of running eight
// suites is that each exercises its own real typed serialization path. So this
// file emits Go source — one function per scenario test, each building a real
// typed input struct and calling a real client method — which the go-sdk
// suite's ordinary build compiles.
//
// What is emitted is the *data* plus the typed calls. Semantics — the context
// bag, value expressions, the closed check set, error matching, `eventually`,
// the six-field failure message — live once in the suite's hand-written
// internal/scenario package and are never re-emitted.
//
// # The naming table
//
// Everything this emitter knows about spelling Go is in the goName* functions
// below, and `-explain -lang go` (explain_typed.go) renders through the same
// ones, so the pseudo-code a reader reproduces a failure with is the source the
// emitter wrote. The table is deliberately tiny:
//
//	service → github.com/aws/aws-sdk-go-v2/service/<lower(sdkId), spaces removed>
//	operation → &<pkg>.<Op>Input{} and client.<Op>(ctx, in)
//	member → the exported field of the same name, first letter capitalized
//	value → the IR's own value grammar, as Go data with scenario.Value
//	        expressions inside it
//
// # Why members are assigned through a helper rather than with aws.String
//
// `in.QueueUrl = aws.String(v)` compiles only where smithy-go made that member
// a pointer, and nothing available at generation time reliably says whether it
// did. The pinned shape snapshot and the vendored SDK are generated from
// different revisions of the same AWS model, and for the pilot service they
// already disagree: ReceiveMessage's MaxNumberOfMessages, VisibilityTimeout and
// WaitTimeSeconds target NullableInteger in models/aws/shapes/sqs.json — which
// says pointer — and are plain int32 fields in aws-sdk-go-v2/service/sqs. An
// emitter that derived pointer-ness from the model would emit three fields that
// do not compile, in the first service it was pointed at.
//
// So the emitted code passes the field's *address* to scenario.Binder.Set,
// which writes through whichever spelling the field turned out to have, and the
// same helper serves an enum (a named string type), a list, a map and a nested
// structure. The input struct is still the SDK's own, still filled member by
// member under its modeled name, and still serialized by the SDK's real
// middleware stack — the deviation is in how the field is written, not in what
// is sent. It is also what keeps this emitter free of a per-shape Go type
// table, which is the part that would otherwise have to grow with every
// service G4 adds.
//
// The java, dotnet and rust emitters face the same question and should answer
// it the same way wherever their SDK's nullability is not derivable from the
// pinned model.

// goSuiteDir is where the emitted files live, repository-relative.
const goSuiteDir = "compat/suites/go-sdk/internal/groups"

// goEmitReason is the refusal a member the emitter cannot express produces.
// Unlike every other reason in gaps.json it does not mean "no test": the
// operation is generated and the interpreters run it. It means this backend
// cannot compile it, so the whole group is scoped away from go-sdk in the
// generated registry — a suite that cannot execute a group must not be listed
// as able to.
const goEmitReason = "go-emit-unsupported"

// goUnsupportedKinds are the modeled member kinds no Go expression in the
// grammar above can carry. Timestamps, blobs and documents have no portable
// literal and are already refused upstream (compat/model/README.md § Recipes),
// so this is a backstop rather than a live path; a union has a discriminated
// Go representation that reflection cannot fill from a document.
var goUnsupportedKinds = map[string]bool{
	"timestamp": true,
	"blob":      true,
	"document":  true,
	"union":     true,
}

// goEmission is one service's emitted source plus what it could not express.
type goEmission struct {
	// Path is the emitted file, repository-relative.
	Path string
	// Contents is gofmt-clean Go source.
	Contents []byte
	// Refused names the groups this backend cannot execute, and Gaps says why.
	Refused map[string]bool
	Gaps    []gap
}

// emitGo renders one service's generated groups as Go source for the go-sdk
// suite.
func emitGo(gen *generation) (*goEmission, error) {
	e := &goEmission{
		Path:    goSuiteDir + "/" + goFileName(gen.scenario.Service),
		Refused: map[string]bool{},
	}
	s := gen.scenario
	w := &goWriter{}

	groups := make([]group, 0, len(s.Groups))
	for _, g := range s.Groups {
		if refusals := goRefusals(gen, g); len(refusals) > 0 {
			e.Refused[g.Name] = true
			e.Gaps = append(e.Gaps, refusals...)
			continue
		}
		groups = append(groups, g)
	}

	if err := goMethodNamesAreUnique(s.Service, groups); err != nil {
		return nil, err
	}

	pkg := goNamePackage(s.Client.SDKID)
	recv := goNameReceiver(s.Service)

	w.linef("// Code generated by cmd/compatgen; DO NOT EDIT.")
	w.linef("")
	w.linef("package groups")
	w.linef("")
	w.linef("import (")
	w.linef("\t%q", "context")
	w.linef("\t%q", "sync")
	w.linef("")
	w.linef("\t%q", goNameModule(s.Client.SDKID))
	w.linef("\t%q", "github.com/overcast-sh/overcast-compat-go-sdk/internal/clients")
	w.linef("\t%q", "github.com/overcast-sh/overcast-compat-go-sdk/internal/harness")
	w.linef("\t%q", "github.com/overcast-sh/overcast-compat-go-sdk/internal/scenario")
	w.linef(")")
	w.linef("")
	goWriteHeaderComment(w, s)
	w.linef("func %s(c *clients.Clients) ServiceGroup {", goNameConstructor(s.Service))
	w.linef("\tg := &%s{c: c}", recv)
	w.linef("\treturn ServiceGroup{")
	w.linef("\t\tName: %q,", "scenarios/"+s.Service)
	w.linef("\t\tImpls: map[string]harness.TestFn{")
	for _, g := range groups {
		for _, t := range g.Tests {
			w.linef("\t\t\t%q: g.%s,", g.Name+":"+t.Name, goNameTestMethod(g.Name, t.Name))
		}
	}
	w.linef("\t\t},")
	w.linef("\t\tSetup: map[string]func(context.Context, *harness.TestContext) error{")
	for _, g := range groups {
		w.linef("\t\t\t%q: g.%s,", g.Name, goNameSetupMethod(g.Name))
	}
	w.linef("\t\t},")
	w.linef("\t\tTeardown: map[string]func(context.Context, *harness.TestContext) error{")
	for _, g := range groups {
		w.linef("\t\t\t%q: g.%s,", g.Name, goNameTeardownMethod(g.Name))
	}
	w.linef("\t\t},")
	w.linef("\t}")
	w.linef("}")
	w.linef("")
	w.linef("type %s struct {", recv)
	w.linef("\tc      *clients.Clients")
	w.linef("\tonce   sync.Once")
	w.linef("\tclient *%s.Client", pkg)
	w.linef("}")
	w.linef("")
	w.linef("// cl builds this service's client once, from the config the suite's")
	w.linef("// hand-written groups share. A generated group builds its own rather than")
	w.linef("// adding an accessor to internal/clients for every service the generator")
	w.linef("// learns to cover; nothing else about the client differs.")
	w.linef("func (g *%s) cl() *%s.Client {", recv, pkg)
	w.linef("\tg.once.Do(func() { g.client = %s.NewFromConfig(g.c.Config()) })", pkg)
	w.linef("\treturn g.client")
	w.linef("}")

	for _, g := range groups {
		if err := goWriteGroup(w, gen, pkg, recv, g); err != nil {
			return nil, err
		}
	}

	contents, err := format.Source([]byte(w.String()))
	if err != nil {
		// The emitter produced source Go cannot parse. That is a generator
		// bug, and it must stop generation rather than land a file the suite
		// cannot build; the offending source is printed so the fault is
		// diagnosable from the failure alone.
		return nil, fmt.Errorf("emitted Go for %s does not parse: %w\n%s", s.Service, err, w.String())
	}
	e.Contents = contents
	sortGaps(e.Gaps)
	return e, nil
}

func goWriteHeaderComment(w *goWriter, s *scenario) {
	w.linef("// %s returns the generated %s groups.", goNameConstructor(s.Service), s.Service)
	w.linef("//")
	w.linef("// Generated from %s by cmd/compatgen.", scenarioPath(s.Service))
	w.linef("// The semantics live in internal/scenario; this file is the data and the")
	w.linef("// typed SDK calls.")
}

// goWriteGroup emits one group: its identity, its setup and teardown hooks —
// registered even when empty, because an empty phase is a no-op and not a
// missing one — and one function per test.
func goWriteGroup(w *goWriter, gen *generation, pkg, recv string, g group) error {
	file := scenarioPath(gen.scenario.Service)
	w.linef("")
	w.linef("var %s = scenario.Group{Name: %q, File: %q}", goNameGroupVar(g.Name), g.Name, file)

	w.linef("")
	w.linef("func (g *%s) %s(ctx context.Context, t *harness.TestContext) error {", recv, goNameSetupMethod(g.Name))
	if err := goWriteHookBody(w, pkg, g, "RunSetup", g.Setup); err != nil {
		return err
	}
	w.linef("}")

	w.linef("")
	w.linef("func (g *%s) %s(ctx context.Context, t *harness.TestContext) error {", recv, goNameTeardownMethod(g.Name))
	if err := goWriteHookBody(w, pkg, g, "RunTeardown", g.Teardown); err != nil {
		return err
	}
	w.linef("}")

	for _, t := range g.Tests {
		w.linef("")
		w.linef("func (g *%s) %s(ctx context.Context, t *harness.TestContext) error {", recv, goNameTestMethod(g.Name, t.Name))
		w.linef("\treturn %s.RunTest(ctx, t, %q, scenario.Test{", goNameGroupVar(g.Name), t.Name)
		if err := goWriteCall(w, pkg, t.Call, "\t\t", "Call: "); err != nil {
			return err
		}
		w.linef("\t\tAssert: []scenario.Clause{")
		for _, a := range t.Assert {
			if err := goWriteClause(w, pkg, a, "\t\t\t"); err != nil {
				return err
			}
		}
		w.linef("\t\t},")
		w.linef("\t})")
		w.linef("}")
	}
	return nil
}

func goWriteHookBody(w *goWriter, pkg string, g group, method string, calls []call) error {
	if len(calls) == 0 {
		w.linef("\t// No %s steps: an empty phase is a no-op, not a missing one.",
			strings.ToLower(strings.TrimPrefix(method, "Run")))
		w.linef("\treturn %s.%s(ctx, t)", goNameGroupVar(g.Name), method)
		return nil
	}
	w.linef("\treturn %s.%s(ctx, t,", goNameGroupVar(g.Name), method)
	for _, c := range calls {
		if err := goWriteCall(w, pkg, c, "\t\t", ""); err != nil {
			return err
		}
	}
	w.linef("\t)")
	return nil
}

// goWriteCall emits one scenario.Call: the operation, the params as the
// scenario file writes them, the typed input build, the client method and the
// exports. prefix carries whatever the call's position needs in front of it —
// a struct field name, or the "&" the list clauses want, since those take a
// pointer so that "no call of its own" can be nil.
func goWriteCall(w *goWriter, pkg string, c call, indent, prefix string) error {
	w.linef("%s%sscenario.Call{", indent, prefix)
	w.linef("%s\tOp: %q,", indent, c.Op)
	raw, err := goRawParams(c.Params)
	if err != nil {
		return err
	}
	w.linef("%s\tParams: %s,", indent, raw)
	w.linef("%s\tBuild: func(b *scenario.Binder) any {", indent)
	lines, err := goInputLines(pkg, c.Op, c.Params, indent+"\t\t")
	if err != nil {
		return err
	}
	for _, line := range lines {
		w.linef("%s\t\t%s", indent, line)
	}
	w.linef("%s\t\treturn in", indent)
	w.linef("%s\t},", indent)
	w.linef("%s\tSend: func(ctx context.Context, in any) (any, error) {", indent)
	w.linef("%s\t\treturn g.cl().%s", indent, goNameClientCall(pkg, c.Op))
	w.linef("%s\t},", indent)
	if len(c.Export) > 0 {
		w.linef("%s\tExport: map[string]string{", indent)
		for _, path := range sortedStringKeys(c.Export) {
			w.linef("%s\t\t%q: %q,", indent, path, c.Export[path])
		}
		w.linef("%s\t},", indent)
	}
	w.linef("%s},", indent)
	return nil
}

// goWriteClause emits one assertion clause through the constructors in
// internal/scenario, which are the same closed set ir.go builds.
func goWriteClause(w *goWriter, pkg string, a assertion, indent string) error {
	switch a.Kind {
	case assertResponseField:
		w.linef("%sscenario.ResponseField(", indent)
		goWriteChecks(w, a.Checks, indent+"\t")
		w.linef("%s),", indent)
	case assertReadback:
		w.linef("%sscenario.Readback(", indent)
		if err := goWriteCall(w, pkg, *a.Call, indent+"\t", ""); err != nil {
			return err
		}
		goWriteChecks(w, a.Checks, indent+"\t")
		w.linef("%s),", indent)
	case assertListContains, assertAbsent:
		if a.Kind == assertAbsent && a.Error != nil {
			w.linef("%sscenario.AbsentByError(", indent)
			if err := goWriteCall(w, pkg, *a.Call, indent+"\t", ""); err != nil {
				return err
			}
			w.linef("%s\tscenario.Error(%q, %q),", indent, a.Error.Shape, a.Error.Code)
			w.linef("%s),", indent)
			return nil
		}
		name := "ListContains"
		if a.Kind == assertAbsent {
			name = "AbsentFromList"
		}
		w.linef("%sscenario.%s(", indent, name)
		if a.Call == nil {
			w.linef("%s\tnil,", indent)
		} else if err := goWriteCall(w, pkg, *a.Call, indent+"\t", "&"); err != nil {
			return err
		}
		w.linef("%s\t%q,", indent, a.ItemsPath)
		for _, path := range sortedValueKeys(a.Where) {
			value, err := goValue(a.Where[path], indent+"\t")
			if err != nil {
				return err
			}
			w.linef("%s\tscenario.Where(%q, %s),", indent, path, value)
		}
		w.linef("%s),", indent)
	case assertErrorCode:
		w.linef("%sscenario.ErrorCode(scenario.Error(%q, %q)),", indent, a.Error.Shape, a.Error.Code)
	case assertEventually:
		w.linef("%sscenario.Eventually(%d, %d,", indent, a.MaxAttempts, a.DelayMs)
		if err := goWriteClause(w, pkg, *a.Assert, indent+"\t"); err != nil {
			return err
		}
		w.linef("%s),", indent)
	default:
		return fmt.Errorf("cannot emit assertion kind %q", a.Kind)
	}
	return nil
}

// goWriteChecks emits a clause's checks in path order, so a failure message is
// the same on every run and in every backend.
func goWriteChecks(w *goWriter, checks map[string]check, indent string) {
	for _, path := range sortedCheckPaths(checks) {
		w.linef("%s%s,", indent, goCheck(path, checks[path], indent))
	}
}

func goCheck(path string, c check, indent string) string {
	switch {
	case c.NonEmpty:
		return fmt.Sprintf("scenario.NonEmpty(%q)", path)
	case c.IsList:
		return fmt.Sprintf("scenario.IsList(%q)", path)
	case c.Missing:
		return fmt.Sprintf("scenario.Missing(%q)", path)
	case c.Matches != "":
		return fmt.Sprintf("scenario.Matches(%q, %q)", path, c.Matches)
	default:
		value, err := goValue(c.Equals, indent)
		if err != nil {
			// Unreachable: goValue is total over the IR's value grammar, and
			// validateAssertion has already rejected anything else.
			value = fmt.Sprintf("%#v", c.Equals)
		}
		return fmt.Sprintf("scenario.Equals(%q, %s)", path, value)
	}
}

// ---------------------------------------------------------------------------
// The naming table — shared with -explain -lang go (explain_typed.go)
// ---------------------------------------------------------------------------

// goNamePackage is the Go SDK v2 package for a service: the SDK id lower-cased
// with spaces removed. Cost Explorer → costexplorer.
//
// compat/model/README.md § Naming records the two derivations known to break
// (SFN → sfn, ELB → elasticloadbalancing); neither service has a scenario, and
// the override table belongs here when one does.
func goNamePackage(sdkID string) string {
	return strings.ToLower(strings.ReplaceAll(sdkID, " ", ""))
}

// goNameModule is the import path of that package.
func goNameModule(sdkID string) string {
	return "github.com/aws/aws-sdk-go-v2/service/" + goNamePackage(sdkID)
}

// goNameInput is the operation's input struct: &sqs.CreateQueueInput{}.
func goNameInput(pkg, op string) string { return fmt.Sprintf("&%s.%sInput{}", pkg, op) }

// goNameInputType is that struct's type, for the assertion Send does.
func goNameInputType(pkg, op string) string { return fmt.Sprintf("*%s.%sInput", pkg, op) }

// goNameClientCall is the client method call, given an input already built.
func goNameClientCall(pkg, op string) string {
	return fmt.Sprintf("%s(ctx, in.(%s))", op, goNameInputType(pkg, op))
}

// goNameField is the Go field smithy-go generates for a modeled member: the
// member name with its first letter capitalized. Almost every AWS member is
// already PascalCase, but not all — SQS models CreateQueue's tags as `tags`
// and ListDeadLetterSourceQueues' page as `queueUrls`, and the Go SDK spells
// both with a capital.
//
// The member's own name stays the label the emitted b.Set carries, because
// that is what a failure message must name; only the field access is
// capitalized. The suite's internal/scenario applies the same rule when it
// walks a response path, and its exportedName carries the reasoning.
func goNameField(member string) string {
	if member == "" {
		return member
	}
	r := []rune(member)
	if !unicode.IsLower(r[0]) {
		return member
	}
	r[0] = unicode.ToUpper(r[0])
	return string(r)
}

// goInputLines renders the statements that build one call's typed input. It is
// the emitter's Build body and, line for line, what `-explain -lang go` prints,
// which is what keeps the two from drifting.
func goInputLines(pkg, op string, params map[string]any, indent string) ([]string, error) {
	lines := []string{fmt.Sprintf("in := %s", goNameInput(pkg, op))}
	for _, member := range sortedValueKeys(params) {
		value, err := goValue(params[member], indent)
		if err != nil {
			return nil, fmt.Errorf("%s.%s: %w", op, member, err)
		}
		lines = append(lines, fmt.Sprintf("b.Set(%q, &in.%s, %s)", member, goNameField(member), value))
	}
	return lines, nil
}

// goValueWidth is how wide a composite may be before it is written one entry
// per line. gofmt does not break a long literal, so a params map rendered
// inline stays inline however wide it gets, and a generated file nobody can
// read in a diff is a generated file nobody reviews.
const goValueWidth = 80

// goValue renders one IR value as a Go expression, indented for the line it
// will sit on.
//
// The IR's grammar maps onto Go data directly: an object is a map[string]any,
// a list a []any, a scalar itself, and each of the five expression forms is a
// scenario.Value constructor. Nothing else is representable, which is what
// makes this total.
func goValue(v any, indent string) (string, error) {
	if key, arg, ok := exprOf(v); ok {
		switch key {
		case "$lit":
			inner, err := goValue(arg, indent)
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("scenario.Lit(%s)", inner), nil
		case "$ref":
			return fmt.Sprintf("scenario.Ref(%q)", arg.(string)), nil
		case "$name":
			return fmt.Sprintf("scenario.Name(%q)", arg.(string)), nil
		case "$concat":
			var parts []string
			for _, part := range arg.([]any) {
				rendered, err := goValue(part, indent)
				if err != nil {
					return "", err
				}
				parts = append(parts, rendered)
			}
			return fmt.Sprintf("scenario.Concat(%s)", strings.Join(parts, ", ")), nil
		case "$index":
			pair := arg.([]any)
			inner, err := goValue(pair[0], indent)
			if err != nil {
				return "", err
			}
			n, err := integerOf(pair[1])
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("scenario.Index(%s, %d)", inner, n), nil
		}
	}
	switch value := v.(type) {
	case nil:
		return "nil", nil
	case string:
		return strconv.Quote(value), nil
	case bool:
		return strconv.FormatBool(value), nil
	case json.Number:
		return value.String(), nil
	case float64:
		return strconv.FormatFloat(value, 'g', -1, 64), nil
	case []any:
		items := make([]string, 0, len(value))
		for _, item := range value {
			rendered, err := goValue(item, indent+"\t")
			if err != nil {
				return "", err
			}
			items = append(items, rendered)
		}
		return goComposite("[]any{", items, indent), nil
	case map[string]any:
		var entries []string
		for _, k := range sortedKeys(value) {
			rendered, err := goValue(value[k], indent+"\t")
			if err != nil {
				return "", err
			}
			entries = append(entries, fmt.Sprintf("%q: %s", k, rendered))
		}
		return goComposite("map[string]any{", entries, indent), nil
	}
	return "", fmt.Errorf("no Go expression for %T", v)
}

// goComposite joins a composite literal's entries, on one line while that is
// readable and one per line when it is not.
func goComposite(open string, entries []string, indent string) string {
	inline := open + strings.Join(entries, ", ") + "}"
	if len(inline) <= goValueWidth {
		return inline
	}
	var b strings.Builder
	b.WriteString(open)
	for _, entry := range entries {
		b.WriteString("\n" + indent + "\t" + entry + ",")
	}
	b.WriteString("\n" + indent + "}")
	return b.String()
}

// goRawParams renders a call's params as the scenario file writes them —
// expressions unevaluated — for failure-message field 3 when a value could not
// be evaluated and nothing was sent. It is the same canonical JSON the
// interpreters print in that case.
func goRawParams(params map[string]any) (string, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if params == nil {
		params = map[string]any{}
	}
	if err := enc.Encode(params); err != nil {
		return "", err
	}
	raw := strings.TrimRight(buf.String(), "\n")
	if !strings.ContainsAny(raw, "`\r\n") {
		return "`" + raw + "`", nil
	}
	return strconv.Quote(raw), nil
}

// ---------------------------------------------------------------------------
// Identifiers
// ---------------------------------------------------------------------------

func goFileName(service string) string {
	return "scenarios_" + strings.ReplaceAll(service, "-", "_") + "_gen.go"
}

// goNameConstructor is the exported entry point the index file calls.
func goNameConstructor(service string) string { return "Scenarios" + goCamel(service) }

func goNameReceiver(service string) string { return goLowerCamel(service) + "Scenarios" }

func goNameGroupVar(group string) string { return "group" + goCamel(group) }

func goNameSetupMethod(group string) string { return "setup" + goCamel(group) }

func goNameTeardownMethod(group string) string { return "teardown" + goCamel(group) }

func goNameTestMethod(group, test string) string { return "test" + goCamel(group) + test }

// goCamel turns a kebab-case registry name into a Go identifier fragment:
// sqs-gen-queue → SqsGenQueue.
func goCamel(name string) string {
	var out strings.Builder
	for _, part := range strings.Split(name, "-") {
		out.WriteString(pascal(part))
	}
	return out.String()
}

func goLowerCamel(name string) string {
	camel := goCamel(name)
	if camel == "" {
		return camel
	}
	return strings.ToLower(camel[:1]) + camel[1:]
}

// goMethodNamesAreUnique refuses a service whose group and test names collide
// once folded into Go identifiers. Two names differing only in where their
// hyphens fall would otherwise emit two methods of the same name, and the
// suite would fail to build with no indication of which pair caused it.
func goMethodNamesAreUnique(service string, groups []group) error {
	seen := map[string]string{}
	claim := func(name, owner string) error {
		if first, dup := seen[name]; dup {
			return fmt.Errorf("%s: %s and %s both emit the Go method %s; rename one", service, first, owner, name)
		}
		seen[name] = owner
		return nil
	}
	for _, g := range groups {
		if err := claim(goNameSetupMethod(g.Name), g.Name+" setup"); err != nil {
			return err
		}
		if err := claim(goNameTeardownMethod(g.Name), g.Name+" teardown"); err != nil {
			return err
		}
		for _, t := range g.Tests {
			if err := claim(goNameTestMethod(g.Name, t.Name), g.Name+"/"+t.Name); err != nil {
				return err
			}
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Refusals
// ---------------------------------------------------------------------------

// goRefusals reports the members of a group's calls this backend cannot
// express. A group with any is not emitted and is scoped away from go-sdk.
func goRefusals(gen *generation, g group) []gap {
	var out []gap
	seen := map[string]bool{}
	record := func(op, member, detail string) {
		key := op + "." + member
		if seen[key] {
			return
		}
		seen[key] = true
		out = append(out, gap{
			Service:   gen.scenario.Service,
			Operation: op,
			Group:     g.Name,
			Reason:    goEmitReason + ":" + member,
			Detail:    detail,
		})
	}
	for _, c := range goCallsOf(g) {
		input := gen.model.InputShape(c.Op)
		if input == "" {
			continue
		}
		for _, member := range sortedValueKeys(c.Params) {
			target, ok := gen.model.MemberTarget(input, member)
			if !ok {
				continue // checked against the model already; not this backend's to report
			}
			if kind := gen.model.Kind(target); goUnsupportedKinds[kind] {
				record(c.Op, member, fmt.Sprintf("the go-sdk emitter has no Go value expression for a %s member (%s.%s)", kind, input, member))
			}
		}
	}
	sortGaps(out)
	return out
}

// goCallsOf collects every call a group makes: setup, each test's primary
// call, every clause's call however deeply an eventually nests it, and
// teardown.
func goCallsOf(g group) []call {
	var out []call
	out = append(out, g.Setup...)
	var fromClause func(a assertion)
	fromClause = func(a assertion) {
		if a.Call != nil {
			out = append(out, *a.Call)
		}
		if a.Assert != nil {
			fromClause(*a.Assert)
		}
	}
	for _, t := range g.Tests {
		out = append(out, t.Call)
		for _, a := range t.Assert {
			fromClause(a)
		}
	}
	out = append(out, g.Teardown...)
	return out
}

// ---------------------------------------------------------------------------
// The index file
// ---------------------------------------------------------------------------

// goIndexPath is the file groups.All() calls into. It is emitted whether or
// not go-sdk is a scenario backend, so the package compiles either way.
var goIndexPath = goSuiteDir + "/scenarios_gen.go"

// emitGoIndex renders the list of generated service constructors.
func emitGoIndex(services []string) ([]byte, error) {
	sorted := append([]string(nil), services...)
	sort.Strings(sorted)
	w := &goWriter{}
	w.linef("// Code generated by cmd/compatgen; DO NOT EDIT.")
	w.linef("")
	w.linef("package groups")
	w.linef("")
	w.linef("import %q", "github.com/overcast-sh/overcast-compat-go-sdk/internal/clients")
	w.linef("")
	w.linef("// scenarioGroups returns every generated service group, which All appends")
	w.linef("// to the hand-written ones. The list is empty until cmd/compatgen's")
	w.linef("// scenarioBackends table names go-sdk.")
	if len(sorted) == 0 {
		w.linef("func scenarioGroups(_ *clients.Clients) []ServiceGroup { return nil }")
	} else {
		w.linef("func scenarioGroups(c *clients.Clients) []ServiceGroup {")
		w.linef("\treturn []ServiceGroup{")
		for _, service := range sorted {
			w.linef("\t\t%s(c),", goNameConstructor(service))
		}
		w.linef("\t}")
		w.linef("}")
	}
	contents, err := format.Source([]byte(w.String()))
	if err != nil {
		return nil, fmt.Errorf("emitted Go index does not parse: %w\n%s", err, w.String())
	}
	return contents, nil
}

// ---------------------------------------------------------------------------
// Writing
// ---------------------------------------------------------------------------

// goWriter accumulates source lines. Everything it produces goes through
// go/format before it is written, so indentation here is a readability aid for
// whoever reads this file, not the output's actual layout.
type goWriter struct{ lines []string }

func (w *goWriter) linef(format string, args ...any) {
	w.lines = append(w.lines, fmt.Sprintf(format, args...))
}

func (w *goWriter) String() string { return strings.Join(w.lines, "\n") + "\n" }
