package conformance

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/overcast-sh/overcast/internal/clock"
	"github.com/overcast-sh/overcast/internal/protocol"
	"github.com/overcast-sh/overcast/internal/protocol/codec"
)

// NewNaiveJSONFixture builds the naive stub Fixture for AWS JSON 1.1 — one
// of the two protocol families the meta-test (meta_test.go) exercises
// Check against. See naive_stub.go for what the stub gets right and wrong.
func NewNaiveJSONFixture() Fixture {
	clk := clock.NewMock()
	logic := newNaiveLogic("widget-json", clk)
	const targetPrefix = "NaiveWidgetService."

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		op := strings.TrimPrefix(r.Header.Get("X-Amz-Target"), targetPrefix)
		var fields map[string]any
		if aerr := codec.JSON11.Decode(r, &fields); aerr != nil {
			codec.JSON11.WriteError(w, r, aerr)
			return
		}
		if fields == nil {
			fields = map[string]any{}
		}
		out, aerr := dispatchNaive(logic, op, fields)
		if aerr != nil {
			codec.JSON11.WriteError(w, r, aerr)
			return
		}
		codec.JSON11.WriteResponse(w, r, http.StatusOK, out)
	})

	return Fixture{
		Service:  "widget-json",
		Codec:    codec.JSON11,
		Handler:  handler,
		Resource: naiveResourceOps(),
		Errors:   naiveErrorCodes(),
		Input:    naiveInput,
		Reset:    logic.reset,
		Clock:    clk,
		Encode: func(op string, fields map[string]any) *http.Request {
			body, _ := json.Marshal(fields)
			req := httpPost("/", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/x-amz-json-1.1")
			req.Header.Set("X-Amz-Target", targetPrefix+op)
			return req
		},
		Decode: decodeJSONResponse,
	}
}

// dispatchNaive is the op-name switch shared by every naive stub's HTTP
// handler — the only per-protocol part is how fields got decoded and how
// out gets written back.
func dispatchNaive(logic *naiveLogic, op string, fields map[string]any) (map[string]any, *protocol.AWSError) {
	switch op {
	case "CreateWidget":
		return logic.create(fields)
	case "GetWidget":
		return logic.read(fields)
	case "UpdateWidget":
		return logic.update(fields)
	case "DeleteWidget":
		return logic.delete(fields)
	case "ListWidgets":
		return logic.list(fields)
	case "PingWidget":
		return logic.ping(fields)
	default:
		return nil, protocol.ErrNotImplemented
	}
}

func httpPost(path string, body io.Reader) *http.Request {
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, path, body)
	if err != nil {
		panic(fmt.Sprintf("conformance: building request: %v", err))
	}
	return req
}

// decodeJSONResponse turns an AWS JSON 1.1 wire response back into a
// logical field map, or a WireError for the jsonErrorResponse envelope
// (protocol.WriteJSONError's `{"__type": "...", "message": "..."}` shape).
func decodeJSONResponse(resp *http.Response) (map[string]any, *WireError) {
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()

	if resp.StatusCode >= http.StatusBadRequest {
		var env struct {
			Type    string `json:"__type"`
			Message string `json:"message"`
		}
		_ = json.Unmarshal(body, &env)
		return nil, &WireError{Code: env.Type, HTTPStatus: resp.StatusCode}
	}

	var fields map[string]any
	if len(body) > 0 {
		_ = json.Unmarshal(body, &fields)
	}
	return fields, nil
}
