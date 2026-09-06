//go:build dev

package main

import (
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"sort"
	"strings"
)

// The review report — docs/plans/compat-coverage-modelgen.md §3.5.
//
// Humans review the curated layer, not thousands of JSON lines. For a PR
// body, `-review-report [service]` prints, per service: operations covered vs
// modeled (by group), every refusal with its reason, every automatic
// name-match binding (rule 2 — the riskiest inference), every curated or
// synthetic value that was bound, and N sampled scenarios rendered as
// pseudo-code. The sample is drawn with a fixed seed so two runs over the
// same corpus print the same report.

const reportSeed = 1113 // the programme issue

func runReport(opts options, stdout io.Writer) error {
	c, err := loadCorpus(opts.root)
	if err != nil {
		return err
	}
	generations, _, err := generateAll(opts.root, c)
	if err != nil {
		return err
	}
	found := false
	for _, gen := range generations {
		if opts.service != "" && gen.scenario.Service != opts.service {
			continue
		}
		found = true
		writeReport(stdout, gen, opts.sample)
	}
	if !found {
		return fmt.Errorf("no recipe for service %q", opts.service)
	}
	return nil
}

func writeReport(w io.Writer, gen *generation, sample int) {
	s := gen.scenario
	ops := gen.model.Operations()
	tests := 0
	for _, g := range s.Groups {
		tests += len(g.Tests)
	}
	fmt.Fprintf(w, "## compatgen review — %s\n\n", s.Service)
	fmt.Fprintf(w, "%d of %d modeled operations covered by %d tests in %d groups; %d refusal(s).\n\n",
		len(gen.covered), len(ops), tests, len(s.Groups), len(gen.gaps))

	fmt.Fprintln(w, "### Coverage by operation")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "| Operation | Emulator | Covered by |")
	fmt.Fprintln(w, "| --- | --- | --- |")
	for _, op := range ops {
		covered := gen.covered[op]
		cell := "—"
		if len(covered) > 0 {
			cell = "`" + strings.Join(covered, "`, `") + "`"
		}
		fmt.Fprintf(w, "| %s | %s | %s |\n", op, gen.caps.statusLabel(op), cell)
	}
	fmt.Fprintln(w)

	fmt.Fprintln(w, "### Refusals")
	fmt.Fprintln(w)
	if len(gen.gaps) == 0 {
		fmt.Fprintln(w, "None.")
	} else {
		counts := make(map[string]int)
		for _, gp := range gen.gaps {
			counts[reasonCode(gp.Reason)]++
		}
		var codes []string
		for code := range counts {
			codes = append(codes, code)
		}
		sort.Strings(codes)
		var parts []string
		for _, code := range codes {
			parts = append(parts, fmt.Sprintf("%s ×%d", code, counts[code]))
		}
		fmt.Fprintf(w, "By reason: %s.\n\n", strings.Join(parts, ", "))
		fmt.Fprintln(w, "| Operation | Group | Reason | Detail |")
		fmt.Fprintln(w, "| --- | --- | --- | --- |")
		for _, gp := range gen.gaps {
			fmt.Fprintf(w, "| %s | %s | `%s` | %s |\n", gp.Operation, gp.Group, gp.Reason, gp.Detail)
		}
	}
	fmt.Fprintln(w)

	fmt.Fprintln(w, "### Automatic name-match bindings (§3.3 rule 2)")
	fmt.Fprintln(w)
	if len(gen.auto) == 0 {
		fmt.Fprintln(w, "None — every bound member came from an explicit `binds`, a curated value or a constraint.")
	} else {
		fmt.Fprintln(w, "| Group | Operation | Member | Bound to |")
		fmt.Fprintln(w, "| --- | --- | --- | --- |")
		for _, a := range sortedAuto(gen.auto) {
			fmt.Fprintf(w, "| %s | %s | %s | `%s` |\n", a.Group, a.Op, a.Member, a.Ref)
		}
	}
	fmt.Fprintln(w)

	fmt.Fprintln(w, "### Curated and synthetic values bound (§3.3 rules 3–4)")
	fmt.Fprintln(w)
	if len(gen.uses) == 0 {
		fmt.Fprintln(w, "None.")
	} else {
		fmt.Fprintln(w, "| Group | Operation | Member | Source | Value |")
		fmt.Fprintln(w, "| --- | --- | --- | --- | --- |")
		for _, u := range sortedUses(gen.uses) {
			fmt.Fprintf(w, "| %s | %s | %s | %s | `%s` |\n", u.Group, u.Op, u.Member, u.Source, compactJSON(u.Value))
		}
	}
	fmt.Fprintln(w)

	if len(gen.folded) > 0 || len(gen.noTeardown) > 0 {
		fmt.Fprintln(w, "### Notes")
		fmt.Fprintln(w)
		for _, f := range gen.folded {
			fmt.Fprintf(w, "- `%s` has no test of its own: the group already exercises the operation.\n", f)
		}
		for _, id := range gen.noTeardown {
			fmt.Fprintf(w, "- resource `%s` declares no delete, so its group has no teardown for it.\n", id)
		}
		fmt.Fprintln(w)
	}

	fmt.Fprintf(w, "### Sampled scenarios (%d, seed %d)\n\n", sample, reportSeed)
	for _, key := range sampleTests(s, sample) {
		g, t, _ := s.findTest(key[0], key[1])
		fmt.Fprintf(w, "#### %s/%s\n\n```python\n", g.Name, t.Name)
		// The python rendering needs nothing from renderEnv, which is what
		// keeps the review report — a PR-body artifact — independent of
		// whether the go-sdk suite module's dependencies have been fetched.
		fmt.Fprint(w, renderers["python"](renderEnv{}, s, g, t))
		fmt.Fprintln(w, "```")
		fmt.Fprintln(w)
	}
}

// sampleTests picks n tests deterministically.
func sampleTests(s *scenario, n int) [][2]string {
	var all [][2]string
	for _, g := range s.Groups {
		for _, t := range g.Tests {
			all = append(all, [2]string{g.Name, t.Name})
		}
	}
	if n >= len(all) {
		return all
	}
	rng := rand.New(rand.NewSource(reportSeed))
	rng.Shuffle(len(all), func(i, j int) { all[i], all[j] = all[j], all[i] })
	picked := all[:n]
	sort.Slice(picked, func(i, j int) bool {
		if picked[i][0] != picked[j][0] {
			return picked[i][0] < picked[j][0]
		}
		return picked[i][1] < picked[j][1]
	})
	return picked
}

// sortedAuto and sortedUses order a report table field by field, and stably.
// Concatenating the key fields would sort "g" + "AB" before "gA" + "B", and an
// unstable sort would then reorder the ties differently from run to run, which
// is exactly the kind of churn a byte-identical regeneration gate exists to
// prevent.
func sortedAuto(auto []autoBinding) []autoBinding {
	out := append([]autoBinding(nil), auto...)
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Group != b.Group {
			return a.Group < b.Group
		}
		if a.Op != b.Op {
			return a.Op < b.Op
		}
		if a.Member != b.Member {
			return a.Member < b.Member
		}
		return a.Ref < b.Ref
	})
	return out
}

func sortedUses(uses []valueUse) []valueUse {
	out := append([]valueUse(nil), uses...)
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Group != b.Group {
			return a.Group < b.Group
		}
		if a.Op != b.Op {
			return a.Op < b.Op
		}
		return a.Member < b.Member
	})
	return out
}

func compactJSON(v any) string {
	contents, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(contents)
}
