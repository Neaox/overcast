package dynamodb

// expr_reserved.go holds DynamoDB's reserved-word list and the error shape the
// expression compilers use to reject one.
//
// Per the AWS DynamoDB Developer Guide, "Reserved words in DynamoDB"
// (https://docs.aws.amazon.com/amazondynamodb/latest/developerguide/ReservedWords.html):
// "The following keywords are reserved for use by DynamoDB. Don't use any of
// these words as attribute names in expressions. This list isn't
// case-sensitive." The escape hatch is ExpressionAttributeNames — the guide's
// "Expression attribute names (aliases) in DynamoDB" page shows a
// ProjectionExpression of "Comment" failing and "#c" with
// {"#c":"Comment"} succeeding — so the check is on the identifier as *written*
// in the expression, never on the attribute name an alias resolves to.
//
// The rule applies to every attribute name in a document path, nested segments
// included, and to every expression parameter: UpdateExpression,
// ConditionExpression, FilterExpression, KeyConditionExpression and
// ProjectionExpression. Function names are not attribute names, so size(...),
// attribute_exists(...) and friends stay legal even though SIZE and EXISTS are
// on the list — the compilers consume a function name before a path is parsed,
// which is what keeps that true here.
//
// Message shape verified against moto's DynamoDB emulator
// (moto/dynamodb/exceptions.py: InvalidUpdateExpression /
// InvalidConditionExpression + AttributeIsReservedKeyword) and floci's
// DynamoDbReservedWords.check, both of which render
// "Invalid <parameter>: Attribute name is a reserved keyword; reserved
// keyword: <word>", and against the reports collected for the same message on
// KeyConditionExpression and ProjectionExpression.

import (
	"bufio"
	_ "embed"
	"errors"
	"fmt"
	"strings"
	"sync"
)

// reservedWordsData is the published list, one upper-case word per line, with
// '#' comment lines carrying its provenance. Generated — see
// scripts/dynamodb-reserved-words.go.
//
//go:embed reserved_words.txt
var reservedWordsData string

// exprKind is the name of the request parameter an expression arrived in. AWS
// quotes it in the ValidationException it returns for a malformed expression,
// so the compilers have to carry it as far as the error.
type exprKind string

const (
	exprUpdate       exprKind = "UpdateExpression"
	exprCondition    exprKind = "ConditionExpression"
	exprFilter       exprKind = "FilterExpression"
	exprKeyCondition exprKind = "KeyConditionExpression"
	exprProjection   exprKind = "ProjectionExpression"
)

// reservedWords is the parsed list, keyed by the upper-case word. Built once
// on first use rather than in an init or a service New(), which the startup
// budget in CONTRIBUTING.md rules out.
var reservedWords = sync.OnceValue(func() map[string]struct{} {
	words := make(map[string]struct{}, 640)
	sc := bufio.NewScanner(strings.NewReader(reservedWordsData))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		words[line] = struct{}{}
	}
	return words
})

// isReservedWord reports whether ident is a DynamoDB reserved word. The
// comparison is case-insensitive because the published list is not
// case-sensitive; strings.ToUpper is allocation-free for an identifier that is
// already upper-case, and the map lookup is O(1) either way.
func isReservedWord(ident string) bool {
	_, ok := reservedWords()[strings.ToUpper(ident)]
	return ok
}

// reservedWordError reports a reserved word used as a bare attribute name.
//
// It travels up from the path parser as a plain error so the intermediate
// clause context ("SET: …", "ProjectionExpression: …") can wrap it, and is
// unwrapped again by exprValidationError at the top of each compiler — AWS
// names the expression parameter and the keyword, and nothing else.
type reservedWordError struct{ word string }

func (e *reservedWordError) Error() string {
	return "Attribute name is a reserved keyword; reserved keyword: " + e.word
}

// checkReservedWord returns a reservedWordError when ident may not be used as
// a bare attribute name. Callers that resolved ident from an
// ExpressionAttributeNames alias must not call it: the alias is the documented
// way to use a reserved word.
func checkReservedWord(ident string) error {
	if isReservedWord(ident) {
		return &reservedWordError{word: ident}
	}
	return nil
}

// exprValidationError renders a compiler error as the message AWS returns for
// the given expression parameter. Only the reserved-word fault has a
// documented AWS wording; everything else is passed through unchanged.
func exprValidationError(kind exprKind, err error) error {
	var rw *reservedWordError
	if errors.As(err, &rw) {
		return fmt.Errorf("Invalid %s: %s", kind, rw.Error())
	}
	return err
}
