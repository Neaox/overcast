package ecs

// service_events_test.go — the guard that keeps service_events.go the whole
// truth about what a service event can say.
//
// The contract test in package cloudformation checks that CloudFormation's
// failure matcher understands every message declared in service_events.go. That
// is only worth anything while the declarations are complete: an event recorded
// from a literal written at the call site is invisible to it, and a new failure
// wording introduced that way is exactly the drift the whole arrangement exists
// to catch. So this test reads the package's own source and insists that every
// service event is recorded from a declared, classified constant — and that
// every declared constant is actually recorded somewhere.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// eventRecorders are the methods that put a message on a service's event list.
// Everything a caller ever reads passes through one of them; they forward to
// each other, so only their callers choose wording.
var eventRecorders = map[string]bool{"addServiceEvent": true, "addServiceEventAt": true, "addServiceEventOnce": true}

func TestServiceEvents_everyCallSiteUsesADeclaredFormat(t *testing.T) {
	// Given: the package's implementation files, the string constants they
	// declare, and the classification service_events.go puts those constants in.
	fset := token.NewFileSet()
	files := parsePackageSource(t, fset, ".")
	declared := declaredStringConstants(files)
	classified := make(map[string]bool)
	for _, format := range append(FailureServiceEventFormats(), RoutineServiceEventFormats()...) {
		classified[format] = true
	}

	// When: every call that records a service event is traced back to the
	// wording it used.
	recorded := make(map[string]bool)
	for _, file := range files {
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil || eventRecorders[fn.Name.Name] {
				// The recorders forward each other the caller's message; only
				// their callers choose wording.
				continue
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok || !eventRecorders[sel.Sel.Name] || len(call.Args) < 2 {
					return true
				}
				where := fset.Position(call.Pos())

				// Then: the message came from a constant this package declares,
				// and that constant is classified as a failure or as routine.
				name, ok := serviceEventFormatConstant(call.Args[1])
				if !ok {
					t.Errorf("%s: %s builds its message without a declared constant.\n"+
						"Declare the wording in service_events.go and list it in FailureServiceEventFormats or "+
						"RoutineServiceEventFormats, so CloudFormation's failure matcher is tested against it.",
						where, sel.Sel.Name)
					return true
				}
				format, ok := declared[name]
				if !ok {
					t.Errorf("%s: %s formats %s, which is not a string constant declared in this package.\n"+
						"Move the wording into service_events.go so it can be classified and tested.",
						where, sel.Sel.Name, name)
					return true
				}
				if !classified[format] {
					t.Errorf("%s: %s is declared but appears in neither FailureServiceEventFormats nor "+
						"RoutineServiceEventFormats.\nDecide which it is: CloudFormation reports a failure event "+
						"as the reason a stack failed, and must leave a routine one alone.", where, name)
					return true
				}
				recorded[format] = true
				return true
			})
		}
	}

	// Then: nothing is classified that the emulator never actually emits — a
	// stale entry would have the contract test guarding wording no service ever
	// publishes, which reads as coverage and is not.
	for _, format := range append(FailureServiceEventFormats(), RoutineServiceEventFormats()...) {
		if !recorded[format] {
			t.Errorf("no call site records %q; delete it or record it", format)
		}
	}
}

// parsePackageSource parses the package's implementation files. Test files are
// left out deliberately: they record events from literals to set up the state
// they are about, which is fine — it is production wording that has to be
// declared, because production wording is what CloudFormation reads.
func parsePackageSource(t *testing.T, fset *token.FileSet, dir string) []*ast.File {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read package directory: %v", err)
	}
	var files []*ast.File
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		files = append(files, file)
	}
	if len(files) == 0 {
		t.Fatal("no implementation files parsed, so this guard would pass without checking anything")
	}
	return files
}

// declaredStringConstants maps each string constant the package declares to its
// value, so a call site naming one can be checked against the classification.
func declaredStringConstants(files []*ast.File) map[string]string {
	out := make(map[string]string)
	for _, file := range files {
		for _, decl := range file.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.CONST {
				continue
			}
			for _, spec := range gen.Specs {
				value, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for i, name := range value.Names {
					if i >= len(value.Values) {
						continue
					}
					lit, ok := value.Values[i].(*ast.BasicLit)
					if !ok || lit.Kind != token.STRING {
						continue
					}
					unquoted, err := strconv.Unquote(lit.Value)
					if err != nil {
						continue
					}
					out[name.Name] = unquoted
				}
			}
		}
	}
	return out
}

// serviceEventFormatConstant returns the name of the constant a recorded
// message was built from: the constant passed straight through, or the format
// argument of the fmt.Sprintf that fills in the service name and counts.
func serviceEventFormatConstant(arg ast.Expr) (string, bool) {
	if call, ok := arg.(*ast.CallExpr); ok {
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "Sprintf" || len(call.Args) == 0 {
			return "", false
		}
		if pkg, ok := sel.X.(*ast.Ident); !ok || pkg.Name != "fmt" {
			return "", false
		}
		arg = call.Args[0]
	}
	ident, ok := arg.(*ast.Ident)
	if !ok {
		return "", false
	}
	return ident.Name, true
}
