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
