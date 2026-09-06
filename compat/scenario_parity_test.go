package compat

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"testing"
)

// The cli and go-sdk suites are separate Go modules (compat/suites/cli and
// compat/suites/go-sdk have their own go.mod), so they cannot share a helper
// package without a shared module — not warranted for five small functions
// and a constant. compat/suites/cli/internal/scenario/path.go and
// compat/suites/go-sdk/internal/scenario/path.go carry a cross-reference
// comment saying so and naming the rule: change both or neither. This test is
// what enforces that rule, rather than leaving it to be noticed in review.
//
// parsePath is byte-identical between the two files. canonicalJSON, jsonEqual,
// render, renderResolved and missingValue differ only in comments — their
// code (signature and body, or value) must still match exactly. Keep this
// list explicit and small: it names precisely the identifiers the two files'
// header comments promise to keep in sync, not every coincidental similarity
// between the packages.
var scenarioPathParityFuncs = []string{
	"parsePath",
	"canonicalJSON",
	"jsonEqual",
	"render",
	"renderResolved",
}

const scenarioPathParityConst = "missingValue"

const (
	cliScenarioPathGo   = "suites/cli/internal/scenario/path.go"
	goSDKScenarioPathGo = "suites/go-sdk/internal/scenario/path.go"
)

// TestScenarioPathParity fails the build the moment one of the two files
// above changes a listed function or constant without the other following,
// which is exactly the drift a "change both or neither" comment cannot catch
// on its own.
func TestScenarioPathParity(t *testing.T) {
	cliFset := token.NewFileSet()
	cliFile := parseScenarioFile(t, cliFset, cliScenarioPathGo)

	goSDKFset := token.NewFileSet()
	goSDKFile := parseScenarioFile(t, goSDKFset, goSDKScenarioPathGo)

	for _, name := range scenarioPathParityFuncs {
		cliDecl := findFuncDecl(t, cliFile, cliScenarioPathGo, name)
		goSDKDecl := findFuncDecl(t, goSDKFile, goSDKScenarioPathGo, name)

		cliSrc := printNode(t, cliFset, cliDecl.Type) + printNode(t, cliFset, cliDecl.Body)
		goSDKSrc := printNode(t, goSDKFset, goSDKDecl.Type) + printNode(t, goSDKFset, goSDKDecl.Body)

		if cliSrc != goSDKSrc {
			t.Errorf("%s: %s and %s have drifted — their signature+body must be identical (comments aside)\n--- %[2]s ---\n%s\n--- %[3]s ---\n%s",
				name, cliScenarioPathGo, goSDKScenarioPathGo, cliSrc, goSDKSrc)
		}
	}

	cliConst := findConstValue(t, cliFile, cliScenarioPathGo, scenarioPathParityConst)
	goSDKConst := findConstValue(t, goSDKFile, goSDKScenarioPathGo, scenarioPathParityConst)
	if cliConst != goSDKConst {
		t.Errorf("%s: %s (%s) and %s (%s) have drifted — the constant's value must be identical",
			scenarioPathParityConst, cliScenarioPathGo, cliConst, goSDKScenarioPathGo, goSDKConst)
	}
}

func parseScenarioFile(t *testing.T, fset *token.FileSet, relPath string) *ast.File {
	t.Helper()
	f, err := parser.ParseFile(fset, relPath, nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse %s: %v", relPath, err)
	}
	return f
}

func findFuncDecl(t *testing.T, f *ast.File, relPath, name string) *ast.FuncDecl {
	t.Helper()
	for _, decl := range f.Decls {
		if fd, ok := decl.(*ast.FuncDecl); ok && fd.Recv == nil && fd.Name.Name == name {
			return fd
		}
	}
	t.Fatalf("%s: no top-level function named %s", relPath, name)
	return nil
}

// findConstValue returns the literal text of a single-value const declaration
// named name, e.g. `const missingValue = "<missing>"` returns `"<missing>"`.
func findConstValue(t *testing.T, f *ast.File, relPath, name string) string {
	t.Helper()
	for _, decl := range f.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.CONST {
			continue
		}
		for _, spec := range gd.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for i, ident := range vs.Names {
				if ident.Name != name {
					continue
				}
				if i >= len(vs.Values) {
					t.Fatalf("%s: %s has no value expression", relPath, name)
				}
				lit, ok := vs.Values[i].(*ast.BasicLit)
				if !ok {
					t.Fatalf("%s: %s is not a basic literal", relPath, name)
				}
				return lit.Value
			}
		}
	}
	t.Fatalf("%s: no top-level const named %s", relPath, name)
	return ""
}

func printNode(t *testing.T, fset *token.FileSet, node ast.Node) string {
	t.Helper()
	var buf bytes.Buffer
	if err := printer.Fprint(&buf, fset, node); err != nil {
		t.Fatalf("print node: %v", err)
	}
	return buf.String()
}
