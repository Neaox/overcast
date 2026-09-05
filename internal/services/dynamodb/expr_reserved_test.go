package dynamodb

import (
	"errors"
	"strings"
	"testing"
)

func TestReservedWords_publishedListIsLoaded(t *testing.T) {
	// Given: the list generated from the AWS Developer Guide's
	// "Reserved words in DynamoDB" page

	// When: it is parsed
	got := reservedWords()

	// Then: it holds every word the page publishes, and no comment lines
	const want = 573
	if len(got) != want {
		t.Errorf("reserved word count = %d, want %d — regenerate with "+
			"`go run ./scripts/dynamodb-reserved-words.go` and update this test "+
			"if AWS changed the published list", len(got), want)
	}
	for _, word := range []string{"ABORT", "SIZE", "NAME", "STATUS", "COMMENT", "ZONE"} {
		if _, ok := got[word]; !ok {
			t.Errorf("%q missing from the reserved word list", word)
		}
	}
	for _, word := range []string{"REMOVE", "CONTAINS", "TAGS", ""} {
		if _, ok := got[word]; ok {
			t.Errorf("%q is not on the published list but was loaded", word)
		}
	}
	for word := range got {
		if strings.HasPrefix(word, "#") {
			t.Errorf("comment line %q was loaded as a reserved word", word)
		}
	}
}

func TestIsReservedWord_caseInsensitive(t *testing.T) {
	// Given: a word on the published list, spelled several ways
	cases := []struct {
		ident string
		want  bool
	}{
		{ident: "SIZE", want: true},
		{ident: "size", want: true},
		{ident: "Size", want: true},
		{ident: "sIzE", want: true},
		{ident: "sizes", want: false},
		{ident: "mysize", want: false},
		{ident: "tags", want: false},
	}

	for _, tc := range cases {
		t.Run(tc.ident, func(t *testing.T) {
			// When: it is looked up
			got := isReservedWord(tc.ident)

			// Then: the published list is matched without regard to case
			if got != tc.want {
				t.Errorf("isReservedWord(%q) = %v, want %v", tc.ident, got, tc.want)
			}
		})
	}
}

func TestExprValidationError_namesTheExpressionParameter(t *testing.T) {
	// Given: a reserved-word fault wrapped in intermediate clause context, as
	// the update compiler wraps it
	err := errors.Join(&reservedWordError{word: "size"})

	// When: it is rendered for an UpdateExpression
	got := exprValidationError(exprUpdate, err)

	// Then: only AWS's wording survives — the parameter and the keyword
	const want = "Invalid UpdateExpression: Attribute name is a reserved keyword; reserved keyword: size"
	if got.Error() != want {
		t.Errorf("message mismatch\n got: %s\nwant: %s", got, want)
	}
}

func TestExprValidationError_passesOtherErrorsThrough(t *testing.T) {
	// Given: a parse error that is not a reserved-word fault
	err := errors.New("expected attribute name, got \",\" at position 4")

	// When: it is rendered
	got := exprValidationError(exprFilter, err)

	// Then: it is returned unchanged — AWS's wording is only known for the
	// reserved-word case
	if got != err {
		t.Errorf("got %v, want the original error unchanged", got)
	}
}
