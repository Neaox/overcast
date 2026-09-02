// Command stub-report scans the Overcast codebase for typed operation
// manifests and prints a summary report per service.
//
// Phase 7 of the Smithy wire-protocol plan (docs/plans/level2-codegen.md).
//
// Usage: go run ./cmd/stub-report [--workspace <dir>]
package main

import (
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/overcast-sh/overcast/internal/awsapi"
)

type serviceOps struct {
	name   string
	ops    []opEntry
	protos []string
}

type opEntry struct {
	name     string
	reqType  string
	respType string
}

// subServices maps virtual service names to their directory path under
// internal/services/, for service packages that live nested inside another
// service's directory (e.g. CloudWatch Logs lives at cloudwatch/logs rather
// than a top-level internal/services/cloudwatch-logs). Mirrors the same map
// in cmd/capgen/main.go.
var subServices = map[string]string{
	"cloudwatch-logs": filepath.Join("cloudwatch", "logs"),
}

func main() {
	workspace := flag.String("workspace", ".", "workspace root (directory containing go.mod); defaults to the current directory")
	flag.Parse()

	root, err := findWorkspaceRoot(*workspace)
	if err != nil {
		fmt.Fprintf(os.Stderr, "workspace root: %v\n", err)
		os.Exit(1)
	}

	svcs, err := scanServices(filepath.Join(root, "internal", "services"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "scan: %v\n", err)
		os.Exit(1)
	}
	sort.Slice(svcs, func(i, j int) bool { return svcs[i].name < svcs[j].name })

	excluded, err := excludedServices(filepath.Join(root, "internal", "services"), svcs)
	if err != nil {
		fmt.Fprintf(os.Stderr, "excluded services: %v\n", err)
		os.Exit(1)
	}

	total := 0
	modelled := modelledOperationCounts()
	fmt.Println("# Overcast Operation Manifest")
	fmt.Println()
	fmt.Println("This manifest counts **typed-dispatch operation registrations** — one row " +
		"per `op.NewTyped`/`op.NewRaw`/`op.NewTypedAny` call in a service's `typed_ops.go` " +
		"— not Overcast's overall implementation coverage. That is a different metric from " +
		"the \"Ops\" column in [docs/README.md](./README.md)'s service index and from " +
		"[docs/generated/service-support.json](./generated/service-support.json), which both " +
		"count capability-registry entries (implemented operations plus explicit stubs) for " +
		"every service, including the ones below that have no typed dispatch at all. The two " +
		"kinds of counts are expected to disagree with each other; neither is wrong, they are " +
		"answering different questions.")
	fmt.Println()
	for _, s := range svcs {
		total += len(s.ops)
		pstr := fmt.Sprintf(", modelled: %d", modelled[s.name])
		if len(s.protos) > 0 {
			pstr += fmt.Sprintf(", protocols: %s", strings.Join(s.protos, ", "))
		}
		fmt.Printf("## %s — %d ops%s\n", s.name, len(s.ops), pstr)
		for _, op := range s.ops {
			fmt.Printf("  - %s (%s → %s)\n", op.name, op.reqType, op.respType)
		}
		fmt.Println()
	}

	details := excludedServiceDetails(excluded)
	fmt.Println("## Services outside this manifest")
	fmt.Println()
	fmt.Printf("The %d service(s) below implement operations through the REST router or a "+
		"not-yet-migrated legacy dispatcher, so they have no `typed_ops.go` and get no "+
		"section above. Their modelled operation counts (from the pinned AWS model corpus) "+
		"are shown for reference; see docs/generated/service-support.json for what Overcast "+
		"actually implements per service.\n", len(details))
	fmt.Println()
	fmt.Println("| Service | Modelled ops | Why excluded |")
	fmt.Println("|---|---|---|")
	for _, d := range details {
		fmt.Printf("| %s | %d | %s |\n", d.name, d.modelled, exclusionReason(d.protocols))
	}
	fmt.Println()

	modelledTotal := 0
	for _, count := range modelled {
		modelledTotal += count
	}
	fmt.Printf("---\nModel corpus: %d operations across %d services; typed registrations: %d across %d services (%d services outside typed dispatch, listed above)\n",
		modelledTotal, len(modelled), total, len(svcs), len(details))
}

// excludedServices returns the sorted names of top-level directories under
// root that were not selected by scanServices — i.e. services (or bare
// parent directories of a nested service, like cloudwatch/) with no
// typed_ops.go of their own. This is what makes the manifest's omissions
// explicit instead of silent (#748): every service Overcast registers either
// gets a typed-dispatch section above, or an entry in this list with a
// reason.
func excludedServices(root string, included []serviceOps) ([]string, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	includedSet := make(map[string]bool, len(included))
	for _, s := range included {
		includedSet[s.name] = true
	}
	var excluded []string
	for _, e := range entries {
		if !e.IsDir() || includedSet[e.Name()] {
			continue
		}
		excluded = append(excluded, e.Name())
	}
	sort.Strings(excluded)
	return excluded, nil
}

// excludedInfo carries the pinned-model-corpus facts stub-report can derive
// about a service that has no typed_ops.go, so the manifest can explain why
// it's missing instead of just naming it.
type excludedInfo struct {
	name      string
	modelled   int
	protocols []string
}

// excludedServiceDetails looks up each excluded service's modelled operation
// count and protocol set in the pinned AWS model corpus (the same corpus
// modelledOperationCounts reads), so the reason a service is excluded is
// derived from data rather than a hand-maintained list that can go stale the
// same way the manifest itself did.
func excludedServiceDetails(names []string) []excludedInfo {
	nameSet := make(map[string]bool, len(names))
	protoSets := make(map[string]map[string]bool, len(names))
	counts := make(map[string]int, len(names))
	for _, n := range names {
		nameSet[n] = true
		protoSets[n] = make(map[string]bool)
	}
	awsapi.WalkOperations(func(op awsapi.Operation) bool {
		key := awsapi.ServiceKey(op.Service)
		if !nameSet[key] {
			return true
		}
		counts[key]++
		if op.Protocol != "" {
			protoSets[key][string(op.Protocol)] = true
		}
		return true
	})

	out := make([]excludedInfo, 0, len(names))
	for _, n := range names {
		protos := make([]string, 0, len(protoSets[n]))
		for p := range protoSets[n] {
			protos = append(protos, p)
		}
		sort.Strings(protos)
		out = append(out, excludedInfo{name: n, modelled: counts[n], protocols: protos})
	}
	return out
}

// exclusionReason turns a service's modelled protocol set into a plain-English
// reason it has no typed_ops.go. Services modelled with a REST protocol are
// routed by chi on path/method rather than a target header, which is the
// architectural reason typed dispatch does not apply to them; everything else
// with modelled operations is a service that could migrate to typed dispatch
// but has not yet.
func exclusionReason(protocols []string) string {
	for _, p := range protocols {
		if p == string(awsapi.ProtocolRESTJSON) || p == string(awsapi.ProtocolRESTXML) {
			return "REST-routed (chi router, path/method dispatch), not target-header typed dispatch"
		}
	}
	if len(protocols) > 0 {
		return "not yet migrated to the typed op registry"
	}
	return "no protocol data in the pinned model corpus"
}

// modelledOperationCounts reads the generated AWS corpus rather than treating
// typed_ops.go as the operation universe. Typed source remains a registration
// signal only; every other modelled operation is owned by the router fallback.
func modelledOperationCounts() map[string]int {
	counts := make(map[string]int)
	awsapi.WalkOperations(func(op awsapi.Operation) bool {
		counts[awsapi.ServiceKey(op.Service)]++
		return true
	})
	return counts
}

// scanServices walks internal/services, one level deep, plus the known
// subServices nested a level further (e.g. cloudwatch/logs), and returns an
// entry for every directory that has a typed_ops.go file.
//
// It intentionally does not recurse arbitrarily deep: internal/services is a
// flat-per-service layout (see AGENTS.md "Using subfolders as sub-packages
// inside a service" as a mistake to avoid), and the only nested service
// packages that exist today are declared in subServices. Walking unbounded
// would risk picking up unrelated nested directories (e.g. template assets)
// that happen to sit under a service directory.
func scanServices(root string) ([]serviceOps, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	for name := range subServices {
		names = append(names, name)
	}
	sort.Strings(names)

	var result []serviceOps
	for _, name := range names {
		svcDir := filepath.Join(root, serviceDir(name))
		opsFile := filepath.Join(svcDir, "typed_ops.go")
		if _, err := os.Stat(opsFile); err != nil {
			continue
		}
		svc := serviceOps{name: name}
		svc.ops = extractOps(opsFile)
		svc.protos = extractProtocols(opsFile)
		result = append(result, svc)
	}
	return result, nil
}

// serviceDir returns the directory path for a service relative to
// root/internal/services/.
func serviceDir(name string) string {
	if sub, ok := subServices[name]; ok {
		return sub
	}
	return name
}

// findWorkspaceRoot walks up from start until it finds go.mod, so the tool
// works from any working directory instead of assuming a fixed container
// path.
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

func extractOps(filename string) []opEntry {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, filename, nil, 0)
	if err != nil {
		return nil
	}

	var ops []opEntry
	ast.Inspect(f, func(n ast.Node) bool {
		ce, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		funcName := extractSelector(ce.Fun)
		if funcName != "op.NewTyped" && funcName != "op.NewTypedAny" && funcName != "op.NewRaw" {
			return true
		}
		// First arg is the string key in the map literal — skip that.
		// For "key": op.NewTyped[...](name, fn), the CallExpr args are (name, fn).
		if len(ce.Args) < 1 {
			return true
		}
		bl, ok := ce.Args[0].(*ast.BasicLit)
		if !ok || bl.Kind != token.STRING {
			return true
		}
		opName := strings.Trim(bl.Value, `"`)

		reqType := "?"
		respType := "?"
		// Extract type args from op.NewTyped[Req, Resp]
		if idx, ok := ce.Fun.(*ast.IndexExpr); ok {
			if il, ok := idx.Index.(*ast.IndexListExpr); ok && len(il.Indices) >= 2 {
				reqType = exprToStr(il.Indices[0])
				respType = exprToStr(il.Indices[1])
			} else {
				reqType = exprToStr(idx.Index)
			}
		} else if idx, ok := ce.Fun.(*ast.IndexListExpr); ok && len(idx.Indices) >= 2 {
			reqType = exprToStr(idx.Indices[0])
			respType = exprToStr(idx.Indices[1])
		}

		ops = append(ops, opEntry{name: opName, reqType: reqType, respType: respType})
		return true
	})

	sort.Slice(ops, func(i, j int) bool { return ops[i].name < ops[j].name })
	return ops
}

func extractSelector(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.SelectorExpr:
		if x, ok := e.X.(*ast.Ident); ok {
			return x.Name + "." + e.Sel.Name
		}
	case *ast.IndexExpr:
		return extractSelector(e.X)
	case *ast.IndexListExpr:
		return extractSelector(e.X)
	}
	return ""
}

func exprToStr(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StructType:
		return "struct{}"
	case *ast.StarExpr:
		return "*" + exprToStr(t.X)
	case *ast.SelectorExpr:
		return exprToStr(t.X) + "." + t.Sel.Name
	default:
		return fmt.Sprintf("<%T>", expr)
	}
}

func extractProtocols(filename string) []string {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, filename, nil, 0)
	if err != nil {
		return nil
	}
	var protos []string
	ast.Inspect(f, func(n ast.Node) bool {
		cl, ok := n.(*ast.CompositeLit)
		if !ok {
			return true
		}
		at, ok := cl.Type.(*ast.ArrayType)
		if !ok {
			return true
		}
		se, ok := at.Elt.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if se.Sel.Name != "Codec" {
			return true
		}
		for _, e := range cl.Elts {
			if sel, ok := e.(*ast.SelectorExpr); ok {
				protos = append(protos, sel.Sel.Name)
			} else if idx, ok := e.(*ast.IndexExpr); ok {
				if sel, ok := idx.X.(*ast.SelectorExpr); ok {
					protos = append(protos, sel.Sel.Name)
				}
			}
		}
		return true
	})
	return protos
}
