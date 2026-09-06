//! Error matching (compat/model/README.md § Errors).
//!
//! A clause carries both the modeled shape and the wire code, because SDKs
//! disagree about which of the two they surface — for SQS's not-found,
//! `QueueDoesNotExist` and `AWS.SimpleQueueService.NonExistentQueue` — so
//! either is accepted, but by **equality** against a code parsed out of a
//! surface this SDK actually has, never by containment. Containment cannot tell
//! a code from a code that ends with it: a clause naming `NotFoundException`
//! would be satisfied by a `ResourceNotFoundException`, which is a different
//! error from a different branch of the service, and by the word appearing
//! anywhere in the SDK's prose.
//!
//! The surfaces the AWS SDK for Rust gives this suite, and where each comes
//! from:
//!
//! | Surface | Where it comes from |
//! | --- | --- |
//! | `ProvideErrorMetadata::code()` | the code the protocol deserializer resolved out of the body — the AWS JSON protocols' `__type` with its namespace already stripped, or the REST JSON body's `code`. This is the `bodyType` and `bodyCode` carriers |
//! | the raw response body | the same body, kept by this suite's own interceptor as the SDK deserializes it (`read_after_deserialization`, for the reason [`super::capture`] gives). It is the carrier the shared fixtures exercise, and it is what makes `bodyType` observable even where the SDK folds two codes into one |
//! | `x-amzn-query-error` | the header an `awsQueryCompatible` service sends, as `<code>;<Sender\|Receiver>`, read off the raw response an `SdkError` carries |
//!
//! What this suite does **not** observe is the `exceptionName` carrier. Rust
//! models a service's errors as one enum per operation, and a modeled variant's
//! name is reachable only through `Debug`, which is a rendering rather than a
//! surface: parsing it would be inventing a carrier, not reading one. Fixtures
//! whose only positive expectation matches through `exceptionName` are skipped
//! by name and with a reason, which is what the shared fixture contract asks
//! for.
//!
//! When no surface states a code the clause does **not** match. There is no
//! containment fallback, and the absence of one is the rule rather than an
//! omission: an error with no code surface is no evidence that the service
//! raised the named error.

use serde_json::Value as Json;

/// Names one error two ways, because SDKs disagree about which they surface:
/// the modeled shape name and the wire code. Either is accepted.
pub struct ErrorSpec {
    pub shape: &'static str,
    pub code: &'static str,
}

/// The header an `awsQueryCompatible` service returns alongside the JSON body,
/// carrying the legacy Query code with a fault suffix.
pub(crate) const QUERY_ERROR_HEADER: &str = "x-amzn-query-error";

/// Reports whether a failed call carries the error a clause names.
pub(crate) fn matches(codes: &[String], want: &ErrorSpec) -> bool {
    codes
        .iter()
        .flat_map(|code| spellings(code))
        .any(|got| got == want.shape || got == want.code)
}

/// One observed code in every spelling a clause may name it by, which is the
/// list compat/model/README.md § Errors fixes: the value itself, what follows
/// the last `#` of a Smithy id (`com.amazonaws.sqs#QueueDoesNotExist` states the
/// same code as `QueueDoesNotExist`), and what precedes the first `;` of the
/// `<code>;<fault>` form the `x-amzn-query-error` header uses.
///
/// Splitting at those separators and nowhere else is what keeps the match an
/// equality: no spelling of `ResourceNotFoundException` is `NotFoundException`.
pub(crate) fn spellings(code: &str) -> Vec<String> {
    let mut out = vec![code.to_string()];
    if let Some((_, after)) = code.rsplit_once('#') {
        out.push(after.to_string());
    }
    if let Some((before, _)) = code.split_once(';') {
        out.push(before.to_string());
    }
    out
}

/// Every code an observation states, in the order the surfaces are read.
///
/// The arguments are the surfaces themselves rather than an `SdkError`, so the
/// shared conformance fixtures run through the same extraction a live failure
/// does.
pub(crate) fn surfaces(
    sdk_code: Option<&str>,
    query_error_header: Option<&str>,
    body: Option<&Json>,
) -> Vec<String> {
    let mut out = Vec::new();
    let mut push = |code: Option<String>| {
        if let Some(code) = code {
            if !code.is_empty() && !out.contains(&code) {
                out.push(code);
            }
        }
    };
    push(sdk_code.map(str::to_string));
    push(query_error_header.map(str::to_string));
    push(body.and_then(body_code));
    out
}

/// The code a parsed error body states, in whichever spelling the protocol and
/// the SDK use.
fn body_code(body: &Json) -> Option<String> {
    let object = body.as_object()?;
    for key in ["__type", "Code", "code"] {
        if let Some(Json::String(value)) = object.get(key) {
            return Some(value.clone());
        }
    }
    if let Some(Json::Object(nested)) = object.get("Error") {
        for key in ["Code", "code"] {
            if let Some(Json::String(value)) = nested.get(key) {
                return Some(value.clone());
            }
        }
    }
    None
}

/// Renders both halves of an error clause for a failure message.
pub(crate) fn accepted_codes(want: &ErrorSpec) -> String {
    if want.shape == want.code {
        format!("error {:?}", want.shape)
    } else {
        format!("error {:?} or {:?}", want.shape, want.code)
    }
}
