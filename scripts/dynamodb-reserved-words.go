//go:build ignore

// Script: dynamodb-reserved-words
// Regenerates internal/services/dynamodb/reserved_words.txt from the AWS
// DynamoDB Developer Guide's "Reserved words in DynamoDB" page.
//
// Why this exists: DynamoDB rejects a reserved word used as a bare attribute
// name in an expression, and the list is ~570 words long. Typing it out by
// hand makes it unauditable and impossible to refresh when AWS adds a word, so
// the list is fetched from the published page instead and the fetch is
// recorded in the generated file's header.
//
// The page is fetched in its Markdown rendering (ReservedWords.md, linked from
// the HTML page as "View a markdown version of this page"), which puts the
// whole list in a single fenced code block, one word per line. The HTML page's
// body is assembled client-side and does not contain the words at all.
//
// Run: go run ./scripts/dynamodb-reserved-words.go   (or: make generate-ddb-reserved-words)
//
// The generated file is committed — internal/services/dynamodb embeds it, so a
// bare `git clone && go build ./...` has to find it. Nothing in CI re-runs this
// script: it needs network access, and the list changes about as often as AWS
// adds a keyword. Refresh it by hand and review the diff.
package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	sourceURL = "https://docs.aws.amazon.com/amazondynamodb/latest/developerguide/ReservedWords.md"
	pageURL   = "https://docs.aws.amazon.com/amazondynamodb/latest/developerguide/ReservedWords.html"
	outPath   = "internal/services/dynamodb/reserved_words.txt"

	// A sanity floor. The published list has been ~570 words for years; a
	// fetch that returns far fewer means the page changed shape, and silently
	// shrinking the list would turn a validation gate off.
	minWords = 500
)

// codeBlockRE matches the first fenced code block in the Markdown page.
var codeBlockRE = regexp.MustCompile("(?s)```\n(.*?)```")

// wordRE is what a reserved word may look like. Every published word is
// upper-case ASCII letters, which is also what makes an upper-cased lookup a
// correct case-insensitive match.
var wordRE = regexp.MustCompile(`^[A-Z]+$`)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "dynamodb-reserved-words:", err)
		os.Exit(1)
	}
}

func run() error {
	body, err := fetch(sourceURL)
	if err != nil {
		return err
	}

	words, err := parse(body)
	if err != nil {
		return err
	}

	out := render(words)
	path := filepath.FromSlash(outPath)
	if err := os.WriteFile(path, []byte(out), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	fmt.Printf("wrote %s (%d words)\n", path, len(words))
	return nil
}

func fetch(url string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("get %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("get %s: status %d", url, resp.StatusCode)
	}
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", url, err)
	}
	return string(b), nil
}

func parse(page string) ([]string, error) {
	m := codeBlockRE.FindStringSubmatch(page)
	if m == nil {
		return nil, fmt.Errorf("no fenced code block in %s — the page shape changed", sourceURL)
	}

	seen := make(map[string]struct{})
	var words []string
	for _, line := range strings.Split(m[1], "\n") {
		word := strings.TrimSpace(line)
		if word == "" {
			continue
		}
		if !wordRE.MatchString(word) {
			return nil, fmt.Errorf("unexpected entry %q in the reserved-word block — the page shape changed", word)
		}
		if _, dup := seen[word]; dup {
			continue
		}
		seen[word] = struct{}{}
		words = append(words, word)
	}
	if len(words) < minWords {
		return nil, fmt.Errorf("only %d words parsed, expected at least %d — the page shape changed", len(words), minWords)
	}
	sort.Strings(words)
	return words, nil
}

func render(words []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, `# DynamoDB reserved words. GENERATED FILE — DO NOT EDIT.
#
# Source:  %s
#          (the Markdown rendering of %s)
# Fetched: %s
# Count:   %d
#
# Regenerate with: go run ./scripts/dynamodb-reserved-words.go
#
# AWS: "The following keywords are reserved for use by DynamoDB. Don't use any
# of these words as attribute names in expressions. This list isn't
# case-sensitive." Lines starting with '#' and blank lines are ignored by the
# loader in internal/services/dynamodb/expr_reserved.go.
`, sourceURL, pageURL, time.Now().UTC().Format(time.DateOnly), len(words))
	for _, w := range words {
		b.WriteString(w)
		b.WriteString("\n")
	}
	return b.String()
}
