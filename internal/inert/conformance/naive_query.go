package conformance

import (
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/protocol/codec"
)

// NewNaiveQueryFixture builds the naive stub Fixture for the AWS Query
// protocol (form-encoded request, XML response) — the second protocol
// family the meta-test exercises Check against, alongside JSON 1.1. Same
// business logic (naiveLogic) and the same bugs as naive_json.go; only the
// wire encoding differs, which is exactly the point — the clause ids and
// the set the naive stub violates must not depend on protocol family.
func NewNaiveQueryFixture() Fixture {
	clk := clock.NewMock()
	logic := newNaiveLogic("widget-query", clk)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = r.Body.Close()
		values, err := url.ParseQuery(string(body))
		if err != nil {
			codec.QueryXML.WriteError(w, r, protocol.ErrInvalidArgument("malformed form body: "+err.Error()))
			return
		}
		op := values.Get("Action")
		fields := map[string]any{}
		for key, vals := range values {
			if key == "Action" || key == "Version" || len(vals) == 0 {
				continue
			}
			fields[key] = vals[0]
		}

		out, aerr := dispatchNaive(logic, op, fields)
		if aerr != nil {
			codec.QueryXML.WriteError(w, r, aerr)
			return
		}
		writeQueryEnvelope(w, r, out)
	})

	return Fixture{
		Service:  "widget-query",
		Codec:    codec.QueryXML,
		Handler:  handler,
		Resource: naiveResourceOps(),
		Errors:   naiveErrorCodes(),
		Input:    naiveInput,
		Reset:    logic.reset,
		Clock:    clk,
		Encode: func(op string, fields map[string]any) *http.Request {
			values := url.Values{}
			values.Set("Action", op)
			values.Set("Version", "2026-01-01")
			for k, v := range fields {
				values.Set(k, fmt.Sprint(v))
			}
			req := httpPost("/", strings.NewReader(values.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			return req
		},
		Decode: decodeQueryResponse,
	}
}

// queryWidget is the XML shape one widget record is written/read as. It
// carries both xml and json tags: xml for the real wire bytes, json purely
// as an internal bridge so decodeQueryResponse can flatten an unmarshalled
// struct into the map[string]any Check operates on, the same shape
// decodeJSONResponse produces.
type queryWidget struct {
	Name             string  `xml:"Name" json:"Name"`
	Description      *string `xml:"Description" json:"Description,omitempty"`
	Size             *string `xml:"Size" json:"Size,omitempty"`
	Status           *string `xml:"Status" json:"Status,omitempty"`
	Arn              *string `xml:"Arn" json:"Arn,omitempty"`
	CreationTime     *string `xml:"CreationTime" json:"CreationTime,omitempty"`
	LastModifiedTime *string `xml:"LastModifiedTime" json:"LastModifiedTime,omitempty"`
}

type queryEnvelope struct {
	XMLName xml.Name `xml:"WidgetResponse"`
	queryWidget
	Widgets   []queryWidget `xml:"Widgets>member" json:"Widgets,omitempty"`
	NextToken *string       `xml:"NextToken" json:"NextToken,omitempty"`
	Result    *string       `xml:"Result" json:"Result,omitempty"`
}

func strPtr(s string) *string { return &s }

// widgetFromMap builds the XML-marshalable envelope from the naive logic's
// map[string]any output. Only present keys are set, so absence survives
// the round-trip (needed by §3.2/no-fabrication).
func widgetFromMap(m map[string]any) queryWidget {
	var w queryWidget
	if v, ok := m["Name"]; ok {
		w.Name = fmt.Sprint(v)
	}
	if v, ok := m["Description"]; ok {
		w.Description = strPtr(fmt.Sprint(v))
	}
	if v, ok := m["Size"]; ok {
		w.Size = strPtr(fmt.Sprint(v))
	}
	if v, ok := m["Status"]; ok {
		w.Status = strPtr(fmt.Sprint(v))
	}
	if v, ok := m["Arn"]; ok {
		w.Arn = strPtr(fmt.Sprint(v))
	}
	if v, ok := m["CreationTime"]; ok {
		w.CreationTime = strPtr(fmt.Sprint(v))
	}
	if v, ok := m["LastModifiedTime"]; ok {
		w.LastModifiedTime = strPtr(fmt.Sprint(v))
	}
	return w
}

func writeQueryEnvelope(w http.ResponseWriter, r *http.Request, out map[string]any) {
	env := queryEnvelope{queryWidget: widgetFromMap(out)}
	if rawItems, ok := out["Widgets"].([]any); ok {
		for _, raw := range rawItems {
			if item, ok := raw.(map[string]any); ok {
				env.Widgets = append(env.Widgets, widgetFromMap(item))
			}
		}
	}
	if v, ok := out["NextToken"]; ok {
		env.NextToken = strPtr(fmt.Sprint(v))
	}
	if v, ok := out["Result"]; ok {
		env.Result = strPtr(fmt.Sprint(v))
	}
	protocol.WriteQueryXML(w, r, http.StatusOK, &env)
}

// decodeQueryResponse turns a Query-protocol XML response back into a
// logical field map, or a WireError for protocol.WriteQueryXMLError's
// <ErrorResponse><Error><Code>...</Code>...</Error></ErrorResponse> envelope.
func decodeQueryResponse(resp *http.Response) (map[string]any, *WireError) {
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()

	if resp.StatusCode >= http.StatusBadRequest {
		var env struct {
			XMLName xml.Name `xml:"ErrorResponse"`
			Error   struct {
				Code string `xml:"Code"`
			} `xml:"Error"`
		}
		_ = xml.Unmarshal(body, &env)
		return nil, &WireError{Code: env.Error.Code, HTTPStatus: resp.StatusCode}
	}

	var env queryEnvelope
	if len(body) > 0 {
		_ = xml.Unmarshal(body, &env)
	}

	fields := widgetToMap(env.queryWidget)
	if env.NextToken != nil {
		fields["NextToken"] = *env.NextToken
	}
	if env.Result != nil {
		fields["Result"] = *env.Result
	}
	if env.Widgets != nil {
		items := make([]any, 0, len(env.Widgets))
		for _, w := range env.Widgets {
			items = append(items, widgetToMap(w))
		}
		fields["Widgets"] = items
	}
	return fields, nil
}

// widgetToMap is decodeResponse's half of the queryWidget<->map bridge —
// widgetFromMap above is the other half, used when writing.
func widgetToMap(w queryWidget) map[string]any {
	m := map[string]any{"Name": w.Name}
	set := func(key string, p *string) {
		if p != nil {
			m[key] = *p
		}
	}
	set("Description", w.Description)
	set("Size", w.Size)
	set("Status", w.Status)
	set("Arn", w.Arn)
	set("CreationTime", w.CreationTime)
	set("LastModifiedTime", w.LastModifiedTime)
	return m
}
