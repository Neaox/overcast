//go:build dev

// Command capgen is a developer tool that cross-checks handler operation
// registrations against capabilities_dev.go declarations, and can generate
// the static capability snapshot (all.gen.go) and regenerate docs tables.
//
// Usage:
//
// go run -tags dev ./cmd/capgen [flags]
//
// Flags:
//
// --check        verify handler ops match declared capabilities; exit 1 on mismatch
// --generate     write internal/capabilities/all.gen.go
// --write-docs   regenerate sentinel-bracketed tables in docs/services/*.md
// --service      limit to one service name (default: all)
// --workspace    workspace root (default: directory containing go.mod)
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

// CapabilityDecl is a capability entry parsed from a capabilities_dev.go file.
type CapabilityDecl struct {
	Service     string
	Operation   string
	Category    string
	Status      string // e.g. "StatusSupported"
	Notes       string
	DocsURL     string
	DisplayName string
	DocOnly     bool
	Since       string
}

// Operation is a handler operation extracted from service source files.
type Operation struct {
	Name   string
	IsStub bool
}

func main() {
	var (
		workspace = flag.String("workspace", ".", "workspace root (directory with go.mod)")
		check     = flag.Bool("check", false, "check capabilities against handler ops; exit 1 on mismatch")
		generate  = flag.Bool("generate", false, "generate internal/capabilities/all.gen.go")
		initCaps  = flag.Bool("init", false, "generate missing capabilities_dev.go files from detected handler ops")
		writeDocs = flag.Bool("write-docs", false, "regenerate sentinel-bracketed tables in docs/services/*.md")
		initDocs  = flag.Bool("init-docs", false, "add sentinel markers to docs that don't have them yet")
		service   = flag.String("service", "", "limit to one service (all if empty)")
	)
	flag.Parse()

	if !*check && !*generate && !*writeDocs && !*initCaps && !*initDocs {
		flag.Usage()
		fmt.Fprintln(os.Stderr, "\ncapgen: no action specified; use --check, --generate, --write-docs, --init, or --init-docs")
		os.Exit(1)
	}

	root, err := findWorkspaceRoot(*workspace)
	if err != nil {
		fatalf("workspace root: %v", err)
	}

	services, err := listServices(root)
	if err != nil {
		fatalf("listing services: %v", err)
	}
	if *service != "" {
		services = []string{strings.ToLower(*service)}
	}

	failures := 0
	var allCaps []CapabilityDecl

	for _, svc := range services {
		svcDir := filepath.Join(root, "internal", "services", serviceDir(svc))
		caps, parseErr := parseCapabilitiesFile(svcDir, svc)
		if parseErr != nil && !os.IsNotExist(parseErr) {
			fmt.Fprintf(os.Stderr, "capgen: %s: parse capabilities_dev.go: %v\n", svc, parseErr)
		}
		// Tag each cap with the service name derived from directory
		for i := range caps {
			if caps[i].Service == "" {
				caps[i].Service = svc
			}
		}
		allCaps = append(allCaps, caps...)

		if *check {
			ops, comprehensive, opsErr := parseHandlerOps(svcDir)
			if opsErr != nil {
				fmt.Fprintf(os.Stderr, "capgen: %s: parse handlers: %v\n", svc, opsErr)
				continue
			}
			if len(ops) == 0 {
				// No action-dispatch ops detected (likely a REST-routed service).
				// Cross-check is not possible; skip silently.
				continue
			}
			failures += checkService(svc, ops, caps, comprehensive)
		}
	}

	if *generate {
		if err := generateAllGenGo(root, allCaps); err != nil {
			fatalf("writing all.gen.go: %v", err)
		}
		fmt.Printf("capgen: wrote internal/capabilities/all.gen.go (%d capabilities)\n", len(allCaps))
	}

	if *writeDocs {
		for _, svc := range services {
			svcCaps := capsByService(allCaps, svc)
			if len(svcCaps) == 0 {
				continue
			}
			docPath := filepath.Join(root, "docs", "services", svc+".md")
			if _, statErr := os.Stat(docPath); os.IsNotExist(statErr) {
				continue
			}
			if writeErr := writeDocTable(docPath, svc, svcCaps); writeErr != nil {
				fmt.Fprintf(os.Stderr, "capgen: %s: write docs: %v\n", svc, writeErr)
			} else {
				fmt.Printf("capgen: updated docs/services/%s.md\n", svc)
			}
		}
		if changed, err := updateStatusMd(root, allCaps); err != nil {
			fmt.Fprintf(os.Stderr, "capgen: STATUS.md: %v\n", err)
		} else if changed {
			fmt.Println("capgen: updated STATUS.md op counts")
		}
		if changed, err := updateDocsReadmeServiceIndex(root, allCaps); err != nil {
			fmt.Fprintf(os.Stderr, "capgen: docs/README.md: %v\n", err)
		} else if changed {
			fmt.Println("capgen: updated docs/README.md service index")
		}
		if err := generateServiceSupportJSON(root, allCaps); err != nil {
			fmt.Fprintf(os.Stderr, "capgen: service-support.json: %v\n", err)
		} else {
			fmt.Println("capgen: wrote docs/generated/service-support.json")
		}
	}

	if *initCaps {
		for _, svc := range services {
			svcDir := filepath.Join(root, "internal", "services", serviceDir(svc))
			capsPath := filepath.Join(svcDir, "capabilities_dev.go")
			if _, statErr := os.Stat(capsPath); statErr == nil {
				// Already exists — skip.
				continue
			}
			ops, _, opsErr := parseHandlerOps(svcDir)
			if opsErr != nil {
				fmt.Fprintf(os.Stderr, "capgen: %s: parse handlers: %v\n", svc, opsErr)
				continue
			}
			if len(ops) == 0 {
				fmt.Fprintf(os.Stderr, "capgen: %s: no ops detected; skipping init\n", svc)
				continue
			}
			if writeErr := writeInitialCapabilities(capsPath, svc, ops); writeErr != nil {
				fmt.Fprintf(os.Stderr, "capgen: %s: write capabilities_dev.go: %v\n", svc, writeErr)
			} else {
				fmt.Printf("capgen: created internal/services/%s/capabilities_dev.go (%d ops)\n", svc, len(ops))
			}
		}
	}

	if *initDocs {
		for _, svc := range services {
			docPath := filepath.Join(root, "docs", "services", svc+".md")
			if _, statErr := os.Stat(docPath); os.IsNotExist(statErr) {
				continue
			}
			if err := addSentinelMarkers(docPath, svc); err != nil {
				fmt.Fprintf(os.Stderr, "capgen: %s: add sentinels: %v\n", svc, err)
			} else {
				fmt.Printf("capgen: added sentinels to docs/services/%s.md\n", svc)
			}
		}
	}

	if failures > 0 {
		os.Exit(1)
	}
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "capgen: "+format+"\n", args...)
	os.Exit(1)
}

// writeInitialCapabilities generates a capabilities_dev.go from detected handler ops.
// All implemented ops get StatusSupported; all stub ops get StatusUnsupported.
func writeInitialCapabilities(path, svc string, ops []Operation) error {
	var buf bytes.Buffer
	buf.WriteString("//go:build dev\n\n")
	buf.WriteString("package " + svc + "\n\n")
	buf.WriteString("import \"github.com/Neaox/overcast/internal/capabilities\"\n\n")
	buf.WriteString("func init() {\n")
	buf.WriteString("\tcapabilities.Default.Register(\n")
	for _, op := range ops {
		status := "capabilities.StatusSupported"
		notes := ""
		if op.IsStub {
			status = "capabilities.StatusUnsupported"
			notes = "stub; returns 501"
		}
		buf.WriteString("\t\t{Service: \"" + svc + "\", Operation: \"" + op.Name + "\", Category: \"General\", Status: " + status)
		if notes != "" {
			buf.WriteString(", Notes: \"" + notes + "\"")
		}
		buf.WriteString("},\n")
	}
	buf.WriteString("\t)\n}\n")
	return os.WriteFile(path, buf.Bytes(), 0o644)
}

// addSentinelMarkers inserts <!-- BEGIN/END overcast:capabilities --> markers into a doc file.
// If markers are already present, this is a no-op. The markers are inserted before the last
// "## Known limitations" or "## Notes" section, or appended before any trailing "---" separator.
func addSentinelMarkers(docPath, svc string) error {
	data, err := os.ReadFile(docPath)
	if err != nil {
		return err
	}
	content := string(data)
	const beginMarker = "<!-- BEGIN overcast:capabilities -->"
	const endMarker = "<!-- END overcast:capabilities -->"
	if strings.Contains(content, beginMarker) {
		return nil // already present
	}
	sentinels := "\n" + beginMarker + "\n" + endMarker + "\n"

	// Prefer the existing manual table location so generated tables replace the
	// manual section in-place rather than drifting below later narrative notes.
	if anchor := findManualTableAnchor(content); anchor >= 0 {
		content = content[:anchor] + sentinels + "\n" + content[anchor:]
		return os.WriteFile(docPath, []byte(content), 0o644)
	}

	// Find the first heading that signals end of the generated zone.
	// Prefer "## Known limitations", "## Notes", or "## Known issues".
	for _, heading := range []string{"## Known limitations", "## Notes", "## Known issues"} {
		idx := strings.Index(content, "\n"+heading)
		if idx >= 0 {
			content = content[:idx] + sentinels + "\n" + content[idx+1:]
			return os.WriteFile(docPath, []byte(content), 0o644)
		}
	}
	// Fallback: insert before the last "---" separator if any.
	lastSep := strings.LastIndex(content, "\n---\n")
	if lastSep >= 0 {
		content = content[:lastSep] + "\n" + sentinels + content[lastSep:]
		return os.WriteFile(docPath, []byte(content), 0o644)
	}
	// Final fallback: append.
	content = strings.TrimRight(content, "\n") + "\n" + sentinels + "\n"
	return os.WriteFile(docPath, []byte(content), 0o644)
}

// findWorkspaceRoot walks up from start until it finds go.mod.
func findWorkspaceRoot(start string) (string, error) {
	abs, err := filepath.Abs(start)
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(abs, "go.mod")); err == nil {
			return abs, nil
		}
		parent := filepath.Dir(abs)
		if parent == abs {
			return "", fmt.Errorf("go.mod not found in %s or any parent", start)
		}
		abs = parent
	}
}

func listServices(root string) ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(root, "internal", "services"))
	if err != nil {
		return nil, err
	}
	var services []string
	for _, e := range entries {
		if e.IsDir() {
			services = append(services, e.Name())
		}
	}
	// Include known sub-services that have their own capabilities files.
	for name := range subServices {
		services = append(services, name)
	}
	sort.Strings(services)
	return services, nil
}

// subServices maps virtual service names to their directory path under internal/services/.
var subServices = map[string]string{
	"cloudwatch-logs": "cloudwatch/logs",
}

// serviceDir returns the directory path for a service relative to root/internal/services/.
func serviceDir(svc string) string {
	if sub, ok := subServices[svc]; ok {
		return sub
	}
	return svc
}

// parseHandlerOps extracts operation names from handler source files in svcDir.
// It detects two registration patterns:
//
//  1. Map keys in map[string]http.HandlerFunc{...} literals.
//  2. Case strings in switch statements that have 3+ PascalCase operation names.
//
// Stub operations are detected by finding methods that call protocol.NotImplemented*.
// The second return value is true when at least one map-based registration was found
// (i.e., detection is comprehensive). When false, only switch-dispatch ops were found,
// which means the service uses REST routing for its primary dispatch and ORPHAN
// violations should not be treated as failures.
func parseHandlerOps(svcDir string) ([]Operation, bool, error) {
	entries, err := os.ReadDir(svcDir)
	if err != nil {
		return nil, false, err
	}

	fset := token.NewFileSet()
	stubMethods := map[string]struct{}{}

	// Pass 1: collect stub method names.
	for _, e := range entries {
		if shouldSkipFile(e) {
			continue
		}
		absPath := filepath.Join(svcDir, e.Name())
		f, err := parseGoFile(fset, absPath)
		if err != nil {
			continue
		}
		ast.Inspect(f, func(n ast.Node) bool {
			fd, ok := n.(*ast.FuncDecl)
			if !ok || fd.Recv == nil || fd.Body == nil {
				return true
			}
			// A method is a stub only if its body directly calls protocol.NotImplemented*.
			// Do not rely on file name (handler_stubs.go may contain real implementations).
			if containsNotImplementedCall(fd.Body) {
				stubMethods[fd.Name.Name] = struct{}{}
			}
			return true
		})
	}

	// Pass 2: collect operation names from handler registrations.
	seen := map[string]struct{}{}
	var ops []Operation
	hasMap := false // true when a map[string]http.HandlerFunc was detected

	for _, e := range entries {
		if shouldSkipFile(e) {
			continue
		}
		absPath := filepath.Join(svcDir, e.Name())
		f, err := parseGoFile(fset, absPath)
		if err != nil {
			continue
		}

		// Pattern 1: map[string]http.HandlerFunc{...} literals.
		ast.Inspect(f, func(n ast.Node) bool {
			cl, ok := n.(*ast.CompositeLit)
			if !ok || !isHandlerFuncMap(cl) {
				return true
			}
			hasMap = true
			for _, elt := range cl.Elts {
				kv, ok := elt.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				lit, ok := kv.Key.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					continue
				}
				opName := strings.Trim(lit.Value, `"`)
				if !isAWSOperation(opName) {
					continue
				}
				if _, exists := seen[opName]; exists {
					continue
				}
				seen[opName] = struct{}{}
				isStub := false
				if sel, ok := kv.Value.(*ast.SelectorExpr); ok {
					if _, found := stubMethods[sel.Sel.Name]; found {
						isStub = true
					}
				}
				ops = append(ops, Operation{Name: opName, IsStub: isStub})
			}
			return true
		})

		// Pattern 2: switch statements with PascalCase string cases.
		ast.Inspect(f, func(n ast.Node) bool {
			sw, ok := n.(*ast.SwitchStmt)
			if !ok {
				return true
			}
			if !isOperationSwitch(sw) {
				return true
			}
			awsCases := collectAWSCasesFromSwitch(sw)
			if len(awsCases) < 3 {
				return true
			}
			for _, opName := range awsCases {
				if _, exists := seen[opName]; exists {
					continue
				}
				seen[opName] = struct{}{}
				ops = append(ops, Operation{Name: opName, IsStub: false})
			}
			return true
		})
	}

	sort.Slice(ops, func(i, j int) bool { return ops[i].Name < ops[j].Name })
	return ops, hasMap, nil
}

func isOperationSwitch(sw *ast.SwitchStmt) bool {
	if sw.Tag == nil {
		return false
	}
	name := strings.ToLower(exprName(sw.Tag))
	return name == "action" || name == "operation" || name == "op" || strings.HasSuffix(name, "action") || strings.HasSuffix(name, "operation")
}

func exprName(expr ast.Expr) string {
	switch v := expr.(type) {
	case *ast.Ident:
		return v.Name
	case *ast.SelectorExpr:
		return v.Sel.Name
	case *ast.CallExpr:
		return exprName(v.Fun)
	case *ast.ParenExpr:
		return exprName(v.X)
	default:
		return ""
	}
}

func shouldSkipFile(e os.DirEntry) bool {
	if e.IsDir() {
		return true
	}
	name := e.Name()
	if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
		return true
	}
	if name == "capabilities_dev.go" {
		return true
	}
	return false
}

func parseGoFile(fset *token.FileSet, path string) (*ast.File, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return parser.ParseFile(fset, path, src, 0)
}

func containsNotImplementedCall(body *ast.BlockStmt) bool {
	found := false
	ast.Inspect(body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if strings.HasPrefix(sel.Sel.Name, "NotImplemented") {
			found = true
		}
		return true
	})
	return found
}

func isHandlerFuncMap(cl *ast.CompositeLit) bool {
	mt, ok := cl.Type.(*ast.MapType)
	if !ok {
		return false
	}
	keyIdent, ok := mt.Key.(*ast.Ident)
	if !ok || keyIdent.Name != "string" {
		return false
	}
	valSel, ok := mt.Value.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	return valSel.Sel.Name == "HandlerFunc"
}

func collectAWSCasesFromSwitch(sw *ast.SwitchStmt) []string {
	var cases []string
	for _, s := range sw.Body.List {
		cc, ok := s.(*ast.CaseClause)
		if !ok || len(cc.List) == 0 {
			continue
		}
		lit, ok := cc.List[0].(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			continue
		}
		val := strings.Trim(lit.Value, `"`)
		if isAWSOperation(val) && !isKnownNonOperationCase(val) {
			cases = append(cases, val)
		}
	}
	return cases
}

func isKnownNonOperationCase(s string) bool {
	switch s {
	case "GreaterThanThreshold", "GreaterThanOrEqualToThreshold", "LessThanThreshold", "LessThanOrEqualToThreshold":
		return true
	default:
		return false
	}
}

// isAWSOperation returns true if s looks like an AWS API operation name (PascalCase, 3-80 chars).
// All-caps identifiers (e.g. "AWS", "HTTP", "MOCK") are excluded because AWS operation names
// are always mixed-case PascalCase and never consist entirely of uppercase letters.
func isAWSOperation(s string) bool {
	if len(s) < 3 || len(s) > 80 {
		return false
	}
	if !unicode.IsUpper(rune(s[0])) {
		return false
	}
	hasLower := false
	for _, r := range s {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			return false
		}
		if unicode.IsLower(r) {
			hasLower = true
		}
	}
	// Require at least one lowercase letter to distinguish PascalCase from ALL_CAPS constants.
	return hasLower
}

// parseCapabilitiesFile parses capabilities_dev.go in svcDir and extracts Capability literals.
// If a Capability literal omits the Service field (e.g. when using RegisterForService),
// the svc parameter is used as the fallback so the generated docs and all.gen.go stay correct.
func parseCapabilitiesFile(svcDir, svc string) ([]CapabilityDecl, error) {
	path := filepath.Join(svcDir, "capabilities_dev.go")
	src, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, os.ErrNotExist
	}
	if err != nil {
		return nil, err
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, src, 0)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	stringConsts := collectFileStringConsts(f)
	var caps []CapabilityDecl
	ast.Inspect(f, func(n ast.Node) bool {
		cl, ok := n.(*ast.CompositeLit)
		if !ok || !isCapabilityLit(cl) {
			return true
		}
		c := CapabilityDecl{}
		for _, elt := range cl.Elts {
			kv, ok := elt.(*ast.KeyValueExpr)
			if !ok {
				continue
			}
			key, ok := kv.Key.(*ast.Ident)
			if !ok {
				continue
			}
			switch key.Name {
			case "Service":
				c.Service = stringExpr(kv.Value, stringConsts)
			case "Operation":
				c.Operation = stringExpr(kv.Value, stringConsts)
			case "Category":
				c.Category = stringExpr(kv.Value, stringConsts)
			case "Status":
				c.Status = selectorOrIdent(kv.Value)
			case "Notes":
				c.Notes = stringExpr(kv.Value, stringConsts)
			case "DocsURL":
				c.DocsURL = stringExpr(kv.Value, stringConsts)
			case "DisplayName":
				c.DisplayName = stringExpr(kv.Value, stringConsts)
			case "DocOnly":
				c.DocOnly = boolLit(kv.Value)
			case "Since":
				c.Since = stringExpr(kv.Value, stringConsts)
			}
		}
		if c.Operation != "" {
			if c.Service == "" {
				c.Service = svc // filled in by RegisterForService at runtime; use dir name here
			}
			caps = append(caps, c)
		}
		return true
	})
	return caps, nil
}

func collectFileStringConsts(f *ast.File) map[string]string {
	out := make(map[string]string)
	for _, decl := range f.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}
		for _, spec := range gen.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok || len(vs.Names) == 0 || len(vs.Values) == 0 {
				continue
			}
			for i, name := range vs.Names {
				v := vs.Values[0]
				if i < len(vs.Values) {
					v = vs.Values[i]
				}
				if s := stringLit(v); s != "" {
					out[name.Name] = s
				}
			}
		}
	}
	return out
}

func stringExpr(e ast.Expr, consts map[string]string) string {
	if s := stringLit(e); s != "" {
		return s
	}
	id, ok := e.(*ast.Ident)
	if !ok {
		return ""
	}
	return consts[id.Name]
}

func isCapabilityLit(cl *ast.CompositeLit) bool {
	if cl.Type == nil {
		return false
	}
	if sel, ok := cl.Type.(*ast.SelectorExpr); ok {
		return sel.Sel.Name == "Capability"
	}
	if id, ok := cl.Type.(*ast.Ident); ok {
		return id.Name == "Capability"
	}
	return false
}

func stringLit(e ast.Expr) string {
	lit, ok := e.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return ""
	}
	// Parse string literals using Go unquoting semantics so escaped
	// characters are rendered correctly in generated docs.
	v, err := strconv.Unquote(lit.Value)
	if err != nil {
		return strings.Trim(lit.Value, `"`)
	}
	return v
}

func selectorOrIdent(e ast.Expr) string {
	if sel, ok := e.(*ast.SelectorExpr); ok {
		return sel.Sel.Name
	}
	if id, ok := e.(*ast.Ident); ok {
		return id.Name
	}
	return ""
}

func boolLit(e ast.Expr) bool {
	id, ok := e.(*ast.Ident)
	if !ok {
		return false
	}
	return id.Name == "true"
}

// checkService reports missing and orphaned capability entries for a service.
// Returns the number of violations found.
// When comprehensive is false (ops detected only via switch-case, not map),
// ORPHAN entries are printed as warnings but do not count as violations, because
// REST-routed operations cannot be detected by static analysis.
func checkService(service string, ops []Operation, caps []CapabilityDecl, comprehensive bool) int {
	capByOp := make(map[string]CapabilityDecl, len(caps))
	for _, c := range caps {
		capByOp[c.Operation] = c
	}
	opByName := make(map[string]Operation, len(ops))
	for _, op := range ops {
		opByName[op.Name] = op
	}

	violations := 0
	for _, op := range ops {
		cap, declared := capByOp[op.Name]
		if !declared {
			fmt.Printf("MISSING    %s/%s  (add to internal/services/%s/capabilities_dev.go)\n",
				service, op.Name, service)
			violations++
			continue
		}
		if op.IsStub && cap.Status != "StatusUnsupported" && cap.Status != "StatusWIP" {
			fmt.Printf("WRONG_STATUS %s/%s  (stub returns 501 but declared as %s)\n",
				service, op.Name, cap.Status)
			violations++
		}
	}
	for _, cap := range caps {
		if cap.DocOnly {
			continue
		}
		if cap.Status == "StatusUnsupported" {
			continue
		}
		if _, found := opByName[cap.Operation]; !found {
			if comprehensive {
				fmt.Printf("ORPHAN     %s/%s  (in capabilities_dev.go but not in handler)\n",
					service, cap.Operation)
				violations++
			} else {
				fmt.Printf("ORPHAN     %s/%s  (REST-routed; not detectable — skipping)\n",
					service, cap.Operation)
			}
		}
	}
	return violations
}

func capsByService(all []CapabilityDecl, service string) []CapabilityDecl {
	var out []CapabilityDecl
	for _, c := range all {
		if c.Service == service {
			out = append(out, c)
		}
	}
	return out
}

// generateAllGenGo writes internal/capabilities/all.gen.go.
func generateAllGenGo(root string, caps []CapabilityDecl) error {
	sorted := make([]CapabilityDecl, len(caps))
	copy(sorted, caps)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].Service != sorted[j].Service {
			return sorted[i].Service < sorted[j].Service
		}
		return sorted[i].Operation < sorted[j].Operation
	})

	var buf bytes.Buffer
	buf.WriteString("//go:build dev\n\n")
	buf.WriteString("// Code generated by cmd/capgen; DO NOT EDIT.\n")
	buf.WriteString("// Regenerate with: make generate-caps\n\n")
	buf.WriteString("package capabilities\n\n")
	buf.WriteString("// AllCapabilities is the static snapshot of all declared service capabilities.\n")
	buf.WriteString("// Used by tools (e.g. overcast-mcp) that need capability data without importing\n")
	buf.WriteString("// all service packages. Only included in dev builds.\n")
	buf.WriteString("var AllCapabilities = []Capability{\n")
	for _, c := range sorted {
		buf.WriteString(fmt.Sprintf("\t{Service: %q, Operation: %q, Category: %q, Status: %s, Notes: %q, DocsURL: %q, DisplayName: %q, DocOnly: %t, Since: %q},\n",
			c.Service, c.Operation, c.Category, c.Status, c.Notes, c.DocsURL, c.DisplayName, c.DocOnly, c.Since))
	}
	buf.WriteString("}\n")

	out := filepath.Join(root, "internal", "capabilities", "all.gen.go")
	return os.WriteFile(out, buf.Bytes(), 0o644)
}

// writeDocTable rewrites the sentinel-bracketed capability tables in a doc file.
func writeDocTable(docPath, service string, caps []CapabilityDecl) error {
	existing, err := os.ReadFile(docPath)
	if err != nil {
		return err
	}

	content := string(existing)
	const beginMarker = "<!-- BEGIN overcast:capabilities -->"
	const endMarker = "<!-- END overcast:capabilities -->"

	generated := buildDocSection(service, caps)
	generatedBlock := beginMarker + "\n" + generated + endMarker

	begin := strings.Index(content, beginMarker)
	end := strings.Index(content, endMarker)

	var baseContent string
	if begin >= 0 && end >= 0 && end > begin {
		// Remove the existing generated block entirely so it can be reinserted at
		// the manual-table anchor if the markers were placed incorrectly before.
		baseContent = strings.TrimRight(content[:begin], "\n") + "\n\n" + strings.TrimLeft(content[end+len(endMarker):], "\n")
	} else {
		baseContent = content
	}

	var newContent string
	if start, end, ok := findManualTableRegion(baseContent); ok {
		newContent = baseContent[:start] + generatedBlock + "\n\n" + strings.TrimLeft(baseContent[end:], "\n")
	} else {
		// Fallback: preserve append behavior when no manual table anchor exists.
		newContent = strings.TrimRight(baseContent, "\n") + "\n\n" + generatedBlock + "\n"
	}

	return os.WriteFile(docPath, []byte(newContent), 0o644)
}

// findManualTableRegion returns the byte range for the legacy manual capability
// tables so the generated block can replace them in-place.
func findManualTableRegion(content string) (start, end int, ok bool) {
	start = findManualTableAnchor(content)
	if start < 0 {
		return 0, 0, false
	}
	endpoints := findTopLevelHeading(content, "## Endpoints")
	searchFrom := start + 1
	if endpoints >= start {
		searchFrom = endpoints + 1
	}
	end = findNextTopLevelHeading(content, searchFrom)
	if end < 0 {
		end = len(content)
	}
	return start, end, true
}

// findManualTableAnchor returns the byte offset where the generated capability
// block should be inserted so it replaces the legacy manual tables in-place.
// Prefer the first top-level manual "## Summary" heading when it precedes a
// manual "## Endpoints" section; otherwise fall back to the manual endpoints
// heading itself.
func findManualTableAnchor(content string) int {
	summary := findTopLevelHeading(content, "## Summary")
	endpoints := findTopLevelHeading(content, "## Endpoints")
	if summary >= 0 && endpoints >= 0 && summary < endpoints {
		return summary
	}
	if endpoints >= 0 {
		return endpoints
	}
	return -1
}

func findTopLevelHeading(content, heading string) int {
	offset := 0
	for _, line := range strings.SplitAfter(content, "\n") {
		trimmed := strings.TrimRight(line, "\r\n")
		if trimmed == heading {
			return offset
		}
		offset += len(line)
	}
	if strings.TrimRight(content, "\r\n") == heading {
		return 0
	}
	return -1
}

func findNextTopLevelHeading(content string, after int) int {
	if after < 0 {
		after = 0
	}
	offset := 0
	for _, line := range strings.SplitAfter(content, "\n") {
		trimmed := strings.TrimRight(line, "\r\n")
		if offset > after && strings.HasPrefix(trimmed, "## ") {
			return offset
		}
		offset += len(line)
	}
	return -1
}

// displayWidth returns the visible display width of a string, matching how
// Prettier (via the string-width npm package) measures markdown table cells.
// Emoji are counted as width 2; variation selectors (U+FE0F) as 0; ASCII as 1.
func displayWidth(s string) int {
	w := 0
	for _, r := range s {
		switch {
		case r == 0xFE0F: // variation selector-16 — zero width
		case r >= 0x1F000: // supplementary emoji (e.g. 🚧)
			w += 2
		case r >= 0x2600 && r <= 0x27FF: // misc symbols & dingbats (e.g. ✅ ⚠ ❌)
			w += 2
		default:
			w++
		}
	}
	return w
}

// escapeMDCell escapes characters that would break a GFM table cell.
// Pipe characters are backslash-escaped so that inline markdown formatting
// (including backtick code spans) can be used freely in notes without
// disrupting the column structure.
func escapeMDCell(s string) string {
	return strings.ReplaceAll(s, "|", `\|`)
}

// formatTable renders a markdown table with Prettier-style column alignment.
// headers is a slice of column header strings; rows is a slice of rows (each a slice of cell strings).
// All columns are padded to the display width of their widest cell.
func formatTable(headers []string, rows [][]string) string {
	widths := make([]int, len(headers))
	for i, h := range headers {
		widths[i] = displayWidth(h)
	}
	for _, row := range rows {
		for i, cell := range row {
			if i < len(widths) {
				if dw := displayWidth(escapeMDCell(cell)); dw > widths[i] {
					widths[i] = dw
				}
			}
		}
	}

	pad := func(s string, w int) string {
		return s + strings.Repeat(" ", w-displayWidth(s))
	}

	var sb strings.Builder

	// Header row.
	sb.WriteString("|")
	for i, h := range headers {
		sb.WriteString(" " + pad(h, widths[i]) + " |")
	}
	sb.WriteString("\n")

	// Separator row.
	sb.WriteString("|")
	for _, w := range widths {
		sb.WriteString(" " + strings.Repeat("-", w) + " |")
	}
	sb.WriteString("\n")

	// Data rows.
	for _, row := range rows {
		sb.WriteString("|")
		for i := range headers {
			cell := ""
			if i < len(row) {
				cell = escapeMDCell(row[i])
			}
			sb.WriteString(" " + pad(cell, widths[i]) + " |")
		}
		sb.WriteString("\n")
	}

	return sb.String()
}

// buildDocSection generates the markdown table section for a service.
func buildDocSection(service string, caps []CapabilityDecl) string {
	docsBase := serviceDocsBase(service)

	// Collect ordered unique categories preserving declaration order.
	catOrder := []string{}
	catSeen := map[string]struct{}{}
	byCat := map[string][]CapabilityDecl{}
	for _, c := range caps {
		cat := c.Category
		if cat == "" {
			cat = "Operations"
		}
		if _, ok := catSeen[cat]; !ok {
			catSeen[cat] = struct{}{}
			catOrder = append(catOrder, cat)
		}
		byCat[cat] = append(byCat[cat], c)
	}

	var buf bytes.Buffer

	// Summary table.
	buf.WriteString("\n## Summary\n\n")
	allSummaryHeaders := []string{"Category", "✅ Supported", "🧊 Inert", "⚠️ Partial", "🚧 WIP", "❌ Unsupported"}
	statusKeys := []string{"StatusSupported", "StatusInert", "StatusPartial", "StatusWIP", "StatusUnsupported"}
	// Accumulate raw counts per row so we can detect all-zero columns.
	type summaryCount struct {
		cat    string
		counts [5]int
	}
	rawSummary := make([]summaryCount, 0, len(catOrder))
	colTotals := [5]int{}
	for _, cat := range catOrder {
		var sc summaryCount
		sc.cat = cat
		for _, c := range byCat[cat] {
			for i, k := range statusKeys {
				if c.Status == k {
					sc.counts[i]++
					colTotals[i]++
				}
			}
		}
		rawSummary = append(rawSummary, sc)
	}
	// Build filtered headers and rows, skipping all-zero columns and blank-ing zero cells.
	summaryHeaders := []string{allSummaryHeaders[0]}
	activeColIdx := []int{}
	for i := range statusKeys {
		if colTotals[i] > 0 {
			summaryHeaders = append(summaryHeaders, allSummaryHeaders[i+1])
			activeColIdx = append(activeColIdx, i)
		}
	}
	summaryRows := make([][]string, 0, len(rawSummary))
	for _, sc := range rawSummary {
		row := []string{sc.cat}
		for _, i := range activeColIdx {
			if sc.counts[i] == 0 {
				row = append(row, "")
			} else {
				row = append(row, fmt.Sprintf("%d", sc.counts[i]))
			}
		}
		summaryRows = append(summaryRows, row)
	}
	buf.WriteString(formatTable(summaryHeaders, summaryRows))

	buf.WriteString("\n---\n\n## Endpoints\n")

	// Endpoints tables per category.
	endpointHeaders := []string{"Operation", "Status", "Notes", "AWS Docs"}
	for _, cat := range catOrder {
		buf.WriteString(fmt.Sprintf("\n### %s\n\n", cat))
		rows := make([][]string, 0, len(byCat[cat]))
		for _, c := range byCat[cat] {
			status := statusLabel(c.Status)
			docsURL := c.DocsURL
			if docsURL == "" && docsBase != "" {
				docsURL = fmt.Sprintf("[docs](%s%s.html)", docsBase, c.Operation)
			}
			displayOp := c.Operation
			if c.DisplayName != "" {
				displayOp = c.DisplayName
			}
			rows = append(rows, []string{"`" + displayOp + "`", status, c.Notes, docsURL})
		}
		buf.WriteString(formatTable(endpointHeaders, rows))
	}

	buf.WriteString("\n")
	return buf.String()
}

func statusLabel(s string) string {
	switch s {
	case "StatusSupported":
		return "✅ Supported"
	case "StatusInert":
		return "🧊 Inert"
	case "StatusPartial":
		return "⚠️ Partial"
	case "StatusWIP":
		return "🚧 WIP"
	default:
		return "❌ Unsupported"
	}
}

// serviceDocsBase returns the AWS API docs URL base for a service (up to the operation name).
var serviceDocsBaseMap = map[string]string{
	"acm":             "https://docs.aws.amazon.com/acm/latest/APIReference/API_",
	"apigateway":      "https://docs.aws.amazon.com/apigateway/latest/api/API_",
	"appconfig":       "https://docs.aws.amazon.com/appconfig/2019-10-09/APIReference/API_",
	"appconfigdata":   "https://docs.aws.amazon.com/appconfig/2019-10-09/APIReference/API_",
	"appregistry":     "https://docs.aws.amazon.com/servicecatalog/latest/dg/API_app-registry_",
	"appsync":         "https://docs.aws.amazon.com/appsync/latest/APIReference/API_",
	"athena":          "https://docs.aws.amazon.com/athena/latest/APIReference/API_",
	"backup":          "https://docs.aws.amazon.com/aws-backup/latest/devguide/API_",
	"bedrock":         "https://docs.aws.amazon.com/bedrock/latest/APIReference/API_",
	"cloudformation":  "https://docs.aws.amazon.com/AWSCloudFormation/latest/APIReference/API_",
	"cloudfront":      "https://docs.aws.amazon.com/cloudfront/latest/APIReference/API_",
	"cloudtrail":      "https://docs.aws.amazon.com/awscloudtrail/latest/APIReference/API_",
	"cloudwatch":      "https://docs.aws.amazon.com/AmazonCloudWatch/latest/APIReference/API_",
	"cloudwatch-logs": "https://docs.aws.amazon.com/AmazonCloudWatchLogs/latest/APIReference/API_",
	"cognito":         "https://docs.aws.amazon.com/cognito-user-identity-pools/latest/APIReference/API_",
	"dynamodb":        "https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_",
	"dynamodbstreams": "https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_",
	"ec2":             "https://docs.aws.amazon.com/AWSEC2/latest/APIReference/API_",
	"ecr":             "https://docs.aws.amazon.com/AmazonECR/latest/APIReference/API_",
	"ecs":             "https://docs.aws.amazon.com/AmazonECS/latest/APIReference/API_",
	"eks":             "https://docs.aws.amazon.com/eks/latest/APIReference/API_",
	"elasticache":     "https://docs.aws.amazon.com/AmazonElastiCache/latest/APIReference/API_",
	"elbv2":           "https://docs.aws.amazon.com/elasticloadbalancing/latest/APIReference/API_",
	"eventbridge":     "https://docs.aws.amazon.com/eventbridge/latest/APIReference/API_",
	"firehose":        "https://docs.aws.amazon.com/firehose/latest/APIReference/API_",
	"glue":            "https://docs.aws.amazon.com/glue/latest/dg/aws-glue-api-",
	"iam":             "https://docs.aws.amazon.com/IAM/latest/APIReference/API_",
	"kinesis":         "https://docs.aws.amazon.com/kinesis/latest/APIReference/API_",
	"kms":             "https://docs.aws.amazon.com/kms/latest/APIReference/API_",
	"lambda":          "https://docs.aws.amazon.com/lambda/latest/dg/API_",
	"msk":             "https://docs.aws.amazon.com/msk/latest/developerguide/API_",
	"opensearch":      "https://docs.aws.amazon.com/opensearch-service/latest/APIReference/API_",
	"organizations":   "https://docs.aws.amazon.com/organizations/latest/APIReference/API_",
	"pipes":           "https://docs.aws.amazon.com/eventbridge/latest/pipes-reference/API_",
	"rds":             "https://docs.aws.amazon.com/AmazonRDS/latest/APIReference/API_",
	"route53":         "https://docs.aws.amazon.com/Route53/latest/APIReference/API_",
	"s3":              "https://docs.aws.amazon.com/AmazonS3/latest/API/API_",
	"scheduler":       "https://docs.aws.amazon.com/scheduler/latest/APIReference/API_",
	"secretsmanager":  "https://docs.aws.amazon.com/secretsmanager/latest/apireference/API_",
	"ses":             "https://docs.aws.amazon.com/ses/latest/APIReference/API_",
	"shield":          "https://docs.aws.amazon.com/waf/latest/DDOSAPIReference/API_",
	"sns":             "https://docs.aws.amazon.com/sns/latest/api/API_",
	"sqs":             "https://docs.aws.amazon.com/AWSSimpleQueueService/latest/APIReference/API_",
	"ssm":             "https://docs.aws.amazon.com/systems-manager/latest/APIReference/API_",
	"stepfunctions":   "https://docs.aws.amazon.com/step-functions/latest/apireference/API_",
	"sts":             "https://docs.aws.amazon.com/STS/latest/APIReference/API_",
	"transfer":        "https://docs.aws.amazon.com/transfer/latest/userguide/API_",
	"waf":             "https://docs.aws.amazon.com/waf/latest/APIReference/API_",
}

func serviceDocsBase(service string) string {
	return serviceDocsBaseMap[service]
}

// statusDisplayNames maps service IDs to their display names as they appear in STATUS.md tables.
var statusDisplayNames = map[string]string{
	"acm":             "ACM",
	"apigateway":      "API Gateway",
	"appconfig":       "AppConfig",
	"appconfigdata":   "AppConfigData",
	"appregistry":     "AppRegistry",
	"appsync":         "AppSync",
	"athena":          "Athena",
	"autoscaling":     "Auto Scaling",
	"backup":          "Backup",
	"bedrock":         "Bedrock",
	"cloudformation":  "CloudFormation",
	"cloudfront":      "CloudFront",
	"cloudtrail":      "CloudTrail",
	"cloudwatch":      "CloudWatch",
	"cloudwatch-logs": "CloudWatch Logs",
	"cognito":         "Cognito",
	"dynamodb":        "DynamoDB",
	"dynamodbstreams": "DynamoDB Streams",
	"ec2":             "EC2 / VPC",
	"ecr":             "ECR",
	"ecs":             "ECS",
	"eks":             "EKS",
	"elasticache":     "ElastiCache",
	"elbv2":           "ELBv2",
	"eventbridge":     "EventBridge",
	"firehose":        "Firehose",
	"glue":            "Glue",
	"iam":             "IAM",
	"kinesis":         "Kinesis",
	"kms":             "KMS",
	"lambda":          "Lambda",
	"msk":             "MSK",
	"opensearch":      "OpenSearch",
	"organizations":   "Organizations",
	"pipes":           "Pipes",
	"rds":             "RDS",
	"route53":         "Route 53",
	"s3":              "S3",
	"scheduler":       "Scheduler",
	"secretsmanager":  "Secrets Manager",
	"ses":             "SES",
	"shield":          "Shield",
	"sns":             "SNS",
	"sqs":             "SQS",
	"ssm":             "SSM",
	"stepfunctions":   "Step Functions",
	"sts":             "STS",
	"transfer":        "Transfer Family",
	"waf":             "WAF v2",
}

// statusTableOrder defines the display order for the sentinel-generated op-count
// table. Mirrors the tier ordering of the hand-maintained STATUS.md tables.
var statusTableOrder = []string{
	"s3", "sqs", "dynamodb", "lambda", "apigateway", "appsync", "cloudfront",
	"cognito", "ec2", "sns",
	"iam", "ecs", "ecr", "kms", "kinesis", "eventbridge", "scheduler",
	"cloudformation", "rds", "elasticache", "appconfig", "appconfigdata",
	"secretsmanager", "ssm", "cloudwatch-logs", "ses", "sts",
	"stepfunctions", "pipes", "waf", "shield", "acm", "athena", "bedrock",
	"cloudwatch", "dynamodbstreams", "firehose", "glue", "opensearch",
	"appregistry", "autoscaling", "backup", "cloudtrail", "eks", "elbv2", "msk",
	"organizations", "route53", "transfer",
}

var serviceIndexTiers = map[string]string{
	"s3":              "Comprehensive / broad support",
	"sqs":             "Comprehensive / broad support",
	"dynamodb":        "Comprehensive / broad support",
	"lambda":          "Comprehensive / broad support",
	"apigateway":      "Comprehensive / broad support",
	"appsync":         "Comprehensive / broad support",
	"cloudfront":      "Comprehensive / broad support",
	"cognito":         "Comprehensive / broad support",
	"ec2":             "Comprehensive / broad support",
	"sns":             "Comprehensive / broad support",
	"iam":             "Core CRUD + common workflows",
	"ecs":             "Core CRUD + common workflows",
	"ecr":             "Core CRUD + common workflows",
	"kms":             "Core CRUD + common workflows",
	"kinesis":         "Core CRUD + common workflows",
	"eventbridge":     "Core CRUD + common workflows",
	"scheduler":       "Core CRUD + common workflows",
	"cloudformation":  "Core CRUD + common workflows",
	"rds":             "Core CRUD + common workflows",
	"elasticache":     "Core CRUD + common workflows",
	"appconfig":       "Core CRUD + common workflows",
	"appconfigdata":   "Core CRUD + common workflows",
	"secretsmanager":  "Core CRUD + common workflows",
	"ssm":             "Core CRUD + common workflows",
	"cloudwatch-logs": "Core CRUD + common workflows",
	"ses":             "Core CRUD + common workflows",
	"sts":             "Core CRUD + common workflows",
	"stepfunctions":   "Minimal / targeted support",
	"pipes":           "Minimal / targeted support",
	"waf":             "Minimal / targeted support",
	"shield":          "Minimal / targeted support",
	"acm":             "Minimal / targeted support",
	"athena":          "Minimal / targeted support",
	"bedrock":         "Minimal / targeted support",
	"cloudwatch":      "Minimal / targeted support",
	"dynamodbstreams": "Minimal / targeted support",
	"firehose":        "Minimal / targeted support",
	"glue":            "Minimal / targeted support",
	"opensearch":      "Minimal / targeted support",
	"appregistry":     "IaC/discovery-oriented stub",
	"autoscaling":     "IaC/discovery-oriented stub",
	"backup":          "IaC/discovery-oriented stub",
	"cloudtrail":      "IaC/discovery-oriented stub",
	"eks":             "IaC/discovery-oriented stub",
	"elbv2":           "IaC/discovery-oriented stub",
	"msk":             "IaC/discovery-oriented stub",
	"organizations":   "IaC/discovery-oriented stub",
	"route53":         "IaC/discovery-oriented stub",
	"transfer":        "IaC/discovery-oriented stub",
}

var serviceDocFileNames = map[string]string{
	"elbv2": "elb",
}

// updateStatusMd keeps STATUS.md op counts consistent with the capability
// registry. It does two things:
//
//  1. Inline-patches the "Ops" column in the existing hand-maintained tables
//     so the displayed tier-grouped rows stay current.
//
//  2. Replaces the content between <!-- BEGIN overcast:status --> and
//     <!-- END overcast:status --> with a freshly generated flat table for
//     direct comparison against the hand-maintained counts.
//
// Returns true if the file was modified.
func updateStatusMd(root string, allCaps []CapabilityDecl) (bool, error) {
	const beginMarker = "<!-- BEGIN overcast:status -->"
	const endMarker = "<!-- END overcast:status -->"

	// Count total ops per service.
	opCounts := map[string]int{}
	for _, c := range allCaps {
		opCounts[c.Service]++
	}

	// Build reverse map: lower-cased display name → service ID.
	nameToID := make(map[string]string, len(statusDisplayNames))
	for id, name := range statusDisplayNames {
		nameToID[strings.ToLower(strings.TrimSpace(name))] = id
	}

	statusPath := filepath.Join(root, "STATUS.md")
	raw, err := os.ReadFile(statusPath)
	if err != nil {
		return false, err
	}

	lines := strings.Split(string(raw), "\n")
	changed := false

	// Part 1: inline-patch Ops cells in the existing hand-maintained tables.
	for i, line := range lines {
		if !strings.HasPrefix(line, "|") {
			continue
		}
		// Split on "|" — at least 4 parts for "| svc | ops | highlights |"
		parts := strings.Split(line, "|")
		if len(parts) < 4 {
			continue
		}
		svcCell := strings.TrimSpace(parts[1])
		if svcCell == "" || svcCell == "Service" || strings.Contains(svcCell, "---") {
			continue
		}
		svcID, ok := nameToID[strings.ToLower(svcCell)]
		if !ok {
			continue
		}
		count, ok := opCounts[svcID]
		if !ok {
			continue
		}
		// Replace ops cell (parts[2]) with the new count.
		oldOps := strings.TrimSpace(parts[2])
		newOps := fmt.Sprintf("%d", count)
		if oldOps == newOps {
			continue
		}
		// Preserve original cell width padding.
		parts[2] = fmt.Sprintf(" %-3s ", newOps)
		lines[i] = strings.Join(parts, "|")
		changed = true
	}

	// Part 2: replace sentinel section with a flat generated table.
	content := strings.Join(lines, "\n")
	serviceCountLine := fmt.Sprintf("%d AWS services are registered. Coverage varies from comprehensive to stub.", len(opCounts))
	content = regexpMustReplace(content, `(?m)^\d+ AWS services are registered\. Coverage varies from comprehensive to stub\.$`, serviceCountLine, &changed)
	beginIdx := strings.Index(content, beginMarker)
	endIdx := strings.Index(content, endMarker)
	if beginIdx >= 0 && endIdx > beginIdx {
		var buf strings.Builder
		buf.WriteString(beginMarker + "\n\n")
		buf.WriteString("| Service         | Ops |\n")
		buf.WriteString("| --------------- | --- |\n")
		ordered := orderedServices(opCounts)
		for _, svcID := range ordered {
			name := statusDisplayNames[svcID]
			if name == "" {
				name = svcID
			}
			buf.WriteString(fmt.Sprintf("| %-15s | %-3d |\n", name, opCounts[svcID]))
		}
		buf.WriteString("\n" + endMarker)
		replacement := buf.String()
		oldSection := content[beginIdx : endIdx+len(endMarker)]
		if replacement != oldSection {
			content = content[:beginIdx] + replacement + content[endIdx+len(endMarker):]
			changed = true
		}
	}

	if !changed {
		return false, nil
	}
	return true, os.WriteFile(statusPath, []byte(content), 0o644)
}

// orderedServices returns service IDs in statusTableOrder first, followed by
// any remaining service IDs sorted alphabetically.
func orderedServices(opCounts map[string]int) []string {
	inOrder := make(map[string]bool, len(statusTableOrder))
	for _, s := range statusTableOrder {
		inOrder[s] = true
	}

	ordered := make([]string, 0, len(opCounts))
	for _, s := range statusTableOrder {
		if _, ok := opCounts[s]; ok {
			ordered = append(ordered, s)
		}
	}

	remainder := make([]string, 0, len(opCounts))
	for s := range opCounts {
		if !inOrder[s] {
			remainder = append(remainder, s)
		}
	}
	sort.Strings(remainder)

	return append(ordered, remainder...)
}

func regexpMustReplace(content, pattern, replacement string, changed *bool) string {
	re := regexp.MustCompile(pattern)
	next := re.ReplaceAllString(content, replacement)
	if next != content {
		*changed = true
	}
	return next
}

func updateDocsReadmeServiceIndex(root string, allCaps []CapabilityDecl) (bool, error) {
	const beginMarker = "<!-- BEGIN overcast:service-index -->"
	const endMarker = "<!-- END overcast:service-index -->"

	opCounts := map[string]int{}
	for _, c := range allCaps {
		opCounts[c.Service]++
	}

	path := filepath.Join(root, "docs", "README.md")
	raw, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	content := string(raw)
	beginIdx := strings.Index(content, beginMarker)
	endIdx := strings.Index(content, endMarker)
	if beginIdx < 0 || endIdx <= beginIdx {
		return false, fmt.Errorf("missing %s/%s markers", beginMarker, endMarker)
	}

	rows := make([][]string, 0, len(opCounts))
	for _, svc := range orderedServices(opCounts) {
		name := statusDisplayNames[svc]
		if name == "" {
			name = svc
		}
		docFile := svc
		if override := serviceDocFileNames[svc]; override != "" {
			docFile = override
		}
		tier := serviceIndexTiers[svc]
		if tier == "" {
			tier = "See service doc"
		}
		rows = append(rows, []string{
			name,
			fmt.Sprintf("[%s.md](./services/%s.md)", docFile, docFile),
			fmt.Sprintf("%d", opCounts[svc]),
			tier,
		})
	}

	var buf strings.Builder
	buf.WriteString(beginMarker + "\n\n")
	buf.WriteString(formatTable([]string{"Service", "Doc", "Ops", "Coverage tier"}, rows))
	buf.WriteString("\n" + endMarker)

	replacement := buf.String()
	oldSection := content[beginIdx : endIdx+len(endMarker)]
	if replacement == oldSection {
		return false, nil
	}
	content = content[:beginIdx] + replacement + content[endIdx+len(endMarker):]
	return true, os.WriteFile(path, []byte(content), 0o644)
}

// generateServiceSupportJSON writes docs/generated/service-support.json, a
// machine-readable aggregate of all declared capabilities grouped by service.
// The file is intended for consumption by the web UI, CI checks, and MCP tools.
// It is regenerated by `capgen --write-docs` and checked by `make docs-check`.
func generateServiceSupportJSON(root string, allCaps []CapabilityDecl) error {
	type opEntry struct {
		Operation string `json:"operation"`
		Category  string `json:"category,omitempty"`
		Status    string `json:"status"`
		Notes     string `json:"notes,omitempty"`
		DocsURL   string `json:"docs_url,omitempty"`
		DocOnly   bool   `json:"doc_only,omitempty"`
	}
	type svcEntry struct {
		Service        string    `json:"service"`
		DisplayName    string    `json:"display_name,omitempty"`
		TotalOps       int       `json:"total_ops"`
		ImplementedOps int       `json:"implemented_ops"`
		Operations     []opEntry `json:"operations"`
	}
	type manifest struct {
		GeneratedBy string     `json:"generated_by"`
		TotalOps    int        `json:"total_ops"`
		Services    []svcEntry `json:"services"`
	}

	// Group by service in statusTableOrder, then alphabetical remainder.
	inOrder := make(map[string]bool, len(statusTableOrder))
	for _, s := range statusTableOrder {
		inOrder[s] = true
	}
	// Build unique service list: ordered first, then remainder sorted.
	allSvcs := make(map[string]bool)
	for _, c := range allCaps {
		allSvcs[c.Service] = true
	}
	ordered := make([]string, 0, len(allSvcs))
	for _, s := range statusTableOrder {
		if allSvcs[s] {
			ordered = append(ordered, s)
		}
	}
	prefixLen := len(ordered)
	for svc := range allSvcs {
		if !inOrder[svc] {
			ordered = append(ordered, svc)
		}
	}
	sort.Slice(ordered[prefixLen:], func(i, j int) bool {
		base := prefixLen
		return ordered[base+i] < ordered[base+j]
	})

	svcs := make([]svcEntry, 0, len(ordered))
	for _, svc := range ordered {
		var ops []opEntry
		implemented := 0
		for _, c := range allCaps {
			if c.Service != svc {
				continue
			}
			status := statusLabel(c.Status)
			ops = append(ops, opEntry{
				Operation: c.Operation,
				Category:  c.Category,
				Status:    status,
				Notes:     c.Notes,
				DocsURL:   c.DocsURL,
				DocOnly:   c.DocOnly,
			})
			if c.Status != "StatusUnsupported" {
				implemented++
			}
		}
		svcs = append(svcs, svcEntry{
			Service:        svc,
			DisplayName:    statusDisplayNames[svc],
			TotalOps:       len(ops),
			ImplementedOps: implemented,
			Operations:     ops,
		})
	}

	m := manifest{
		GeneratedBy: "go run -tags dev ./cmd/capgen --write-docs",
		TotalOps:    len(allCaps),
		Services:    svcs,
	}

	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')

	outDir := filepath.Join(root, "docs", "generated")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(outDir, "service-support.json"), data, 0o644)
}
