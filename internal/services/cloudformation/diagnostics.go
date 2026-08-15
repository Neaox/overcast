package cloudformation

// diagnostics.go — the deploy-diagnostics journal: what Overcast knows about a
// deploy that failed, kept past the rollback that deletes the evidence.
//
// The problem this answers is not a fidelity gap. When an ECS service cannot
// keep its tasks alive, the best sentence CloudFormation can carry is the one
// ECS gives it — "(service Foo) is unable to consistently start tasks
// successfully" — and that is byte-correct AWS behaviour. The actual answer,
// that container `app` exited 1 having printed "DATABASE_URL is not set", is
// not expressible in CloudFormation's vocabulary at all, so no amount of
// further fidelity work can surface it. It has to be said in Overcast's own
// voice, on an Overcast-only endpoint, or not at all.
//
// Two rules constrain everything here, and a change that breaks either is
// wrong even if it is useful:
//
//  1. Nothing Overcast authors may enter an AWS-shaped field. Not
//     ResourceStatusReason, not StackStatusReason, not DescribeStackEvents.
//     The journal is a separate record read through a separate endpoint, and
//     it is deliberately not reachable from the AWS surface — not even as a
//     "see the Diagnostics tab" pointer appended to a reason string.
//  2. No resource outlives its rollback. Capture reads; it never retains.
//     Keeping a container alive so its logs stayed readable would destroy the
//     semantics the journal exists to preserve.
//
// Provenance is how the payload stays honest without a disclaimer badge, which
// people stop seeing. Every section says where it came from, and the three
// tiers are chosen so the tag tells a reader something they need in order to
// act: whether the signal they are about to write a fix against is one they
// will also have in production.
//
// See docs/plans/cfn-deploy-diagnostics.md for the reasoning in full.

// Section provenance. These are contract values — the console renders one
// label per tier — so they are not free text.
const (
	// provenanceAWSAPI marks evidence real AWS would have returned too:
	// `aws ecs describe-services` would have shown it.
	provenanceAWSAPI = "aws-api"
	// provenanceCapture marks evidence Overcast saved before rollback deleted
	// it. Real AWS discards it too — recovering it there would need `awslogs`
	// configured on the task definition beforehand.
	provenanceCapture = "overcast-capture"
	// provenanceInference marks Overcast's own reading of the evidence. Not an
	// AWS concept, and labelled so nobody mistakes it for one.
	provenanceInference = "overcast-inference"
)

// Section kinds. Three, deliberately: facts, events and a log tail cover ECS
// today and the two collectors most likely to come next (a Lambda init failure
// is facts plus log; an RDS start failure is facts plus log). A fourth kind
// added speculatively would give the console a branch that renders nothing.
// The union is discriminated on Kind so one component per kind renders every
// collector, present and future.
const (
	sectionKindFacts  = "facts"
	sectionKindEvents = "events"
	sectionKindLog    = "log"
)

// The stack operation a journal entry describes.
const (
	deployOperationCreate = "CREATE"
	deployOperationUpdate = "UPDATE"
)

// DeployDiagnostics is one stack's most recent failed deploy, as Overcast
// understood it at the moment of failure.
//
// **The invariant: an entry exists if and only if the most recent deploy of
// that stack failed.** One entry per stack, replaced on each failed deploy and
// deleted when a deploy succeeds. The console probes this endpoint
// unconditionally and shows its Diagnostics tab on a 200, so a stale entry on
// a stack that has since been fixed would put a failure explanation on a green
// stack — precisely the misleading surface the two hard rules exist to
// prevent. Keeping only the answer to "why did the last deploy fail" is also
// what bounds the journal: entry count is bounded by the number of stacks
// whose latest deploy failed, and no eviction machinery is needed.
//
// StackName is never omitted. The console identifies a real payload by its
// presence, because a 404's {"error": …} body and an empty body must both read
// as "no diagnostics"; a payload without it would be silently discarded and
// the tab would never appear.
type DeployDiagnostics struct {
	StackName string `json:"stackName"`
	StackID   string `json:"stackId"`
	Operation string `json:"operation"`
	// StackStatus is the status the stack came to rest at. The journal is
	// written after the rollback finishes precisely so this field can say
	// ROLLBACK_COMPLETE rather than the in-progress status capture ran under —
	// the evidence is gathered before teardown, the record is written after.
	StackStatus string `json:"stackStatus"`
	CapturedAt  string `json:"capturedAt"`

	// AWSReason is the sentence CloudFormation itself recorded, repeated here
	// so the tab can show what AWS would have told you next to what Overcast
	// found. It is a copy of an AWS-surface value, which is the only direction
	// that copying is allowed to go.
	AWSReason string `json:"awsReason,omitempty"`

	// Headline is the one-sentence answer, always provenance
	// overcast-inference. Empty when nothing could be inferred, in which case
	// the console leads with the sections instead of with a guess.
	Headline string `json:"headline,omitempty"`

	// Counterfactual names what real AWS would have given instead. It does
	// more anti-misleading work than any badge, because it teaches the
	// difference rather than disclaiming it.
	//
	// Optional, and omitted rather than softened when there is nothing to
	// say: a capture whose sections are all aws-api preserved nothing AWS
	// would have discarded, so any sentence claiming otherwise would be
	// false. The console drops its footer entirely when this is absent.
	Counterfactual string `json:"counterfactual,omitempty"`

	Resources []DiagnosticResource `json:"resources"`
}

// DiagnosticResource is one failed resource and everything gathered about it.
type DiagnosticResource struct {
	LogicalID  string `json:"logicalId"`
	PhysicalID string `json:"physicalId,omitempty"`
	Type       string `json:"type"`
	// StatusReason is what CloudFormation recorded — an AWS-surface value,
	// copied in so the two accounts can be read side by side.
	StatusReason string              `json:"statusReason,omitempty"`
	Sections     []DiagnosticSection `json:"sections,omitempty"`
}

// DiagnosticSection is one pane of evidence. Kind discriminates the union:
// exactly one of Facts, Events or Log is populated.
//
// Provenance is orthogonal to Kind. As of today the headline is the only
// overcast-inference thing in a payload and every section is aws-api or
// overcast-capture — but that is a statement of what the current collectors
// happen to emit, not a rule. A future collector could reasonably offer an
// inferred facts pane ("the image's architecture does not match the task's
// platform"), and nothing here should stop it.
type DiagnosticSection struct {
	// ID is stable and unique within the resource, so the console can key its
	// panes on something other than position.
	ID         string `json:"id"`
	Title      string `json:"title"`
	Provenance string `json:"provenance"`
	// Note is an optional one-line gloss in Overcast's voice — what this pane
	// is, or why it is empty.
	Note string `json:"note,omitempty"`
	Kind string `json:"kind"`

	Facts  []DiagnosticFact  `json:"facts,omitempty"`
	Events []DiagnosticEvent `json:"events,omitempty"`
	Log    *DiagnosticLog    `json:"log,omitempty"`
}

// DiagnosticFact is a labelled value — an exit code, a stop code, a count.
type DiagnosticFact struct {
	Label string `json:"label"`
	Value string `json:"value"`
	// Hint qualifies the value where the label cannot: which container an exit
	// code belongs to, which task a stop code came from.
	Hint string `json:"hint,omitempty"`
}

// DiagnosticEvent is a timestamped line from a service's own event history.
//
// Ordering is the source service's, preserved and not re-sorted. ECS lists its
// service events newest first, and which of two failure events survives to be
// read is the whole subject of PR #993 — a collector that quietly reversed
// them would undo that. The console renders the slice as given.
type DiagnosticEvent struct {
	At      string `json:"at,omitempty"`
	Message string `json:"message"`
}

// DiagnosticLog is a bounded tail of something a container printed.
//
// Truncated is not decoration: a reader who cannot tell a complete log from
// its last 16 KiB will read a missing first line as an absent one. CapturedAt
// says when the copy was taken, so the pane never implies a container that is
// still there.
type DiagnosticLog struct {
	// Label is optional — send it when there is something useful to say about
	// which task and container this output came from.
	Label      string `json:"label,omitempty"`
	Text       string `json:"text"`
	Truncated  bool   `json:"truncated"`
	CapturedAt string `json:"capturedAt,omitempty"`
}

// hasEvidence reports whether anything was actually gathered. A journal with
// no evidence is worth writing — it still carries the AWS reason — but it must
// never displace one that has some.
func (d *DeployDiagnostics) hasEvidence() bool {
	if d == nil {
		return false
	}
	for _, r := range d.Resources {
		if len(r.Sections) > 0 {
			return true
		}
	}
	return false
}

// preservedSomething reports whether any pane holds evidence real AWS would
// have thrown away. It is the condition on the counterfactual: with nothing
// preserved there is no difference to teach, and a sentence claiming one would
// be false.
func (d *DeployDiagnostics) preservedSomething() bool {
	if d == nil {
		return false
	}
	for _, r := range d.Resources {
		for _, s := range r.Sections {
			if s.Provenance != provenanceAWSAPI {
				return true
			}
		}
	}
	return false
}
