//go:build dev

package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// `-explain <group>/<test> -lang <language>` — docs/plans/compat-coverage-modelgen.md §3.2.
//
// Renders one generated test as idiomatic pseudo-code so a failing generated
// test can be reproduced by hand in seconds. It reads the committed scenario
// file, not the recipe, so what it shows is exactly what an interpreter
// executes. The renderings are pseudo-code: readable and faithful to each
// SDK's conventions, not guaranteed to compile.

type renderer func(s *scenario, g *group, t *test) string

var renderers = map[string]renderer{
	"python": renderPython,
	"node":   renderNode,
	"cli":    renderCLI,
	"go":     renderGo,
	"java":   renderJava,
	"dotnet": renderDotnet,
	"rust":   renderRust,
}

func runExplain(opts options, stdout io.Writer) error {
	render, ok := renderers[opts.lang]
	if !ok {
		return fmt.Errorf("unknown -lang %q; want one of %s", opts.lang, strings.Join(rendererNames(), ", "))
	}
	groupName, testName, ok := strings.Cut(opts.explain, "/")
	if !ok || groupName == "" || testName == "" {
		return fmt.Errorf("-explain wants <group>/<test>, got %q", opts.explain)
	}
	service, _, ok := strings.Cut(groupName, "-gen-")
	if !ok {
		return fmt.Errorf("%q is not a generated group name (<service>-gen-<resource>)", groupName)
	}
	s, err := readScenario(filepath.Join(opts.root, filepath.FromSlash(scenarioPath(service))))
	if err != nil {
		return err
	}
	g, t, ok := s.findTest(groupName, testName)
	if !ok {
		return fmt.Errorf("no test %s in %s", opts.explain, scenarioPath(service))
	}
	_, err = io.WriteString(stdout, render(s, g, t))
	return err
}

func rendererNames() []string {
	names := make([]string, 0, len(renderers))
	for name := range renderers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func readScenario(path string) (*scenario, error) {
	contents, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read scenario %s: %w", path, err)
	}
	var s scenario
	if err := decodeStrict(contents, &s); err != nil {
		return nil, fmt.Errorf("parse scenario %s: %w", path, err)
	}
	return &s, nil
}

// ---------------------------------------------------------------------------
// Shared rendering of values and paths
// ---------------------------------------------------------------------------

// A style says how a language spells the pieces the IR is made of.
type style struct {
	comment  string                  // line-comment prefix
	ref      func(ref string) string // a context lookup
	name     func(suffix string) string
	str      func(s string) string // a string literal
	concat   func(parts []string) string
	index    func(list string, i int) string
	object   func(entries [][2]string) string // key already rendered as a literal
	list     func(items []string) string
	pathExpr func(root, path string) string // response path access
	// call renders an operation call; the top-level members are rendered
	// individually so builder-style SDKs can spell them as setters.
	call     func(op string, members [][2]string) string
	tru, fls string
}

func (st style) value(v any) string {
	if key, arg, ok := exprOf(v); ok {
		switch key {
		case "$lit":
			return st.value(arg)
		case "$ref":
			return st.ref(arg.(string))
		case "$name":
			return st.name(arg.(string))
		case "$concat":
			var parts []string
			for _, part := range arg.([]any) {
				if s, isString := part.(string); isString {
					parts = append(parts, st.str(s))
				} else {
					parts = append(parts, st.value(part))
				}
			}
			return st.concat(parts)
		case "$index":
			pair := arg.([]any)
			i, _ := integerOf(pair[1])
			return st.index(st.value(pair[0]), i)
		}
	}
	switch value := v.(type) {
	case string:
		return st.str(value)
	case bool:
		if value {
			return st.tru
		}
		return st.fls
	case json.Number:
		return value.String()
	case float64:
		return fmt.Sprint(value)
	case nil:
		return "null"
	case []any:
		items := make([]string, 0, len(value))
		for _, item := range value {
			items = append(items, st.value(item))
		}
		return st.list(items)
	case map[string]any:
		var entries [][2]string
		for _, k := range sortedKeys(value) {
			entries = append(entries, [2]string{st.str(k), st.value(value[k])})
		}
		return st.object(entries)
	}
	return fmt.Sprint(v)
}

// members renders a params object member by member: [name, rendered value].
func (st style) members(params map[string]any) [][2]string {
	var out [][2]string
	for _, k := range sortedKeys(params) {
		out = append(out, [2]string{k, st.value(params[k])})
	}
	return out
}

// kv renders members as `k: v` pairs joined by sep.
func kv(members [][2]string, sep string, quoteKey bool) string {
	var parts []string
	for _, m := range members {
		key := m[0]
		if quoteKey {
			key = quote(key)
		}
		parts = append(parts, key+": "+m[1])
	}
	return strings.Join(parts, sep)
}

func quote(s string) string {
	contents, _ := json.Marshal(s)
	return string(contents)
}

// explainer assembles a test's steps as language-neutral events which each
// renderer formats: this keeps the seven renderers honest about the IR while
// letting them differ in syntax.
type explainer struct {
	st    style
	lines []string
}

func (e *explainer) linef(format string, args ...any) {
	e.lines = append(e.lines, fmt.Sprintf(format, args...))
}

func (e *explainer) commentf(format string, args ...any) {
	e.lines = append(e.lines, e.st.comment+" "+fmt.Sprintf(format, args...))
}

func (e *explainer) callLine(assign, op string, params map[string]any, export map[string]string) {
	e.linef("%s%s", assign, e.st.call(op, e.st.members(params)))
	for _, ctx := range sortedStringKeys(export) {
		e.commentf("export %s = %s", ctx, export[ctx])
	}
}

func (e *explainer) checkLines(root string, checks map[string]check) {
	for _, path := range sortedCheckPaths(checks) {
		c := checks[path]
		access := e.st.pathExpr(root, path)
		switch {
		case c.NonEmpty:
			e.linef("assert %s is present and not empty", access)
		case c.Equals != nil:
			e.linef("assert %s == %s", access, e.st.value(c.Equals))
		case c.Matches != "":
			e.linef("assert %s matches %s", access, e.st.str(c.Matches))
		case c.Missing:
			e.linef("assert %s is absent", access)
		}
	}
}

func (e *explainer) whereText(where map[string]any) string {
	var parts []string
	for _, path := range sortedValueKeys(where) {
		parts = append(parts, fmt.Sprintf("item%s == %s", strings.TrimPrefix(path, "$"), e.st.value(where[path])))
	}
	return strings.Join(parts, " and ")
}

func (e *explainer) assertion(a assertion, index int) {
	switch a.Kind {
	case assertResponseField:
		e.commentf("assert[%d] responseField", index)
		e.checkLines("resp", a.Checks)
	case assertReadback:
		e.commentf("assert[%d] readback", index)
		e.callLine("back = ", a.Call.Op, a.Call.Params, a.Call.Export)
		e.checkLines("back", a.Checks)
	case assertListContains:
		e.commentf("assert[%d] listContains", index)
		root := "resp"
		if a.Call != nil {
			e.callLine("listed = ", a.Call.Op, a.Call.Params, a.Call.Export)
			root = "listed"
		}
		e.linef("assert any(%s for item in %s)  %s non-empty, and contains the resource", e.whereText(a.Where), e.st.pathExpr(root, a.ItemsPath), e.st.comment)
	case assertAbsent:
		e.commentf("assert[%d] absent", index)
		if a.Error != nil {
			e.linef("expect %s to fail with %s (or %s)", e.st.call(a.Call.Op, e.st.members(a.Call.Params)), a.Error.Code, a.Error.Shape)
			return
		}
		root := "resp"
		if a.Call != nil {
			e.callLine("listed = ", a.Call.Op, a.Call.Params, a.Call.Export)
			root = "listed"
		}
		e.linef("assert not any(%s for item in %s)  %s a missing list counts as empty", e.whereText(a.Where), e.st.pathExpr(root, a.ItemsPath), e.st.comment)
	case assertErrorCode:
		e.commentf("assert[%d] errorCode: the call above must fail with %s (or %s)", index, a.Error.Code, a.Error.Shape)
	case assertEventually:
		e.commentf("assert[%d] eventually: retry the clause below up to %d times, %d ms apart, until it passes", index, a.MaxAttempts, a.DelayMs)
		e.assertion(*a.Assert, index)
	}
}

func (e *explainer) test(s *scenario, g *group, t *test, header func()) string {
	e.commentf("%s/%s — op %s (scenario %s)", g.Name, t.Name, t.Op, scenarioPath(s.Service))
	if len(t.Depends) > 0 {
		e.commentf("depends on %s", strings.Join(t.Depends, ", "))
	}
	header()
	expectsError := false
	for _, a := range t.Assert {
		if a.Kind == assertErrorCode {
			expectsError = true
		}
	}
	if expectsError {
		e.commentf("the primary call is expected to fail; catch the error and check its code")
	}
	e.callLine("resp = ", t.Call.Op, t.Call.Params, t.Call.Export)
	for i, a := range t.Assert {
		e.assertion(a, i)
	}
	return strings.Join(e.lines, "\n") + "\n"
}

// ---------------------------------------------------------------------------
// Per-language styles
// ---------------------------------------------------------------------------

func pyStyle() style {
	return style{
		comment: "#",
		ref:     func(ref string) string { return fmt.Sprintf("ctx[%s]", quote(ref)) },
		name:    func(suffix string) string { return fmt.Sprintf("f\"{run_id}-{group}-%s\"", suffix) },
		str:     quote,
		concat:  func(parts []string) string { return strings.Join(parts, " + ") },
		index:   func(list string, i int) string { return fmt.Sprintf("%s[%d]", list, i) },
		object: func(entries [][2]string) string {
			var parts []string
			for _, e := range entries {
				parts = append(parts, e[0]+": "+e[1])
			}
			return "{" + strings.Join(parts, ", ") + "}"
		},
		list:     func(items []string) string { return "[" + strings.Join(items, ", ") + "]" },
		pathExpr: func(root, path string) string { return root + pathAsSubscripts(path) },
		call: func(op string, members [][2]string) string {
			var args []string
			for _, m := range members {
				args = append(args, m[0]+"="+m[1])
			}
			return fmt.Sprintf("client.%s(%s)", snake(op), strings.Join(args, ", "))
		},
		tru: "True", fls: "False",
	}
}

func renderPython(s *scenario, g *group, t *test) string {
	e := &explainer{st: pyStyle()}
	return e.test(s, g, t, func() {
		e.linef("client = boto3.client(%s, endpoint_url=endpoint)  # sdkId %s, protocol %s", quote(cliService(s.Client)), s.Client.SDKID, s.Client.Protocol)
		e.linef("group = %s", quote(g.Name))
	})
}

func jsStyle() style {
	st := pyStyle()
	st.comment = "//"
	st.name = func(suffix string) string { return fmt.Sprintf("`${runId}-${group}-%s`", suffix) }
	st.object = func(entries [][2]string) string {
		var parts []string
		for _, e := range entries {
			parts = append(parts, e[0]+": "+e[1])
		}
		return "{ " + strings.Join(parts, ", ") + " }"
	}
	st.pathExpr = func(root, path string) string { return root + pathAsJS(path) }
	st.call = func(op string, members [][2]string) string {
		return fmt.Sprintf("await client.send(new %sCommand({ %s }))", op, kv(members, ", ", false))
	}
	st.tru, st.fls = "true", "false"
	return st
}

func renderNode(s *scenario, g *group, t *test) string {
	e := &explainer{st: jsStyle()}
	return e.test(s, g, t, func() {
		e.linef("const client = new %sClient({ endpoint });  // @aws-sdk/client-%s", pascalSDK(s.Client.SDKID), kebab(s.Client.SDKID))
		e.linef("const group = %s;", quote(g.Name))
	})
}

func cliStyle(client clientInfo) style {
	st := pyStyle()
	st.name = func(suffix string) string { return fmt.Sprintf("\"$RUN_ID-$GROUP-%s\"", suffix) }
	st.ref = func(ref string) string { return "$" + strings.ToUpper(strings.NewReplacer(".", "_").Replace(ref)) }
	st.pathExpr = func(root, path string) string {
		return fmt.Sprintf("jq '%s' <<< \"$%s\"", strings.TrimPrefix(path, "$"), root)
	}
	st.call = func(op string, members [][2]string) string {
		return fmt.Sprintf("$(aws %s %s --cli-input-json '{%s}')", cliService(client), kebab(op), kv(members, ", ", true))
	}
	st.tru, st.fls = "true", "false"
	return st
}

func renderCLI(s *scenario, g *group, t *test) string {
	e := &explainer{st: cliStyle(s.Client)}
	return e.test(s, g, t, func() {
		e.linef("export AWS_ENDPOINT_URL=$ENDPOINT  # service command %q from endpointPrefix %q", cliService(s.Client), s.Client.EndpointPrefix)
		e.linef("GROUP=%s", quote(g.Name))
	})
}

func goStyle() style {
	st := jsStyle()
	st.name = func(suffix string) string { return fmt.Sprintf("runID + \"-\" + group + \"-%s\"", suffix) }
	st.object = func(entries [][2]string) string {
		var parts []string
		for _, e := range entries {
			parts = append(parts, e[0]+": "+e[1])
		}
		return "map[string]any{" + strings.Join(parts, ", ") + "}"
	}
	st.list = func(items []string) string { return "[]T{" + strings.Join(items, ", ") + "}" }
	st.pathExpr = func(root, path string) string { return root + pathAsGo(path) }
	st.call = func(op string, members [][2]string) string {
		return fmt.Sprintf("client.%s(ctx, &%sInput{%s})", op, op, kv(members, ", ", false))
	}
	return st
}

func renderGo(s *scenario, g *group, t *test) string {
	e := &explainer{st: goStyle()}
	return e.test(s, g, t, func() {
		e.linef("client := %s.NewFromConfig(cfg)  // github.com/aws/aws-sdk-go-v2/service/%s", goPackage(s.Client.SDKID), goPackage(s.Client.SDKID))
		e.linef("group := %s", quote(g.Name))
	})
}

func javaStyle() style {
	st := goStyle()
	st.name = func(suffix string) string { return fmt.Sprintf("runId + \"-\" + group + \"-%s\"", suffix) }
	st.object = func(entries [][2]string) string {
		var parts []string
		for _, e := range entries {
			parts = append(parts, e[0]+", "+e[1])
		}
		return "Map.of(" + strings.Join(parts, ", ") + ")"
	}
	st.list = func(items []string) string { return "List.of(" + strings.Join(items, ", ") + ")" }
	st.pathExpr = func(root, path string) string { return root + pathAsGetters(path, "()") }
	st.call = func(op string, members [][2]string) string {
		var setters []string
		for _, m := range members {
			setters = append(setters, "."+lowerFirst(m[0])+"("+m[1]+")")
		}
		return fmt.Sprintf("client.%s(%sRequest.builder()%s.build())", lowerFirst(op), op, strings.Join(setters, ""))
	}
	return st
}

func renderJava(s *scenario, g *group, t *test) string {
	e := &explainer{st: javaStyle()}
	return e.test(s, g, t, func() {
		e.linef("%sClient client = %sClient.builder().endpointOverride(endpoint).build();", pascalSDK(s.Client.SDKID), pascalSDK(s.Client.SDKID))
		e.linef("String group = %s;", quote(g.Name))
	})
}

func dotnetStyle() style {
	st := goStyle()
	st.object = func(entries [][2]string) string {
		var parts []string
		for _, e := range entries {
			parts = append(parts, "{ "+e[0]+", "+e[1]+" }")
		}
		return "new Dictionary<string, T> { " + strings.Join(parts, ", ") + " }"
	}
	st.list = func(items []string) string { return "new List<T> { " + strings.Join(items, ", ") + " }" }
	st.pathExpr = func(root, path string) string { return root + pathAsGetters(path, "") }
	st.call = func(op string, members [][2]string) string {
		var parts []string
		for _, m := range members {
			parts = append(parts, m[0]+" = "+m[1])
		}
		return fmt.Sprintf("await client.%sAsync(new %sRequest { %s })", op, op, strings.Join(parts, ", "))
	}
	return st
}

func renderDotnet(s *scenario, g *group, t *test) string {
	e := &explainer{st: dotnetStyle()}
	return e.test(s, g, t, func() {
		e.linef("var client = new Amazon%sClient(new Amazon%sConfig { ServiceURL = endpoint });", pascalSDK(s.Client.SDKID), pascalSDK(s.Client.SDKID))
		e.linef("var group = %s;", quote(g.Name))
	})
}

func rustStyle() style {
	st := goStyle()
	st.name = func(suffix string) string { return fmt.Sprintf("format!(\"{run_id}-{group}-%s\")", suffix) }
	st.object = func(entries [][2]string) string {
		var parts []string
		for _, e := range entries {
			parts = append(parts, "("+e[0]+", "+e[1]+")")
		}
		return "HashMap::from([" + strings.Join(parts, ", ") + "])"
	}
	st.list = func(items []string) string { return "vec![" + strings.Join(items, ", ") + "]" }
	st.pathExpr = func(root, path string) string { return root + pathAsGetters(path, "()") }
	st.call = func(op string, members [][2]string) string {
		var setters []string
		for _, m := range members {
			setters = append(setters, "."+snake(m[0])+"("+m[1]+")")
		}
		return fmt.Sprintf("client.%s()%s.send().await?", snake(op), strings.Join(setters, ""))
	}
	return st
}

func renderRust(s *scenario, g *group, t *test) string {
	e := &explainer{st: rustStyle()}
	return e.test(s, g, t, func() {
		e.linef("let client = aws_sdk_%s::Client::new(&config);  // endpoint_url set on the config", strings.ReplaceAll(kebab(s.Client.SDKID), "-", "_"))
		e.linef("let group = %s;", quote(g.Name))
	})
}

// ---------------------------------------------------------------------------
// Naming helpers — the derivations compat/model/README.md documents
// ---------------------------------------------------------------------------

// pathAsSubscripts renders $.A.B[0] as ["A"]["B"][0].
func pathAsSubscripts(path string) string {
	var out strings.Builder
	for _, segment := range mustPath(path).segments {
		if segment.index >= 0 {
			fmt.Fprintf(&out, "[%d]", segment.index)
		} else {
			fmt.Fprintf(&out, "[%s]", quote(segment.name))
		}
	}
	return out.String()
}

// pathAsJS renders $.A.B[0] as .A.B[0].
func pathAsJS(path string) string {
	return strings.TrimPrefix(path, "$")
}

// pathAsGo renders $.A.B[0] as .A.B[0].
func pathAsGo(path string) string {
	var out strings.Builder
	for _, segment := range mustPath(path).segments {
		if segment.index >= 0 {
			fmt.Fprintf(&out, "[%d]", segment.index)
		} else {
			fmt.Fprintf(&out, ".%s", segment.name)
		}
	}
	return out.String()
}

// pathAsGetters renders $.A.B[0] as .a().b().get(0) (Java, Rust) or .A.B[0]
// (.NET, with call="").
func pathAsGetters(path string, call string) string {
	var out strings.Builder
	for _, segment := range mustPath(path).segments {
		switch {
		case segment.index >= 0 && call != "":
			fmt.Fprintf(&out, ".get(%d)", segment.index)
		case segment.index >= 0:
			fmt.Fprintf(&out, "[%d]", segment.index)
		case call != "":
			fmt.Fprintf(&out, ".%s%s", lowerFirst(segment.name), call)
		default:
			fmt.Fprintf(&out, ".%s", segment.name)
		}
	}
	return out.String()
}

// snake turns PascalCase into snake_case: SendMessageBatch → send_message_batch.
func snake(name string) string {
	var out strings.Builder
	runes := []rune(name)
	for i, r := range runes {
		upper := r >= 'A' && r <= 'Z'
		if upper && i > 0 {
			prevUpper := runes[i-1] >= 'A' && runes[i-1] <= 'Z'
			nextLower := i+1 < len(runes) && runes[i+1] >= 'a' && runes[i+1] <= 'z'
			if !prevUpper || nextLower {
				out.WriteByte('_')
			}
		}
		if upper {
			r += 'a' - 'A'
		}
		out.WriteRune(r)
	}
	return out.String()
}

// kebab turns PascalCase or an SDK ID into kebab-case: "Cost Explorer" →
// cost-explorer, SendMessageBatch → send-message-batch.
func kebab(name string) string {
	return strings.ReplaceAll(snake(strings.ReplaceAll(name, " ", "")), "_", "-")
}

// pascalSDK renders an SDK ID as a class prefix: "SQS" → SQS, "Cost
// Explorer" → CostExplorer.
func pascalSDK(sdkID string) string { return strings.ReplaceAll(sdkID, " ", "") }

func goPackage(sdkID string) string {
	return strings.ToLower(strings.ReplaceAll(sdkID, " ", ""))
}

func lowerFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToLower(s[:1]) + s[1:]
}

// cliService is the aws CLI command name: botocore's service name, which is
// the endpoint prefix for every service in the pilot. The README flags the
// services where that derivation is known to differ.
func cliService(c clientInfo) string {
	if c.EndpointPrefix != "" {
		return c.EndpointPrefix
	}
	return strings.ToLower(c.SDKID)
}
