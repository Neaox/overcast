package stepfunctions

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// State type names, as spelled in the Amazon States Language.
const (
	stateTypePass     = "Pass"
	stateTypeTask     = "Task"
	stateTypeChoice   = "Choice"
	stateTypeWait     = "Wait"
	stateTypeSucceed  = "Succeed"
	stateTypeFail     = "Fail"
	stateTypeParallel = "Parallel"
	stateTypeMap      = "Map"
)

// aslStateTypes is the complete set of Amazon States Language state types.
// Anything outside it is an invalid definition, exactly as on AWS — not an
// Overcast gap. Overcast interprets all eight; the gaps are in individual
// fields and Task resource integrations, which fail loudly at run time.
var aslStateTypes = map[string]bool{
	stateTypePass:     true,
	stateTypeTask:     true,
	stateTypeChoice:   true,
	stateTypeWait:     true,
	stateTypeSucceed:  true,
	stateTypeFail:     true,
	stateTypeParallel: true,
	stateTypeMap:      true,
}

// aslBranch is a state machine body: a start state plus a named state map.
// The top-level definition, every Parallel branch and every Map processor
// share this shape.
type aslBranch struct {
	Comment        string               `json:"Comment"`
	StartAt        string               `json:"StartAt"`
	States         map[string]*aslState `json:"States"`
	TimeoutSeconds int                  `json:"TimeoutSeconds"`
	Version        string               `json:"Version"`
	QueryLanguage  string               `json:"QueryLanguage"`

	// ProcessorConfig appears on a Map ItemProcessor. Only INLINE mode is
	// interpreted; DISTRIBUTED fails loudly at run time.
	ProcessorConfig json.RawMessage `json:"ProcessorConfig"`

	// order is the deterministic iteration order of States, used only for
	// error messages so that a definition with several problems reports the
	// same one on every run.
	order []string
}

// aslState is one state in a branch. Fields not valid for a state's Type are
// simply unread; validation rejects the combinations AWS rejects.
type aslState struct {
	Type    string `json:"Type"`
	Comment string `json:"Comment"`
	Next    string `json:"Next"`
	End     bool   `json:"End"`

	// Input/output processing. aslPath distinguishes "absent" from an explicit
	// JSON null, which has its own meaning for all three paths.
	InputPath      aslPath         `json:"InputPath"`
	OutputPath     aslPath         `json:"OutputPath"`
	ResultPath     aslPath         `json:"ResultPath"`
	Parameters     json.RawMessage `json:"Parameters"`
	ResultSelector json.RawMessage `json:"ResultSelector"`

	// Pass
	Result json.RawMessage `json:"Result"`

	// Task
	Resource           string          `json:"Resource"`
	TimeoutSeconds     int             `json:"TimeoutSeconds"`
	TimeoutSecondsPath string          `json:"TimeoutSecondsPath"`
	HeartbeatSeconds   int             `json:"HeartbeatSeconds"`
	Credentials        json.RawMessage `json:"Credentials"`
	Retry              []aslRetrier    `json:"Retry"`
	Catch              []aslCatcher    `json:"Catch"`

	// Choice
	Choices []*aslChoiceRule `json:"Choices"`
	Default string           `json:"Default"`

	// Wait
	Seconds       *int   `json:"Seconds"`
	SecondsPath   string `json:"SecondsPath"`
	Timestamp     string `json:"Timestamp"`
	TimestampPath string `json:"TimestampPath"`

	// Fail
	Error     string `json:"Error"`
	ErrorPath string `json:"ErrorPath"`
	Cause     string `json:"Cause"`
	CausePath string `json:"CausePath"`

	// Parallel
	Branches []*aslBranch `json:"Branches"`

	// Map
	Iterator       *aslBranch      `json:"Iterator"`
	ItemProcessor  *aslBranch      `json:"ItemProcessor"`
	ItemsPath      string          `json:"ItemsPath"`
	ItemSelector   json.RawMessage `json:"ItemSelector"`
	MaxConcurrency int             `json:"MaxConcurrency"`
	ItemReader     json.RawMessage `json:"ItemReader"`
	ItemBatcher    json.RawMessage `json:"ItemBatcher"`
	ResultWriter   json.RawMessage `json:"ResultWriter"`

	// JSONata query language fields. Present only in JSONata state machines,
	// which Overcast does not interpret; their presence is caught by the
	// QueryLanguage check rather than read here.
	Assign json.RawMessage `json:"Assign"`
	Output json.RawMessage `json:"Output"`
}

// aslRetrier is a Retry entry on a Task/Parallel/Map state.
type aslRetrier struct {
	ErrorEquals     []string `json:"ErrorEquals"`
	IntervalSeconds *float64 `json:"IntervalSeconds"`
	MaxAttempts     *int     `json:"MaxAttempts"`
	BackoffRate     *float64 `json:"BackoffRate"`
	MaxDelaySeconds *float64 `json:"MaxDelaySeconds"`
	JitterStrategy  string   `json:"JitterStrategy"`
}

// interval returns the retrier's first-attempt delay in seconds (AWS default 1).
func (r aslRetrier) interval() float64 {
	if r.IntervalSeconds == nil {
		return 1
	}
	return *r.IntervalSeconds
}

// maxAttempts returns the retrier's attempt cap (AWS default 3).
func (r aslRetrier) maxAttempts() int {
	if r.MaxAttempts == nil {
		return 3
	}
	return *r.MaxAttempts
}

// backoffRate returns the retrier's multiplier (AWS default 2.0).
func (r aslRetrier) backoffRate() float64 {
	if r.BackoffRate == nil {
		return 2.0
	}
	return *r.BackoffRate
}

// aslCatcher is a Catch entry on a Task/Parallel/Map state.
type aslCatcher struct {
	ErrorEquals []string `json:"ErrorEquals"`
	Next        string   `json:"Next"`
	ResultPath  aslPath  `json:"ResultPath"`
}

// aslPath is an optional reference-path field. ASL gives "absent" and an
// explicit JSON null different meanings for InputPath, OutputPath and
// ResultPath, so a plain *string (which decodes null as nil) cannot represent
// them — this type keeps the two apart.
type aslPath struct {
	Set   bool
	Null  bool
	Value string
}

// UnmarshalJSON records whether the field was present and whether it was null.
func (p *aslPath) UnmarshalJSON(data []byte) error {
	p.Set = true
	if string(data) == "null" {
		p.Null = true
		p.Value = ""
		return nil
	}
	return json.Unmarshal(data, &p.Value)
}

// invalidDefinitionError marks a definition as structurally invalid. It is
// surfaced to the caller as AWS's InvalidDefinition at CreateStateMachine time.
type invalidDefinitionError struct{ msg string }

func (e *invalidDefinitionError) Error() string { return e.msg }

func invalidDefinitionf(format string, args ...any) error {
	return &invalidDefinitionError{msg: fmt.Sprintf(format, args...)}
}

// parseDefinition parses and structurally validates an ASL document.
//
// It rejects exactly what AWS rejects at CreateStateMachine time — malformed
// JSON, a missing or dangling StartAt, an unknown state Type, a state that
// neither ends nor names a Next, a dangling transition. It deliberately does
// not reject definitions that are valid ASL but use features Overcast cannot
// interpret: those provision (so CDK/CloudFormation deploys keep working) and
// then fail the execution loudly, which is what docs/plans/
// full-emulation-priority.md §2.1 asks for.
func parseDefinition(definition string) (*aslBranch, error) {
	if strings.TrimSpace(definition) == "" {
		return nil, invalidDefinitionf("definition is empty")
	}
	var branch aslBranch
	dec := json.NewDecoder(strings.NewReader(definition))
	dec.UseNumber()
	if err := dec.Decode(&branch); err != nil {
		return nil, invalidDefinitionf("definition is not valid JSON: %v", err)
	}
	if err := validateBranch(&branch, ""); err != nil {
		return nil, err
	}
	return &branch, nil
}

// validateBranch checks one branch (top level, Parallel branch or Map
// processor). where is a human-readable prefix naming the nesting position.
func validateBranch(branch *aslBranch, where string) error {
	prefix := ""
	if where != "" {
		prefix = where + ": "
	}
	if branch == nil {
		return invalidDefinitionf("%smissing state machine body", prefix)
	}
	if branch.StartAt == "" {
		return invalidDefinitionf("%sStartAt is required", prefix)
	}
	if len(branch.States) == 0 {
		return invalidDefinitionf("%sStates is required and must not be empty", prefix)
	}
	if _, ok := branch.States[branch.StartAt]; !ok {
		return invalidDefinitionf("%sStartAt %q does not name a state in States", prefix, branch.StartAt)
	}
	branch.order = make([]string, 0, len(branch.States))
	for name := range branch.States {
		branch.order = append(branch.order, name)
	}
	sort.Strings(branch.order)

	for _, name := range branch.order {
		state := branch.States[name]
		if state == nil {
			return invalidDefinitionf("%sstate %q is null", prefix, name)
		}
		if err := validateState(branch, name, state, where); err != nil {
			return err
		}
	}
	return nil
}

// terminalStateTypes never carry Next/End — reaching one ends the branch.
var terminalStateTypes = map[string]bool{stateTypeSucceed: true, stateTypeFail: true}

func validateState(branch *aslBranch, name string, state *aslState, where string) error {
	loc := "state " + name
	if where != "" {
		loc = where + " state " + name
	}
	if state.Type == "" {
		return invalidDefinitionf("%s: Type is required", loc)
	}
	if !aslStateTypes[state.Type] {
		return invalidDefinitionf("%s: unknown state Type %q", loc, state.Type)
	}

	switch state.Type {
	case stateTypeChoice:
		if len(state.Choices) == 0 {
			return invalidDefinitionf("%s: Choice requires a non-empty Choices array", loc)
		}
		for i, rule := range state.Choices {
			if rule == nil {
				return invalidDefinitionf("%s: Choices[%d] is null", loc, i)
			}
			if rule.Next == "" {
				return invalidDefinitionf("%s: Choices[%d] requires Next", loc, i)
			}
			if err := checkTransition(branch, loc, fmt.Sprintf("Choices[%d].Next", i), rule.Next); err != nil {
				return err
			}
			if err := validateChoiceRule(rule, loc, fmt.Sprintf("Choices[%d]", i)); err != nil {
				return err
			}
		}
		if state.Default != "" {
			if err := checkTransition(branch, loc, "Default", state.Default); err != nil {
				return err
			}
		}
	case stateTypeTask:
		if state.Resource == "" {
			return invalidDefinitionf("%s: Task requires Resource", loc)
		}
	case stateTypeParallel:
		if len(state.Branches) == 0 {
			return invalidDefinitionf("%s: Parallel requires a non-empty Branches array", loc)
		}
		for i, sub := range state.Branches {
			if err := validateBranch(sub, fmt.Sprintf("%s Branches[%d]", loc, i)); err != nil {
				return err
			}
		}
	case stateTypeMap:
		processor := state.ItemProcessor
		if processor == nil {
			processor = state.Iterator
		}
		if processor == nil {
			return invalidDefinitionf("%s: Map requires ItemProcessor (or the legacy Iterator)", loc)
		}
		if err := validateBranch(processor, loc+" ItemProcessor"); err != nil {
			return err
		}
	case stateTypeWait:
		if state.Seconds == nil && state.SecondsPath == "" && state.Timestamp == "" && state.TimestampPath == "" {
			return invalidDefinitionf("%s: Wait requires one of Seconds, SecondsPath, Timestamp or TimestampPath", loc)
		}
	}

	// Retry/Catch are only meaningful on the three states that can fail.
	if len(state.Catch) > 0 {
		for i, catcher := range state.Catch {
			if len(catcher.ErrorEquals) == 0 {
				return invalidDefinitionf("%s: Catch[%d] requires a non-empty ErrorEquals", loc, i)
			}
			if catcher.Next == "" {
				return invalidDefinitionf("%s: Catch[%d] requires Next", loc, i)
			}
			if err := checkTransition(branch, loc, fmt.Sprintf("Catch[%d].Next", i), catcher.Next); err != nil {
				return err
			}
		}
	}
	for i, retrier := range state.Retry {
		if len(retrier.ErrorEquals) == 0 {
			return invalidDefinitionf("%s: Retry[%d] requires a non-empty ErrorEquals", loc, i)
		}
	}

	// Transition shape. Choice states route through Choices/Default only.
	switch {
	case terminalStateTypes[state.Type]:
		if state.Next != "" || state.End {
			return invalidDefinitionf("%s: a %s state must not declare Next or End", loc, state.Type)
		}
	case state.Type == stateTypeChoice:
		if state.Next != "" || state.End {
			return invalidDefinitionf("%s: a Choice state must not declare Next or End", loc)
		}
	default:
		if state.End && state.Next != "" {
			return invalidDefinitionf("%s: declares both Next and End", loc)
		}
		if !state.End && state.Next == "" {
			return invalidDefinitionf("%s: requires either Next or End", loc)
		}
		if state.Next != "" {
			if err := checkTransition(branch, loc, "Next", state.Next); err != nil {
				return err
			}
		}
	}
	return nil
}

func checkTransition(branch *aslBranch, loc, field, target string) error {
	if _, ok := branch.States[target]; !ok {
		return invalidDefinitionf("%s: %s %q does not name a state in the same States block", loc, field, target)
	}
	return nil
}

// processor returns the branch a Map state iterates with, preferring the
// current ItemProcessor field over the deprecated Iterator.
func (s *aslState) processor() *aslBranch {
	if s.ItemProcessor != nil {
		return s.ItemProcessor
	}
	return s.Iterator
}
