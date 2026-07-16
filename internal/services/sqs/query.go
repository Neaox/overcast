package sqs

// query.go adds SQS Query protocol support (form-encoded Action= requests with
// XML responses). This is the legacy protocol used by AWS SDK v1 and older CLIs.
//
// The approach is a transparent adapter: form params are converted to JSON,
// the existing JSON handler is invoked via a ResponseRecorder, and the JSON
// output is converted to Query XML. No handler code is duplicated.

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/Neaox/overcast/internal/protocol"
)

// ── Router interface implementations ───────────────────────────────────────

// OwnsAction satisfies router.QueryActionOwner.
func (s *Service) OwnsAction(action string) bool {
	_, ok := s.handler.ops[action]
	return ok
}

// DispatchQuery satisfies router.QueryDispatcher.
// It converts an SQS Query-protocol request (form-encoded) into the JSON
// body the existing handlers expect, captures their JSON response, and
// writes the equivalent Query XML response.
func (s *Service) DispatchQuery(w http.ResponseWriter, r *http.Request) {
	action := r.FormValue("Action")
	fn, ok := s.handler.ops[action]
	if !ok {
		protocol.WriteQueryXMLError(w, r, &protocol.AWSError{
			Code:       "InvalidAction",
			Message:    "The action " + action + " is not valid for this web service.",
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}

	// Convert form params → JSON body.
	jsonBody, err := sqsFormToJSON(action, r.Form)
	if err != nil {
		protocol.WriteQueryXMLError(w, r, &protocol.AWSError{
			Code:       "MalformedInput",
			Message:    "Could not parse request parameters: " + err.Error(),
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}

	// Swap body + content-type so the JSON handler works unchanged.
	r.Body = io.NopCloser(bytes.NewReader(jsonBody))
	r.ContentLength = int64(len(jsonBody))
	r.Header.Set("Content-Type", "application/x-amz-json-1.0")

	// Capture JSON response.
	rec := httptest.NewRecorder()
	fn(rec, r)

	// Convert to Query XML.
	writeQueryXMLFromJSON(w, r, action, rec)
}

// ── Form → JSON conversion ─────────────────────────────────────────────────

// fieldKind describes how a form parameter maps to JSON.
type fieldKind int

const (
	kString    fieldKind = iota // simple string: "QueueName" → {"QueueName":"v"}
	kInt                        // integer: "DelaySeconds" → {"DelaySeconds":5}
	kOptInt                     // optional integer pointer (nil if absent)
	kNameValue                  // Attribute.N.Name/Value → map[string]string
	kKeyValue                   // Tag.N.Key/Value → map[string]string
	kList                       // AttributeName.N → []string
	kMsgAttrs                   // MessageAttribute.N.Name + Value.* → map[string]MessageAttribute
	kEntries                    // batch entries: SendMessageBatchRequestEntry.N.* → []map[string]any
)

// field describes one form parameter group.
type field struct {
	form string    // form key or numbered-param prefix
	json string    // JSON object key
	kind fieldKind // conversion strategy
}

// schemas maps each SQS action to its form-parameter schema.
// Actions not listed still dispatch but send an empty JSON body — handlers
// that require specific fields will reject with their own validation error.
var schemas = map[string][]field{
	"CreateQueue": {
		{form: "QueueName", json: "QueueName", kind: kString},
		{form: "Attribute", json: "Attributes", kind: kNameValue},
		{form: "Tag", json: "tags", kind: kKeyValue},
	},
	"GetQueueUrl": {
		{form: "QueueName", json: "QueueName", kind: kString},
		{form: "QueueOwnerAWSAccountId", json: "QueueOwnerAWSAccountId", kind: kString},
	},
	"SendMessage": {
		{form: "QueueUrl", json: "QueueUrl", kind: kString},
		{form: "MessageBody", json: "MessageBody", kind: kString},
		{form: "DelaySeconds", json: "DelaySeconds", kind: kInt},
		{form: "MessageDeduplicationId", json: "MessageDeduplicationId", kind: kString},
		{form: "MessageGroupId", json: "MessageGroupId", kind: kString},
		{form: "MessageAttribute", json: "MessageAttributes", kind: kMsgAttrs},
	},
	"ReceiveMessage": {
		{form: "QueueUrl", json: "QueueUrl", kind: kString},
		{form: "MaxNumberOfMessages", json: "MaxNumberOfMessages", kind: kInt},
		{form: "VisibilityTimeout", json: "VisibilityTimeout", kind: kOptInt},
		{form: "WaitTimeSeconds", json: "WaitTimeSeconds", kind: kInt},
		{form: "ReceiveRequestAttemptId", json: "ReceiveRequestAttemptId", kind: kString},
		{form: "AttributeName", json: "AttributeNames", kind: kList},
		{form: "MessageSystemAttributeName", json: "MessageSystemAttributeNames", kind: kList},
		{form: "MessageAttributeName", json: "MessageAttributeNames", kind: kList},
	},
	"DeleteMessage": {
		{form: "QueueUrl", json: "QueueUrl", kind: kString},
		{form: "ReceiptHandle", json: "ReceiptHandle", kind: kString},
	},
	"GetQueueAttributes": {
		{form: "QueueUrl", json: "QueueUrl", kind: kString},
		{form: "AttributeName", json: "AttributeNames", kind: kList},
	},
	"SetQueueAttributes": {
		{form: "QueueUrl", json: "QueueUrl", kind: kString},
		{form: "Attribute", json: "Attributes", kind: kNameValue},
	},
	"DeleteQueue": {
		{form: "QueueUrl", json: "QueueUrl", kind: kString},
	},
	"ListQueues": {
		{form: "QueueNamePrefix", json: "QueueNamePrefix", kind: kString},
		{form: "MaxResults", json: "MaxResults", kind: kInt},
	},
	"PurgeQueue": {
		{form: "QueueUrl", json: "QueueUrl", kind: kString},
	},
	"ChangeMessageVisibility": {
		{form: "QueueUrl", json: "QueueUrl", kind: kString},
		{form: "ReceiptHandle", json: "ReceiptHandle", kind: kString},
		{form: "VisibilityTimeout", json: "VisibilityTimeout", kind: kInt},
	},
	"TagQueue": {
		{form: "QueueUrl", json: "QueueUrl", kind: kString},
		{form: "Tag", json: "Tags", kind: kKeyValue},
	},
	"UntagQueue": {
		{form: "QueueUrl", json: "QueueUrl", kind: kString},
		{form: "TagKey", json: "TagKeys", kind: kList},
	},
	"ListQueueTags": {
		{form: "QueueUrl", json: "QueueUrl", kind: kString},
	},
	"ListDeadLetterSourceQueues": {
		{form: "QueueUrl", json: "QueueUrl", kind: kString},
	},
	"SendMessageBatch": {
		{form: "QueueUrl", json: "QueueUrl", kind: kString},
		{form: "SendMessageBatchRequestEntry", json: "Entries", kind: kEntries},
	},
	"DeleteMessageBatch": {
		{form: "QueueUrl", json: "QueueUrl", kind: kString},
		{form: "DeleteMessageBatchRequestEntry", json: "Entries", kind: kEntries},
	},
	"ChangeMessageVisibilityBatch": {
		{form: "QueueUrl", json: "QueueUrl", kind: kString},
		{form: "ChangeMessageVisibilityBatchRequestEntry", json: "Entries", kind: kEntries},
	},
}

// sqsFormToJSON converts SQS Query form params to the JSON body the handlers expect.
func sqsFormToJSON(action string, form url.Values) ([]byte, error) {
	schema, ok := schemas[action]
	if !ok {
		// Unknown actions still dispatch — handler returns its own error.
		return []byte("{}"), nil
	}

	result := make(map[string]any)
	for _, f := range schema {
		switch f.kind {
		case kString:
			if v := form.Get(f.form); v != "" {
				result[f.json] = v
			}
		case kInt:
			if v := form.Get(f.form); v != "" {
				n, err := strconv.Atoi(v)
				if err != nil {
					return nil, fmt.Errorf("invalid integer for %s: %s", f.form, v)
				}
				result[f.json] = n
			}
		case kOptInt:
			if v := form.Get(f.form); v != "" {
				n, err := strconv.Atoi(v)
				if err != nil {
					return nil, fmt.Errorf("invalid integer for %s: %s", f.form, v)
				}
				result[f.json] = n
			}
		case kNameValue:
			if m := parseNameValueMap(form, f.form); len(m) > 0 {
				result[f.json] = m
			}
		case kKeyValue:
			if m := parseKeyValueMap(form, f.form); len(m) > 0 {
				result[f.json] = m
			}
		case kList:
			if l := parseNumberedList(form, f.form); len(l) > 0 {
				result[f.json] = l
			}
		case kMsgAttrs:
			if m := parseMessageAttributes(form, f.form); len(m) > 0 {
				result[f.json] = m
			}
		case kEntries:
			if entries := parseBatchEntries(form, f.form); len(entries) > 0 {
				result[f.json] = entries
			}
		}
	}
	return json.Marshal(result)
}

// ── Numbered form param parsers ─────────────────────────────────────────────

// parseNameValueMap handles Attribute.N.Name / Attribute.N.Value patterns.
func parseNameValueMap(form url.Values, prefix string) map[string]string {
	m := map[string]string{}
	for i := 1; ; i++ {
		name := form.Get(fmt.Sprintf("%s.%d.Name", prefix, i))
		if name == "" {
			break
		}
		value := form.Get(fmt.Sprintf("%s.%d.Value", prefix, i))
		m[name] = value
	}
	return m
}

// parseKeyValueMap handles Tag.N.Key / Tag.N.Value patterns.
func parseKeyValueMap(form url.Values, prefix string) map[string]string {
	m := map[string]string{}
	for i := 1; ; i++ {
		key := form.Get(fmt.Sprintf("%s.%d.Key", prefix, i))
		if key == "" {
			break
		}
		value := form.Get(fmt.Sprintf("%s.%d.Value", prefix, i))
		m[key] = value
	}
	return m
}

// parseNumberedList handles AttributeName.N patterns.
func parseNumberedList(form url.Values, prefix string) []string {
	var out []string
	for i := 1; ; i++ {
		v := form.Get(fmt.Sprintf("%s.%d", prefix, i))
		if v == "" {
			break
		}
		out = append(out, v)
	}
	return out
}

// parseMessageAttributes handles MessageAttribute.N.Name + .Value.DataType + .Value.StringValue.
func parseMessageAttributes(form url.Values, prefix string) map[string]any {
	m := map[string]any{}
	for i := 1; ; i++ {
		name := form.Get(fmt.Sprintf("%s.%d.Name", prefix, i))
		if name == "" {
			break
		}
		attr := map[string]string{}
		if v := form.Get(fmt.Sprintf("%s.%d.Value.DataType", prefix, i)); v != "" {
			attr["DataType"] = v
		}
		if v := form.Get(fmt.Sprintf("%s.%d.Value.StringValue", prefix, i)); v != "" {
			attr["StringValue"] = v
		}
		if v := form.Get(fmt.Sprintf("%s.%d.Value.BinaryValue", prefix, i)); v != "" {
			attr["BinaryValue"] = v
		}
		m[name] = attr
	}
	return m
}

// parseBatchEntries handles numbered batch entries like
// SendMessageBatchRequestEntry.N.Id, .MessageBody, etc.
// It extracts all sub-keys for each numbered entry into a flat map,
// plus handles nested MessageAttribute.M.* within each entry.
func parseBatchEntries(form url.Values, prefix string) []map[string]any {
	// Discover which entry numbers exist.
	entryNums := map[int]bool{}
	entryRe := regexp.MustCompile(`^` + regexp.QuoteMeta(prefix) + `\.(\d+)\.`)
	for key := range form {
		if m := entryRe.FindStringSubmatch(key); m != nil {
			n, _ := strconv.Atoi(m[1])
			entryNums[n] = true
		}
	}
	if len(entryNums) == 0 {
		return nil
	}

	// Sort entry numbers for deterministic output.
	nums := make([]int, 0, len(entryNums))
	for n := range entryNums {
		nums = append(nums, n)
	}
	sort.Ints(nums)

	var entries []map[string]any
	for _, n := range nums {
		entry := map[string]any{}
		p := fmt.Sprintf("%s.%d.", prefix, n)

		// Extract simple fields.
		for key, vals := range form {
			if !strings.HasPrefix(key, p) {
				continue
			}
			subKey := key[len(p):]
			// Skip nested MessageAttribute keys — handled below.
			if strings.HasPrefix(subKey, "MessageAttribute.") {
				continue
			}
			if len(vals) > 0 {
				// Convert known integer fields.
				switch subKey {
				case "DelaySeconds", "VisibilityTimeout":
					if v, err := strconv.Atoi(vals[0]); err == nil {
						entry[subKey] = v
					}
				default:
					entry[subKey] = vals[0]
				}
			}
		}

		// Extract nested MessageAttributes within this entry.
		entryPrefix := fmt.Sprintf("%s.%d.MessageAttribute", prefix, n)
		if msgAttrs := parseMessageAttributes(form, entryPrefix); len(msgAttrs) > 0 {
			entry["MessageAttributes"] = msgAttrs
		}

		entries = append(entries, entry)
	}
	return entries
}

// ── JSON → Query XML response conversion ────────────────────────────────────

// writeQueryXMLFromJSON converts a captured JSON response to SQS Query XML format.
func writeQueryXMLFromJSON(w http.ResponseWriter, r *http.Request, action string, rec *httptest.ResponseRecorder) {
	reqID := protocol.RequestIDFromContext(r.Context())

	// Error response — convert JSON error to Query XML error.
	if rec.Code >= 400 {
		writeQueryXMLErrorFromJSON(w, r, rec)
		return
	}

	// Success — wrap JSON body in <{Action}Response><{Action}Result>...</>.
	var data map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &data); err != nil {
		// Handlers that write empty success (e.g. DeleteQueue, PurgeQueue).
		data = map[string]any{}
	}

	var buf bytes.Buffer
	buf.WriteString(xml.Header)
	buf.WriteString(fmt.Sprintf("<%sResponse xmlns=\"http://queue.amazonaws.com/doc/2012-11-05/\">\n", action))
	buf.WriteString(fmt.Sprintf("  <%sResult>\n", action))
	writeXMLMap(&buf, data, "    ")
	buf.WriteString(fmt.Sprintf("  </%sResult>\n", action))
	buf.WriteString("  <ResponseMetadata>\n")
	buf.WriteString(fmt.Sprintf("    <RequestId>%s</RequestId>\n", xmlEscape(reqID)))
	buf.WriteString("  </ResponseMetadata>\n")
	buf.WriteString(fmt.Sprintf("</%sResponse>\n", action))

	body := buf.Bytes()
	w.Header().Set("Content-Type", "text/xml")
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.Header().Set("x-amzn-requestid", reqID)
	w.WriteHeader(rec.Code)
	w.Write(body) //nolint:errcheck
}

// writeQueryXMLErrorFromJSON converts a captured JSON error to Query XML format.
func writeQueryXMLErrorFromJSON(w http.ResponseWriter, r *http.Request, rec *httptest.ResponseRecorder) {
	reqID := protocol.RequestIDFromContext(r.Context())

	var jsonErr struct {
		Type    string `json:"__type"`
		Message string `json:"message"`
	}
	json.Unmarshal(rec.Body.Bytes(), &jsonErr) //nolint:errcheck

	// Check for 501 to set emulator header.
	if rec.Code == http.StatusNotImplemented {
		w.Header().Set("x-emulator-unsupported", "true")
	}

	body, _ := xml.Marshal(&queryXMLErrorEnvelope{
		Error: queryXMLErrorBody{
			Type:    "Sender",
			Code:    jsonErr.Type,
			Message: jsonErr.Message,
		},
		RequestID: reqID,
	})
	full := append([]byte(xml.Header), body...)
	w.Header().Set("Content-Type", "text/xml")
	w.Header().Set("Content-Length", strconv.Itoa(len(full)))
	w.Header().Set("x-amzn-requestid", reqID)
	w.WriteHeader(rec.Code)
	w.Write(full) //nolint:errcheck
}

type queryXMLErrorEnvelope struct {
	XMLName   xml.Name          `xml:"ErrorResponse"`
	Error     queryXMLErrorBody `xml:"Error"`
	RequestID string            `xml:"RequestId"`
}

type queryXMLErrorBody struct {
	Type    string `xml:"Type"`
	Code    string `xml:"Code"`
	Message string `xml:"Message"`
}

// ── Generic JSON map → XML element writer ───────────────────────────────────

// collectionElements maps JSON array keys to their XML element names.
// SQS Query XML uses singular element names for repeated items.
var collectionElements = map[string]string{
	"QueueUrls":  "QueueUrl",
	"Messages":   "Message",
	"Successful": "SendMessageBatchResultEntry",
	"Failed":     "BatchResultErrorEntry",
}

// mapElements lists JSON keys that represent Name→Value maps
// and should be rendered as repeated <Attribute><Name>k</Name><Value>v</Value></Attribute>.
var mapElements = map[string]string{
	"Attributes":        "Attribute",
	"Tags":              "Tag",
	"MessageAttributes": "MessageAttribute",
}

// writeXMLMap recursively converts a JSON map to XML elements.
func writeXMLMap(buf *bytes.Buffer, data map[string]any, indent string) {
	// Sort keys for deterministic output.
	keys := make([]string, 0, len(data))
	for k := range data {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, key := range keys {
		val := data[key]
		switch v := val.(type) {
		case map[string]any:
			if elemName, ok := mapElements[key]; ok {
				// Render as repeated <elemName><Name>k</Name><Value>v</Value></elemName>.
				writeXMLNameValueMap(buf, v, elemName, indent)
			} else {
				// Nested object.
				buf.WriteString(indent + "<" + key + ">\n")
				writeXMLMap(buf, v, indent+"  ")
				buf.WriteString(indent + "</" + key + ">\n")
			}
		case []any:
			elemName := collectionElements[key]
			if elemName == "" {
				elemName = singularize(key)
			}
			for _, item := range v {
				switch elem := item.(type) {
				case map[string]any:
					buf.WriteString(indent + "<" + elemName + ">\n")
					writeXMLMap(buf, elem, indent+"  ")
					buf.WriteString(indent + "</" + elemName + ">\n")
				default:
					buf.WriteString(indent + "<" + elemName + ">" + xmlEscape(fmt.Sprint(elem)) + "</" + elemName + ">\n")
				}
			}
		case string:
			buf.WriteString(indent + "<" + key + ">" + xmlEscape(v) + "</" + key + ">\n")
		case float64:
			// JSON numbers are float64 — render as int if no fractional part.
			if v == float64(int64(v)) {
				buf.WriteString(indent + "<" + key + ">" + strconv.FormatInt(int64(v), 10) + "</" + key + ">\n")
			} else {
				buf.WriteString(indent + "<" + key + ">" + strconv.FormatFloat(v, 'f', -1, 64) + "</" + key + ">\n")
			}
		case bool:
			buf.WriteString(indent + "<" + key + ">" + strconv.FormatBool(v) + "</" + key + ">\n")
		case nil:
			// Skip nil values.
		default:
			buf.WriteString(indent + "<" + key + ">" + xmlEscape(fmt.Sprint(v)) + "</" + key + ">\n")
		}
	}
}

// writeXMLNameValueMap renders a JSON map as repeated XML elements like
// <elemName><Name>k</Name><Value>v</Value></elemName>.
func writeXMLNameValueMap(buf *bytes.Buffer, data map[string]any, elemName, indent string) {
	keys := make([]string, 0, len(data))
	for k := range data {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, k := range keys {
		buf.WriteString(indent + "<" + elemName + ">\n")
		buf.WriteString(indent + "  <Name>" + xmlEscape(k) + "</Name>\n")
		switch v := data[k].(type) {
		case map[string]any:
			// MessageAttribute: render Value sub-fields inline.
			buf.WriteString(indent + "  <Value>\n")
			writeXMLMap(buf, v, indent+"    ")
			buf.WriteString(indent + "  </Value>\n")
		default:
			buf.WriteString(indent + "  <Value>" + xmlEscape(fmt.Sprint(v)) + "</Value>\n")
		}
		buf.WriteString(indent + "</" + elemName + ">\n")
	}
}

// singularize strips a trailing 's' for use as XML element name.
func singularize(s string) string {
	if strings.HasSuffix(s, "s") {
		return s[:len(s)-1]
	}
	return s
}

// xmlEscape escapes special XML characters.
func xmlEscape(s string) string {
	var buf bytes.Buffer
	_ = xml.EscapeText(&buf, []byte(s))
	return buf.String()
}
