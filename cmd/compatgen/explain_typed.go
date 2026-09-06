//go:build dev

package main

import (
	"fmt"
	"strings"
)

// Renderings for the backends whose SDKs are typed: a call is a request
// struct or builder, and a response is read through fields or getters rather
// than by subscripting a map. The data-shaped backends are in
// explain_dynamic.go; the shared machinery is in explain.go.

// typedStyle is what the four typed backends share: a request object built
// from named members, and a response read through fields or getters. Each of
// them overrides the call and the object/list syntax; go additionally renders
// through its emitter (goStyle below).
func typedStyle() style {
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

// goStyle renders a call through the emitter's own naming table (emit_go.go),
// so `-explain -lang go` prints the statements cmd/compatgen writes into
// compat/suites/go-sdk/internal/groups/scenarios_*_gen.go rather than a second
// description of them. The definition of done for the Go backend asks for one
// naming table; this is how there comes to be only one.
//
// The pkg it renders against is the scenario's own, so the reader can paste
// the lines under the client the header line builds.
func goStyle(sdkID string) style {
	st := typedStyle()
	pkg := goNamePackage(sdkID)
	st.callLines = func(op string, params map[string]any) []string {
		lines, err := goInputLines(pkg, op, params, "")
		if err != nil {
			// Unreachable for a committed scenario: the emitter refuses at
			// generation time what it cannot render, so a value this cannot
			// spell never reaches a scenario file.
			return []string{fmt.Sprintf("// %v", err)}
		}
		return append(lines, fmt.Sprintf("g.cl().%s(ctx, in)", op))
	}
	return st
}

func renderGo(s *scenario, g *group, t *test) string {
	e := &explainer{st: goStyle(s.Client.SDKID)}
	return e.test(s, g, t, func() {
		e.linef("client := %s.NewFromConfig(cfg)  // %s", goNamePackage(s.Client.SDKID), goNameModule(s.Client.SDKID))
		e.linef("group := %s", quote(g.Name))
		e.commentf("b is the scenario.Binder the generated Build closure receives")
	})
}

func javaStyle() style {
	st := typedStyle()
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
	st := typedStyle()
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
	st := typedStyle()
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
// Path rendering for the typed backends
// ---------------------------------------------------------------------------

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
