//go:build dev

// Package routecheck answers the question `make aws-models-check` cannot: for
// an operation Overcast *declares* it implements, does the AWS-modeled wire
// binding actually reach a handler?
//
// It is the reusable core behind two callers that both need to drive real HTTP
// requests at a running instance and grade the answers the same way:
//
//   - scripts/route-reachability.go, the standalone CLI documented in
//     docs/plans/route-reachability-audit.md, run by hand (or against an RC
//     image) with `go run -tags dev`.
//   - tests/integration/router/route_reachability_dev_test.go, which points
//     Sweep at an in-process httptest server carrying the full default service
//     set, so the same sweep runs on every PR under `-tags slim,dev` with no
//     Docker instance and no network.
//
// Both need `-tags dev`: internal/capabilities.AllCapabilities, the
// expectation set this package sweeps, only exists under that tag — which is
// also why this package itself is dev-only. See the audit doc for the fault
// this closes: an implementation mounted on an Overcast-invented path or
// X-Amz-Target prefix satisfies every static gate (a modeled operation still
// 501s where nothing else claims it; the declared operation name is real)
// while remaining unreachable from any AWS SDK. Static route-table gates
// (internal/router/modelbinding_dev_test.go and friends, see
// docs/plans/manifest-enforcement.md) already assert a route is *registered*
// at the modeled binding; this package proves something a route table cannot —
// that issuing the request over the wire actually reaches a handler.
package routecheck

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Neaox/overcast/internal/awsapi"
	"github.com/Neaox/overcast/internal/capabilities"
)

// Probe is one request issued at one modeled binding.
type Probe struct {
	Binding     string // "POST /applications" or "X-Amz-Target: AppConfig.CreateApplication"
	Status      int    // HTTP status the instance answered with
	Unsupported bool   // x-emulator-unsupported marker
	// Shared marks a binding more than one AWS service models, so a handler
	// answering here is not proof that *this* service's handler answered.
	Shared bool
	Body   string
	Error  string
}

// Finding is the verdict for one declared capability.
type Finding struct {
	Service   string
	Operation string
	Status    string
	Verdict   string
	Probes    []Probe
}

const (
	VerdictReachable   = "reachable"
	VerdictShared      = "shared-path"
	VerdictUnreachable = "unreachable"
	VerdictUnmodeled   = "unmodeled"
)

// Sweep probes every capability in caps against its modeled binding(s) at
// base and returns one Finding per capability. Pass a non-empty service to
// narrow the sweep to one Overcast service key, matching -service on the CLI.
//
// caps is deliberately a parameter rather than always reading
// capabilities.AllCapabilities: that snapshot is generated
// (`make generate-caps`) and only as fresh as the last regeneration, which is
// the right tradeoff for the CLI — a standalone binary that talks to a remote
// instance and cannot link the service packages that populate the live
// registry. A caller that already links every service (as
// tests/integration/router's in-process sweep does, through
// tests/helpers.NewTestServer) should pass capabilities.Default.All() instead,
// so a newly declared capability is swept the moment it is written, with no
// codegen step standing between the source and the gate.
//
// client's Timeout bounds every individual request, so a sweep against a
// non-responsive instance fails in client.Timeout*len(probes) at the outside
// rather than hanging — callers should size it deliberately (the CLI defaults
// to 10s; the in-process test uses a much smaller bound because a loopback
// httptest server either answers immediately or has a real bug).
func Sweep(ctx context.Context, client *http.Client, base, onlyService string, caps []capabilities.Capability) []Finding {
	base = strings.TrimSuffix(base, "/")
	bindings := ModeledBindings()
	registry := awsapi.NewRegistry()
	declared := map[string]bool{}
	for _, cap := range caps {
		declared[cap.Service] = true
	}
	shared := SharedBindings(declared)

	var findings []Finding
	for _, cap := range caps {
		if cap.DocOnly || cap.Status == capabilities.StatusUnsupported {
			continue
		}
		if onlyService != "" && cap.Service != onlyService {
			continue
		}
		ops := bindings[cap.Service+"/"+ModeledOperationName(cap)]
		if len(ops) == 0 {
			findings = append(findings, Finding{
				Service: cap.Service, Operation: cap.Operation,
				Status: cap.Status.String(), Verdict: VerdictUnmodeled,
			})
			continue
		}
		f := Finding{
			Service: cap.Service, Operation: cap.Operation,
			Status: cap.Status.String(), Verdict: VerdictUnreachable,
		}
		for _, op := range ops {
			p := ProbeOperation(ctx, client, base, registry, shared, op)
			f.Probes = append(f.Probes, p)
			if p.Error != "" || (p.Status == http.StatusNotImplemented && p.Unsupported) {
				continue
			}
			// A handler answered. On a binding another AWS service also
			// models, that is not yet proof this service's handler
			// answered — and a reachable probe on an exclusive binding
			// outranks a shared one.
			if p.Shared {
				if f.Verdict == VerdictUnreachable {
					f.Verdict = VerdictShared
				}
				continue
			}
			f.Verdict = VerdictReachable
		}
		findings = append(findings, f)
	}
	return findings
}

// ModeledBindings indexes the pinned manifest by Overcast service key and
// operation name. A key can hold more than one binding: Overcast's "ses"
// covers both SES v1 (Query) and SES v2 (REST), and "apigateway" covers v1
// and v2, so an operation is reachable if *any* of its modeled bindings
// reaches a handler.
func ModeledBindings() map[string][]awsapi.Operation {
	index := map[string][]awsapi.Operation{}
	awsapi.WalkOperations(func(op awsapi.Operation) bool {
		key := awsapi.ServiceKey(op.Service) + "/" + op.Name
		index[key] = append(index[key], op)
		return true
	})
	return index
}

// ModeledOperationName mirrors the two naming exceptions cmd/capgen applies
// when it checks capability rows against the model. Keeping them identical is
// what makes an "unmodeled" verdict here mean the same thing as capgen's
// UNKNOWN_MODEL_OPERATION rather than a second, subtly different rule.
func ModeledOperationName(cap capabilities.Capability) string {
	if cap.Service == "apigateway" {
		return strings.Replace(cap.Operation, "V2", "", 1)
	}
	if cap.Service == "cloudfront" && cap.Operation == "DeleteFieldLevelEncryption" {
		return "DeleteFieldLevelEncryptionConfig"
	}
	return cap.Operation
}

// ProbeOperation issues the smallest request that identifies an operation on
// the wire, exactly as an SDK would address it.
func ProbeOperation(ctx context.Context, client *http.Client, base string, registry *awsapi.Registry, shared map[string]bool, op awsapi.Operation) Probe {
	req, binding, err := BuildRequest(ctx, base, registry, op)
	if err != nil {
		return Probe{Binding: binding, Error: err.Error()}
	}
	resp, err := client.Do(req)
	if err != nil {
		return Probe{Binding: binding, Error: err.Error()}
	}
	body := drain(resp)
	return Probe{
		Binding:     binding,
		Status:      resp.StatusCode,
		Unsupported: resp.Header.Get("x-emulator-unsupported") == "true",
		Shared:      shared[BindingKey(op)],
		Body:        snippet(body),
	}
}

// BindingKey names the wire binding two operations would collide on. Only REST
// and Query bindings can collide: an X-Amz-Target value carries its own
// service shape, and a Smithy RPC v2 path names the service explicitly.
func BindingKey(op awsapi.Operation) string {
	switch op.Protocol {
	case awsapi.ProtocolRESTJSON, awsapi.ProtocolRESTXML:
		path, query := ConcretePath(op.URI)
		return op.HTTPMethod + " " + path + "?" + query
	case awsapi.ProtocolAWSQuery, awsapi.ProtocolEC2Query:
		return op.APIVersion + " " + op.Name
	case awsapi.ProtocolAWSJSON10, awsapi.ProtocolAWSJSON11,
		awsapi.ProtocolRPCV2CBOR, awsapi.ProtocolRPCV2JSON, awsapi.ProtocolUnknown:
		// An X-Amz-Target value and a Smithy RPC v2 path both name their own
		// service, so neither can collide with another service's binding.
	}
	return ""
}

// SharedBindings names every wire binding that more than one *Overcast*
// service models. AWS separates such services by endpoint hostname; a
// single-endpoint emulator cannot, so whichever service registered the chi
// route first answers for both — AppConfig's POST /applications is served by
// AppRegistry's handler. A handler answering a shared binding proves a route
// exists, not that the right service owns it, and the verdict says so.
//
// Only services Overcast declares capabilities for count. AWS models plenty of
// collisions Overcast can never hit — RDS, Neptune and DocumentDB share an API
// version and action names, and every REST service collides with S3's
// PUT /{Bucket} — and flagging those would bury the real ones.
func SharedBindings(declared map[string]bool) map[string]bool {
	owners := map[string]map[string]bool{}
	awsapi.WalkOperations(func(op awsapi.Operation) bool {
		svc := awsapi.ServiceKey(op.Service)
		key := BindingKey(op)
		if key == "" || !declared[svc] {
			return true
		}
		if owners[key] == nil {
			owners[key] = map[string]bool{}
		}
		owners[key][svc] = true
		return true
	})
	shared := map[string]bool{}
	for key, svcs := range owners {
		if len(svcs) > 1 {
			shared[key] = true
		}
	}
	return shared
}

func BuildRequest(ctx context.Context, base string, registry *awsapi.Registry, op awsapi.Operation) (*http.Request, string, error) {
	switch {
	case op.TargetPrefix != "" && (op.Protocol == awsapi.ProtocolAWSJSON10 || op.Protocol == awsapi.ProtocolAWSJSON11):
		target := op.TargetPrefix + op.Name
		binding := "X-Amz-Target: " + target
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/", strings.NewReader("{}"))
		if err != nil {
			return nil, binding, err
		}
		req.Header.Set("Content-Type", "application/x-amz-json-1.0")
		req.Header.Set("X-Amz-Target", target)
		req.Header.Set("Authorization", SigV4(op.Service))
		return req, binding, nil

	case op.Protocol == awsapi.ProtocolAWSQuery || op.Protocol == awsapi.ProtocolEC2Query:
		binding := "Action=" + op.Name + "&Version=" + op.APIVersion
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/", strings.NewReader(binding))
		if err != nil {
			return nil, binding, err
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Authorization", SigV4(op.Service))
		return req, binding, nil

	case op.Protocol == awsapi.ProtocolRESTJSON || op.Protocol == awsapi.ProtocolRESTXML:
		if op.HTTPMethod == "" || op.URI == "" {
			return nil, op.Name, fmt.Errorf("modeled with no HTTP binding")
		}
		path, query := concretePathForProbe(op)
		target := base + path
		if query != "" {
			target += "?" + query
		}
		binding := op.HTTPMethod + " " + path
		if query != "" {
			binding += "?" + query
		}
		req, err := http.NewRequestWithContext(ctx, op.HTTPMethod, target, strings.NewReader("{}"))
		if err != nil {
			return nil, binding, err
		}
		req.Header.Set("Content-Type", "application/json")
		if awsapi.ServiceKey(op.Service) == "s3" {
			// S3's own bindings must be signed as S3 and must not carry
			// X-Amz-Account-Id, or the router legitimately treats them as
			// S3 Control. The generated registry holds no S3 operation, and
			// bucket/object paths like PUT /{Bucket} collide with plenty of
			// other services' modeled templates, so asking it which service
			// owns the path would sign an S3 probe as something else.
			req.Header.Set("Authorization", SigV4("s3"))
			return req, binding, nil
		}
		claim, ok := registry.ClaimRESTQuery(op.HTTPMethod, path, query)
		scope := op.Service
		switch {
		case ok && claim.SigningName != "":
			scope = claim.SigningName
		case ok && !claim.Ambiguous:
			// Modeled with no aws.auth#sigv4 name: a bearer-token API, which
			// is the only evidence separating it from an S3 object request.
			req.Header.Set("Authorization", "Bearer probe-token")
			return req, binding, nil
		}
		req.Header.Set("Authorization", SigV4(scope))
		// X-Amz-Account-Id is what separates S3 Control from S3, which share
		// a signing name. Sending it on an S3-scoped probe would divert a
		// genuine S3 object request away from S3's routes and report a
		// fallthrough no real caller produces, so it goes on every other
		// scope and never S3's.
		if !IsS3SigningName(scope) {
			req.Header.Set("X-Amz-Account-Id", "000000000000")
		}
		return req, binding, nil
	}
	return nil, op.Name, fmt.Errorf("unprobed protocol %s", op.Protocol)
}

// ConcretePath turns a Smithy URI template into a routable path, replacing
// every greedy or plain label with a placeholder no real resource uses.
func ConcretePath(uri string) (path, query string) {
	if i := strings.IndexByte(uri, '?'); i >= 0 {
		uri, query = uri[:i], uri[i+1:]
	}
	var b strings.Builder
	for _, segment := range strings.Split(uri, "/") {
		if segment == "" {
			continue
		}
		b.WriteByte('/')
		if strings.HasPrefix(segment, "{") {
			b.WriteString("probevalue")
		} else {
			b.WriteString(segment)
		}
	}
	if b.Len() == 0 {
		return "/", query
	}
	return b.String(), query
}

// concretePathForProbe is ConcretePath, except a resourceArn-shaped label is
// filled with a syntactically valid ARN scoped to the operation's own service
// rather than the bare "probevalue" placeholder every other label gets.
//
// Why this label needs different treatment: five services (AppConfig, AWS
// AppRegistry, EKS, Pipes, Scheduler — plus AppSync and MSK on /v1/tags) share
// /tags/{ResourceArn} and /v1/tags/{resourceArn}, and the main router
// dispatches those requests by reading the ARN's own service segment
// (tagsDispatch, internal/router/router.go), not by path or SigV4 scope —
// see docs/plans/manifest-enforcement.md's "shared bindings" discussion of
// why an ARN is the only self-describing part of that request. "probevalue"
// is not a well-formed ARN, so protocol.ServiceFromARN cannot read a service
// out of it, and every one of those operations' probes falls through to the
// generic restFallback 501 — a false "unreachable" verdict for operations
// that are, in fact, served, once a caller addresses them the way a real
// resourceArn does. docs/plans/route-reachability-audit.md's own "Shared
// bindings that are not faults" section names this exact path group as
// checked and correct; this is that same check made automatic instead of
// hand-verified once.
func concretePathForProbe(op awsapi.Operation) (path, query string) {
	uri := op.URI
	if i := strings.IndexByte(uri, '?'); i >= 0 {
		uri, query = uri[:i], uri[i+1:]
	}
	var b strings.Builder
	for _, segment := range strings.Split(uri, "/") {
		if segment == "" {
			continue
		}
		b.WriteByte('/')
		if strings.HasPrefix(segment, "{") {
			label := strings.TrimSuffix(strings.TrimPrefix(segment, "{"), "}")
			label = strings.TrimSuffix(label, "+") // greedy label, e.g. "{Key+}"
			if isARNLabel(label) {
				b.WriteString(probeARN(op))
			} else {
				b.WriteString("probevalue")
			}
		} else {
			b.WriteString(segment)
		}
	}
	if b.Len() == 0 {
		return "/", query
	}
	return b.String(), query
}

// isARNLabel reports whether a Smithy URI label names a resource ARN member —
// the shape ARN-keyed dispatch reads to pick a tag router. AWS spells the
// member both ResourceArn and resourceArn depending on the service.
func isARNLabel(label string) bool {
	return strings.EqualFold(label, "ResourceArn") || strings.EqualFold(label, "Arn")
}

// probeARN builds a syntactically valid ARN whose service segment matches
// what tagsDispatch reads for op's own service, so a probe of a
// resourceArn-keyed operation actually reaches that service's tag router
// instead of universally missing every one.
//
// op.Service is already the manifest's own wire service identifier for every
// case that matters here except one: AppRegistry's manifest entry reads
// "service-catalog-appregistry" (derived from its SDKID), but its real ARNs —
// and the router's tagRouters key — say "servicecatalog", because its SDK
// shares API Gateway's tagging endpoint (internal/router/router.go's
// "/tags service dispatch" section). Everything else passes through
// unchanged: AppConfig, EKS, Pipes and Scheduler's manifest Service fields are
// already their own ARN segment, and so is AppSync's and MSK's ("kafka") on
// /v1/tags.
func probeARN(op awsapi.Operation) string {
	segment := op.Service
	if segment == "service-catalog-appregistry" {
		segment = "servicecatalog"
	}
	return "arn:aws:" + segment + ":us-east-1:000000000000:probe/route-reachability"
}

// IsS3SigningName reports whether a credential scope belongs to a service
// that speaks the S3 object API itself, and whose probes must therefore reach
// S3's routes. It mirrors the router's own list in internal/router/router.go.
func IsS3SigningName(signingName string) bool {
	switch strings.ToLower(signingName) {
	case "s3", "s3-object-lambda", "s3express":
		return true
	}
	return false
}

// SigV4 builds a syntactically valid Authorization header with a dummy
// signature. Routing reads the credential scope, never the signature.
func SigV4(service string) string {
	return "AWS4-HMAC-SHA256 Credential=probe/20260810/us-east-1/" + service +
		"/aws4_request, SignedHeaders=host, Signature=probe"
}

// drain reads and closes the body so the transport reuses the connection.
// Closing with an unread body tears the socket down, and a sweep makes
// thousands of requests — enough TIME_WAIT sockets to exhaust the Windows
// ephemeral port range and fail every later dial.
func drain(resp *http.Response) []byte {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	return body
}

func snippet(body []byte) string {
	const max = 200
	s := strings.TrimSpace(string(bytes.ReplaceAll(body, []byte("\n"), []byte(" "))))
	if len(s) > max {
		return s[:max] + "…"
	}
	return s
}

// DefaultProbeTimeout is the default per-request timeout the CLI uses;
// exported so the script and any other caller share one number rather than
// two copies drifting apart.
const DefaultProbeTimeout = 10 * time.Second
