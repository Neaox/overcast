package docslint

import (
	"strings"
	"testing"
)

func tellDoc(sentence string) Doc {
	return Doc{Path: "docs/demo-guide.md", Body: "# Demo\n\n" + sentence + "\n"}
}

func TestCheck_rejectsTheContrastDefinition(t *testing.T) {
	for _, sentence := range []string{
		"The payoff is not the padlock — it is HTTP/2 for the console.",
		"These fields are not merely inert — they are dropped.",
		"This isn't a proxy, it's a full emulator.",
		"Overcast is not just a mock; rather, a wire-compatible emulator.",
	} {
		t.Run(sentence, func(t *testing.T) {
			// Given: a sentence that defines by correcting the reader.
			// When / Then: it is reported with the phrase it matched.
			assertReports(t, Check([]Doc{tellDoc(sentence)}), "contrast-definition")
		})
	}
}

func TestCheck_acceptsOrdinaryNegation(t *testing.T) {
	for _, sentence := range []string{
		"Requests are not blocked — but shutdown's final flush is synchronous.",
		"The domain is not found, rather than an error.",
		"Reads are not distributed, but the writer answers them.",
		"Absence is not permission — a VPC record that names it still adopts it.",
	} {
		t.Run(sentence, func(t *testing.T) {
			// Given: a plain factual statement that happens to contain "not".
			// When / Then: the rule stays out of the way. A lint that fires on
			// these is a lint that gets deleted.
			assertClean(t, Check([]Doc{tellDoc(sentence)}))
		})
	}
}

func TestCheck_rejectsBrochureVocabulary(t *testing.T) {
	for _, sentence := range []string{
		"Containers attach seamlessly to the VPC network.",
		"Let's delve into the routing table.",
		"Overcast is fast, simple, and complete.",
		"It's worth noting that the port is fixed.",
		"It's not about which port you pick.",
	} {
		t.Run(sentence, func(t *testing.T) {
			assertReports(t, Check([]Doc{tellDoc(sentence)}), "docs/demo-guide.md:3")
		})
	}
}

func TestCheck_ignoresTellsInsideFencedCode(t *testing.T) {
	// Given: a sample the writer did not author — a log line, a config value.
	body := "# Demo\n\n```text\nWARN seamless fallback: it is not a proxy — it is a shim\n```\n"

	// When / Then: only prose is the writer's to answer for.
	assertClean(t, Check([]Doc{{Path: "docs/demo-guide.md", Body: body}}))
}

func TestCheck_honoursAnAllowlistedPhrase(t *testing.T) {
	// Given: a phrase argued for once, pinned to the page and the exact text.
	doc := tellDoc("The payoff is not the padlock — it is HTTP/2 for the console.")
	allow := ParseAllowlist("# a comment\n\ndocs/demo-guide.md: is not the padlock — it is\n")

	// When / Then: the exception applies where it was granted.
	assertClean(t, CheckWith([]Doc{doc}, Options{Allowlist: allow}))

	// And: it does not travel to another page.
	elsewhere := Doc{Path: "docs/other.md", Body: doc.Body}
	assertReports(t, CheckWith([]Doc{elsewhere}, Options{Allowlist: allow}), "contrast-definition")
}

func TestCheck_rejectsAnAllowlistEntryThatMatchesNothing(t *testing.T) {
	// Given: an exception whose sentence has since been rewritten.
	allow := ParseAllowlist("docs/demo-guide.md: is not the padlock — it is\n")

	// When / Then: the allowance leaves with the sentence.
	problems := CheckWith([]Doc{tellDoc("The payoff is HTTP/2 for the console.")}, Options{Allowlist: allow, WholeCorpus: true})
	assertReports(t, problems, "match nothing any more")
}

func TestParseAllowlist_readsPathAndPhrase(t *testing.T) {
	// Given: a file with comments, blank lines and a phrase containing a colon.
	got := ParseAllowlist("# why\n\n  docs/a.md :  it is not a cache — it is a queue: really  \nmalformed-line\n")

	// When / Then: one usable entry, trimmed, with the colon in the phrase kept.
	if len(got) != 1 {
		t.Fatalf("got %d entries, want 1: %+v", len(got), got)
	}
	if got[0].Path != "docs/a.md" || !strings.HasSuffix(got[0].Match, "a queue: really") {
		t.Errorf("parsed %+v, want the path and the whole phrase", got[0])
	}
}

func TestCheck_rejectsThePageNarratingItself(t *testing.T) {
	for _, sentence := range []string{
		"This page explains how to configure CDK to target Overcast.",
		"This guide covers the three ways to close that gap.",
		"**This page describes** the wire protocol.",
		"> This guide documents every environment variable.",
	} {
		t.Run(sentence, func(t *testing.T) {
			// Given: an opening sentence about the page rather than about the
			// reader's problem.
			// When / Then: it is reported, with the fix in the message.
			assertReports(t, Check([]Doc{tellDoc(sentence)}), "page-narration")
		})
	}
}

func TestCheck_rejectsThePageDescribingItsOwnJob(t *testing.T) {
	for _, sentence := range []string{
		"Re-approving a root certificate is what this section exists to avoid.",
		"A hand-maintained list rots quickly, so this guide carries none.",
		"This page measures Overcast against LocalStack's interface.",
		"Start with the short version; this page is the audit behind it.",
	} {
		t.Run(sentence, func(t *testing.T) {
			assertReports(t, Check([]Doc{tellDoc(sentence)}), "page-self-reference")
		})
	}
}

func TestCheck_rejectsTheAddedHedgesAndBrochureWord(t *testing.T) {
	for _, sentence := range []string{
		"The port is fixed. Note that a cold start can be slower.",
		"In summary, the two backends differ only in when they flush.",
		"Under the hood this uses GET /_overcast/tls/status.",
	} {
		t.Run(sentence, func(t *testing.T) {
			assertReports(t, Check([]Doc{tellDoc(sentence)}), "docs/demo-guide.md:3")
		})
	}
}

func TestCheck_acceptsOrdinarySelfReferenceAndNotes(t *testing.T) {
	for _, sentence := range []string{
		"This page lists every variable Overcast reads, with its default.",
		"The console notes that a rebuild is pending, and says which build.",
		"A note that names the mode is written to the startup log.",
		"This guide is for the platform engineer.",
	} {
		t.Run(sentence, func(t *testing.T) {
			// Given: a sentence the new rules must not claim.
			// When / Then: a rule that fires on these is a rule that gets
			// allowlisted into meaninglessness.
			assertClean(t, Check([]Doc{tellDoc(sentence)}))
		})
	}
}
