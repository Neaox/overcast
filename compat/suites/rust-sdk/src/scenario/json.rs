//! Paths, canonical rendering and JSON equality.
//!
//! Paths (compat/model/README.md § Paths): `$` is the response, `.Name` selects
//! a structure member or map key, `[n]` selects a list element. Nothing else —
//! no wildcards, filters, quoting or recursive descent.
//!
//! A path is walked over the response body as it came off the wire (see the
//! module documentation in `mod.rs`), so member names are the modeled names
//! every backend writes and nothing has to be translated between the SDK's
//! object model and the IR's.

use std::fmt::Write as _;

use serde_json::Value as Json;

/// What a failure message prints where a path did not resolve.
pub(crate) const MISSING: &str = "<missing>";

/// One step of a path: a member name or a list index.
enum Segment {
    Member(String),
    Index(usize),
}

/// Splits a path into its segments.
///
/// Anything the IR's path grammar does not admit is rejected, so a malformed
/// path fails the step rather than silently resolving to nothing — the two are
/// very different bugs.
fn parse(path: &str) -> Result<Vec<Segment>, String> {
    let mut rest = match path.strip_prefix('$') {
        Some(rest) => rest,
        None => return Err(format!("path {path:?} does not start with $")),
    };
    let mut segments = Vec::new();
    while !rest.is_empty() {
        if let Some(after) = rest.strip_prefix('.') {
            let end = after.find(['.', '[']).unwrap_or(after.len());
            if end == 0 {
                return Err(format!("path {path:?} has an empty member name"));
            }
            segments.push(Segment::Member(after[..end].to_string()));
            rest = &after[end..];
        } else if rest.starts_with('[') {
            let Some(end) = rest.find(']') else {
                return Err(format!("path {path:?} has an unterminated index"));
            };
            let raw = &rest[1..end];
            match raw.parse::<usize>() {
                Ok(n) => segments.push(Segment::Index(n)),
                Err(_) => return Err(format!("path {path:?} has a non-numeric index {raw:?}")),
            }
            rest = &rest[end + 1..];
        } else {
            let unexpected = rest.chars().next().unwrap_or_default();
            return Err(format!(
                "path {path:?} has an unexpected character {unexpected:?}"
            ));
        }
    }
    Ok(segments)
}

/// Walks a path over a document.
///
/// `Ok(None)` means a segment was absent — which is what `missing` tests for,
/// and what makes an absent list count as empty for `listContains` and
/// `absent`. A member the service sent as JSON `null` is present and resolves
/// to `null`; a member it omitted does not resolve, and neither does an index
/// past the end of a list.
pub(crate) fn resolve<'a>(doc: &'a Json, path: &str) -> Result<Option<&'a Json>, String> {
    let mut current = doc;
    for segment in parse(path)? {
        match segment {
            Segment::Index(n) => match current.as_array().and_then(|items| items.get(n)) {
                Some(next) => current = next,
                None => return Ok(None),
            },
            Segment::Member(name) => match current.as_object().and_then(|obj| obj.get(&name)) {
                Some(next) => current = next,
                None => return Ok(None),
            },
        }
    }
    Ok(Some(current))
}

/// Renders a value in a stable form: object keys sorted, no trailing newline.
///
/// It is both how values are printed in a failure message and how they are
/// compared, so "expected X, actual Y" reads in the same notation the scenario
/// file is written in. The keys are sorted here rather than left to
/// `serde_json`, whose ordering depends on a cargo feature another crate in the
/// graph could turn on.
pub(crate) fn render(value: &Json) -> String {
    let mut out = String::new();
    write_canonical(&mut out, value);
    out
}

fn write_canonical(out: &mut String, value: &Json) {
    match value {
        Json::Null => out.push_str("null"),
        Json::Bool(b) => {
            let _ = write!(out, "{b}");
        }
        Json::Number(n) => {
            let _ = write!(out, "{n}");
        }
        Json::String(s) => out.push_str(&Json::String(s.clone()).to_string()),
        Json::Array(items) => {
            out.push('[');
            for (i, item) in items.iter().enumerate() {
                if i > 0 {
                    out.push(',');
                }
                write_canonical(out, item);
            }
            out.push(']');
        }
        Json::Object(entries) => {
            let mut keys: Vec<&String> = entries.keys().collect();
            keys.sort();
            out.push('{');
            for (i, key) in keys.into_iter().enumerate() {
                if i > 0 {
                    out.push(',');
                }
                out.push_str(&Json::String(key.clone()).to_string());
                out.push(':');
                write_canonical(out, &entries[key]);
            }
            out.push('}');
        }
    }
}

/// Renders a resolved-or-not value for a failure message.
pub(crate) fn render_resolved(value: Option<&Json>) -> String {
    match value {
        Some(value) => render(value),
        None => MISSING.to_string(),
    }
}

/// The IR's "equal, as JSON" (compat/model/README.md § Assertions).
///
/// Comparison is in the JSON type system directly, with no coercion: `"30"`
/// never equals `30` and `true` never equals `1`. Numbers are the one place a
/// normalisation is needed — a service that answers `30` and one that answers
/// `30.0` have said the same thing, and `serde_json` keeps the two
/// representations apart — so numbers are compared as `f64`, which is also what
/// the three interpreters compare after parsing.
pub(crate) fn equal(a: &Json, b: &Json) -> bool {
    match (a, b) {
        (Json::Number(x), Json::Number(y)) => match (x.as_f64(), y.as_f64()) {
            (Some(x), Some(y)) => x == y,
            _ => x == y,
        },
        (Json::Array(x), Json::Array(y)) => {
            x.len() == y.len() && x.iter().zip(y).all(|(x, y)| equal(x, y))
        }
        (Json::Object(x), Json::Object(y)) => {
            x.len() == y.len()
                && x.iter()
                    .all(|(key, value)| y.get(key).is_some_and(|other| equal(value, other)))
        }
        _ => a == b,
    }
}

/// The IR's emptiness: `null`, `""`, `[]` or `{}`. Numbers and booleans are
/// never empty, which is what stops `nonEmpty` failing on a legitimate 0 or
/// `false`.
pub(crate) fn is_empty(value: &Json) -> bool {
    match value {
        Json::Null => true,
        Json::String(s) => s.is_empty(),
        Json::Array(items) => items.is_empty(),
        Json::Object(entries) => entries.is_empty(),
        _ => false,
    }
}
