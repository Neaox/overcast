//go:build dev

// Command compatgen turns the pruned AWS shape snapshot plus hand-curated
// recipes into the compat scenario IR, the refusal report and the generated
// registry sibling. It is a build-time tool whose output is committed data;
// nothing under compat/ imports it or any other emulator Go code.
//
// Usage:
//
//	go run -tags dev ./cmd/compatgen [flags]
//
// Flags:
//
//	(none)                    generate every recipe under compat/model/recipes/
//	-check                    prove the committed output is byte-identical, writing nothing
//	-scaffold <service>       print a recipe skeleton for a service in the shape snapshot
//	-review-report [service]  print the Markdown review report for a PR body
//	-explain <group>/<test>   render one generated test as pseudo-code (with -lang)
//	-lang <language>          python | node | cli | go | java | dotnet | rust
//	-sample <n>               scenarios rendered in the review report (default 3)
//	-root <dir>               repository root (default: the current directory)
//
// See compat/model/README.md for the IR and cmd/compatgen/README.md for the
// workflow. Design: docs/plans/compat-coverage-modelgen.md §3.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// options is the parsed command line.
type options struct {
	root     string
	check    bool
	scaffold string
	report   bool
	service  string
	explain  string
	lang     string
	sample   int
}

func run(args []string, stdout, stderr io.Writer) int {
	var opts options
	fs := flag.NewFlagSet("compatgen", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&opts.root, "root", ".", "repository root")
	fs.BoolVar(&opts.check, "check", false, "verify the committed output matches the generator's without writing")
	fs.StringVar(&opts.scaffold, "scaffold", "", "print a recipe skeleton for the named service")
	fs.BoolVar(&opts.report, "review-report", false, "print the Markdown review report (optionally for one service, given as the argument)")
	fs.StringVar(&opts.explain, "explain", "", "render one test as pseudo-code: <group>/<test>")
	fs.StringVar(&opts.lang, "lang", "python", "language for -explain: python, node, cli, go, java, dotnet, rust")
	fs.IntVar(&opts.sample, "sample", 3, "scenarios rendered in the review report")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 1 || (fs.NArg() == 1 && !opts.report) {
		fmt.Fprintln(stderr, "compatgen: unexpected arguments", fs.Args())
		return 2
	}
	if fs.NArg() == 1 {
		opts.service = fs.Arg(0)
	}
	modes := 0
	for _, on := range []bool{opts.check, opts.scaffold != "", opts.report, opts.explain != ""} {
		if on {
			modes++
		}
	}
	if modes > 1 {
		fmt.Fprintln(stderr, "compatgen: -check, -scaffold, -review-report and -explain are mutually exclusive")
		return 2
	}
	var err error
	switch {
	case opts.scaffold != "":
		err = runScaffold(opts, stdout)
	case opts.explain != "":
		err = runExplain(opts, stdout)
	case opts.report:
		err = runReport(opts, stdout)
	default:
		err = runGenerate(opts, stdout)
	}
	if err != nil {
		fmt.Fprintf(stderr, "compatgen: %v\n", err)
		return 1
	}
	return 0
}

// corpus is everything a generation run reads.
type corpus struct {
	schemas *schemaSet
	recipes []recipe
	values  *valuesTable
}

func loadCorpus(root string) (*corpus, error) {
	schemas, err := loadSchemas(filepath.Join(root, filepath.FromSlash(modelDir)))
	if err != nil {
		return nil, err
	}
	recipes, err := loadRecipes(filepath.Join(root, filepath.FromSlash(recipesDir)), schemas)
	if err != nil {
		return nil, err
	}
	values, err := loadValues(filepath.Join(root, filepath.FromSlash(valuesPath)), schemas)
	if err != nil {
		return nil, err
	}
	return &corpus{schemas: schemas, recipes: recipes, values: values}, nil
}

// generateAll runs the generator over every recipe and renders every output.
func generateAll(root string, c *corpus) ([]*generation, outputSet, error) {
	var generations []*generation
	var scenarios []*scenario
	gaps := gapsDocument{Version: gapsVersion, Gaps: []gap{}}
	outputs := make(outputSet)
	for _, r := range c.recipes {
		model, err := loadModel(filepath.Join(root, filepath.FromSlash(shapesDir)), r.modelService())
		if err != nil {
			return nil, nil, err
		}
		client, err := clientInfoFor(model, r.Service)
		if err != nil {
			return nil, nil, err
		}
		gen, err := generate(model, r, c.values, capabilitiesFor(r.Service), client)
		if err != nil {
			return nil, nil, fmt.Errorf("%s: %w", r.Service, err)
		}
		generations = append(generations, gen)
		scenarios = append(scenarios, gen.scenario)
		gaps.Gaps = append(gaps.Gaps, gen.gaps...)
		contents, err := encodeDocument(gen.scenario)
		if err != nil {
			return nil, nil, err
		}
		outputs[scenarioPath(r.Service)] = contents
	}
	sortGaps(gaps.Gaps)
	contents, err := encodeDocument(gaps)
	if err != nil {
		return nil, nil, err
	}
	outputs[gapsPath] = contents
	registry := buildRegistry(scenarios, scenarioBackends)
	contents, err = encodeDocument(registry)
	if err != nil {
		return nil, nil, err
	}
	outputs[registryPath] = contents
	for rel, contents := range outputs {
		if err := validateOutput(c.schemas, rel, contents); err != nil {
			return nil, nil, err
		}
	}
	return generations, outputs, nil
}

// validateOutput checks a generated file against its schema before it is
// written: the schema is the interpreters' contract, so a document the
// generator produced but the schema rejects is a generator bug.
func validateOutput(schemas *schemaSet, rel string, contents []byte) error {
	switch {
	case strings.HasPrefix(rel, scenarioDir+"/"):
		return wrapSchemaErr(rel, schemas.validate(schemaScenario, contents))
	case rel == gapsPath:
		return wrapSchemaErr(rel, schemas.validate(schemaGaps, contents))
	}
	return nil
}

func wrapSchemaErr(rel string, err error) error {
	if err != nil {
		return fmt.Errorf("generated %s: %w", rel, err)
	}
	return nil
}

func runGenerate(opts options, stdout io.Writer) error {
	c, err := loadCorpus(opts.root)
	if err != nil {
		return err
	}
	generations, outputs, err := generateAll(opts.root, c)
	if err != nil {
		return err
	}
	if err := checkStaleScenarios(opts.root, outputs, opts.check); err != nil {
		return err
	}
	if opts.check {
		if err := outputs.check(opts.root); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "compat model is up to date (%d service(s), %d file(s))\n", len(generations), len(outputs))
		return nil
	}
	if err := outputs.write(opts.root); err != nil {
		return err
	}
	for _, gen := range generations {
		fmt.Fprintf(stdout, "%s: %s\n", gen.scenario.Service, gen.summaryLine())
	}
	return nil
}

// checkStaleScenarios catches a scenario file whose recipe was deleted: it
// would otherwise linger, and a loader would keep reading it.
func checkStaleScenarios(root string, outputs outputSet, check bool) error {
	dir := filepath.Join(root, filepath.FromSlash(scenarioDir))
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var stale []string
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		rel := scenarioDir + "/" + entry.Name()
		if _, produced := outputs[rel]; produced {
			continue
		}
		if check {
			stale = append(stale, rel)
			continue
		}
		if err := os.Remove(filepath.Join(dir, entry.Name())); err != nil {
			return err
		}
	}
	if len(stale) > 0 {
		sort.Strings(stale)
		return fmt.Errorf("scenario file(s) with no recipe: %s; run `make generate-compat-model` to remove them", strings.Join(stale, ", "))
	}
	return nil
}

// summaryLine is the one-line generation summary printed per service.
func (gen *generation) summaryLine() string {
	tests := 0
	for _, g := range gen.scenario.Groups {
		tests += len(g.Tests)
	}
	return fmt.Sprintf("%d group(s), %d test(s), %d of %d operation(s) covered, %d refusal(s)",
		len(gen.scenario.Groups), tests, len(gen.covered), len(gen.model.Operations()), len(gen.gaps))
}
