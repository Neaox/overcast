//! The six-field failure message.
//!
//! Debuggability is the generated backend's whole cost, and it is paid here:
//! one builder makes every failure message and every clause uses it, so a
//! generated failure carries as much as a hand-written one would.
//!
//! compat/model/README.md § Failure messages fixes the six fields and their
//! order:
//!
//! 1. `group/test`
//! 2. the operation — of the primary call, or of the clause's own call
//! 3. the exact params JSON sent, after evaluating every expression
//! 4. the assertion kind and, for `checks`/`where`, the path
//! 5. expected vs actual
//! 6. the scenario file and the step index
//!
//! Rendered:
//!
//! ```text
//! sqs-gen-queue/SetQueueAttributes: GetQueueAttributes params {"AttributeNames":["All"],"QueueUrl":"http://…"}: readback equals at $.Attributes.VisibilityTimeout: expected "60", actual "30" (compat/model/scenarios/sqs.json assert[0].assert)
//! ```
//!
//! A failure the emulator answered with 501 carries the harness's
//! `unimplemented` classification tag rather than relying on that harness's
//! substring heuristic, which would otherwise read field 3 — where a run id or
//! a port number can put a "501" that means nothing about the status.

use serde_json::Value as Json;

use super::json;
use super::WhereEntry;
use crate::harness;

/// One failure, as its six fields.
pub(crate) struct Failure {
    pub(crate) group: String,
    pub(crate) test: String,
    pub(crate) op: String,
    pub(crate) params: String,
    pub(crate) kind: String,
    pub(crate) path: String,
    pub(crate) expected: String,
    pub(crate) actual: String,
    pub(crate) file: String,
    pub(crate) step: String,
    /// What `eventually` puts in front of its last attempt's message when it
    /// gives up. Empty for every other failure, which is why it is a field
    /// rather than a second message builder: the six fields stay the six
    /// fields, and the budget that was spent on them reads in front of them.
    pub(crate) prefix: String,
    /// The emulator answered 501. The harness classifies the result from the
    /// tag this adds rather than from the message.
    pub(crate) unimplemented: bool,
}

impl Failure {
    /// The message, without any classification tag.
    pub(crate) fn message(&self) -> String {
        let mut out = format!("{}{}/{}: {}", self.prefix, self.group, self.test, self.op);
        if !self.params.is_empty() {
            out.push_str(&format!(" params {}", self.params));
        }
        out.push_str(&format!(": {}", self.kind));
        if !self.path.is_empty() {
            out.push_str(&format!(" at {}", self.path));
        }
        out.push_str(&format!(
            ": expected {}, actual {} ({} {})",
            self.expected, self.actual, self.file, self.step
        ));
        out
    }

    /// The message as the harness receives it: the six fields behind the tag
    /// that states how the result is classified.
    pub(crate) fn into_error(self) -> String {
        let tag = if self.unimplemented {
            harness::UNIMPLEMENTED_TAG
        } else {
            harness::FAIL_TAG
        };
        format!("{tag}{}", self.message())
    }
}

/// Renders a string as a failure message's expected or actual value.
///
/// An SDK error's text can be multi-line, so it is folded onto one line: the
/// NDJSON `error` field is read as a single line by the report tooling. It is
/// capped too — a transport failure can carry a long chain of wrapped causes.
pub(crate) fn quote(value: &str) -> String {
    let folded = value.split_whitespace().collect::<Vec<_>>().join(" ");
    format!("{:?}", clip(&folded))
}

/// Caps one field of one failure message. Every failure ends up in a
/// single-line NDJSON `error` that the dashboard renders and the report tooling
/// diffs, so a field running to megabytes costs far more than the diagnosis it
/// buys. A few KiB is enough to identify a wrong value and to see the start of
/// the list or the message it came from.
const MAX_RENDERED: usize = 4096;

/// Trims a rendered value and says how much it dropped, so the reader knows the
/// value is not all of what was there. The cut lands on a character boundary.
pub(crate) fn clip(value: &str) -> String {
    if value.len() <= MAX_RENDERED {
        return value.to_string();
    }
    let mut cut = MAX_RENDERED;
    while cut > 0 && !value.is_char_boundary(cut) {
        cut -= 1;
    }
    format!("{}… ({} bytes elided)", &value[..cut], value.len() - cut)
}

/// Prints the list a membership check searched.
///
/// It is the actual value of the failure, so it is printed rather than
/// summarised — a generated failure that says only "no match" cannot be
/// diagnosed without re-running — but it is capped, for the same reason every
/// other field is.
pub(crate) fn render_list(items: &[Json]) -> String {
    if items.is_empty() {
        return "an empty list".to_string();
    }
    clip(&json::render(&Json::Array(items.to_vec())))
}

/// Prints a where list for a failure message, in path order.
pub(crate) fn render_where(criteria: &[WhereEntry], wanted: &[Json]) -> String {
    let parts: Vec<String> = criteria
        .iter()
        .zip(wanted)
        .map(|(entry, want)| format!("{}={}", entry.path, json::render(want)))
        .collect();
    format!("{{{}}}", parts.join(", "))
}
