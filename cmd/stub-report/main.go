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

	total := 0
	fmt.Println("# Overcast Operation Manifest")
	fmt.Println()
	for _, s := range svcs {
		total += len(s.ops)
		pstr := ""
		if len(s.protos) > 0 {
			pstr = fmt.Sprintf(", protocols: %s", strings.Join(s.protos, ", "))
		}
		fmt.Printf("## %s — %d ops%s\n", s.name, len(s.ops), pstr)
		for _, op := range s.ops {
			fmt.Printf("  - %s (%s → %s)\n", op.name, op.reqType, op.respType)
		}
		fmt.Println()
	}
	fmt.Printf("---\nTotal: %d operations across %d services\n", total, len(svcs))
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
