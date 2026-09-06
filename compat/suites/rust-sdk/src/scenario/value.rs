//! Value expressions (compat/model/README.md § Values), as Rust.
//!
//! The IR's five forms are five variants here. A value is ordinary JSON — a
//! string, a number, a bool, a list, an object — and an expression anywhere
//! inside it is evaluated against the group's context bag, exactly as an object
//! with one `$`-prefixed key is an expression in the JSON the interpreters
//! read. There are no conditionals, no arithmetic and no scripting: eight
//! implementations have to agree on every value.
//!
//! ```text
//! {"$lit": v}        → Value::Lit(v)
//! {"$ref": "q.url"}  → Value::Context("q.url")
//! {"$name": "q"}     → Value::Name("q")
//! {"$concat": [...]} → Value::Concat(...)
//! {"$index": [v, n]} → Value::Index(v, n)
//! ```
//!
//! # Why the typed call reads the evaluated params rather than the expression
//!
//! A call's params are evaluated once, whole, before anything is sent: that
//! evaluation is failure-message field 3, and it is what an unresolvable `$ref`
//! is reported against. The emitted typed call then asks the [`Binder`] for one
//! member of *that* document by path. So the value the SDK is handed and the
//! value the failure message quotes are the same value, and the emitted source
//! spells each expression once, as data, rather than once as data and again as
//! an argument.

use serde_json::Value as Json;

use super::json;
use crate::harness::TestContext;

/// One value, as the scenario file writes it.
#[derive(Clone, Debug)]
pub enum Value {
    /// A literal, verbatim.
    Lit(Json),
    /// `$ref`: the context value at that path.
    Context(&'static str),
    /// `$name`: `{run_id}-{group}-{suffix}`.
    Name(&'static str),
    /// `$concat`: the parts joined, each of which must evaluate to a string.
    Concat(Vec<Value>),
    /// `$index`: element `n` of a list-valued expression. No recipe in the
    /// current corpus writes one; the grammar is closed, not corpus-shaped.
    #[allow(dead_code)]
    Index(Box<Value>, usize),
    /// A list of values.
    List(Vec<Value>),
    /// A structure or map of values.
    Map(Vec<(&'static str, Value)>),
}

/// Why a value could not be evaluated.
pub(crate) enum EvalError {
    /// A `$ref` whose context path is not set — the one failure a teardown step
    /// is allowed to be skipped for.
    Unresolved(String),
    /// Anything else: a concat part that is not a string, an index past the end.
    Message(String),
}

impl EvalError {
    pub(crate) fn message(&self) -> String {
        match self {
            EvalError::Unresolved(path) => format!("context path {path:?} is not set"),
            EvalError::Message(message) => message.clone(),
        }
    }
}

impl Value {
    /// Evaluates the value against a group's context.
    pub(crate) fn eval(&self, bag: &Bag<'_>) -> Result<Json, EvalError> {
        match self {
            Value::Lit(value) => Ok(value.clone()),
            Value::Context(path) => bag
                .get(path)
                .ok_or_else(|| EvalError::Unresolved((*path).to_string())),
            Value::Name(suffix) => Ok(Json::String(bag.name(suffix))),
            Value::Concat(parts) => {
                let mut out = String::new();
                for part in parts {
                    match part.eval(bag)? {
                        Json::String(s) => out.push_str(&s),
                        other => {
                            return Err(EvalError::Message(format!(
                                "concat part evaluated to {}, which is not a string",
                                json::render(&other)
                            )))
                        }
                    }
                }
                Ok(Json::String(out))
            }
            Value::Index(inner, n) => match inner.eval(bag)? {
                Json::Array(items) => items.get(*n).cloned().ok_or_else(|| {
                    EvalError::Message(format!(
                        "index {n} is past the end of a list of {}",
                        items.len()
                    ))
                }),
                other => Err(EvalError::Message(format!(
                    "index applies to a list, got {}",
                    json::render(&other)
                ))),
            },
            Value::List(items) => {
                let mut out = Vec::with_capacity(items.len());
                for item in items {
                    out.push(item.eval(bag)?);
                }
                Ok(Json::Array(out))
            }
            Value::Map(entries) => {
                let mut out = serde_json::Map::new();
                for (key, value) in entries {
                    out.insert((*key).to_string(), value.eval(bag)?);
                }
                Ok(Json::Object(out))
            }
        }
    }

    /// The value as the scenario file writes it, expressions unevaluated.
    ///
    /// It is failure-message field 3 for a failure raised *before* anything was
    /// sent: nothing went on the wire, so the message shows what the file asked
    /// for. The rendering is the IR's own `$`-prefixed spelling, which is what
    /// the three interpreters print in the same case.
    pub(crate) fn raw(&self) -> Json {
        match self {
            Value::Lit(value) => value.clone(),
            Value::Context(path) => expr("$ref", Json::String((*path).to_string())),
            Value::Name(suffix) => expr("$name", Json::String((*suffix).to_string())),
            Value::Concat(parts) => expr(
                "$concat",
                Json::Array(parts.iter().map(Value::raw).collect()),
            ),
            Value::Index(inner, n) => expr(
                "$index",
                Json::Array(vec![inner.raw(), Json::Number((*n).into())]),
            ),
            Value::List(items) => Json::Array(items.iter().map(Value::raw).collect()),
            Value::Map(entries) => {
                let mut out = serde_json::Map::new();
                for (key, value) in entries {
                    out.insert((*key).to_string(), value.raw());
                }
                Json::Object(out)
            }
        }
    }
}

fn expr(key: &str, arg: Json) -> Json {
    let mut out = serde_json::Map::new();
    out.insert(key.to_string(), arg);
    Json::Object(out)
}

/// The map from context path (`queue.url`) to value that a group's exports fill
/// in and its `$ref`s read.
///
/// It lives on the harness `TestContext`, which the harness creates once per
/// group run and hands to setup, every test and teardown — exactly the lifetime
/// the IR gives a group's context. That bag holds strings only (see
/// compat/suites/rust-sdk/AGENTS.md § Inter-test state), so a value is JSON on
/// the way in and parsed on the way out, and the keys are namespaced so a
/// generated group and a hand-written one cannot collide.
pub(crate) struct Bag<'a> {
    ctx: &'a TestContext,
    group: &'a str,
}

/// The prefix every scenario context path is stored under.
const BAG_PREFIX: &str = "scenario:";

impl<'a> Bag<'a> {
    pub(crate) fn new(ctx: &'a TestContext, group: &'a str) -> Self {
        Self { ctx, group }
    }

    pub(crate) fn get(&self, path: &str) -> Option<Json> {
        self.ctx
            .get(&format!("{BAG_PREFIX}{path}"))
            .and_then(|raw| serde_json::from_str(&raw).ok())
    }

    pub(crate) fn set(&self, path: &str, value: &Json) {
        if let Ok(raw) = serde_json::to_string(value) {
            self.ctx.set(&format!("{BAG_PREFIX}{path}"), raw);
        }
    }

    /// `$name`: `{run_id}-{group}-{suffix}`, with the group token the whole
    /// group name and no shortening anywhere. That is what makes the
    /// name-hygiene convention hold by construction, and what lets the orphan
    /// sweep find anything a crashed run left behind.
    pub(crate) fn name(&self, suffix: &str) -> String {
        format!("{}-{}-{suffix}", self.ctx.run_id, self.group)
    }
}

/// A member the emitted typed call could not fill.
///
/// It is returned rather than recorded, so an emitted builder chain is a flat
/// list of setters ending in `?` and the whole call is abandoned before
/// anything is sent.
pub struct BindError {
    /// The member's path inside the params document — failure-message field 4.
    pub member: String,
    pub message: String,
}

/// Hands the emitted typed call one member of the already-evaluated params.
///
/// Only a member the scenario writes as a deferred expression goes through
/// here; a literal is written into the emitted source as a literal, so the
/// typed spelling is visible where it is used and nothing is converted at run
/// time that could be decided at generation time.
pub struct Binder {
    params: Json,
}

impl Binder {
    pub(crate) fn new(params: Json) -> Self {
        Self { params }
    }

    /// The value at a member path, as a string.
    pub fn string(&self, member: &str) -> Result<String, BindError> {
        match self.at(member)? {
            Json::String(s) => Ok(s.clone()),
            other => Err(self.wrong(member, "a string", other)),
        }
    }

    /// The value at a member path, as a boolean.
    #[allow(dead_code)]
    pub fn boolean(&self, member: &str) -> Result<bool, BindError> {
        match self.at(member)? {
            Json::Bool(b) => Ok(*b),
            other => Err(self.wrong(member, "a boolean", other)),
        }
    }

    /// The value at a member path, as an `i8`.
    ///
    /// This and the four accessors below it are smithy-rs's remaining scalar
    /// widths — byte, short, long, float, double. The current corpus binds an
    /// expression only into an `i32` and a `String`, but which width a member
    /// needs is the model's answer and not the corpus's, so the set is complete
    /// rather than grown one refusal at a time.
    #[allow(dead_code)]
    pub fn i8(&self, member: &str) -> Result<i8, BindError> {
        self.integer(member, i8::MIN as i64, i8::MAX as i64, "i8")
            .map(|n| n as i8)
    }

    /// The value at a member path, as an `i16`.
    #[allow(dead_code)]
    pub fn i16(&self, member: &str) -> Result<i16, BindError> {
        self.integer(member, i16::MIN as i64, i16::MAX as i64, "i16")
            .map(|n| n as i16)
    }

    /// The value at a member path, as an `i32`.
    #[allow(dead_code)]
    pub fn i32(&self, member: &str) -> Result<i32, BindError> {
        self.integer(member, i32::MIN as i64, i32::MAX as i64, "i32")
            .map(|n| n as i32)
    }

    /// The value at a member path, as an `i64`.
    #[allow(dead_code)]
    pub fn i64(&self, member: &str) -> Result<i64, BindError> {
        self.integer(member, i64::MIN, i64::MAX, "i64")
    }

    /// The value at a member path, as an `f32`.
    #[allow(dead_code)]
    pub fn f32(&self, member: &str) -> Result<f32, BindError> {
        self.float(member, "f32").map(|n| n as f32)
    }

    /// The value at a member path, as an `f64`.
    #[allow(dead_code)]
    pub fn f64(&self, member: &str) -> Result<f64, BindError> {
        self.float(member, "f64")
    }

    #[allow(dead_code)]
    fn integer(&self, member: &str, min: i64, max: i64, kind: &str) -> Result<i64, BindError> {
        let value = self.at(member)?;
        let Some(number) = value.as_f64() else {
            return Err(self.wrong(member, "a number", value));
        };
        if number.trunc() != number {
            return Err(self.wrong(member, "a whole number", value));
        }
        if number < min as f64 || number > max as f64 {
            return Err(self.wrong(member, &format!("a number in range for {kind}"), value));
        }
        Ok(number as i64)
    }

    #[allow(dead_code)]
    fn float(&self, member: &str, kind: &str) -> Result<f64, BindError> {
        let value = self.at(member)?;
        value
            .as_f64()
            .ok_or_else(|| self.wrong(member, &format!("a {kind}"), value))
    }

    /// Resolves one member path inside the evaluated params.
    ///
    /// The path is a member name, optionally followed by `.member` and `[n]`
    /// segments — the same grammar as a response path, minus the leading `$`,
    /// which is added here. A map key containing a `.` is therefore not
    /// addressable; no scenario in scope has one, and the alternative is a
    /// second path grammar for one document.
    fn at(&self, member: &str) -> Result<&Json, BindError> {
        json::resolve(&self.params, &format!("$.{member}"))
            .ok()
            .flatten()
            .ok_or_else(|| BindError {
                member: member.to_string(),
                message: "the evaluated params carry no such member; this is a generator bug"
                    .to_string(),
            })
    }

    fn wrong(&self, member: &str, wanted: &str, got: &Json) -> BindError {
        BindError {
            member: member.to_string(),
            message: format!("wanted {wanted}, got {}", json::render(got)),
        }
    }
}
