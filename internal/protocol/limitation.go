package protocol

// Limitations — what Overcast will not do with a resource it just created.
//
// A 501 says "this operation is not emulated" and fails the call. This says
// something narrower and more common: the call succeeded, the resource exists,
// and there is something about it Overcast will not act on. A CloudWatch alarm
// whose configuration cannot be evaluated is the case this was written for —
// the alarm exists, DescribeAlarms returns it, and nothing computes its state —
// but the channel is deliberately not named after that: a property silently
// ignored, a mode stored but never entered, a resource created as metadata
// only, all belong here too, and a header called "inert" could carry none of
// them.
//
// # Why say it rather than refuse
//
// The alternative is a 501, and for alarms that was the rule: an alarm that
// looks armed but is never evaluated is worse than no alarm at all. It is
// worse. But a 501 fails the CloudFormation resource, and with it the stack and
// the deploy, over a resource whose only defect is that Overcast will not act
// on it — trading a misleading resource for no environment at all. Saying so
// keeps what the refusal was protecting: CloudFormation copies the value onto
// the resource's ResourceStatusReason, so it appears in `cdk deploy` output and
// in describe-stack-resources, beside the resource it is about.
//
// # The name
//
// Every service doc in docs/services/ ends in a "Limitations" section listing
// exactly this class of fact — what is stored but not acted on, what is
// accepted but ignored. This header is the machine-readable form of a line from
// one, so it takes that word, and CloudFormation's ResourceStatusReason then
// reads as the same sentence a reader would have found in the docs.
//
// The alternatives, and why not:
//
//   - "inert" — the codebase's own tier name (capabilities.StatusInert), which
//     is what made it tempting. But inert means nothing happens at all, and a
//     resource whose *one* property was ignored is not inert; the header could
//     never carry that. Too narrow for a channel meant to explain anything.
//   - "not-emulated" — collides with x-emulator-unsupported, which is the 501.
//     Here the operation *was* emulated and the resource does exist.
//   - "partial" — right for a half-honoured resource, an overstatement for one
//     nothing acts on.
//   - "caveat" / "notice" — general enough, but advisory in tone, and a header
//     that could carry anything tends to.
//
// # Why not x-overcast-backing
//
// ECS answers every task request with `x-overcast-backing: container |
// metadata-only` and a slug reason, which asks a neighbouring question: is
// there a container behind this record. The negative halves line up; the
// positive ones do not — an alarm's opposite state is "evaluated", not
// "container" — and that header carries a slug for a probe where this one
// carries a sentence for a person mid-deploy. Two conventions in one header
// would make both harder to read.

import (
	"net/http"
	"strings"
)

// LimitationHeader carries one sentence about what Overcast will not do with
// the resource a successful response just created or updated: what is not done
// and, where there is one, what to use instead. It is read by someone mid-deploy
// who has not read the source.
//
// It belongs on 2xx responses only. A refusal is a 501 carrying
// x-emulator-unsupported, which says something different.
//
// More than one may be set on a response; they are separate header values
// rather than one joined string, so a reader can take them apart again.
//
// The name is qualified because "limitation" alone would not say whose: a
// header that might be announcing an AWS quota is a header a reader has to
// investigate. This one is always about what *the emulator* does not do.
const EmulationLimitationHeader = "x-overcast-emulation-limitation"

// MarkLimitation records something Overcast will not do with this response's
// resource. An empty reason records nothing, so a caller can pass the result of
// a check that usually passes without branching around it. Repeated calls add
// rather than replace: a handler may have several things to say.
func MarkLimitation(w http.ResponseWriter, reason string) {
	if reason == "" {
		return
	}
	w.Header().Add(EmulationLimitationHeader, reason)
}

// Limitations reads back everything a response said it would not do.
func Limitations(h http.Header) []string {
	return h.Values(EmulationLimitationHeader)
}

// LimitationText joins a response's limitations into one line, for somewhere
// that has a single string to put them — CloudFormation's ResourceStatusReason
// being the reason this exists.
func LimitationText(h http.Header) string {
	return strings.Join(h.Values(EmulationLimitationHeader), " ")
}
