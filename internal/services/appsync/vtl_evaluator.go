package appsync

// vtl_evaluator.go — minimal VTL (Velocity Template Language) interpreter for AppSync.
//
// Implements the MappingTemplateEvaluator interface defined in executor.go.
// Supports the subset of VTL that AppSync resolvers actually use:
//   - Variable references ($context, $ctx, $util, custom vars)
//   - Property access and method calls on references
//   - #set, #if/#elseif/#else/#end, #foreach/#end, #return
//   - Quiet references ($!var)
//   - String interpolation
//   - JSON object/array literals in template output
//   - $util helper functions (toJson, parseJson, autoId, time, dynamodb, etc.)
//   - Math, comparison, and logical operators

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"github.com/google/uuid"

	"github.com/Neaox/overcast/internal/clock"
)

// ─── VTL Error types ─────────────────────────────────────────────────────────

// vtlError is raised by $util.error() to abort template evaluation.
type vtlError struct {
	Message   string
	ErrorType string
	Data      any
}

func (e *vtlError) Error() string { return e.Message }

// vtlReturnError signals #return($data) — caught by Evaluate to produce output.
type vtlReturnError struct {
	Value string
}

func (e *vtlReturnError) Error() string { return e.Value }

// vtlAppendError holds a non-fatal error appended via $util.appendError().
type vtlAppendError struct {
	Message   string
	ErrorType string
	Data      any
}

// ─── VTL Evaluator ───────────────────────────────────────────────────────────

// vtlEvaluatorImpl implements the MappingTemplateEvaluator interface.
type vtlEvaluatorImpl struct {
	clk clock.Clock
}

// newVTLEvaluator creates a new VTL evaluator with the given clock.
func newVTLEvaluator(clk clock.Clock) *vtlEvaluatorImpl {
	return &vtlEvaluatorImpl{clk: clk}
}

// Evaluate processes a VTL template string with the given context map.
func (v *vtlEvaluatorImpl) Evaluate(template string, context map[string]any) (result string, err error) {
	// Catch panics from $util.error() etc.
	defer func() {
		if r := recover(); r != nil {
			switch e := r.(type) {
			case *vtlError:
				err = e
			case *vtlReturnError:
				result = e.Value
				err = nil
			default:
				err = fmt.Errorf("appsync: vtl: panic: %v", r)
			}
		}
	}()

	scope := newVTLScope(context, v.clk)
	tokens := vtlLex(template)
	nodes := vtlParse(tokens)
	var buf strings.Builder
	vtlExec(nodes, scope, &buf)
	return buf.String(), nil
}

// ─── VTL Scope (variable environment) ────────────────────────────────────────

type vtlScope struct {
	vars map[string]any
	clk  clock.Clock
	// appended errors collected by $util.appendError
	appendedErrors []vtlAppendError
}

func newVTLScope(context map[string]any, clk clock.Clock) *vtlScope {
	s := &vtlScope{
		vars: make(map[string]any),
		clk:  clk,
	}
	if context == nil {
		context = map[string]any{}
	}
	s.vars["context"] = context
	s.vars["ctx"] = context

	// Build $util
	s.vars["util"] = s.buildUtil()

	// $null
	s.vars["null"] = nil

	return s
}

func (s *vtlScope) get(name string) (any, bool) {
	v, ok := s.vars[name]
	return v, ok
}

func (s *vtlScope) set(name string, val any) {
	s.vars[name] = val
}

// ─── $util object ────────────────────────────────────────────────────────────

type vtlFunc func(args []any) any

func (s *vtlScope) buildUtil() map[string]any {
	util := map[string]any{
		"toJson":               vtlFunc(utilToJson),
		"parseJson":            vtlFunc(utilParseJson),
		"autoId":               vtlFunc(utilAutoId),
		"isNull":               vtlFunc(utilIsNull),
		"isNullOrEmpty":        vtlFunc(utilIsNullOrEmpty),
		"isNullOrBlank":        vtlFunc(utilIsNullOrEmpty),
		"defaultIfNull":        vtlFunc(utilDefaultIfNull),
		"defaultIfNullOrEmpty": vtlFunc(utilDefaultIfNullOrEmpty),
		"defaultIfNullOrBlank": vtlFunc(utilDefaultIfNullOrEmpty),
		"isString":             vtlFunc(utilIsString),
		"isList":               vtlFunc(utilIsList),
		"isMap":                vtlFunc(utilIsMap),
		"isNumber":             vtlFunc(utilIsNumber),
		"isBoolean":            vtlFunc(utilIsBoolean),
		"matches":              vtlFunc(utilMatches),
		"error":                vtlFunc(utilError),
		"appendError":          vtlFunc(s.utilAppendError),
		"validate":             vtlFunc(utilValidate),
	}

	// $util.time
	util["time"] = map[string]any{
		"nowISO8601":           vtlFunc(s.utilTimeNowISO8601),
		"nowEpochSeconds":      vtlFunc(s.utilTimeNowEpochSeconds),
		"nowEpochMilliSeconds": vtlFunc(s.utilTimeNowEpochMilliSeconds),
	}

	// $util.dynamodb
	util["dynamodb"] = map[string]any{
		"toDynamoDBJson":  vtlFunc(utilDynamoDBToDynamoDBJson),
		"toMapValuesJson": vtlFunc(utilDynamoDBToMapValuesJson),
		"toStringJson":    vtlFunc(utilDynamoDBToStringJson),
		"toNumberJson":    vtlFunc(utilDynamoDBToNumberJson),
		"toBooleanJson":   vtlFunc(utilDynamoDBToBooleanJson),
		"toNullJson":      vtlFunc(utilDynamoDBToNullJson),
		"toListJson":      vtlFunc(utilDynamoDBToListJson),
		"toMapJson":       vtlFunc(utilDynamoDBToMapJson),
		"toStringSetJson": vtlFunc(utilDynamoDBToStringSetJson),
		"toNumberSetJson": vtlFunc(utilDynamoDBToNumberSetJson),
	}

	// $util.transform
	util["transform"] = vtlBuildTransform()

	// $util.http
	util["http"] = vtlBuildHttp()

	// $util.str
	util["str"] = vtlBuildStr()

	return util
}

// ─── $util function implementations ──────────────────────────────────────────

func utilToJson(args []any) any {
	if len(args) == 0 {
		return "null"
	}
	b, err := json.Marshal(args[0])
	if err != nil {
		return "null"
	}
	return string(b)
}

func utilParseJson(args []any) any {
	if len(args) == 0 {
		return nil
	}
	s, ok := args[0].(string)
	if !ok {
		return nil
	}
	var v any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		return nil
	}
	return v
}

func utilAutoId(_ []any) any {
	return uuid.New().String()
}

func utilIsNull(args []any) any {
	if len(args) == 0 {
		return true
	}
	return args[0] == nil
}

func utilIsNullOrEmpty(args []any) any {
	if len(args) == 0 {
		return true
	}
	v := args[0]
	if v == nil {
		return true
	}
	if s, ok := v.(string); ok {
		return s == ""
	}
	if l, ok := v.([]any); ok {
		return len(l) == 0
	}
	if m, ok := v.(map[string]any); ok {
		return len(m) == 0
	}
	return false
}

func utilDefaultIfNull(args []any) any {
	if len(args) < 2 {
		return nil
	}
	if args[0] == nil {
		return args[1]
	}
	return args[0]
}

func utilDefaultIfNullOrEmpty(args []any) any {
	if len(args) < 2 {
		return nil
	}
	v := args[0]
	if v == nil {
		return args[1]
	}
	if s, ok := v.(string); ok && s == "" {
		return args[1]
	}
	return v
}

func utilIsString(args []any) any {
	if len(args) == 0 {
		return false
	}
	_, ok := args[0].(string)
	return ok
}

func utilIsList(args []any) any {
	if len(args) == 0 {
		return false
	}
	_, ok := args[0].([]any)
	return ok
}

func utilIsMap(args []any) any {
	if len(args) == 0 {
		return false
	}
	_, ok := args[0].(map[string]any)
	return ok
}

func utilIsNumber(args []any) any {
	if len(args) == 0 {
		return false
	}
	switch args[0].(type) {
	case float64, float32, int, int64, int32, json.Number:
		return true
	}
	return false
}

func utilIsBoolean(args []any) any {
	if len(args) == 0 {
		return false
	}
	_, ok := args[0].(bool)
	return ok
}

func utilMatches(args []any) any {
	if len(args) < 2 {
		return false
	}
	pattern, ok1 := args[0].(string)
	str, ok2 := args[1].(string)
	if !ok1 || !ok2 {
		return false
	}
	matched, err := regexp.MatchString(pattern, str)
	if err != nil {
		return false
	}
	return matched
}

func utilError(args []any) any {
	msg := ""
	if len(args) > 0 {
		msg = vtlToString(args[0])
	}
	errType := ""
	if len(args) > 1 {
		errType = vtlToString(args[1])
	}
	var data any
	if len(args) > 2 {
		data = args[2]
	}
	panic(&vtlError{Message: msg, ErrorType: errType, Data: data})
}

func (s *vtlScope) utilAppendError(args []any) any {
	msg := ""
	if len(args) > 0 {
		msg = vtlToString(args[0])
	}
	errType := ""
	if len(args) > 1 {
		errType = vtlToString(args[1])
	}
	var data any
	if len(args) > 2 {
		data = args[2]
	}
	s.appendedErrors = append(s.appendedErrors, vtlAppendError{
		Message: msg, ErrorType: errType, Data: data,
	})
	return nil
}

func utilValidate(args []any) any {
	if len(args) < 2 {
		return nil
	}
	b := vtlToBool(args[0])
	if !b {
		msg := vtlToString(args[1])
		errType := ""
		if len(args) > 2 {
			errType = vtlToString(args[2])
		}
		panic(&vtlError{Message: msg, ErrorType: errType})
	}
	return nil
}

// $util.time implementations.
func (s *vtlScope) utilTimeNowISO8601(_ []any) any {
	return s.clk.Now().UTC().Format("2006-01-02T15:04:05.000Z")
}

func (s *vtlScope) utilTimeNowEpochSeconds(_ []any) any {
	return float64(s.clk.Now().Unix())
}

func (s *vtlScope) utilTimeNowEpochMilliSeconds(_ []any) any {
	return float64(s.clk.Now().UnixMilli())
}

// ─── $util.dynamodb implementations ──────────────────────────────────────────

func toDynamoDBValue(v any) any {
	if v == nil {
		return map[string]any{"NULL": true}
	}
	switch val := v.(type) {
	case string:
		return map[string]any{"S": val}
	case bool:
		return map[string]any{"BOOL": val}
	case float64:
		return map[string]any{"N": strconv.FormatFloat(val, 'f', -1, 64)}
	case int:
		return map[string]any{"N": strconv.Itoa(val)}
	case int64:
		return map[string]any{"N": strconv.FormatInt(val, 10)}
	case json.Number:
		return map[string]any{"N": val.String()}
	case []any:
		items := make([]any, len(val))
		for i, item := range val {
			items[i] = toDynamoDBValue(item)
		}
		return map[string]any{"L": items}
	case map[string]any:
		m := make(map[string]any, len(val))
		for k, v := range val {
			m[k] = toDynamoDBValue(v)
		}
		return map[string]any{"M": m}
	default:
		return map[string]any{"S": fmt.Sprintf("%v", v)}
	}
}

func utilDynamoDBToDynamoDBJson(args []any) any {
	if len(args) == 0 {
		return "null"
	}
	v := toDynamoDBValue(args[0])
	b, _ := json.Marshal(v)
	return string(b)
}

func utilDynamoDBToMapValuesJson(args []any) any {
	if len(args) == 0 {
		return "{}"
	}
	m, ok := args[0].(map[string]any)
	if !ok {
		return "{}"
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = toDynamoDBValue(v)
	}
	b, _ := json.Marshal(out)
	return string(b)
}

func utilDynamoDBToStringJson(args []any) any {
	if len(args) == 0 {
		return `{"S":""}`
	}
	s := vtlToString(args[0])
	b, _ := json.Marshal(map[string]any{"S": s})
	return string(b)
}

func utilDynamoDBToNumberJson(args []any) any {
	if len(args) == 0 {
		return `{"N":"0"}`
	}
	n := vtlToString(args[0])
	b, _ := json.Marshal(map[string]any{"N": n})
	return string(b)
}

func utilDynamoDBToBooleanJson(args []any) any {
	if len(args) == 0 {
		return `{"BOOL":false}`
	}
	bv := vtlToBool(args[0])
	b, _ := json.Marshal(map[string]any{"BOOL": bv})
	return string(b)
}

func utilDynamoDBToNullJson(_ []any) any {
	return `{"NULL":true}`
}

func utilDynamoDBToListJson(args []any) any {
	if len(args) == 0 {
		return `{"L":[]}`
	}
	list, ok := args[0].([]any)
	if !ok {
		return `{"L":[]}`
	}
	items := make([]any, len(list))
	for i, item := range list {
		items[i] = toDynamoDBValue(item)
	}
	b, _ := json.Marshal(map[string]any{"L": items})
	return string(b)
}

func utilDynamoDBToMapJson(args []any) any {
	if len(args) == 0 {
		return `{"M":{}}`
	}
	m, ok := args[0].(map[string]any)
	if !ok {
		return `{"M":{}}`
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = toDynamoDBValue(v)
	}
	b, _ := json.Marshal(map[string]any{"M": out})
	return string(b)
}

func utilDynamoDBToStringSetJson(args []any) any {
	if len(args) == 0 {
		return `{"SS":[]}`
	}
	list, ok := args[0].([]any)
	if !ok {
		return `{"SS":[]}`
	}
	ss := make([]string, len(list))
	for i, item := range list {
		ss[i] = vtlToString(item)
	}
	b, _ := json.Marshal(map[string]any{"SS": ss})
	return string(b)
}

func utilDynamoDBToNumberSetJson(args []any) any {
	if len(args) == 0 {
		return `{"NS":[]}`
	}
	list, ok := args[0].([]any)
	if !ok {
		return `{"NS":[]}`
	}
	ns := make([]string, len(list))
	for i, item := range list {
		ns[i] = vtlToString(item)
	}
	b, _ := json.Marshal(map[string]any{"NS": ns})
	return string(b)
}

// ─── VTL Token types ─────────────────────────────────────────────────────────

type vtlTokenType int

const (
	tokText    vtlTokenType = iota // literal text
	tokRef                         // $var or $!var reference
	tokSet                         // #set(...)
	tokIf                          // #if(...)
	tokElseIf                      // #elseif(...)
	tokElse                        // #else
	tokEnd                         // #end
	tokForeach                     // #foreach(...)
	tokReturn                      // #return(...)
	tokComment                     // ## comment (discarded)
)

type vtlToken struct {
	typ  vtlTokenType
	text string // raw text or expression content
}

// ─── Lexer ───────────────────────────────────────────────────────────────────

func vtlLex(input string) []vtlToken {
	var tokens []vtlToken
	i := 0
	n := len(input)

	for i < n {
		// Check for ## single-line comment
		if i < n-1 && input[i] == '#' && input[i+1] == '#' {
			j := i + 2
			for j < n && input[j] != '\n' {
				j++
			}
			if j < n {
				j++ // skip newline
			}
			i = j
			continue
		}

		// Check for #* ... *# block comment
		if i < n-1 && input[i] == '#' && input[i+1] == '*' {
			j := i + 2
			for j < n-1 {
				if input[j] == '*' && input[j+1] == '#' {
					j += 2
					break
				}
				j++
			}
			if j >= n-1 {
				j = n
			}
			i = j
			continue
		}

		// Check for directives: #set, #if, #elseif, #else, #end, #foreach, #return
		if input[i] == '#' {
			if tok, end, ok := lexDirective(input, i, n); ok {
				tokens = append(tokens, tok)
				i = end
				continue
			}
		}

		// Check for references: $ or $!
		if input[i] == '$' {
			if tok, end, ok := lexReference(input, i, n); ok {
				tokens = append(tokens, tok)
				i = end
				continue
			}
		}

		// Text
		j := i + 1
		for j < n {
			if input[j] == '#' || input[j] == '$' {
				break
			}
			j++
		}
		tokens = append(tokens, vtlToken{typ: tokText, text: input[i:j]})
		i = j
	}

	return tokens
}

func lexDirective(input string, i, n int) (vtlToken, int, bool) {
	rest := input[i:]

	directives := []struct {
		prefix string
		typ    vtlTokenType
		paren  bool
	}{
		{"#set(", tokSet, true},
		{"#set (", tokSet, true},
		{"#if(", tokIf, true},
		{"#if (", tokIf, true},
		{"#elseif(", tokElseIf, true},
		{"#elseif (", tokElseIf, true},
		{"#foreach(", tokForeach, true},
		{"#foreach (", tokForeach, true},
		{"#return(", tokReturn, true},
		{"#return (", tokReturn, true},
		{"#else", tokElse, false},
		{"#end", tokEnd, false},
	}

	for _, d := range directives {
		if !strings.HasPrefix(rest, d.prefix) {
			continue
		}
		if d.paren {
			// Find matching close paren
			start := i + len(d.prefix)
			depth := 1
			j := start
			inStr := false
			strChar := byte(0)
			for j < n && depth > 0 {
				c := input[j]
				if inStr {
					switch c {
					case '\\':
						j++
					case strChar:
						inStr = false
					}
				} else {
					switch c {
					case '"', '\'':
						inStr = true
						strChar = c
					case '(':
						depth++
					case ')':
						depth--
					}
				}
				j++
			}
			content := input[start : j-1]
			return vtlToken{typ: d.typ, text: strings.TrimSpace(content)}, j, true
		}
		// Non-parenthesized directive (#else, #end)
		end := i + len(d.prefix)
		// #else and #end should not be followed by alphanumeric
		if end < n && (unicode.IsLetter(rune(input[end])) || unicode.IsDigit(rune(input[end]))) {
			continue
		}
		return vtlToken{typ: d.typ}, end, true
	}

	return vtlToken{}, 0, false
}

func lexReference(input string, i, n int) (vtlToken, int, bool) {
	j := i + 1

	// Quiet reference: $!
	quiet := false
	if j < n && input[j] == '!' {
		quiet = true
		j++
	}

	// Optional braces: ${var} or $!{var}
	braced := false
	if j < n && input[j] == '{' {
		braced = true
		j++
	}

	// Read the variable name (must start with letter or _)
	if j >= n || (!unicode.IsLetter(rune(input[j])) && input[j] != '_') {
		return vtlToken{}, 0, false
	}

	start := j
	for j < n && (unicode.IsLetter(rune(input[j])) || unicode.IsDigit(rune(input[j])) || input[j] == '_') {
		j++
	}

	// Read chain of .property, .method(), [index], .get("key")
	for j < n {
		if input[j] == '.' {
			j++
			propStart := j
			for j < n && (unicode.IsLetter(rune(input[j])) || unicode.IsDigit(rune(input[j])) || input[j] == '_') {
				j++
			}
			if j == propStart {
				j-- // back to the dot, it's not a property access
				break
			}
			// Check for method call with parentheses
			if j < n && input[j] == '(' {
				depth := 1
				j++
				inStr := false
				strChar := byte(0)
				for j < n && depth > 0 {
					c := input[j]
					if inStr {
						switch c {
						case '\\':
							j++
						case strChar:
							inStr = false
						}
					} else {
						switch c {
						case '"', '\'':
							inStr = true
							strChar = c
						case '(':
							depth++
						case ')':
							depth--
						}
					}
					j++
				}
			}
		} else if input[j] == '[' {
			depth := 1
			j++
			for j < n && depth > 0 {
				switch input[j] {
				case '[':
					depth++
				case ']':
					depth--
				}
				j++
			}
		} else {
			break
		}
	}

	if braced {
		if j < n && input[j] == '}' {
			j++
		}
	}

	expr := input[start:j]
	if braced && j > 0 && input[j-1] == '}' {
		expr = input[start : j-1]
	}

	prefix := "$"
	if quiet {
		prefix = "$!"
	}
	return vtlToken{typ: tokRef, text: prefix + expr}, j, true
}

// ─── AST Nodes ───────────────────────────────────────────────────────────────

type vtlNodeType int

const (
	nodeText    vtlNodeType = iota
	nodeRef                 // variable/expression reference
	nodeSet                 // #set assignment
	nodeIf                  // if/elseif/else chain
	nodeForeach             // #foreach loop
	nodeReturn              // #return
)

type vtlNode struct {
	typ      vtlNodeType
	text     string      // for nodeText
	expr     string      // for nodeRef, nodeSet, nodeReturn
	children []vtlNode   // body (for if/foreach)
	branches []vtlBranch // for nodeIf: condition + body pairs
	// foreach fields
	iterVar  string
	iterExpr string
}

type vtlBranch struct {
	condition string // empty for #else
	body      []vtlNode
}

// ─── Parser (tokens → AST) ──────────────────────────────────────────────────

func vtlParse(tokens []vtlToken) []vtlNode {
	nodes, _ := vtlParseUntil(tokens, 0, false, false)
	return nodes
}

func vtlParseUntil(tokens []vtlToken, start int, stopAtEnd bool, stopAtElse bool) ([]vtlNode, int) {
	var nodes []vtlNode
	i := start

	for i < len(tokens) {
		tok := tokens[i]

		switch tok.typ {
		case tokText:
			nodes = append(nodes, vtlNode{typ: nodeText, text: tok.text})
			i++

		case tokRef:
			nodes = append(nodes, vtlNode{typ: nodeRef, expr: tok.text})
			i++

		case tokSet:
			nodes = append(nodes, vtlNode{typ: nodeSet, expr: tok.text})
			i++

		case tokReturn:
			nodes = append(nodes, vtlNode{typ: nodeReturn, expr: tok.text})
			i++

		case tokIf:
			node, end := parseIf(tokens, i)
			nodes = append(nodes, node)
			i = end

		case tokForeach:
			node, end := parseForeach(tokens, i)
			nodes = append(nodes, node)
			i = end

		case tokEnd:
			if stopAtEnd {
				return nodes, i + 1
			}
			i++

		case tokElseIf, tokElse:
			if stopAtElse {
				return nodes, i
			}
			i++

		case tokComment:
			i++ // comments are discarded; skip

		default:
			i++
		}
	}

	return nodes, i
}

func parseIf(tokens []vtlToken, start int) (vtlNode, int) {
	node := vtlNode{typ: nodeIf}
	i := start

	// First branch: #if(condition)
	cond := tokens[i].text
	i++
	body, end := vtlParseUntil(tokens, i, true, true)
	node.branches = append(node.branches, vtlBranch{condition: cond, body: body})
	i = end

	// #elseif / #else branches
	for i < len(tokens) {
		if tokens[i].typ == tokElseIf {
			cond := tokens[i].text
			i++
			body, end := vtlParseUntil(tokens, i, true, true)
			node.branches = append(node.branches, vtlBranch{condition: cond, body: body})
			i = end
		} else if tokens[i].typ == tokElse {
			i++
			body, end := vtlParseUntil(tokens, i, true, false)
			node.branches = append(node.branches, vtlBranch{condition: "", body: body})
			i = end
		} else if tokens[i].typ == tokEnd {
			i++
			break
		} else {
			break
		}
	}

	return node, i
}

func parseForeach(tokens []vtlToken, start int) (vtlNode, int) {
	node := vtlNode{typ: nodeForeach}
	// Parse "#foreach($item in $list)" — text is "item in $list" or "$item in $list"
	expr := tokens[start].text
	parts := strings.SplitN(expr, " in ", 2)
	if len(parts) == 2 {
		node.iterVar = strings.TrimPrefix(strings.TrimSpace(parts[0]), "$")
		node.iterExpr = strings.TrimSpace(parts[1])
	}
	i := start + 1
	body, end := vtlParseUntil(tokens, i, true, false)
	node.children = body
	return node, end
}

// ─── Executor (AST → output) ────────────────────────────────────────────────

func vtlExec(nodes []vtlNode, scope *vtlScope, buf *strings.Builder) {
	for _, node := range nodes {
		switch node.typ {
		case nodeText:
			buf.WriteString(node.text)

		case nodeRef:
			val := vtlEvalRef(node.expr, scope)
			// quiet references: $!var returns "" if nil
			if strings.HasPrefix(node.expr, "$!") {
				if val == nil {
					// empty string
				} else {
					buf.WriteString(vtlToString(val))
				}
			} else {
				if val == nil {
					// VTL behavior: output the literal reference if not found
					buf.WriteString(node.expr)
				} else {
					buf.WriteString(vtlToString(val))
				}
			}

		case nodeSet:
			vtlExecSet(node.expr, scope)

		case nodeIf:
			vtlExecIf(node, scope, buf)

		case nodeForeach:
			vtlExecForeach(node, scope, buf)

		case nodeReturn:
			val := vtlEvalExpr(node.expr, scope)
			s := vtlToString(val)
			panic(&vtlReturnError{Value: s})
		}
	}
}

func vtlExecSet(expr string, scope *vtlScope) {
	// Parse "$varName = expression" or "$context.stash.key = expression"
	parts := strings.SplitN(expr, "=", 2)
	if len(parts) != 2 {
		return
	}
	lhs := strings.TrimSpace(parts[0])
	rhs := strings.TrimSpace(parts[1])

	val := vtlEvalExpr(rhs, scope)

	// Strip $ prefix
	varPath := strings.TrimPrefix(lhs, "$")

	// Check for nested property assignment: context.stash.key
	dotParts := strings.Split(varPath, ".")
	if len(dotParts) == 1 {
		scope.set(varPath, val)
		return
	}

	// Walk the chain to the parent, then set the leaf property.
	root, ok := scope.get(dotParts[0])
	if !ok {
		scope.set(dotParts[0], val)
		return
	}

	current := root
	for i := 1; i < len(dotParts)-1; i++ {
		m, mok := current.(map[string]any)
		if !mok {
			return
		}
		next, exists := m[dotParts[i]]
		if !exists {
			// Create intermediate map
			next = map[string]any{}
			m[dotParts[i]] = next
		}
		current = next
	}

	// Set the final property
	if m, mok := current.(map[string]any); mok {
		m[dotParts[len(dotParts)-1]] = val
	}
}

func vtlExecIf(node vtlNode, scope *vtlScope, buf *strings.Builder) {
	for _, branch := range node.branches {
		if branch.condition == "" {
			// #else
			vtlExec(branch.body, scope, buf)
			return
		}
		cond := vtlEvalExpr(branch.condition, scope)
		if vtlToBool(cond) {
			vtlExec(branch.body, scope, buf)
			return
		}
	}
}

func vtlExecForeach(node vtlNode, scope *vtlScope, buf *strings.Builder) {
	listVal := vtlEvalExpr(node.iterExpr, scope)
	list, ok := listVal.([]any)
	if !ok {
		return
	}

	foreachMap := map[string]any{}
	scope.set("foreach", foreachMap)

	for i, item := range list {
		scope.set(node.iterVar, item)
		foreachMap["count"] = float64(i + 1)
		foreachMap["index"] = float64(i)
		foreachMap["hasNext"] = i < len(list)-1
		foreachMap["first"] = i == 0
		foreachMap["last"] = i == len(list)-1
		vtlExec(node.children, scope, buf)
	}
}

// ─── Expression Evaluator ────────────────────────────────────────────────────

// vtlEvalExpr evaluates a VTL expression string and returns a Go value.
func vtlEvalExpr(expr string, scope *vtlScope) any {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return nil
	}

	// Parse as logical OR expression (lowest precedence)
	val, _ := vtlParseOrExpr(expr, 0, scope)
	return val
}

// vtlParseOrExpr handles ||.
func vtlParseOrExpr(expr string, pos int, scope *vtlScope) (any, int) {
	left, pos := vtlParseAndExpr(expr, pos, scope)
	for {
		pos = skipWS(expr, pos)
		if pos+1 < len(expr) && expr[pos] == '|' && expr[pos+1] == '|' {
			pos += 2
			right, newPos := vtlParseAndExpr(expr, pos, scope)
			pos = newPos
			if vtlToBool(left) {
				left = true
			} else {
				left = vtlToBool(right)
			}
		} else {
			break
		}
	}
	return left, pos
}

// vtlParseAndExpr handles &&.
func vtlParseAndExpr(expr string, pos int, scope *vtlScope) (any, int) {
	left, pos := vtlParseCompExpr(expr, pos, scope)
	for {
		pos = skipWS(expr, pos)
		if pos+1 < len(expr) && expr[pos] == '&' && expr[pos+1] == '&' {
			pos += 2
			right, newPos := vtlParseCompExpr(expr, pos, scope)
			pos = newPos
			left = vtlToBool(left) && vtlToBool(right)
		} else {
			break
		}
	}
	return left, pos
}

// vtlParseCompExpr handles ==, !=, <, >, <=, >=.
func vtlParseCompExpr(expr string, pos int, scope *vtlScope) (any, int) {
	left, pos := vtlParseAddExpr(expr, pos, scope)
	pos = skipWS(expr, pos)
	if pos >= len(expr) {
		return left, pos
	}

	// Check for comparison operators
	op := ""
	if pos+1 < len(expr) {
		two := expr[pos : pos+2]
		if two == "==" || two == "!=" || two == "<=" || two == ">=" {
			op = two
			pos += 2
		}
	}
	if op == "" && pos < len(expr) {
		if expr[pos] == '<' || expr[pos] == '>' {
			op = string(expr[pos])
			pos++
		}
	}
	if op == "" {
		return left, pos
	}

	right, pos := vtlParseAddExpr(expr, pos, scope)
	return vtlCompare(left, right, op), pos
}

// vtlParseAddExpr handles + and -.
func vtlParseAddExpr(expr string, pos int, scope *vtlScope) (any, int) {
	left, pos := vtlParseMulExpr(expr, pos, scope)
	for {
		pos = skipWS(expr, pos)
		if pos >= len(expr) {
			break
		}
		c := expr[pos]
		if c == '+' || c == '-' {
			// Make sure '-' is not part of a negative number in primary
			pos++
			right, newPos := vtlParseMulExpr(expr, pos, scope)
			pos = newPos
			lf := vtlToFloat(left)
			rf := vtlToFloat(right)
			if c == '+' {
				// If either side is a string and not a pure number, concatenate
				_, lok := left.(string)
				_, rok := right.(string)
				if lok || rok {
					left = vtlToString(left) + vtlToString(right)
				} else {
					left = lf + rf
				}
			} else {
				left = lf - rf
			}
		} else {
			break
		}
	}
	return left, pos
}

// vtlParseMulExpr handles * and /.
func vtlParseMulExpr(expr string, pos int, scope *vtlScope) (any, int) {
	left, pos := vtlParseUnaryExpr(expr, pos, scope)
	for {
		pos = skipWS(expr, pos)
		if pos >= len(expr) {
			break
		}
		c := expr[pos]
		if c == '*' || c == '/' {
			pos++
			right, newPos := vtlParseUnaryExpr(expr, pos, scope)
			pos = newPos
			lf := vtlToFloat(left)
			rf := vtlToFloat(right)
			if c == '*' {
				left = lf * rf
			} else {
				if rf == 0 {
					left = math.NaN()
				} else {
					left = lf / rf
				}
			}
		} else {
			break
		}
	}
	return left, pos
}

// vtlParseUnaryExpr handles ! (not).
func vtlParseUnaryExpr(expr string, pos int, scope *vtlScope) (any, int) {
	pos = skipWS(expr, pos)
	if pos < len(expr) && expr[pos] == '!' {
		// Check it's not != comparison
		if pos+1 < len(expr) && expr[pos+1] == '=' {
			return vtlParsePrimary(expr, pos, scope)
		}
		pos++
		val, pos := vtlParseUnaryExpr(expr, pos, scope)
		return !vtlToBool(val), pos
	}
	return vtlParsePrimary(expr, pos, scope)
}

// vtlParsePrimary handles atoms (literals, references, parenthesized expressions).
func vtlParsePrimary(expr string, pos int, scope *vtlScope) (any, int) {
	pos = skipWS(expr, pos)
	if pos >= len(expr) {
		return nil, pos
	}

	c := expr[pos]

	// Parenthesized expression
	if c == '(' {
		pos++ // skip (
		val, newPos := vtlParseOrExpr(expr, pos, scope)
		pos = skipWS(expr, newPos)
		if pos < len(expr) && expr[pos] == ')' {
			pos++
		}
		return val, pos
	}

	// String literal
	if c == '"' || c == '\'' {
		return vtlParseString(expr, pos, scope)
	}

	// Numeric literal
	if c >= '0' && c <= '9' || (c == '-' && pos+1 < len(expr) && expr[pos+1] >= '0' && expr[pos+1] <= '9') {
		return vtlParseNumber(expr, pos)
	}

	// Boolean literals
	if strings.HasPrefix(expr[pos:], "true") && (pos+4 >= len(expr) || !unicode.IsLetter(rune(expr[pos+4]))) {
		return true, pos + 4
	}
	if strings.HasPrefix(expr[pos:], "false") && (pos+5 >= len(expr) || !unicode.IsLetter(rune(expr[pos+5]))) {
		return false, pos + 5
	}

	// null literal
	if strings.HasPrefix(expr[pos:], "null") && (pos+4 >= len(expr) || !unicode.IsLetter(rune(expr[pos+4]))) {
		return nil, pos + 4
	}

	// List literal: [...]
	if c == '[' {
		return vtlParseList(expr, pos, scope)
	}

	// Map literal: {...}
	if c == '{' {
		return vtlParseMap(expr, pos, scope)
	}

	// Variable reference: $var.prop.method()
	if c == '$' {
		refEnd := vtlScanRef(expr, pos)
		ref := expr[pos:refEnd]
		val := vtlEvalRef(ref, scope)
		return val, refEnd
	}

	// Identifier (bare word — could be a variable without $)
	if unicode.IsLetter(rune(c)) || c == '_' {
		end := pos
		for end < len(expr) && (unicode.IsLetter(rune(expr[end])) || unicode.IsDigit(rune(expr[end])) || expr[end] == '_') {
			end++
		}
		name := expr[pos:end]
		if v, ok := scope.get(name); ok {
			return v, end
		}
		return name, end
	}

	return nil, pos + 1
}

func vtlParseString(expr string, pos int, scope *vtlScope) (any, int) {
	quote := expr[pos]
	pos++ // skip opening quote
	var buf strings.Builder
	for pos < len(expr) {
		c := expr[pos]
		if c == '\\' && pos+1 < len(expr) {
			next := expr[pos+1]
			switch next {
			case '"', '\'', '\\':
				buf.WriteByte(next)
			case 'n':
				buf.WriteByte('\n')
			case 't':
				buf.WriteByte('\t')
			default:
				buf.WriteByte('\\')
				buf.WriteByte(next)
			}
			pos += 2
			continue
		}
		if c == quote {
			pos++ // skip closing quote
			return buf.String(), pos
		}
		// String interpolation: $var inside double-quoted strings
		if quote == '"' && c == '$' {
			refEnd := vtlScanRef(expr, pos)
			ref := expr[pos:refEnd]
			val := vtlEvalRef(ref, scope)
			if val != nil {
				buf.WriteString(vtlToString(val))
			} else {
				buf.WriteString(ref)
			}
			pos = refEnd
			continue
		}
		buf.WriteByte(c)
		pos++
	}
	return buf.String(), pos
}

func vtlParseNumber(expr string, pos int) (any, int) {
	start := pos
	if expr[pos] == '-' {
		pos++
	}
	for pos < len(expr) && (expr[pos] >= '0' && expr[pos] <= '9') {
		pos++
	}
	isFloat := false
	if pos < len(expr) && expr[pos] == '.' {
		isFloat = true
		pos++
		for pos < len(expr) && (expr[pos] >= '0' && expr[pos] <= '9') {
			pos++
		}
	}
	s := expr[start:pos]
	if isFloat {
		f, _ := strconv.ParseFloat(s, 64)
		return f, pos
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err == nil {
		return float64(n), pos
	}
	f, _ := strconv.ParseFloat(s, 64)
	return f, pos
}

func vtlParseList(expr string, pos int, scope *vtlScope) (any, int) {
	pos++ // skip [
	var items []any
	pos = skipWS(expr, pos)
	if pos < len(expr) && expr[pos] == ']' {
		return items, pos + 1
	}
	for pos < len(expr) {
		val, newPos := vtlParseOrExpr(expr, pos, scope)
		items = append(items, val)
		pos = skipWS(expr, newPos)
		if pos < len(expr) && expr[pos] == ',' {
			pos++
		} else {
			break
		}
	}
	pos = skipWS(expr, pos)
	if pos < len(expr) && expr[pos] == ']' {
		pos++
	}
	return items, pos
}

func vtlParseMap(expr string, pos int, scope *vtlScope) (any, int) {
	pos++ // skip {
	m := map[string]any{}
	pos = skipWS(expr, pos)
	if pos < len(expr) && expr[pos] == '}' {
		return m, pos + 1
	}
	for pos < len(expr) {
		pos = skipWS(expr, pos)
		// Parse key (string literal or identifier)
		var key string
		if pos < len(expr) && (expr[pos] == '"' || expr[pos] == '\'') {
			kv, newPos := vtlParseString(expr, pos, scope)
			key = vtlToString(kv)
			pos = newPos
		} else {
			end := pos
			for end < len(expr) && expr[end] != ':' && expr[end] != '=' && expr[end] != '}' && !unicode.IsSpace(rune(expr[end])) {
				end++
			}
			key = expr[pos:end]
			pos = end
		}
		pos = skipWS(expr, pos)
		if pos < len(expr) && (expr[pos] == ':' || expr[pos] == '=') {
			pos++
		}
		val, newPos := vtlParseOrExpr(expr, pos, scope)
		m[key] = val
		pos = skipWS(expr, newPos)
		if pos < len(expr) && expr[pos] == ',' {
			pos++
		} else {
			break
		}
	}
	pos = skipWS(expr, pos)
	if pos < len(expr) && expr[pos] == '}' {
		pos++
	}
	return m, pos
}

// vtlScanRef scans a $reference expression and returns the end position.
func vtlScanRef(expr string, pos int) int {
	if pos >= len(expr) || expr[pos] != '$' {
		return pos
	}
	j := pos + 1
	n := len(expr)

	// $! quiet prefix
	if j < n && expr[j] == '!' {
		j++
	}

	// ${braced}
	braced := false
	if j < n && expr[j] == '{' {
		braced = true
		j++
	}

	// identifier
	if j >= n || (!unicode.IsLetter(rune(expr[j])) && expr[j] != '_') {
		return pos + 1
	}
	for j < n && (unicode.IsLetter(rune(expr[j])) || unicode.IsDigit(rune(expr[j])) || expr[j] == '_') {
		j++
	}

	// chain: .property, .method(), [index]
	for j < n {
		if expr[j] == '.' {
			j++
			for j < n && (unicode.IsLetter(rune(expr[j])) || unicode.IsDigit(rune(expr[j])) || expr[j] == '_') {
				j++
			}
			if j < n && expr[j] == '(' {
				depth := 1
				j++
				inStr := false
				strChar := byte(0)
				for j < n && depth > 0 {
					c := expr[j]
					if inStr {
						switch c {
						case '\\':
							j++
						case strChar:
							inStr = false
						}
					} else {
						switch c {
						case '"', '\'':
							inStr = true
							strChar = c
						case '(':
							depth++
						case ')':
							depth--
						}
					}
					j++
				}
			}
		} else if expr[j] == '[' {
			depth := 1
			j++
			for j < n && depth > 0 {
				switch expr[j] {
				case '[':
					depth++
				case ']':
					depth--
				}
				j++
			}
		} else {
			break
		}
	}

	if braced {
		if j < n && expr[j] == '}' {
			j++
		}
	}
	return j
}

// ─── Reference Evaluation ────────────────────────────────────────────────────

// vtlEvalRef evaluates a $reference expression, e.g. "$context.arguments.name" or "$!ctx.args.x".
func vtlEvalRef(ref string, scope *vtlScope) any {
	// Strip $ or $! prefix
	s := ref
	if strings.HasPrefix(s, "$!") {
		s = s[2:]
	} else if strings.HasPrefix(s, "$") {
		s = s[1:]
	}

	// Strip braces
	if strings.HasPrefix(s, "{") && strings.HasSuffix(s, "}") {
		s = s[1 : len(s)-1]
	}

	return vtlEvalChain(s, scope)
}

// vtlEvalChain evaluates a dotted chain like "context.arguments.name" or "util.toJson($obj)".
func vtlEvalChain(chain string, scope *vtlScope) any {
	parts := vtlSplitChain(chain)
	if len(parts) == 0 {
		return nil
	}

	// Resolve root variable
	rootName := parts[0]
	val, ok := scope.get(rootName)
	if !ok {
		return nil
	}

	// Walk the chain
	for _, part := range parts[1:] {
		val = vtlAccessProperty(val, part, scope)
	}

	return val
}

// vtlSplitChain splits "context.arguments.get(\"key\")" into parts, respecting parentheses.
func vtlSplitChain(chain string) []string {
	var parts []string
	current := strings.Builder{}
	depth := 0
	inStr := false
	strChar := byte(0)

	for i := 0; i < len(chain); i++ {
		c := chain[i]
		if inStr {
			current.WriteByte(c)
			if c == '\\' && i+1 < len(chain) {
				i++
				current.WriteByte(chain[i])
			} else if c == strChar {
				inStr = false
			}
			continue
		}
		if c == '"' || c == '\'' {
			inStr = true
			strChar = c
			current.WriteByte(c)
			continue
		}
		if c == '(' {
			depth++
			current.WriteByte(c)
		} else if c == ')' {
			depth--
			current.WriteByte(c)
		} else if c == '[' {
			depth++
			current.WriteByte(c)
		} else if c == ']' {
			depth--
			current.WriteByte(c)
		} else if c == '.' && depth == 0 {
			if current.Len() > 0 {
				parts = append(parts, current.String())
				current.Reset()
			}
		} else {
			current.WriteByte(c)
		}
	}
	if current.Len() > 0 {
		parts = append(parts, current.String())
	}
	return parts
}

// vtlAccessProperty accesses a property or calls a method on a value.
func vtlAccessProperty(val any, prop string, scope *vtlScope) any {
	if val == nil {
		return nil
	}

	// Check for method call: name(args)
	if idx := strings.Index(prop, "("); idx >= 0 {
		methodName := prop[:idx]
		argsStr := strings.TrimSuffix(prop[idx+1:], ")")
		return vtlCallMethod(val, methodName, argsStr, scope)
	}

	// Check for index access: name[index]
	if idx := strings.Index(prop, "["); idx >= 0 {
		// First access the property, then the index
		propName := prop[:idx]
		var target any
		if propName != "" {
			target = vtlGetProp(val, propName)
		} else {
			target = val
		}
		idxStr := strings.TrimSuffix(prop[idx+1:], "]")
		return vtlIndex(target, idxStr, scope)
	}

	// Shorthand: $context.args → $context.arguments
	if prop == "args" {
		if m, ok := val.(map[string]any); ok {
			if _, hasArgs := m["args"]; !hasArgs {
				if arguments, hasArguments := m["arguments"]; hasArguments {
					return arguments
				}
			}
		}
	}

	return vtlGetProp(val, prop)
}

// vtlGetProp retrieves a named property from a value.
func vtlGetProp(val any, name string) any {
	switch v := val.(type) {
	case map[string]any:
		return v[name]
	case vtlFunc:
		// Can't get property of a function
		return nil
	default:
		return nil
	}
}

// vtlIndex indexes into a list or map.
func vtlIndex(val any, idxStr string, scope *vtlScope) any {
	idx := vtlEvalExpr(idxStr, scope)
	switch v := val.(type) {
	case []any:
		n := int(vtlToFloat(idx))
		if n < 0 || n >= len(v) {
			return nil
		}
		return v[n]
	case map[string]any:
		key := vtlToString(idx)
		return v[key]
	}
	return nil
}

// vtlCallMethod calls a method on a value.
func vtlCallMethod(val any, method string, argsStr string, scope *vtlScope) any {
	args := vtlParseArgs(argsStr, scope)

	// Check if val is a vtlFunc (utility function)
	if fn, ok := val.(vtlFunc); ok {
		return fn(args)
	}

	// Check if the val is a map with the method name as a vtlFunc
	if m, ok := val.(map[string]any); ok {
		if fn, ok := m[method].(vtlFunc); ok {
			return fn(args)
		}
		// .get("key"), .containsKey("key"), .put("key", val), .remove("key"), .keySet(), .values(), .entrySet()
		switch method {
		case "get":
			if len(args) > 0 {
				return m[vtlToString(args[0])]
			}
			return nil
		case "containsKey":
			if len(args) > 0 {
				_, ok := m[vtlToString(args[0])]
				return ok
			}
			return false
		case "put":
			if len(args) >= 2 {
				m[vtlToString(args[0])] = args[1]
			}
			return nil
		case "remove":
			if len(args) > 0 {
				delete(m, vtlToString(args[0]))
			}
			return nil
		case "keySet":
			keys := make([]any, 0, len(m))
			for k := range m {
				keys = append(keys, k)
			}
			return keys
		case "values":
			vals := make([]any, 0, len(m))
			for _, v := range m {
				vals = append(vals, v)
			}
			return vals
		case "size":
			return float64(len(m))
		case "isEmpty":
			return len(m) == 0
		}
	}

	// String methods
	if s, ok := val.(string); ok {
		switch method {
		case "length":
			return float64(len(s))
		case "substring":
			if len(args) >= 1 {
				start := int(vtlToFloat(args[0]))
				if start < 0 {
					start = 0
				}
				if start > len(s) {
					start = len(s)
				}
				if len(args) >= 2 {
					end := int(vtlToFloat(args[1]))
					if end > len(s) {
						end = len(s)
					}
					return s[start:end]
				}
				return s[start:]
			}
			return s
		case "contains":
			if len(args) > 0 {
				return strings.Contains(s, vtlToString(args[0]))
			}
			return false
		case "replaceAll":
			if len(args) >= 2 {
				re, err := regexp.Compile(vtlToString(args[0]))
				if err != nil {
					return s
				}
				return re.ReplaceAllString(s, vtlToString(args[1]))
			}
			return s
		case "replace":
			if len(args) >= 2 {
				return strings.ReplaceAll(s, vtlToString(args[0]), vtlToString(args[1]))
			}
			return s
		case "toLowerCase":
			return strings.ToLower(s)
		case "toUpperCase":
			return strings.ToUpper(s)
		case "trim":
			return strings.TrimSpace(s)
		case "split":
			if len(args) > 0 {
				parts := strings.Split(s, vtlToString(args[0]))
				result := make([]any, len(parts))
				for i, p := range parts {
					result[i] = p
				}
				return result
			}
			return []any{s}
		case "startsWith":
			if len(args) > 0 {
				return strings.HasPrefix(s, vtlToString(args[0]))
			}
			return false
		case "endsWith":
			if len(args) > 0 {
				return strings.HasSuffix(s, vtlToString(args[0]))
			}
			return false
		case "indexOf":
			if len(args) > 0 {
				return float64(strings.Index(s, vtlToString(args[0])))
			}
			return float64(-1)
		case "isEmpty":
			return s == ""
		case "matches":
			if len(args) > 0 {
				matched, err := regexp.MatchString(vtlToString(args[0]), s)
				if err != nil {
					return false
				}
				return matched
			}
			return false
		}
	}

	// List methods
	if list, ok := val.([]any); ok {
		switch method {
		case "size":
			return float64(len(list))
		case "isEmpty":
			return len(list) == 0
		case "get":
			if len(args) > 0 {
				idx := int(vtlToFloat(args[0]))
				if idx >= 0 && idx < len(list) {
					return list[idx]
				}
			}
			return nil
		case "add":
			// Note: VTL lists are mutable in real AppSync.
			// We can't easily mutate the original slice, but in practice
			// the list is usually from the scope and we can rebuild.
			if len(args) > 0 {
				// This is a best-effort; VTL list mutation is tricky with Go slices.
				return nil
			}
			return nil
		case "contains":
			if len(args) > 0 {
				for _, item := range list {
					if vtlEquals(item, args[0]) {
						return true
					}
				}
			}
			return false
		case "indexOf":
			if len(args) > 0 {
				for i, item := range list {
					if vtlEquals(item, args[0]) {
						return float64(i)
					}
				}
			}
			return float64(-1)
		}
	}

	return nil
}

// vtlParseArgs parses a comma-separated argument list.
func vtlParseArgs(argsStr string, scope *vtlScope) []any {
	argsStr = strings.TrimSpace(argsStr)
	if argsStr == "" {
		return nil
	}

	var args []any
	pos := 0
	for pos < len(argsStr) {
		val, newPos := vtlParseOrExpr(argsStr, pos, scope)
		args = append(args, val)
		pos = skipWS(argsStr, newPos)
		if pos < len(argsStr) && argsStr[pos] == ',' {
			pos++
		}
	}
	return args
}

// ─── Helpers ─────────────────────────────────────────────────────────────────

func skipWS(s string, pos int) int {
	for pos < len(s) && unicode.IsSpace(rune(s[pos])) {
		pos++
	}
	return pos
}

func vtlToString(v any) string {
	if v == nil {
		return ""
	}
	switch val := v.(type) {
	case string:
		return val
	case bool:
		if val {
			return "true"
		}
		return "false"
	case float64:
		if val == float64(int64(val)) {
			return strconv.FormatInt(int64(val), 10)
		}
		return strconv.FormatFloat(val, 'f', -1, 64)
	case int:
		return strconv.Itoa(val)
	case int64:
		return strconv.FormatInt(val, 10)
	case json.Number:
		return val.String()
	case []any, map[string]any:
		b, _ := json.Marshal(val)
		return string(b)
	default:
		return fmt.Sprintf("%v", val)
	}
}

func vtlToBool(v any) bool {
	if v == nil {
		return false
	}
	switch val := v.(type) {
	case bool:
		return val
	case string:
		return val != ""
	case float64:
		return val != 0
	case int:
		return val != 0
	case []any:
		return len(val) > 0
	case map[string]any:
		return len(val) > 0
	}
	return true
}

func vtlToFloat(v any) float64 {
	if v == nil {
		return 0
	}
	switch val := v.(type) {
	case float64:
		return val
	case int:
		return float64(val)
	case int64:
		return float64(val)
	case string:
		f, _ := strconv.ParseFloat(val, 64)
		return f
	case json.Number:
		f, _ := val.Float64()
		return f
	case bool:
		if val {
			return 1
		}
		return 0
	}
	return 0
}

func vtlCompare(left, right any, op string) bool {
	lf := vtlToFloat(left)
	rf := vtlToFloat(right)

	switch op {
	case "==":
		return vtlEquals(left, right)
	case "!=":
		return !vtlEquals(left, right)
	case "<":
		return lf < rf
	case ">":
		return lf > rf
	case "<=":
		return lf <= rf
	case ">=":
		return lf >= rf
	}
	return false
}

func vtlEquals(a, b any) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	// Compare as strings if either is a string
	as, aok := a.(string)
	bs, bok := b.(string)
	if aok && bok {
		return as == bs
	}
	// Compare as numbers
	af := vtlToFloat(a)
	bf := vtlToFloat(b)
	ab, aIsBool := a.(bool)
	bb, bIsBool := b.(bool)
	if aIsBool && bIsBool {
		return ab == bb
	}
	return af == bf
}
