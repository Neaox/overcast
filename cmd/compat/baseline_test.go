package main

import (
	"strings"
	"testing"

	"github.com/Neaox/overcast/compat"
)

func TestCompareBaseline_currentRegression(t *testing.T) {
	// Given: a baseline with a passing compat test.
	baseline := &compatBaseline{Version: baselineVersion, Entries: []baselineEntry{{
		Suite:  "node-js-sdk",
		Group:  "sqs-basic",
		Test:   "SendMessage",
		Status: compat.StatusPass,
	}}}
	report := reportWithResults(resultSpec{suite: "node-js-sdk", service: "sqs", group: "sqs-basic", test: "SendMessage", status: compat.StatusFail})

	// When: current results are compared to the baseline.
	regressions := compareBaseline(baseline, report)

	// Then: the passing-to-failing change is reported as a regression.
	if len(regressions) != 1 {
		t.Fatalf("regressions len = %d, want 1: %#v", len(regressions), regressions)
	}
	if !strings.Contains(regressions[0], "node-js-sdk/sqs-basic/SendMessage pass -> fail") {
		t.Fatalf("regression message = %q", regressions[0])
	}
}

func TestUpdateBaseline_improvementsOnly(t *testing.T) {
	// Given: a baseline with one passing test and one known failure.
	baseline := &compatBaseline{Version: baselineVersion, Entries: []baselineEntry{
		{Suite: "node-js-sdk", Group: "sqs-basic", Test: "SendMessage", Status: compat.StatusPass},
		{Suite: "node-js-sdk", Group: "sqs-basic", Test: "ReceiveMessage", Status: compat.StatusFail},
	}}
	report := reportWithResults(
		resultSpec{suite: "node-js-sdk", service: "sqs", group: "sqs-basic", test: "SendMessage", status: compat.StatusFail},
		resultSpec{suite: "node-js-sdk", service: "sqs", group: "sqs-basic", test: "ReceiveMessage", status: compat.StatusPass},
	)

	// When: the baseline is updated from current results.
	updated := updateBaseline(baseline, report)
	entries := baselineEntryMap(updated.Entries)

	// Then: improvements are ratcheted forward, but regressions are not accepted.
	if got := entries["node-js-sdk/sqs-basic/SendMessage"].Status; got != compat.StatusPass {
		t.Fatalf("SendMessage status = %s, want pass", got)
	}
	if got := entries["node-js-sdk/sqs-basic/ReceiveMessage"].Status; got != compat.StatusPass {
		t.Fatalf("ReceiveMessage status = %s, want pass", got)
	}
}

func TestLintBaselineChange_downgrade(t *testing.T) {
	// Given: a proposed baseline change that marks a passing test as expected to fail.
	oldBaseline := &compatBaseline{Version: baselineVersion, Entries: []baselineEntry{{
		Suite:  "go-sdk",
		Group:  "s3-crud",
		Test:   "CreateBucket",
		Status: compat.StatusPass,
	}}}
	newBaseline := &compatBaseline{Version: baselineVersion, Entries: []baselineEntry{{
		Suite:  "go-sdk",
		Group:  "s3-crud",
		Test:   "CreateBucket",
		Status: compat.StatusUnimplemented,
	}}}

	// When: the baseline change is linted.
	issues := lintBaselineChange(oldBaseline, newBaseline)

	// Then: the downgrade is rejected.
	if len(issues) != 1 {
		t.Fatalf("issues len = %d, want 1: %#v", len(issues), issues)
	}
	if !strings.Contains(issues[0], "pass -> unimplemented") {
		t.Fatalf("issue message = %q", issues[0])
	}
}

func TestLintBaselineChange_removal(t *testing.T) {
	// Given: a proposed baseline change that removes an existing expectation.
	oldBaseline := &compatBaseline{Version: baselineVersion, Entries: []baselineEntry{{
		Suite:  "go-sdk",
		Group:  "s3-crud",
		Test:   "CreateBucket",
		Status: compat.StatusPass,
	}}}
	newBaseline := &compatBaseline{Version: baselineVersion}

	// When: the baseline change is linted.
	issues := lintBaselineChange(oldBaseline, newBaseline)

	// Then: removing the expectation is rejected.
	if len(issues) != 1 {
		t.Fatalf("issues len = %d, want 1: %#v", len(issues), issues)
	}
	if !strings.Contains(issues[0], "removed") {
		t.Fatalf("issue message = %q", issues[0])
	}
}

func TestCompareBaseline_newFailureNotInBaseline(t *testing.T) {
	// Given: a baseline that says nothing about a test — it did not exist when
	// the baseline was taken.
	baseline := &compatBaseline{Version: baselineVersion, Entries: []baselineEntry{{
		Suite:  "node-js-sdk",
		Group:  "sqs-basic",
		Test:   "SendMessage",
		Status: compat.StatusPass,
	}}}
	// When: that test shows up failing.
	report := reportWithResults(
		resultSpec{suite: "node-js-sdk", service: "sqs", group: "sqs-basic", test: "SendMessage", status: compat.StatusPass},
		resultSpec{suite: "node-js-sdk", service: "sqs", group: "sqs-basic", test: "DeleteMessage", status: compat.StatusFail},
	)
	regressions := compareBaseline(baseline, report)

	// Then: it is a regression. Iterating only over baseline entries would miss
	// it entirely, which is how a brand-new failing test used to land on main
	// with a green check.
	if len(regressions) != 1 {
		t.Fatalf("regressions len = %d, want 1: %#v", len(regressions), regressions)
	}
	if !strings.Contains(regressions[0], "node-js-sdk/sqs-basic/DeleteMessage") {
		t.Fatalf("regression message = %q", regressions[0])
	}
	if !strings.Contains(regressions[0], "not in baseline") {
		t.Fatalf("regression message should explain the test is ungrandfathered: %q", regressions[0])
	}
}

func TestCompareBaseline_newPassingTestIsNotARegression(t *testing.T) {
	// Given: a baseline that predates a newly added test
	baseline := &compatBaseline{Version: baselineVersion}
	// When: the new test passes (or is a known gap)
	report := reportWithResults(
		resultSpec{suite: "go-sdk", service: "s3", group: "s3-crud", test: "CreateBucket", status: compat.StatusPass},
		resultSpec{suite: "go-sdk", service: "s3", group: "s3-crud", test: "DeleteBucket", status: compat.StatusUnimplemented},
		resultSpec{suite: "go-sdk", service: "s3", group: "s3-crud", test: "PutObject", status: compat.StatusSkip},
		resultSpec{suite: "go-sdk", service: "s3", group: "s3-crud", test: "GetObject", status: compat.StatusNA},
	)
	// Then: adding tests never blocks a PR on its own — only failures do.
	if regressions := compareBaseline(baseline, report); len(regressions) != 0 {
		t.Fatalf("regressions = %#v, want none", regressions)
	}
}

func TestCompareBaseline_grandfatheredFailureIsAllowed(t *testing.T) {
	// Given: a baseline that already records a failure (burn-down pending)
	baseline := &compatBaseline{Version: baselineVersion, Entries: []baselineEntry{{
		Suite:  "rust-sdk",
		Group:  "s3-copy",
		Test:   "CreateSourceBucket",
		Status: compat.StatusFail,
	}}}
	report := reportWithResults(resultSpec{suite: "rust-sdk", service: "s3", group: "s3-copy", test: "CreateSourceBucket", status: compat.StatusFail})

	// Then: it is not a regression — the ratchet only forbids getting worse.
	if regressions := compareBaseline(baseline, report); len(regressions) != 0 {
		t.Fatalf("regressions = %#v, want none for a grandfathered failure", regressions)
	}
}

func TestLintBaselineChange_newFailEntryRejected(t *testing.T) {
	// Given: an established baseline, and a change that adds a brand-new
	// expectation at `fail`
	oldBaseline := &compatBaseline{Version: baselineVersion, Entries: []baselineEntry{
		{Suite: "go-sdk", Group: "s3-crud", Test: "DeleteBucket", Status: compat.StatusPass},
	}}
	newBaseline := &compatBaseline{Version: baselineVersion, Entries: []baselineEntry{
		{Suite: "go-sdk", Group: "s3-crud", Test: "CreateBucket", Status: compat.StatusFail},
		{Suite: "go-sdk", Group: "s3-crud", Test: "DeleteBucket", Status: compat.StatusPass},
	}}

	// When: the change is linted
	issues := lintBaselineChange(oldBaseline, newBaseline)

	// Then: the new fail expectation is rejected. Iterating only the old
	// baseline let contributors grandfather fresh failures by adding them.
	if len(issues) != 1 {
		t.Fatalf("issues len = %d, want 1: %#v", len(issues), issues)
	}
	if !strings.Contains(issues[0], "go-sdk/s3-crud/CreateBucket") {
		t.Fatalf("issue message = %q", issues[0])
	}
}

func TestLintBaselineChange_seedingAnEmptyBaselineIsAllowed(t *testing.T) {
	// Given: an empty baseline being populated for the first time, recording
	// reality — which includes failures that already exist
	oldBaseline := &compatBaseline{Version: baselineVersion}
	newBaseline := &compatBaseline{Version: baselineVersion, Entries: []baselineEntry{
		{Suite: "cli", Group: "lambda-invoke", Test: "InvokeDryRun", Status: compat.StatusFail},
		{Suite: "cli", Group: "s3-crud", Test: "CreateBucket", Status: compat.StatusPass},
	}}

	// When: the seeding change is linted
	issues := lintBaselineChange(oldBaseline, newBaseline)

	// Then: it is allowed. There is nothing to regress from, and the burn-down
	// starts from whatever the first run measured.
	if len(issues) != 0 {
		t.Fatalf("issues = %#v, want none when seeding an empty baseline", issues)
	}
}

func TestLintBaselineChange_emptyingAPopulatedBaselineIsRejected(t *testing.T) {
	// Given: someone empties a populated baseline — the move that would
	// otherwise let the next PR re-seed failures freely
	oldBaseline := &compatBaseline{Version: baselineVersion, Entries: []baselineEntry{
		{Suite: "cli", Group: "s3-crud", Test: "CreateBucket", Status: compat.StatusPass},
		{Suite: "cli", Group: "s3-crud", Test: "DeleteBucket", Status: compat.StatusPass},
	}}
	newBaseline := &compatBaseline{Version: baselineVersion}

	// When/Then: every dropped expectation is reported, so the seeding
	// exemption cannot be reached by wiping the file first.
	issues := lintBaselineChange(oldBaseline, newBaseline)
	if len(issues) != 2 {
		t.Fatalf("issues = %#v, want one per removed expectation", issues)
	}
}

func TestCompareBaseline_flakyTestIsToleratedInEitherDirection(t *testing.T) {
	// Given: a test known to be intermittent, declared in the flaky list
	baseline := &compatBaseline{Version: baselineVersion, Entries: []baselineEntry{
		{Suite: "dotnet-sdk", Group: "sns-subscriptions", Test: "PublishDeliveredToSQS", Status: compat.StatusFail},
	}}
	flaky := flakySet{"dotnet-sdk/sns-subscriptions/PublishDeliveredToSQS": true}

	// When: it fails on one run and passes on the next
	for _, status := range []compat.Status{compat.StatusFail, compat.StatusPass, compat.StatusSkip} {
		report := reportWithResults(resultSpec{
			suite: "dotnet-sdk", service: "sns", group: "sns-subscriptions",
			test: "PublishDeliveredToSQS", status: status,
		})
		// Then: neither outcome blocks a PR. An intermittent test that
		// randomly reds the build teaches people to ignore the gate.
		if got := compareBaselineWith(baseline, report, flaky); len(got) != 0 {
			t.Errorf("status %s produced regressions %#v, want none", status, got)
		}
	}
}

func TestCompareBaseline_flakyListDoesNotExcuseOtherTests(t *testing.T) {
	// Given: one flaky test declared, and a different test that regresses
	baseline := &compatBaseline{Version: baselineVersion, Entries: []baselineEntry{
		{Suite: "dotnet-sdk", Group: "sns-subscriptions", Test: "PublishDeliveredToSQS", Status: compat.StatusFail},
		{Suite: "dotnet-sdk", Group: "sns-subscriptions", Test: "SubscribeSQS", Status: compat.StatusPass},
	}}
	flaky := flakySet{"dotnet-sdk/sns-subscriptions/PublishDeliveredToSQS": true}
	report := reportWithResults(
		resultSpec{suite: "dotnet-sdk", service: "sns", group: "sns-subscriptions", test: "PublishDeliveredToSQS", status: compat.StatusPass},
		resultSpec{suite: "dotnet-sdk", service: "sns", group: "sns-subscriptions", test: "SubscribeSQS", status: compat.StatusFail},
	)

	// Then: the quarantine is per test, not a blanket amnesty
	got := compareBaselineWith(baseline, report, flaky)
	if len(got) != 1 || !strings.Contains(got[0], "SubscribeSQS") {
		t.Fatalf("regressions = %#v, want only SubscribeSQS", got)
	}
}

func TestUpdateBaseline_doesNotPromoteFlakyTests(t *testing.T) {
	// Given: a flaky test recorded at its worst observed status
	baseline := &compatBaseline{Version: baselineVersion, Entries: []baselineEntry{
		{Suite: "dotnet-sdk", Group: "sns-subscriptions", Test: "PublishDeliveredToSQS", Status: compat.StatusFail},
		{Suite: "go-sdk", Group: "s3-crud", Test: "CreateBucket", Status: compat.StatusFail},
	}}
	flaky := flakySet{"dotnet-sdk/sns-subscriptions/PublishDeliveredToSQS": true}
	report := reportWithResults(
		resultSpec{suite: "dotnet-sdk", service: "sns", group: "sns-subscriptions", test: "PublishDeliveredToSQS", status: compat.StatusPass},
		resultSpec{suite: "go-sdk", service: "s3", group: "s3-crud", test: "CreateBucket", status: compat.StatusPass},
	)

	// When: the baseline is promoted from a run where the flaky test happened
	// to pass
	updated := updateBaselineWith(baseline, report, flaky)
	entries := baselineEntryMap(updated.Entries)

	// Then: the flaky test keeps its floor — promoting it would make the very
	// next intermittent failure a red build. A genuine fix is promoted by
	// removing it from the flaky list.
	if got := entries["dotnet-sdk/sns-subscriptions/PublishDeliveredToSQS"].Status; got != compat.StatusFail {
		t.Errorf("flaky test promoted to %s, want it held at fail", got)
	}
	// And: everything else still ratchets normally.
	if got := entries["go-sdk/s3-crud/CreateBucket"].Status; got != compat.StatusPass {
		t.Errorf("non-flaky test = %s, want pass", got)
	}
}

func TestBaselineAnnotations_renderGitHubErrorCommands(t *testing.T) {
	// Given: a set of regression messages
	regressions := []string{
		"compat baseline regression: node-js-sdk/sqs-basic/SendMessage pass -> fail",
		"compat baseline new failure, not in baseline: rust-sdk/s3-copy/CreateSourceBucket",
	}

	// When: they are rendered as GitHub workflow commands
	out := baselineAnnotations(regressions)

	// Then: each becomes an ::error line so it lands on the PR checks tab, with
	// newlines escaped — a raw newline would truncate the annotation.
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 2 {
		t.Fatalf("got %d annotation lines, want 2: %q", len(lines), out)
	}
	for i, line := range lines {
		if !strings.HasPrefix(line, "::error title=Compat baseline::") {
			t.Errorf("line %d = %q, want an ::error annotation", i, line)
		}
	}
	if !strings.Contains(lines[0], "SendMessage pass -> fail") {
		t.Errorf("annotation lost its detail: %q", lines[0])
	}
}

func TestBaselineAnnotations_escapesMultilineDetail(t *testing.T) {
	// Given: a regression message carrying a multi-line error body
	out := baselineAnnotations([]string{"compat baseline regression: a/b/c pass -> fail\nsecond line"})

	// When/Then: the annotation stays on one line, with the newline percent-encoded
	// as GitHub's workflow-command format requires.
	if strings.Count(strings.TrimSpace(out), "\n") != 0 {
		t.Fatalf("annotation spans multiple lines: %q", out)
	}
	if !strings.Contains(out, "%0A") {
		t.Fatalf("newline not escaped as %%0A: %q", out)
	}
}

type resultSpec struct {
	suite   string
	service string
	group   string
	test    string
	status  compat.Status
}

func reportWithResults(results ...resultSpec) *compat.RunReport {
	suites := make(map[string]*compat.SuiteReport)
	groups := make(map[string]*compat.GroupReport)
	for _, result := range results {
		suite := suites[result.suite]
		if suite == nil {
			suite = &compat.SuiteReport{Suite: result.suite}
			suites[result.suite] = suite
		}
		groupKey := result.suite + "/" + result.group
		group := groups[groupKey]
		if group == nil {
			group = &compat.GroupReport{Suite: result.suite, Service: result.service, Name: result.group}
			groups[groupKey] = group
			suite.Groups = append(suite.Groups, group)
		}
		group.Tests = append(group.Tests, compat.TestResultEvent{
			Suite:   result.suite,
			Service: result.service,
			Group:   result.group,
			Test:    result.test,
			Status:  result.status,
		})
	}
	var report compat.RunReport
	for _, suite := range suites {
		report.Suites = append(report.Suites, suite)
	}
	return &report
}
