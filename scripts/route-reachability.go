//go:build ignore

// Script: route-reachability
// Probes a running Overcast instance at the AWS-modeled wire binding of every
// operation Overcast *declares* it implements, and reports the ones no AWS SDK
// can reach.
//
// Why this exists: `make aws-models-check` validates that capability rows name
// real AWS operations and that no modeled operation falls through to S3. Both
// gates are satisfied by an operation that answers 501 — so an implementation
// mounted on an Overcast-invented path or an invented X-Amz-Target prefix
// passes CI while being unreachable from any SDK (see #793 EventBridge
// Scheduler, #815 AWS Backup). This script closes that gap by asking the only
// question those gates do not: does the modeled binding reach a handler?
//
// Expectation set: internal/capabilities (the -tags dev snapshot). An operation
// Overcast declares Supported/Partial/Inert/WIP is expected to be reachable at
// its modeled binding. StatusUnsupported and DocOnly rows are excluded — a 501
// is the correct answer for those.
//
// Verdicts:
//
//	reachable    — a handler answered (2xx, or an AWS-shaped 4xx such as a
//	               validation or not-found error, which still proves the route)
//	shared-path  — answered only on a binding another Overcast service also
//	               models, so a route matched but the owner is unproven
//	unreachable  — 501 carrying x-emulator-unsupported on every modeled binding
//	unmodeled    — the capability names no operation in the pinned manifest
//	               (capgen's --check-model exemptions; not a routing fault)
//
// A "reachable" verdict proves a route matched, NOT that the right service
// answered. Where two AWS services share a path (AppConfig and AppRegistry both
// model POST /applications) only -show-body distinguishes them, which is what
// the shared-path verdict exists to prompt.
//
// Usage:
//
//	scripts/run-test-instance.sh --no-logs --name reach     # prints the API URL
//	go run -tags dev ./scripts/route-reachability.go -endpoint http://127.0.0.1:4570
//	go run -tags dev ./scripts/route-reachability.go -endpoint http://127.0.0.1:4570 -service appconfig -show-body
//	go run -tags dev ./scripts/route-reachability.go -endpoint http://127.0.0.1:4570 -json report.json
//
// The -tags dev is required: internal/capabilities is empty without it.
//
// Probes are unauthenticated beyond a syntactically valid SigV4 Authorization
// header carrying the modeled signing name — ownership and routing are decided
// from the credential scope, never from the signature, so no real signing is
// needed. An instance running with signature validation on would need genuinely
// signed requests.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/Neaox/overcast/internal/awsapi"
	"github.com/Neaox/overcast/internal/capabilities"
)

// probe is one request issued at one modeled binding.
type probe struct {
	Binding     string `json:"binding"`     // "POST /applications" or "X-Amz-Target: AppConfig.CreateApplication"
	Status      int    `json:"status"`      // HTTP status the instance answered with
	Unsupported bool   `json:"unsupported"` // x-emulator-unsupported marker
	// Shared marks a binding more than one AWS service models, so a handler
	// answering here is not proof that *this* service's handler answered.
	Shared bool   `json:"shared,omitempty"`
	Body   string `json:"body,omitempty"`
	Error  string `json:"error,omitempty"`
}

// finding is the verdict for one declared capability.
type finding struct {
	Service   string  `json:"service"`
	Operation string  `json:"operation"`
	Status    string  `json:"declared_status"`
	Verdict   string  `json:"verdict"`
	Probes    []probe `json:"probes,omitempty"`
}

const (
	verdictReachable   = "reachable"
	verdictShared      = "shared-path"
	verdictUnreachable = "unreachable"
	verdictUnmodeled   = "unmodeled"
)

func main() {
	endpoint := flag.String("endpoint", "", "base URL of a running Overcast instance, e.g. http://127.0.0.1:4570")
	only := flag.String("service", "", "probe only this Overcast service key")
	jsonOut := flag.String("json", "", "also write the full report as JSON to this path")
	showBody := flag.Bool("show-body", false, "print the first bytes of each response body")
	timeout := flag.Duration("timeout", 10*time.Second, "per-request timeout")
	flag.Parse()

	if *endpoint == "" {
		fatalf("set -endpoint to a running instance, e.g. -endpoint http://127.0.0.1:4570 " +
			"(start one with scripts/run-test-instance.sh --no-logs)")
	}
	base := strings.TrimSuffix(*endpoint, "/")

	if len(capabilities.AllCapabilities) == 0 {
		fatalf("no capabilities compiled in — rerun with -tags dev")
	}

	client := &http.Client{
		Timeout: *timeout,
		// One host, thousands of requests. Without a generous idle pool every
		// probe opens a fresh socket and Windows runs out of ephemeral ports
		// long before the sweep finishes.
		Transport: &http.Transport{MaxIdleConns: 64, MaxIdleConnsPerHost: 64, IdleConnTimeout: 30 * time.Second},
	}
	ctx := context.Background()
	if err := waitReady(ctx, client, base); err != nil {
		fatalf("%v", err)
	}

	bindings := modeledBindings()
	registry := awsapi.NewRegistry()
	declared := map[string]bool{}
	for _, cap := range capabilities.AllCapabilities {
		declared[cap.Service] = true
	}
	shared := sharedBindings(declared)

	var findings []finding
	for _, cap := range capabilities.AllCapabilities {
		if cap.DocOnly || cap.Status == capabilities.StatusUnsupported {
			continue
		}
		if *only != "" && cap.Service != *only {
			continue
		}
		ops := bindings[cap.Service+"/"+modeledOperationName(cap)]
		if len(ops) == 0 {
			findings = append(findings, finding{
				Service: cap.Service, Operation: cap.Operation,
				Status: cap.Status.String(), Verdict: verdictUnmodeled,
			})
			continue
		}
		f := finding{
			Service: cap.Service, Operation: cap.Operation,
			Status: cap.Status.String(), Verdict: verdictUnreachable,
		}
		for _, op := range ops {
			p := probeOperation(ctx, client, base, registry, shared, op)
			if !*showBody {
				p.Body = ""
			}
			f.Probes = append(f.Probes, p)
			if p.Error != "" || (p.Status == http.StatusNotImplemented && p.Unsupported) {
				continue
			}
			// A handler answered. On a binding another AWS service also models,
			// that is not yet proof this service's handler answered — and a
			// reachable probe on an exclusive binding outranks a shared one.
			if p.Shared {
				if f.Verdict == verdictUnreachable {
					f.Verdict = verdictShared
				}
				continue
			}
			f.Verdict = verdictReachable
		}
		findings = append(findings, f)
	}

	report(findings, *showBody)
	if *jsonOut != "" {
		blob, err := json.MarshalIndent(findings, "", "  ")
		if err != nil {
			fatalf("marshal report: %v", err)
		}
		if err := os.WriteFile(*jsonOut, append(blob, '\n'), 0o644); err != nil {
			fatalf("write %s: %v", *jsonOut, err)
		}
		fmt.Printf("\nwrote %s\n", *jsonOut)
	}
}

// waitReady blocks until the instance answers, so a probe sweep started right
// after `docker run` reports routing faults rather than connection refusals.
func waitReady(ctx context.Context, client *http.Client, base string) error {
	deadline := time.Now().Add(60 * time.Second)
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/_overcast/health", nil)
		if err != nil {
			return err
		}
		resp, err := client.Do(req)
		if err == nil {
			drain(resp)
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("no instance answered %s/_overcast/health within 60s: %w", base, err)
		}
		time.Sleep(500 * time.Millisecond)
	}
}

// modeledBindings indexes the pinned manifest by Overcast service key and
// operation name. A key can hold more than one binding: Overcast's "ses" covers
// both SES v1 (Query) and SES v2 (REST), and "apigateway" covers v1 and v2, so
// an operation is reachable if *any* of its modeled bindings reaches a handler.
func modeledBindings() map[string][]awsapi.Operation {
	index := map[string][]awsapi.Operation{}
	awsapi.WalkOperations(func(op awsapi.Operation) bool {
		key := awsapi.ServiceKey(op.Service) + "/" + op.Name
		index[key] = append(index[key], op)
		return true
	})
	return index
}

// modeledOperationName mirrors the two naming exceptions cmd/capgen applies
// when it checks capability rows against the model. Keeping them identical is
// what makes an "unmodeled" verdict here mean the same thing as capgen's
// UNKNOWN_MODEL_OPERATION rather than a second, subtly different rule.
func modeledOperationName(cap capabilities.Capability) string {
	if cap.Service == "apigateway" {
		return strings.Replace(cap.Operation, "V2", "", 1)
	}
	if cap.Service == "cloudfront" && cap.Operation == "DeleteFieldLevelEncryption" {
		return "DeleteFieldLevelEncryptionConfig"
	}
	return cap.Operation
}

// probeOperation issues the smallest request that identifies an operation on
// the wire, exactly as an SDK would address it.
func probeOperation(ctx context.Context, client *http.Client, base string, registry *awsapi.Registry, shared map[string]bool, op awsapi.Operation) probe {
	req, binding, err := buildRequest(ctx, base, registry, op)
	if err != nil {
		return probe{Binding: binding, Error: err.Error()}
	}
	resp, err := client.Do(req)
	if err != nil {
		return probe{Binding: binding, Error: err.Error()}
	}
	body := drain(resp)
	return probe{
		Binding:     binding,
		Status:      resp.StatusCode,
		Unsupported: resp.Header.Get("x-emulator-unsupported") == "true",
		Shared:      shared[bindingKey(op)],
		Body:        snippet(body),
	}
}

// bindingKey names the wire binding two operations would collide on. Only REST
// and Query bindings can collide: an X-Amz-Target value carries its own service
// shape, and a Smithy RPC v2 path names the service explicitly.
func bindingKey(op awsapi.Operation) string {
	switch op.Protocol {
	case awsapi.ProtocolRESTJSON, awsapi.ProtocolRESTXML:
		path, query := concretePath(op.URI)
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

// sharedBindings names every wire binding that more than one *Overcast* service
// models. AWS separates such services by endpoint hostname; a single-endpoint
// emulator cannot, so whichever service registered the chi route first answers
// for both — AppConfig's POST /applications is served by AppRegistry's handler.
// A handler answering a shared binding proves a route exists, not that the right
// service owns it, and the verdict says so.
//
// Only services Overcast declares capabilities for count. AWS models plenty of
// collisions Overcast can never hit — RDS, Neptune and DocumentDB share an API
// version and action names, and every REST service collides with S3's
// PUT /{Bucket} — and flagging those would bury the real ones.
func sharedBindings(declared map[string]bool) map[string]bool {
	owners := map[string]map[string]bool{}
	awsapi.WalkOperations(func(op awsapi.Operation) bool {
		svc := awsapi.ServiceKey(op.Service)
		key := bindingKey(op)
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

func buildRequest(ctx context.Context, base string, registry *awsapi.Registry, op awsapi.Operation) (*http.Request, string, error) {
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
		req.Header.Set("Authorization", sigV4(op.Service))
		return req, binding, nil

	case op.Protocol == awsapi.ProtocolAWSQuery || op.Protocol == awsapi.ProtocolEC2Query:
		binding := "Action=" + op.Name + "&Version=" + op.APIVersion
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/", strings.NewReader(binding))
		if err != nil {
			return nil, binding, err
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Authorization", sigV4(op.Service))
		return req, binding, nil

	case op.Protocol == awsapi.ProtocolRESTJSON || op.Protocol == awsapi.ProtocolRESTXML:
		if op.HTTPMethod == "" || op.URI == "" {
			return nil, op.Name, fmt.Errorf("modeled with no HTTP binding")
		}
		path, query := concretePath(op.URI)
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
			req.Header.Set("Authorization", sigV4("s3"))
			return req, binding, nil
		}
		claim, ok := registry.ClaimRESTQuery(op.HTTPMethod, path, query)
		scope := op.Service
		switch {
		case ok && claim.SigningName != "":
			scope = claim.SigningName
		case ok && !claim.Ambiguous:
			// Modeled with no aws.auth#sigv4 name: a bearer-token API, which is
			// the only evidence separating it from an S3 object request.
			req.Header.Set("Authorization", "Bearer probe-token")
			return req, binding, nil
		}
		req.Header.Set("Authorization", sigV4(scope))
		// X-Amz-Account-Id is what separates S3 Control from S3, which share a
		// signing name. Sending it on an S3-scoped probe would divert a genuine
		// S3 object request away from S3's routes and report a fallthrough no
		// real caller produces, so it goes on every other scope and never S3's.
		if !isS3SigningName(scope) {
			req.Header.Set("X-Amz-Account-Id", "000000000000")
		}
		return req, binding, nil
	}
	return nil, op.Name, fmt.Errorf("unprobed protocol %s", op.Protocol)
}

// concretePath turns a Smithy URI template into a routable path, replacing
// every greedy or plain label with a placeholder no real resource uses.
func concretePath(uri string) (path, query string) {
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

// isS3SigningName reports whether a credential scope belongs to a service that
// speaks the S3 object API itself, and whose probes must therefore reach S3's
// routes. It mirrors the router's own list in internal/router/router.go.
func isS3SigningName(signingName string) bool {
	switch strings.ToLower(signingName) {
	case "s3", "s3-object-lambda", "s3express":
		return true
	}
	return false
}

// sigV4 builds a syntactically valid Authorization header with a dummy
// signature. Routing reads the credential scope, never the signature.
func sigV4(service string) string {
	return "AWS4-HMAC-SHA256 Credential=probe/20260810/us-east-1/" + service +
		"/aws4_request, SignedHeaders=host, Signature=probe"
}

// drain reads and closes the body so the transport reuses the connection.
// Closing with an unread body tears the socket down, and this sweep makes
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

// report prints a per-service summary, then the unreachable operations and the
// shared-binding ones — together, the list an auditor acts on.
func report(findings []finding, showBody bool) {
	type tally struct{ reachable, shared, unreachable, unmodeled int }
	byService := map[string]*tally{}
	for _, f := range findings {
		t := byService[f.Service]
		if t == nil {
			t = &tally{}
			byService[f.Service] = t
		}
		switch f.Verdict {
		case verdictReachable:
			t.reachable++
		case verdictShared:
			t.shared++
		case verdictUnreachable:
			t.unreachable++
		default:
			t.unmodeled++
		}
	}

	services := make([]string, 0, len(byService))
	for svc := range byService {
		services = append(services, svc)
	}
	sort.Strings(services)

	fmt.Printf("%-18s %10s %7s %12s %10s  %s\n", "SERVICE", "REACHABLE", "SHARED", "UNREACHABLE", "UNMODELED", "VERDICT")
	totalUnreachable, totalShared := 0, 0
	for _, svc := range services {
		t := byService[svc]
		totalUnreachable += t.unreachable
		totalShared += t.shared
		verdict := "ok"
		switch {
		case t.reachable == 0 && t.unreachable+t.shared > 0:
			verdict = "WHOLE SERVICE UNREACHABLE OR MISROUTED"
		case t.unreachable > 0:
			verdict = "partial gap"
		case t.shared > 0:
			verdict = "shared bindings — verify owner"
		}
		fmt.Printf("%-18s %10d %7d %12d %10d  %s\n", svc, t.reachable, t.shared, t.unreachable, t.unmodeled, verdict)
	}

	fmt.Printf("\n%d declared operations are unreachable at their modeled binding.\n", totalUnreachable)
	fmt.Printf("%d more answer only on a binding another AWS service also models — read the body to see who answered.\n", totalShared)

	printDetail := func(heading, want string) {
		var shown bool
		for _, f := range findings {
			if f.Verdict != want {
				continue
			}
			if !shown {
				fmt.Println("\n" + heading)
				shown = true
			}
			for _, p := range f.Probes {
				line := fmt.Sprintf("  %s/%s [%s]  %s -> %d", f.Service, f.Operation, f.Status, p.Binding, p.Status)
				if p.Error != "" {
					line += "  ERROR " + p.Error
				}
				fmt.Println(line)
				if showBody && p.Body != "" {
					fmt.Println("      " + p.Body)
				}
			}
		}
	}
	printDetail("UNREACHABLE DETAIL", verdictUnreachable)
	printDetail("SHARED-BINDING DETAIL (rerun with -show-body to see which service answered)", verdictShared)
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "route-reachability: "+format+"\n", args...)
	os.Exit(1)
}
