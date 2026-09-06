//! The response as the IR needs it, and the failure surfaces an error carries.
//!
//! # Why an interceptor
//!
//! An assertion walks a response by path, so the runtime needs the response as
//! a document. The three interpreters hold one already; the Go emitter reflects
//! over the SDK's output struct. Rust has neither route: `aws-sdk-*` output
//! types carry no `serde` derive at the pinned versions, and without reflection
//! the only alternative would be a generated converter per modeled output
//! shape — written by a Go program that cannot read a Rust accessor's return
//! type, which is exactly the assumption the plan forbids making.
//!
//! So the document is the response body itself, kept by [`Capture`] as the SDK
//! deserializes it. The two AWS
//! JSON protocols in scope serialize modeled member names verbatim, so the
//! document a path is walked over is spelled the way the scenario file spells
//! it, and it is the same document the other backends assert against.
//!
//! The two AWS JSON protocols put every modeled output member in the body, so
//! reading the body is reading the response. A REST protocol binds members to
//! headers and to the status line as well, and a scenario over one of those
//! would need more than this module gives it; nothing in scope uses one, so the
//! constraint is recorded rather than enforced.
//!
//! The interceptor is attached per call — `.customize().interceptor(capture)` —
//! rather than to the client, because a probe group's tests share one client and
//! run concurrently; a capture on the client would hand one test another's body.

use std::sync::{Arc, Mutex};

use aws_smithy_runtime_api::box_error::BoxError;
use aws_smithy_runtime_api::client::interceptors::context::AfterDeserializationInterceptorContextRef;
use aws_smithy_runtime_api::client::interceptors::Intercept;
use aws_smithy_runtime_api::client::orchestrator::HttpResponse;
use aws_smithy_runtime_api::client::result::SdkError;
use aws_smithy_runtime_api::client::runtime_components::RuntimeComponents;
use aws_smithy_types::config_bag::ConfigBag;
use aws_smithy_types::error::display::DisplayErrorContext;
use aws_smithy_types::error::metadata::ProvideErrorMetadata;
use serde_json::Value as Json;

use super::errors;

/// Captures one call's raw response body.
///
/// A retried call overwrites the previous attempt's body, which is what the
/// assertions want: the response that produced the outcome is the last one.
#[derive(Clone, Debug, Default)]
pub struct Capture {
    body: Arc<Mutex<Option<Vec<u8>>>>,
}

impl Capture {
    pub fn new() -> Self {
        Self::default()
    }

    /// The captured body, parsed. `None` when nothing was captured — a
    /// transport failure that never got a response — and `Some(Json::Null)`
    /// when a body arrived that is not JSON, which the checks then report as a
    /// path that does not resolve.
    fn parsed(&self) -> Option<Json> {
        let guard = self.body.lock().ok()?;
        let bytes = guard.as_ref()?;
        if bytes.is_empty() {
            // An AWS JSON operation with no output members answers `{}`; some
            // answer nothing at all. Both are an empty document, not a missing
            // response.
            return Some(Json::Object(serde_json::Map::new()));
        }
        Some(serde_json::from_slice(bytes).unwrap_or(Json::Null))
    }
}

impl Intercept for Capture {
    fn name(&self) -> &'static str {
        "overcast-compat-scenario-capture"
    }

    /// Reads the body **after** deserialization, not before.
    ///
    /// The orchestrator hands `read_before_deserialization` a response whose
    /// body is still the stream it came off the socket as, so `bytes()` there
    /// answers `None` for every call. It is `read_body` — run inside the
    /// deserialization step, for every non-streaming operation — that replaces
    /// the body with the bytes it collected, and those bytes are still on the
    /// response when this hook runs. The SDK has parsed them by then, which is
    /// the point: what this keeps is the same body the SDK deserialized.
    fn read_after_deserialization(
        &self,
        context: &AfterDeserializationInterceptorContextRef<'_>,
        _runtime_components: &RuntimeComponents,
        _cfg: &mut ConfigBag,
    ) -> Result<(), BoxError> {
        if let Some(bytes) = context.response().body().bytes() {
            if let Ok(mut guard) = self.body.lock() {
                *guard = Some(bytes.to_vec());
            }
        }
        Ok(())
    }
}

/// What one call produced: a response document, or a failure.
pub struct Outcome {
    /// The response body, as a document. `None` when the call failed.
    pub body: Option<Json>,
    /// The failure, when the call failed.
    pub error: Option<SdkFailure>,
}

/// A failed call, reduced to what the IR asks about it.
pub struct SdkFailure {
    /// The SDK's own message, with its whole `source()` chain — `SdkError`'s
    /// bare `Display` is the single word "service error".
    pub display: String,
    /// Every code the failure states, on any surface, unsplit.
    pub codes: Vec<String>,
    /// The HTTP status, where there was a response.
    pub status: Option<u16>,
}

impl SdkFailure {
    /// Whether the emulator answered "not implemented".
    ///
    /// The status code is read from the response where there is one, which is
    /// exact; there is no fallback over the SDK's prose, because the six-field
    /// message this failure ends up inside embeds the params JSON, where a run
    /// id or a port number can put a "501" that means nothing.
    pub(crate) fn is_unimplemented(&self) -> bool {
        self.status == Some(501)
    }
}

/// Turns one typed SDK result into an [`Outcome`].
///
/// Generated code calls this with the result of `request.send().await` and the
/// [`Capture`] that was attached to the same call.
pub fn observe<O, E>(result: Result<O, SdkError<E, HttpResponse>>, capture: &Capture) -> Outcome
where
    E: ProvideErrorMetadata + std::error::Error + Send + Sync + 'static,
{
    match result {
        Ok(_) => Outcome {
            body: Some(capture.parsed().unwrap_or(Json::Null)),
            error: None,
        },
        Err(err) => {
            let raw = raw_response(&err);
            let status = raw.map(|response| response.status().as_u16());
            let header =
                raw.and_then(|response| response.headers().get(errors::QUERY_ERROR_HEADER));
            let body = capture.parsed();
            let codes = errors::surfaces(err.code(), header, body.as_ref());
            Outcome {
                body: None,
                error: Some(SdkFailure {
                    display: format!("{}", DisplayErrorContext(&err)),
                    codes,
                    status,
                }),
            }
        }
    }
}

/// The raw response an `SdkError` carries, where the exchange got far enough to
/// have one.
fn raw_response<E>(err: &SdkError<E, HttpResponse>) -> Option<&HttpResponse> {
    match err {
        SdkError::ServiceError(inner) => Some(inner.raw()),
        SdkError::ResponseError(inner) => Some(inner.raw()),
        _ => None,
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    use aws_smithy_runtime_api::client::http::{
        HttpClient, HttpConnector, HttpConnectorFuture, HttpConnectorSettings, SharedHttpConnector,
    };
    use aws_smithy_runtime_api::client::orchestrator::HttpRequest;
    use aws_smithy_types::body::SdkBody;

    /// An HTTP client that answers every request with one canned response, so a
    /// case can drive a real client through a real failure without a service.
    #[derive(Clone, Debug)]
    struct Canned {
        status: u16,
        body: &'static str,
    }

    impl HttpConnector for Canned {
        fn call(&self, _request: HttpRequest) -> HttpConnectorFuture {
            let status = self
                .status
                .try_into()
                .expect("the canned status must be a status code");
            HttpConnectorFuture::ready(Ok(HttpResponse::new(status, SdkBody::from(self.body))))
        }
    }

    impl HttpClient for Canned {
        fn http_connector(
            &self,
            _settings: &HttpConnectorSettings,
            _components: &RuntimeComponents,
        ) -> SharedHttpConnector {
            SharedHttpConnector::new(self.clone())
        }
    }

    fn canned_client(status: u16, body: &'static str) -> aws_sdk_sqs::Client {
        let config = aws_sdk_sqs::Config::builder()
            .behavior_version(aws_sdk_sqs::config::BehaviorVersion::latest())
            .region(aws_sdk_sqs::config::Region::new("us-east-1"))
            .credentials_provider(aws_sdk_sqs::config::Credentials::new(
                "test",
                "test",
                None,
                None,
                "overcast-compat",
            ))
            .endpoint_url("http://127.0.0.1:4566")
            .http_client(Canned { status, body })
            .build();
        aws_sdk_sqs::Client::from_conf(config)
    }

    /// The interceptor runs on a **failed** call, so the body is there to read
    /// the error surfaces off.
    ///
    /// Nothing else in the suite shows this. The shared fixtures hand
    /// `errors::surfaces` a document directly, bypassing the interceptor
    /// altogether, and every live failure this suite has met also states its
    /// code through `ProvideErrorMetadata::code()` — so a capture that never
    /// fired on the error path would look exactly like one that did, until a
    /// service answered with a code only the body carries.
    #[tokio::test]
    async fn the_interceptor_keeps_the_body_of_a_failed_call() {
        let capture = Capture::new();
        let client = canned_client(
            400,
            r#"{"__type":"com.amazonaws.sqs#QueueDoesNotExist","message":"no such queue"}"#,
        );

        let result = client
            .get_queue_url()
            .queue_name("missing")
            .customize()
            .interceptor(capture.clone())
            .send()
            .await;

        let body = capture
            .parsed()
            .expect("the interceptor must have kept the body of a failed call");
        assert_eq!(body["__type"], "com.amazonaws.sqs#QueueDoesNotExist");

        let outcome = observe(result, &capture);
        let failure = outcome.error.expect("the call must have failed");
        assert_eq!(failure.status, Some(400));
        assert!(
            failure
                .codes
                .iter()
                .any(|code| code.contains("QueueDoesNotExist")),
            "the body's code must reach the surfaces: {:?}",
            failure.codes
        );
    }
}
