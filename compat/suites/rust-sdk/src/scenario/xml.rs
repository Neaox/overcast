//! An XML response body as one of the IR's documents.
//!
//! [`super::capture`] keeps the raw response body and the assertions walk it,
//! which is exact for the two AWS JSON protocols: they serialize modeled member
//! names verbatim, and JSON carries its own scalar types. AWS Query and
//! REST XML do neither. Their bodies are XML, so parsing one as JSON yields
//! `null` and every path fails to resolve — the fault #1878 was filed about,
//! which nothing surfaces while every Query service in the corpus is Tier 0 and
//! answers 501.
//!
//! This module is the conversion. It is keyed off the body itself rather than
//! off the scenario's declared protocol: a body whose first non-space byte is
//! `<` is XML, and no JSON body begins that way. That reads the same fact the
//! `protocol` field states without needing the emitter to carry it into the
//! runtime, and it is right for a response whose `Content-Type` is missing or
//! wrong — an emulator's error writer and a real service disagree about
//! `text/xml` versus `application/xml` often enough that the header is the
//! weaker signal of the two.
//!
//! # The rules
//!
//! The wire is walked, not the model, so every rule below is one the element
//! tree states on its own:
//!
//! 1. **The root element is dropped.** The document is its content, so
//!    `$.LoadBalancerDescriptions` resolves rather than
//!    `$.DescribeLoadBalancersResponse.…`.
//! 2. **The Query envelope is unwrapped.** A root named `<Op>Response` holding
//!    an `<Op>Result` makes that result the document, and a `ResponseMetadata`
//!    sibling is dropped — it is the SDK's bookkeeping, exactly as go-sdk drops
//!    `ResultMetadata` from its own reflected document. An empty `<Op>Result`
//!    is `{}`, the same empty document an AWS JSON operation with no output
//!    members answers with.
//! 3. **`<member>` lists are flattened.** An element whose children are all
//!    named `member` is the list of those children — including a list of one,
//!    which is why the rule is by name and not by count.
//! 4. **`<entry><key>/<value>` maps are folded** into an object.
//! 5. **Anything else with children is an object** keyed by child element name,
//!    and a name that occurs more than once becomes a list in document order.
//! 6. **A leaf is its text.** An element with no children and no text — `<X/>`
//!    — is an empty list: the wire does not say whether a member it sent
//!    without content is an empty string or an empty list, and `[]` is the
//!    reading that is empty to `nonEmpty` *and* passes `isList`, which is what
//!    the ambiguity should not fail. `$.InstanceStates` on a load balancer with
//!    no instances is that case.
//!
//! Attributes are dropped: AWS puts `xmlns` on the root and nothing a path
//! addresses in one.
//!
//! # What it does not do
//!
//! **Scalars stay strings.** The XML wire carries no types — `<Interval>30`
//! and `<Enabled>true` are text — and this crate has no model at run time to
//! type them from. Every other backend gets its types from its SDK, which does
//! have the model, so `equals: 30` and `equals: true` are what a recipe writes;
//! [`super::json::equal`] therefore compares an XML-derived scalar against the
//! literal's own spelling, and [`super::capture::Wire`] is how it knows to.
//!
//! **The conventions here are Query's.** REST XML spells a list as a repeated
//! element inside a wrapper (`<Buckets><Bucket/><Bucket/></Buckets>`) whose
//! shape only the model gives, and `ec2Query` wraps list entries in `<item>`
//! rather than `<member>`. Neither is in the generated corpus; rule 5 gives a
//! repeated element a list anyway, but a single-element REST XML list comes out
//! as an object and the first REST XML scenario will have to say so here.

use serde_json::{Map, Value as Json};
use xmlparser::{ElementEnd, Token, Tokenizer};

/// Whether a body is XML, decided on its first non-space byte.
///
/// A JSON document begins with `{`, `[`, a quote, a digit, `t`, `f` or `n`;
/// never with `<`. An XML one begins with `<`, whether or not it carries an
/// `<?xml?>` prolog.
pub(crate) fn looks_like_xml(bytes: &[u8]) -> bool {
    bytes
        .iter()
        .find(|byte| !byte.is_ascii_whitespace())
        .is_some_and(|byte| *byte == b'<')
}

/// Converts an XML body to a document, or `None` when it is not well-formed
/// enough to walk. The caller then reports the same unresolvable paths a
/// non-JSON body reported before.
pub(crate) fn to_document(bytes: &[u8]) -> Option<Json> {
    let text = std::str::from_utf8(bytes).ok()?;
    let root = parse(text).ok()?;
    Some(unwrap(&root))
}

/// One element of the parsed tree. Children are kept in document order and by
/// name, because both matter: a repeated name is a list, and its order is the
/// list's.
#[derive(Debug, Default)]
struct Element {
    name: String,
    children: Vec<Element>,
    text: String,
}

/// Parses a body into its element tree.
///
/// `xmlparser` is a tokenizer rather than a tree builder, and it is already in
/// this crate's dependency graph: `aws-smithy-xml`, which every Query and
/// REST XML `aws-sdk-*` crate deserializes with, is built on it. So the reader
/// here is the same reader the SDK read the response with.
fn parse(text: &str) -> Result<Element, String> {
    let mut stack: Vec<Element> = Vec::new();
    let mut open: Option<Element> = None;
    let mut root: Option<Element> = None;

    // Closes `element` into its parent, or into the root slot when it has none.
    // The stack is a slice because only its last frame is touched here; pushing
    // is the caller's business.
    fn close(
        element: Element,
        stack: &mut [Element],
        root: &mut Option<Element>,
    ) -> Result<(), String> {
        match stack.last_mut() {
            Some(parent) => parent.children.push(element),
            None => {
                if root.is_some() {
                    return Err("the body has more than one root element".to_string());
                }
                *root = Some(element);
            }
        }
        Ok(())
    }

    for token in Tokenizer::from(text) {
        match token.map_err(|err| err.to_string())? {
            Token::ElementStart { local, .. } => {
                open = Some(Element {
                    name: local.as_str().to_string(),
                    ..Element::default()
                });
            }
            // Dropped: AWS puts `xmlns` on the root and nothing a path can
            // address in an attribute.
            Token::Attribute { .. } => {}
            Token::ElementEnd { end, .. } => match end {
                ElementEnd::Open => {
                    let element = open.take().ok_or("an element opened without a name")?;
                    stack.push(element);
                }
                ElementEnd::Empty => {
                    let element = open.take().ok_or("an element opened without a name")?;
                    close(element, &mut stack, &mut root)?;
                }
                ElementEnd::Close(_, local) => {
                    let element = stack.pop().ok_or("a closing tag with nothing open")?;
                    if element.name != local.as_str() {
                        return Err(format!("</{}> closes <{}>", local.as_str(), element.name));
                    }
                    close(element, &mut stack, &mut root)?;
                }
            },
            Token::Text { text } => {
                if let Some(element) = stack.last_mut() {
                    element.text.push_str(&unescape(text.as_str()));
                }
            }
            // CDATA is already literal; unescaping it would corrupt it.
            Token::Cdata { text, .. } => {
                if let Some(element) = stack.last_mut() {
                    element.text.push_str(text.as_str());
                }
            }
            // The prolog, comments, processing instructions and any DTD carry
            // nothing a response path addresses.
            _ => {}
        }
    }
    if !stack.is_empty() {
        return Err("the body ends with an element still open".to_string());
    }
    root.ok_or_else(|| "the body has no root element".to_string())
}

/// The five predefined entities and numeric character references.
///
/// `xmlparser` hands text back raw, so this is the whole of what an AWS body
/// can carry: a service escapes `&`, `<` and `>` in a value it echoes back, and
/// nothing here declares an entity of its own. An unrecognised `&…;` is left
/// alone rather than dropped — corrupting a value silently is worse than
/// showing it as it arrived.
fn unescape(raw: &str) -> String {
    if !raw.contains('&') {
        return raw.to_string();
    }
    let mut out = String::with_capacity(raw.len());
    let mut rest = raw;
    while let Some(start) = rest.find('&') {
        out.push_str(&rest[..start]);
        let after = &rest[start..];
        let Some(end) = after.find(';') else {
            out.push_str(after);
            return out;
        };
        let entity = &after[1..end];
        match entity {
            "amp" => out.push('&'),
            "lt" => out.push('<'),
            "gt" => out.push('>'),
            "quot" => out.push('"'),
            "apos" => out.push('\''),
            _ => match numeric_reference(entity) {
                Some(c) => out.push(c),
                None => out.push_str(&after[..=end]),
            },
        }
        rest = &after[end + 1..];
    }
    out.push_str(rest);
    out
}

/// `&#38;` and `&#x26;`, the two numeric forms.
fn numeric_reference(entity: &str) -> Option<char> {
    let digits = entity.strip_prefix('#')?;
    let code = match digits.strip_prefix(['x', 'X']) {
        Some(hex) => u32::from_str_radix(hex, 16).ok()?,
        None => digits.parse::<u32>().ok()?,
    };
    char::from_u32(code)
}

/// The response envelope, unwrapped: rules 1 and 2.
fn unwrap(root: &Element) -> Json {
    if let Some(stem) = root.name.strip_suffix("Response") {
        let result = format!("{stem}Result");
        if let Some(element) = root.children.iter().find(|child| child.name == result) {
            // An operation with no output members answers an empty result
            // element; that is `{}`, not a leaf with no text.
            if element.children.is_empty() {
                return Json::Object(Map::new());
            }
            return convert(element);
        }
    }
    match convert(root) {
        Json::Object(mut members) => {
            members.remove("ResponseMetadata");
            Json::Object(members)
        }
        other => other,
    }
}

/// One element's content, by rules 3 to 6.
fn convert(element: &Element) -> Json {
    if element.children.is_empty() {
        // Whitespace between tags is layout, not a value; a leaf that is only
        // whitespace is an element the service sent without content.
        if element.text.trim().is_empty() {
            return Json::Array(Vec::new());
        }
        return Json::String(element.text.clone());
    }
    if element.children.iter().all(|child| child.name == "member") {
        return Json::Array(element.children.iter().map(convert).collect());
    }
    if let Some(map) = as_map(element) {
        return map;
    }
    let mut out = Map::new();
    for child in &element.children {
        let value = convert(child);
        // Whether the name repeats is decided over the whole child list before
        // anything is inserted, so a repeated element whose own value is a list
        // lands beside its sibling rather than inside it.
        if occurrences(element, &child.name) > 1 {
            match out.get_mut(&child.name) {
                Some(Json::Array(items)) => items.push(value),
                _ => {
                    out.insert(child.name.clone(), Json::Array(vec![value]));
                }
            }
        } else {
            out.insert(child.name.clone(), value);
        }
    }
    Json::Object(out)
}

/// How many of `element`'s children carry `name`.
fn occurrences(element: &Element, name: &str) -> usize {
    element
        .children
        .iter()
        .filter(|child| child.name == name)
        .count()
}

/// Rule 4: `<entry><key>k</key><value>v</value></entry>` folded to an object.
fn as_map(element: &Element) -> Option<Json> {
    if !element.children.iter().all(|child| child.name == "entry") {
        return None;
    }
    let mut out = Map::new();
    for entry in &element.children {
        let key = entry.children.iter().find(|child| child.name == "key")?;
        let value = entry.children.iter().find(|child| child.name == "value")?;
        out.insert(key.text.clone(), convert(value));
    }
    Some(Json::Object(out))
}

#[cfg(test)]
mod tests {
    use super::*;

    use serde_json::json;

    /// A real `DescribeLoadBalancers` response, trimmed to the members the ELB
    /// scenario's paths walk. The shape is AWS's own documented one, and it is
    /// what `elastic-load-balancing.json` asserts against in the other seven
    /// suites.
    const DESCRIBE_LOAD_BALANCERS: &str = r#"<?xml version="1.0"?>
<DescribeLoadBalancersResponse xmlns="http://elasticloadbalancing.amazonaws.com/doc/2012-06-01/">
  <DescribeLoadBalancersResult>
    <LoadBalancerDescriptions>
      <member>
        <LoadBalancerName>run7-lb</LoadBalancerName>
        <DNSName>run7-lb-123.us-east-1.elb.amazonaws.com</DNSName>
        <SecurityGroups/>
        <HealthCheck>
          <Interval>30</Interval>
          <Target>TCP:80</Target>
          <Timeout>5</Timeout>
          <HealthyThreshold>2</HealthyThreshold>
          <UnhealthyThreshold>2</UnhealthyThreshold>
        </HealthCheck>
        <ListenerDescriptions>
          <member>
            <PolicyNames/>
            <Listener>
              <Protocol>HTTP</Protocol>
              <LoadBalancerPort>80</LoadBalancerPort>
              <InstanceProtocol>HTTP</InstanceProtocol>
              <InstancePort>80</InstancePort>
            </Listener>
          </member>
        </ListenerDescriptions>
        <AvailabilityZones>
          <member>us-east-1a</member>
        </AvailabilityZones>
      </member>
    </LoadBalancerDescriptions>
  </DescribeLoadBalancersResult>
  <ResponseMetadata>
    <RequestId>83c88b9d-12b7-11e3-8b82-87b12EXAMPLE</RequestId>
  </ResponseMetadata>
</DescribeLoadBalancersResponse>"#;

    fn document(body: &str) -> Json {
        to_document(body.as_bytes()).expect("the body converts")
    }

    /// The whole conversion on one real body: the envelope gone, `<member>`
    /// lists flattened, a list of one still a list, an empty element an empty
    /// list, and every scalar the text the wire sent.
    #[test]
    fn a_query_response_becomes_the_document_the_ir_walks() {
        assert_eq!(
            document(DESCRIBE_LOAD_BALANCERS),
            json!({
                "LoadBalancerDescriptions": [
                    {
                        "LoadBalancerName": "run7-lb",
                        "DNSName": "run7-lb-123.us-east-1.elb.amazonaws.com",
                        "SecurityGroups": [],
                        "HealthCheck": {
                            "Interval": "30",
                            "Target": "TCP:80",
                            "Timeout": "5",
                            "HealthyThreshold": "2",
                            "UnhealthyThreshold": "2"
                        },
                        "ListenerDescriptions": [
                            {
                                "PolicyNames": [],
                                "Listener": {
                                    "Protocol": "HTTP",
                                    "LoadBalancerPort": "80",
                                    "InstanceProtocol": "HTTP",
                                    "InstancePort": "80"
                                }
                            }
                        ],
                        "AvailabilityZones": ["us-east-1a"]
                    }
                ]
            })
        );
    }

    /// The paths the ELB scenario actually writes, resolved over that document.
    /// Spelled out because a conversion that is right in aggregate and wrong at
    /// one path fails one assertion in one suite, which is the failure this
    /// whole module exists to prevent.
    #[test]
    fn the_scenarios_own_paths_resolve() {
        let doc = document(DESCRIBE_LOAD_BALANCERS);
        for (path, want) in [
            (
                "$.LoadBalancerDescriptions[0].LoadBalancerName",
                json!("run7-lb"),
            ),
            (
                "$.LoadBalancerDescriptions[0].DNSName",
                json!("run7-lb-123.us-east-1.elb.amazonaws.com"),
            ),
            (
                "$.LoadBalancerDescriptions[0].HealthCheck.Interval",
                json!("30"),
            ),
            (
                "$.LoadBalancerDescriptions[0].ListenerDescriptions[0].Listener.LoadBalancerPort",
                json!("80"),
            ),
        ] {
            assert_eq!(
                super::super::json::resolve(&doc, path).expect("a well-formed path"),
                Some(&want),
                "{path}"
            );
        }
    }

    /// An operation whose result element is empty answers the empty document,
    /// not a leaf with no text — the same `{}` an AWS JSON operation with no
    /// output members answers.
    #[test]
    fn an_empty_result_element_is_an_empty_document() {
        let body = r#"<DeleteLoadBalancerResponse xmlns="http://elasticloadbalancing.amazonaws.com/doc/2012-06-01/">
  <DeleteLoadBalancerResult/>
  <ResponseMetadata><RequestId>f4a1a1d4</RequestId></ResponseMetadata>
</DeleteLoadBalancerResponse>"#;
        assert_eq!(document(body), json!({}));
    }

    /// A Query error body: the envelope is not a result envelope, so the
    /// document keeps `Error`, which is the spelling `errors::body_code` reads
    /// the code from. `ResponseMetadata` is the SDK's bookkeeping and is
    /// dropped wherever it appears, exactly as go-sdk drops `ResultMetadata`.
    #[test]
    fn a_query_error_body_keeps_its_error_member() {
        let body = r#"<ErrorResponse xmlns="http://elasticloadbalancing.amazonaws.com/doc/2012-06-01/">
  <Error>
    <Type>Sender</Type>
    <Code>LoadBalancerNotFound</Code>
    <Message>There is no ACTIVE Load Balancer named run7-lb</Message>
  </Error>
  <RequestId>1549581b-12b7-11e3-895e-1334aEXAMPLE</RequestId>
</ErrorResponse>"#;
        let doc = document(body);
        assert_eq!(doc["Error"]["Code"], json!("LoadBalancerNotFound"));
        assert_eq!(
            super::super::errors::surfaces(None, None, Some(&doc)),
            vec!["LoadBalancerNotFound".to_string()]
        );
    }

    /// The `awsQuery` map spelling, folded to an object. This is a real SNS
    /// `GetSubscriptionAttributes` body; ELB models no map, and the rule is the
    /// protocol's rather than the service's.
    #[test]
    fn an_entry_map_folds_to_an_object() {
        let body = r#"<GetSubscriptionAttributesResponse xmlns="http://sns.amazonaws.com/doc/2010-03-31/">
  <GetSubscriptionAttributesResult>
    <Attributes>
      <entry><key>Owner</key><value>123456789012</value></entry>
      <entry><key>RawMessageDelivery</key><value>false</value></entry>
    </Attributes>
  </GetSubscriptionAttributesResult>
  <ResponseMetadata><RequestId>057f074c</RequestId></ResponseMetadata>
</GetSubscriptionAttributesResponse>"#;
        assert_eq!(
            document(body),
            json!({"Attributes": {"Owner": "123456789012", "RawMessageDelivery": "false"}})
        );
    }

    /// Rule 5's two halves: a name that occurs once is its value, and a name
    /// that repeats is a list in document order — including where each
    /// occurrence is itself a `<member>` list, which must land beside its
    /// sibling rather than inside it.
    #[test]
    fn a_repeated_element_name_is_a_list_in_document_order() {
        let body = r#"<Wrapper>
  <Single>one</Single>
  <Repeated>first</Repeated>
  <Repeated>second</Repeated>
  <Nested><member>a</member></Nested>
  <Nested><member>b</member></Nested>
</Wrapper>"#;
        assert_eq!(
            document(body),
            json!({
                "Single": "one",
                "Repeated": ["first", "second"],
                "Nested": [["a"], ["b"]]
            })
        );
    }

    /// A leaf keeps its text as the wire spelled it, entities resolved and
    /// nothing trimmed off a real value; layout whitespace between tags is not
    /// a value at all.
    #[test]
    fn a_leaf_is_its_text_with_entities_resolved() {
        let body = "<Wrapper>\n  <Message>a &amp; b &lt;c&gt; &quot;d&quot; &#38; &#x26;</Message>\n  <Padded> spaced </Padded>\n</Wrapper>";
        assert_eq!(
            document(body),
            json!({"Message": "a & b <c> \"d\" & &", "Padded": " spaced "})
        );
    }

    /// An entity this module does not know is left as it arrived: showing a
    /// value the way the service sent it is better than dropping part of it.
    #[test]
    fn an_unknown_entity_is_left_alone() {
        assert_eq!(unescape("a &nbsp; b &broken"), "a &nbsp; b &broken");
    }

    /// Which conversion runs is the body's own first non-space byte. No JSON
    /// document starts with `<`; every XML one does, prolog or not.
    #[test]
    fn xml_is_recognised_by_its_first_non_space_byte() {
        assert!(looks_like_xml(b"<ErrorResponse/>"));
        assert!(looks_like_xml(b"\n  <?xml version=\"1.0\"?><Foo/>"));
        assert!(!looks_like_xml(br#"{"__type":"QueueDoesNotExist"}"#));
        assert!(!looks_like_xml(b"  [1,2]"));
        assert!(!looks_like_xml(b""));
    }

    /// A body that is not well-formed converts to nothing, and the caller then
    /// reports the same unresolvable paths a non-JSON body reported before this
    /// module existed. Silently returning half a tree would assert against a
    /// response nobody sent.
    #[test]
    fn a_malformed_body_converts_to_nothing() {
        for body in ["<Open><Inner></Open>", "<Unclosed>", "<A/><B/>"] {
            assert!(
                to_document(body.as_bytes()).is_none(),
                "{body:?} converted anyway"
            );
        }
    }

    /// The REST XML limitation, pinned rather than described: a wrapper holding
    /// one repeated element is an object, because only the model says the
    /// wrapper is a list. No scenario in the corpus is REST XML; the first one
    /// will have to change this, and this case is where it will say so.
    #[test]
    fn a_rest_xml_wrapper_is_not_flattened() {
        let body = "<ListAllMyBucketsResult><Buckets><Bucket><Name>one</Name></Bucket></Buckets></ListAllMyBucketsResult>";
        assert_eq!(
            document(body),
            json!({"Buckets": {"Bucket": {"Name": "one"}}})
        );
    }
}
