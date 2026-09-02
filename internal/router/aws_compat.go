package router

import (
	"encoding/base64"
	"encoding/json"
	"encoding/xml"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/overcast-sh/overcast/internal/config"
	"github.com/overcast-sh/overcast/internal/middleware"
	"github.com/overcast-sh/overcast/internal/protocol"
	"github.com/overcast-sh/overcast/internal/services/sqs"
	"github.com/overcast-sh/overcast/internal/smtp"
)

// aws_compat.go — LocalStack's /_aws/ inspection namespace.
//
// health_compat.go and localstack_compat.go serve /_localstack/, the half of
// LocalStack's namespace that infrastructure hits: a healthcheck, an
// init-script poll, a reset between test cases. This file serves the other
// half, the one a test suite hits — to assert on side effects the AWS API
// itself gives no way to read back. SES has no "list the emails you sent";
// SQS's ReceiveMessage consumes what it shows you. LocalStack answers both at
// /_aws/ses and /_aws/sqs/messages, and `curl localhost:4566/_aws/ses` is
// written into one assertion per test that sends an email.
//
// That is the difference from /_localstack/, and why this file exists: a
// compose healthcheck is one line a user can edit once, but an assertion is
// repeated across a suite, and a 404 there is a red build with no obvious
// cause. Each served path is therefore a translation of a store Overcast's own
// API already exposes into LocalStack's wire shape, field for field where the
// data exists and omitted where it does not:
//
//	GET    /_aws/ses            -> the inbox behind /_overcast/ses/inbox/messages,
//	                               emails only, as {"messages": [...]}
//	DELETE /_aws/ses            -> clears those emails (?id= clears one)
//	GET    /_aws/sqs/messages   -> the peek behind GET /{account}/{queue},
//	                               as a ReceiveMessageResponse in XML or JSON
//
// Both translations read through the canonical implementation — the same
// smtp.MailStore the inbox serves, sqs.Service.PeekQueue behind the
// path-style peek — so the alias cannot report a different set of emails or
// messages than Overcast's own endpoint would.
//
// What is deliberately NOT served here, and why, so the omission is a decision
// rather than an oversight. Each is answered by the 404 below with the
// Overcast endpoint to use instead:
//
//   - /_aws/sns/sms-messages and /_aws/sns/platform-endpoint-messages: the
//     same inbox store, filtered to "sms" and "push". Cheap, and next in
//     #1545's order; not in this change.
//   - /_aws/lambda/runtimes: Overcast's /_overcast/lambda/runtimes carries the
//     same list with more per runtime; the shape translation is not done yet.
//   - /_aws/execute-api/{apiId}/{stage}/*: LocalStack's fallback for when the
//     execute-api host form will not resolve. Overcast serves LocalStack's
//     other documented URL, /restapis/{apiId}/{stage}/_user_request_/*, and
//     resolves the host form itself, so this one is most likely unnecessary.
//   - /_aws/sns/subscription-tokens/{arn} and /_aws/dynamodb/expired: these
//     need new behaviour (exposing the confirmation token; a manual TTL
//     sweep), not a new URL. Tracked separately from the aliases.

// The paths this file registers, named once so the routes, the hint lines,
// the ledger in pathnamespace_dev_test.go and the docs cannot disagree about
// their spelling.
const (
	awsCompatSESPath             = middleware.AWSCompatPrefix + "ses"
	awsCompatSQSMessagesPath     = middleware.AWSCompatPrefix + "sqs/messages"
	awsCompatSQSMessagesPathForm = awsCompatSQSMessagesPath + "/{region}/{accountID}/{queueName}"
)

// ---- /_aws/ses -------------------------------------------------------------

// localStackSESMessage is one captured email in the shape GET /_aws/ses
// returns. The field names, their casing and the snake_case body parts are
// LocalStack's, verbatim, because a test suite reads them by name.
//
// Two LocalStack fields are absent rather than faked. Region: the inbox
// captures what came over SMTP, and a region is not part of an email — SES's
// handler knows it, the wire does not carry it, and inventing the configured
// default would be wrong for every send made in another region. RawData:
// LocalStack sets it only for SendRawEmail, and Overcast's capture cannot
// tell a raw send from a composed one after the fact, so it would either be
// present on every message or on none. A field a reader checks by name and
// finds missing is a clearer failure than one that is present and wrong.
type localStackSESMessage struct {
	ID          string                   `json:"Id"`
	Source      string                   `json:"Source"`
	Subject     string                   `json:"Subject"`
	Destination localStackSESDestination `json:"Destination"`
	Body        localStackSESBody        `json:"Body"`
	// Timestamp is LocalStack's naive UTC second-precision form,
	// "2023-09-11T08:37:13" — no zone suffix, because theirs has none.
	Timestamp string `json:"Timestamp"`
}

type localStackSESDestination struct {
	ToAddresses []string `json:"ToAddresses"`
}

// localStackSESBody carries pointers so an absent part renders as null, which
// is what LocalStack returns for an email with no HTML alternative.
type localStackSESBody struct {
	TextPart *string `json:"text_part"`
	HTMLPart *string `json:"html_part"`
}

// localStackSESTimestampLayout is the format LocalStack renders Timestamp in.
const localStackSESTimestampLayout = "2006-01-02T15:04:05"

// localStackSESMessages renders the emails in store in LocalStack's shape,
// oldest first as LocalStack lists them, filtered by id and by source address
// when either is non-empty (LocalStack's ?id= and ?email= parameters).
//
// Only emails. The inbox is shared with the SMS, webhook and mobile-push
// captures, and LocalStack's /_aws/ses lists what SES sent — an SMS in the
// answer would be a message the test never sent through SES.
func localStackSESMessages(store *smtp.MailStore, id, email string) []localStackSESMessage {
	all := store.List() // newest first
	out := make([]localStackSESMessage, 0, len(all))
	for i := len(all) - 1; i >= 0; i-- {
		m := all[i]
		if m.Kind != smtp.KindEmail {
			continue
		}
		if id != "" && m.ID != id {
			continue
		}
		if email != "" && m.From != email {
			continue
		}
		out = append(out, localStackSESMessage{
			ID:          m.ID,
			Source:      m.From,
			Subject:     m.Subject,
			Destination: localStackSESDestination{ToAddresses: append([]string{}, m.To...)},
			Body: localStackSESBody{
				TextPart: nonEmptyPtr(m.TextBody),
				HTMLPart: nonEmptyPtr(m.HTMLBody),
			},
			Timestamp: m.ReceivedAt.UTC().Format(localStackSESTimestampLayout),
		})
	}
	return out
}

func nonEmptyPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// newAWSCompatSESListHandler serves GET /_aws/ses over the same inbox store
// as /_overcast/ses/inbox/messages.
func newAWSCompatSESListHandler(store *smtp.MailStore, hinter *aliasHinter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		hinter.hint(awsCompatSESPath,
			zap.String("use", "/_overcast/ses/inbox/messages"),
			zap.String("note", "LocalStack's captured-email endpoint, served for compatibility; the inbox endpoint carries every capture, SMS and webhooks included"))
		q := r.URL.Query()
		messages := localStackSESMessages(store, q.Get("id"), q.Get("email"))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{"messages": messages})
	}
}

// newAWSCompatSESDeleteHandler serves DELETE /_aws/ses: every captured email,
// or the one named by ?id=. It answers 204 either way, as LocalStack does —
// including for an id that matched nothing, since the state a caller asked
// for is the state they have.
//
// Emails only, which is why this is DeleteWhere rather than Clear: the store
// also holds the SMS, webhook and push captures, and LocalStack's endpoint
// clears what SES sent, not the whole inbox.
func newAWSCompatSESDeleteHandler(store *smtp.MailStore, hinter *aliasHinter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		hinter.hint("DELETE "+awsCompatSESPath,
			zap.String("use", "DELETE /_overcast/ses/inbox/messages"),
			zap.String("note", "LocalStack's captured-email clear, served for compatibility; the inbox endpoint clears every capture"))
		id := r.URL.Query().Get("id")
		store.DeleteWhere(func(m *smtp.CapturedMessage) bool {
			if m.Kind != smtp.KindEmail {
				return true
			}
			return id != "" && m.ID != id
		})
		w.WriteHeader(http.StatusNoContent)
	}
}

// ---- /_aws/sqs/messages ----------------------------------------------------

// localStackReceiveMessageResponse is the body GET /_aws/sqs/messages
// returns: LocalStack renders its peek as the SQS Query protocol's
// ReceiveMessageResponse — XML by default, and the same tree as JSON under
// Accept: application/json — so this one struct carries both sets of tags.
type localStackReceiveMessageResponse struct {
	XMLName  xml.Name                       `xml:"http://queue.amazonaws.com/doc/2012-11-05/ ReceiveMessageResponse" json:"-"`
	Result   localStackReceiveMessageResult `xml:"ReceiveMessageResult" json:"ReceiveMessageResult"`
	Metadata localStackResponseMetadata     `xml:"ResponseMetadata" json:"ResponseMetadata"`
}

type localStackReceiveMessageResult struct {
	// Message is never null in the JSON form: a queue with nothing in it
	// answers [], which is what a test asserting on length expects.
	Message []localStackSQSMessage `xml:"Message" json:"Message"`
}

type localStackResponseMetadata struct {
	RequestID string `xml:"RequestId" json:"RequestId"`
}

// localStackSQSMessage is one queued message in ReceiveMessage's wire shape.
// The receipt handle is the message's real one — Overcast assigns it at send
// time — rather than LocalStack's "SQS/BACKDOOR/ACCESS" placeholder, so a
// handle read from the peek can be passed to DeleteMessage.
type localStackSQSMessage struct {
	MessageID        string                       `xml:"MessageId" json:"MessageId"`
	MD5OfBody        string                       `xml:"MD5OfBody" json:"MD5OfBody"`
	Body             string                       `xml:"Body" json:"Body"`
	Attribute        []localStackNameValue        `xml:"Attribute" json:"Attribute"`
	MessageAttribute []localStackMessageAttribute `xml:"MessageAttribute,omitempty" json:"MessageAttribute,omitempty"`
	ReceiptHandle    string                       `xml:"ReceiptHandle" json:"ReceiptHandle"`
}

type localStackNameValue struct {
	Name  string `xml:"Name" json:"Name"`
	Value string `xml:"Value" json:"Value"`
}

type localStackMessageAttribute struct {
	Name  string                          `xml:"Name" json:"Name"`
	Value localStackMessageAttributeValue `xml:"Value" json:"Value"`
}

type localStackMessageAttributeValue struct {
	DataType    string `xml:"DataType" json:"DataType"`
	StringValue string `xml:"StringValue,omitempty" json:"StringValue,omitempty"`
	// BinaryValue is base64 on the wire in both encodings. encoding/xml
	// would write a []byte as raw bytes, so it is encoded here rather than
	// left to the marshaller.
	BinaryValue string `xml:"BinaryValue,omitempty" json:"BinaryValue,omitempty"`
}

// localStackSQSMessages renders peeked messages in ReceiveMessage's shape,
// applying LocalStack's two visibility switches. By default LocalStack shows
// what a ReceiveMessage would return right now: a message inside its
// visibility timeout needs ShowInvisible=true, one still inside its send
// delay needs ShowDelayed=true. Overcast's peek reports both facts and hides
// neither, so the filtering is done here.
func localStackSQSMessages(msgs []sqs.PeekedMessage, showInvisible, showDelayed bool) []localStackSQSMessage {
	out := make([]localStackSQSMessage, 0, len(msgs))
	for _, m := range msgs {
		switch {
		case m.Delayed && !showDelayed:
			continue
		case m.Inflight && !m.Delayed && !showInvisible:
			continue
		}
		out = append(out, localStackSQSMessage{
			MessageID:        m.MessageID,
			MD5OfBody:        m.MD5OfBody,
			Body:             m.Body,
			Attribute:        localStackSQSAttributes(m),
			MessageAttribute: localStackSQSMessageAttributes(m.MessageAttributes),
			ReceiptHandle:    m.ReceiptHandle,
		})
	}
	return out
}

// localStackSQSAttributes flattens a message's system attributes into the
// Name/Value list the Query protocol uses, sorted by name so the body does not
// reshuffle between calls. ApproximateReceiveCount is taken from the counter
// the peek reports rather than the stored attribute map, so the two cannot
// disagree.
func localStackSQSAttributes(m sqs.PeekedMessage) []localStackNameValue {
	out := make([]localStackNameValue, 0, len(m.Attributes)+1)
	seen := false
	for name, value := range m.Attributes {
		if name == "ApproximateReceiveCount" {
			value = strconv.Itoa(m.ApproximateReceiveCount)
			seen = true
		}
		out = append(out, localStackNameValue{Name: name, Value: value})
	}
	if !seen {
		out = append(out, localStackNameValue{Name: "ApproximateReceiveCount", Value: strconv.Itoa(m.ApproximateReceiveCount)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func localStackSQSMessageAttributes(attrs map[string]sqs.MessageAttribute) []localStackMessageAttribute {
	if len(attrs) == 0 {
		return nil
	}
	out := make([]localStackMessageAttribute, 0, len(attrs))
	for name, a := range attrs {
		value := localStackMessageAttributeValue{DataType: a.DataType, StringValue: a.StringValue}
		if len(a.BinaryValue) > 0 {
			value.BinaryValue = base64.StdEncoding.EncodeToString(a.BinaryValue)
		}
		out = append(out, localStackMessageAttribute{Name: name, Value: value})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// awsCompatQueueTarget is which queue a /_aws/sqs/messages request names,
// resolved from whichever of LocalStack's three forms it used.
type awsCompatQueueTarget struct {
	region    string // "" means the request's own region, then the default
	queueName string
}

// resolveAWSCompatQueue reads the queue from the request, in LocalStack's
// order of preference: the path form /{region}/{account}/{queue}, then
// ?QueueUrl=, then ?QueueName= with an optional ?QueueRegion=.
//
// The account segment is read and ignored. Overcast keys queues by region and
// name under the one configured account, so the account in a queue URL is the
// one that minted it and never selects anything; a queue URL from a different
// account would fail on the AWS API too, with the same NonExistentQueue this
// reports.
func resolveAWSCompatQueue(r *http.Request) (awsCompatQueueTarget, *protocol.AWSError) {
	if name := chi.URLParam(r, "queueName"); name != "" {
		return awsCompatQueueTarget{region: strings.ToLower(chi.URLParam(r, "region")), queueName: name}, nil
	}
	q := r.URL.Query()
	if raw := q.Get("QueueUrl"); raw != "" {
		return queueTargetFromURL(raw, strings.ToLower(q.Get("QueueRegion")))
	}
	if name := q.Get("QueueName"); name != "" {
		return awsCompatQueueTarget{region: strings.ToLower(q.Get("QueueRegion")), queueName: name}, nil
	}
	return awsCompatQueueTarget{}, &protocol.AWSError{
		Code:       "MissingParameter",
		Message:    "The request must specify a queue: ?QueueUrl=, ?QueueName= (with an optional ?QueueRegion=), or the path form " + awsCompatSQSMessagesPath + "/{region}/{account}/{queue}.",
		HTTPStatus: http.StatusBadRequest,
	}
}

// queueTargetFromURL reads region and queue name out of a queue URL. The
// name is the last path segment, as in every form a queue URL takes. The
// region comes from the host when the URL names one — LocalStack's
// sqs.{region}.localhost.localstack.cloud, AWS's sqs.{region}.amazonaws.com
// and the legacy {region}.queue.amazonaws.com — and an explicit QueueRegion
// wins over all of them. A URL minted on a plain origin
// (http://localhost:4566/000000000000/q, which is what Overcast itself mints)
// names no region, and the request's own region applies.
func queueTargetFromURL(raw, explicitRegion string) (awsCompatQueueTarget, *protocol.AWSError) {
	invalid := func() (awsCompatQueueTarget, *protocol.AWSError) {
		return awsCompatQueueTarget{}, &protocol.AWSError{
			Code:       "InvalidParameterValue",
			Message:    "QueueUrl is not a queue URL: " + raw,
			HTTPStatus: http.StatusBadRequest,
		}
	}
	u, err := url.Parse(raw)
	if err != nil {
		return invalid()
	}
	segments := strings.Split(strings.Trim(u.Path, "/"), "/")
	name := segments[len(segments)-1]
	if name == "" {
		return invalid()
	}
	region := explicitRegion
	if region == "" {
		region = regionFromQueueHost(u.Hostname())
	}
	return awsCompatQueueTarget{region: region, queueName: name}, nil
}

// regionFromQueueHost is the host half of queueTargetFromURL. It is
// deliberately narrower than the router's own host parsing: only the three
// hostname shapes that carry a region in a documented position are read, and
// anything else answers "" so the request's own region decides. Reading a
// region out of the wrong segment is how a request ends up scoped to a
// region nothing else can name (see middleware.regionFromEndpointHost).
func regionFromQueueHost(hostname string) string {
	parts := strings.Split(strings.ToLower(hostname), ".")
	switch {
	case len(parts) >= 3 && parts[0] == "sqs":
		return parts[1]
	case len(parts) >= 3 && parts[1] == "queue":
		return parts[0]
	}
	return ""
}

// wantsJSON reports whether the caller asked for LocalStack's JSON rendering
// of the peek. XML is the default, as it is there.
func wantsJSON(r *http.Request) bool {
	return strings.Contains(r.Header.Get("Accept"), "application/json")
}

// newAWSCompatSQSPeekHandler serves GET /_aws/sqs/messages and its path form
// over sqs.Service.PeekQueue, the read behind Overcast's own
// GET /{accountID}/{queueName}.
func newAWSCompatSQSPeekHandler(cfg *config.Config, svc *sqs.Service, hinter *aliasHinter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		hinter.hint(awsCompatSQSMessagesPath,
			zap.String("use", "GET /"+cfg.AccountID+"/{queueName}"),
			zap.String("note", "LocalStack's queue peek, served for compatibility; Overcast's own peek reports visibility per message rather than filtering on it"))

		writeError := func(aerr *protocol.AWSError) {
			if wantsJSON(r) {
				protocol.WriteJSONError(w, r, aerr)
				return
			}
			protocol.WriteQueryXMLError(w, r, aerr)
		}

		target, aerr := resolveAWSCompatQueue(r)
		if aerr != nil {
			writeError(aerr)
			return
		}
		msgs, aerr := svc.PeekQueue(r.Context(), target.region, target.queueName)
		if aerr != nil {
			writeError(aerr)
			return
		}

		q := r.URL.Query()
		body := localStackReceiveMessageResponse{
			Result: localStackReceiveMessageResult{
				Message: localStackSQSMessages(msgs,
					strings.EqualFold(q.Get("ShowInvisible"), "true"),
					strings.EqualFold(q.Get("ShowDelayed"), "true")),
			},
			Metadata: localStackResponseMetadata{RequestID: protocol.RequestIDFromContext(r.Context())},
		}
		if wantsJSON(r) {
			protocol.WriteRESTJSON(w, r, http.StatusOK, map[string]any{"ReceiveMessageResponse": body})
			return
		}
		protocol.WriteXML(w, r, http.StatusOK, body)
	}
}

// ---- the rest of /_aws/ ----------------------------------------------------

// awsCompatReplacements names the Overcast endpoint that covers each
// LocalStack /_aws/ path this file does not serve, for the hint the 404
// carries. Keys are the path under the prefix, matched longest-first against
// the request so /_aws/sns/sms-messages/anything still finds its entry.
//
// A path Overcast actually serves does not belong here — ses and sqs/messages
// are answered above — and TestAWSCompatEndpointMapNamesOnlyUnservedPaths
// enforces that, for the reason localStackEndpointMap gives.
var awsCompatReplacements = map[string]string{
	"sns/sms-messages":               "/_overcast/ses/inbox/messages — SMS captures are the entries with \"kind\": \"sms\"",
	"sns/platform-endpoint-messages": "/_overcast/ses/inbox/messages — mobile-push captures are the entries with \"kind\": \"push\"",
	"lambda/runtimes":                "/_overcast/lambda/runtimes",
	"execute-api":                    "/restapis/{apiId}/{stage}/_user_request_/{path}, or the {apiId}.execute-api.localhost.localstack.cloud host form",
}

// awsCompatUnavailable is the LocalStack /_aws/ paths with no Overcast
// equivalent to point at, each with the reason, so the 404 can say "nothing
// serves this" rather than leave the reader to infer it from a missing hint.
var awsCompatUnavailable = map[string]string{
	"sns/subscription-tokens": "Overcast confirms http/https subscriptions by delivering the confirmation to the endpoint; the token is not exposed separately",
	"dynamodb/expired":        "TTL expiry runs on Overcast's own sweep; there is no manual trigger",
}

// awsCompatLookup finds the entry for a path under the prefix in either map,
// longest matching prefix first, and reports which map it came from.
func awsCompatLookup(endpoint string) (hint string, served bool, known bool) {
	segments := strings.Split(endpoint, "/")
	for n := len(segments); n > 0; n-- {
		key := strings.Join(segments[:n], "/")
		if hint, ok := awsCompatReplacements[key]; ok {
			return hint, true, true
		}
		if hint, ok := awsCompatUnavailable[key]; ok {
			return hint, false, true
		}
	}
	return "", false, false
}

// newAWSCompatNotFoundHandler answers everything under /_aws/ that Overcast
// does not serve — as itself, with the endpoint that replaces it, rather than
// as S3's NoSuchBucket. Mirrors newLocalStackNotFoundHandler.
func newAWSCompatNotFoundHandler(hinter *aliasHinter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		endpoint := strings.Trim(strings.TrimPrefix(r.URL.Path, middleware.AWSCompatPrefix), "/")
		hint, served, known := awsCompatLookup(endpoint)
		hinter.hint(r.URL.Path, zap.String("note", "no Overcast endpoint serves this LocalStack path"))

		message := "Overcast does not serve this LocalStack endpoint. Overcast's own endpoints live under /_overcast/ — see docs/migration-from-localstack.md."
		switch {
		case known && served:
			message = "Overcast serves this as " + hint + ". See docs/migration-from-localstack.md."
		case known:
			message = "Overcast has no equivalent of this LocalStack endpoint: " + hint + ". See docs/migration-from-localstack.md."
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"message":   message,
			"path":      r.URL.Path,
			"served":    awsCompatServedList(),
			"endpoints": awsCompatEndpointList(),
		})
	}
}

// awsCompatServedList names the paths under the prefix this file answers, so
// the 404 for a neighbour says what does work.
func awsCompatServedList() []string {
	return []string{
		"GET " + awsCompatSESPath + " (?id=, ?email=)",
		"DELETE " + awsCompatSESPath + " (?id=)",
		"GET " + awsCompatSQSMessagesPath + " (?QueueUrl=, ?QueueName=&QueueRegion=, or /{region}/{account}/{queue}; ?ShowInvisible=, ?ShowDelayed=; JSON under Accept: application/json)",
	}
}

// awsCompatEndpointList renders both maps in a stable order, so the 404 body
// does not reshuffle between calls.
func awsCompatEndpointList() []string {
	out := make([]string, 0, len(awsCompatReplacements)+len(awsCompatUnavailable))
	for from, to := range awsCompatReplacements {
		out = append(out, middleware.AWSCompatPrefix+from+" -> "+to)
	}
	for from, why := range awsCompatUnavailable {
		out = append(out, middleware.AWSCompatPrefix+from+" -> no equivalent: "+why)
	}
	sort.Strings(out)
	return out
}
