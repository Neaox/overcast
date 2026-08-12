package middleware

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/Neaox/overcast/internal/awsapi"
)

// This file holds the single mapping from a REST-routed HTTP request to the
// AWS operation it invokes. Both consumers read it: the request logger (labels
// on log lines, debug traces and operation metrics) and IAM enforcement (the
// action an authorization decision is made against). They previously each
// carried their own hand-written method+path switch, which is how the logger's
// copy came to claim every Lambda write as an S3 object operation while IAM's
// copy got it right.
//
// The mapping itself is not hand-written at all. `models/aws` pins the AWS
// Smithy models, and every operation in a REST protocol carries an `@http`
// trait naming its method and URI template — a method+path→operation table by
// construction. cmd/awsmodelgen already extracts those traits into
// internal/awsapi (Operation.HTTPMethod / Operation.URI) and compiles them
// into the sorted trie behind Registry.ClaimRESTQuery, which matches a request
// against them with no allocation and no manifest scan. So the correct
// mapping was already generated and committed; nothing here needed a new
// generator, only a caller.
//
// Two things the generated table cannot answer, both handled explicitly below:
//
//   - Emulator-only routes. A handful of Lambda endpoints exist solely for the
//     web UI and are in no AWS model, so no trait describes them.
//   - Bindings the model cannot attribute to one service. When distinct
//     services declare the same method and URI shape, awsmodelgen marks the
//     entry Ambiguous and blanks its service, because attributing it would be
//     a guess. It does keep the set of services that declare the binding and
//     what each calls the operation, so a caller that has already classified
//     the request answers exactly — see awsapi.Registry.RESTOperation.

// restOperation returns the AWS operation name a REST-routed request invokes,
// or "" when the pinned models describe no such operation for svc.
//
// svc is the service the request has already been classified as (see
// detectService). Scoping the lookup to it is what keeps one service's paths
// out of another's labels: the generated trie spans every modeled AWS service,
// so an unscoped lookup happily answers "GET /my-bucket/key" with MediaStore's
// GetObject. A claim that does not belong to svc is discarded rather than
// reported, so an unrecognised path yields no operation at all.
//
// Where several services declare the same binding this stays exact rather than
// approximate: "GET /v2/apis" is API Gateway v2's GetApis for an
// apigateway-signed caller and AppSync's ListApis for an appsync-signed one,
// because the generated candidate set records both, not just whichever service
// sorted first.
func restOperation(svc, method, path, rawQuery string) string {
	if svc == "" || path == "" || path[0] != '/' {
		return ""
	}
	if operation, ok := overcastRESTOperation(svc, method, path); ok {
		return operation
	}
	return awsapi.NewRegistry().RESTOperation(awsapiServiceKey(svc), method, path, rawQuery)
}

// awsapiServiceKey is middlewareServiceKey's inverse, applied where a service
// key travels the other way — from middleware into the generated registry.
// Keeping both directions explicit means a modeled identity that middleware
// renames cannot silently stop matching its own bindings; today only CloudWatch
// Logs is renamed, and it is a JSON-protocol service with no REST bindings to
// miss, but that is a property of the current models rather than of the code.
func awsapiServiceKey(s string) string {
	if s == "logs" {
		return "cloudwatch-logs"
	}
	return s
}

// lambdaFunctionResourcePrefix is the Lambda functions collection that
// Overcast's emulator-only per-function endpoints hang off.
//
// It used to be AWS's own prefix, "/2015-03-31/functions/", which is what made
// this function necessary in the first place: an invented path sitting inside
// a modeled one has to be told apart from its neighbours by hand. Phase 4 of
// docs/plans/non-canonical-url-namespace.md tried to move them and backed it
// out, because "/_" is also what shouldBypassIAM exempts and the move silently
// took the resource-scoped authorization with it. Phase 6 separated those two
// meanings — see shouldBypassIAM — so the paths could finally move.
//
// This table is now load-bearing for authorization, not only for labels: an
// internal path it can name is a path shouldBypassIAM will *not* exempt.
const lambdaFunctionResourcePrefix = "/_overcast/lambda/functions/"

// overcastRESTOperation names the REST routes Overcast serves that no AWS
// model describes. They are emulator-only endpoints the web UI calls on the
// AWS API port — source-code storage for the function editor, saved test
// events for the Test tab, and an SSE invoke that streams progress — so there
// is no `@http` trait to generate them from and they have to be listed. This
// is the only hand-written path mapping left, and it is shared: the logger's
// label and IAM's action both come from here.
//
// See internal/services/lambda/service.go's RegisterRoutes, which is where
// these routes are registered and the only place they can be added.
func overcastRESTOperation(svc, method, path string) (string, bool) {
	if svc != "lambda" {
		return "", false
	}
	rest, found := strings.CutPrefix(path, lambdaFunctionResourcePrefix)
	if !found {
		return "", false
	}
	// Skip the function name to reach the sub-resource.
	slash := strings.IndexByte(rest, '/')
	if slash < 0 {
		return "", false
	}
	subresource := rest[slash+1:]
	nested := false
	if i := strings.IndexByte(subresource, '/'); i >= 0 {
		subresource, nested = subresource[:i], true
	}

	switch subresource {
	case "source":
		if nested {
			return "", false
		}
		switch method {
		case http.MethodGet:
			return "GetFunctionSource", true
		case http.MethodPut:
			return "PutFunctionSource", true
		}
	case "test-events":
		if !nested {
			if method == http.MethodGet {
				return "ListTestEvents", true
			}
			return "", false
		}
		switch method {
		case http.MethodPut:
			return "PutTestEvent", true
		case http.MethodDelete:
			return "DeleteTestEvent", true
		}
	case "invoke-with-progress":
		// The web UI's streaming invoke. It is the same Lambda operation as a
		// plain invoke, and IAM authorizes it the same way.
		if !nested && method == http.MethodPost {
			return "Invoke", true
		}
	}
	return "", false
}

// rawQueryHas reports whether a raw query string carries the named parameter,
// with or without a value. It is the allocation-free equivalent of
// url.Values.Has on r.URL.Query(), which parses and allocates a whole map for
// what the S3 shape rules use as a set of flags.
func rawQueryHas(rawQuery, key string) bool {
	for rawQuery != "" {
		part := rawQuery
		if i := strings.IndexByte(rawQuery, '&'); i >= 0 {
			part, rawQuery = rawQuery[:i], rawQuery[i+1:]
		} else {
			rawQuery = ""
		}
		if part == key {
			return true
		}
		if len(part) > len(key) && part[len(key)] == '=' && part[:len(key)] == key {
			return true
		}
	}
	return false
}

// rawQueryValue returns the named parameter's value, or "" when it is absent.
// Percent- and plus-encoded values are decoded; the common case of a plain
// value costs no allocation.
func rawQueryValue(rawQuery, key string) string {
	for rawQuery != "" {
		part := rawQuery
		if i := strings.IndexByte(rawQuery, '&'); i >= 0 {
			part, rawQuery = rawQuery[:i], rawQuery[i+1:]
		} else {
			rawQuery = ""
		}
		if len(part) <= len(key) || part[len(key)] != '=' || part[:len(key)] != key {
			continue
		}
		value := part[len(key)+1:]
		if strings.ContainsAny(value, "%+") {
			if decoded, err := url.QueryUnescape(value); err == nil {
				return decoded
			}
		}
		return value
	}
	return ""
}

// pathDepth counts a URL path's segments the way strings.Split of its trimmed
// form does, without allocating the slice. strings.Trim reslices in place, so
// this is allocation-free.
func pathDepth(path string) int {
	return 1 + strings.Count(strings.Trim(path, "/"), "/")
}
