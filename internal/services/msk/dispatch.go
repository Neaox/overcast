package msk

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/overcast-sh/overcast/internal/awsapi"
	"github.com/overcast-sh/overcast/internal/protocol"
)

// operationRegistry is the immutable generated AWS operation registry, the same
// value serviceutil and the router hold. It carries no state, so one
// package-level instance serves every request and a lookup allocates nothing.
var operationRegistry = awsapi.NewRegistry()

// modeledOperation names the kafka operation a request to one of MSK's
// ARN-in-path subtrees addresses, and returns the ARN it addresses it on.
//
// The subtrees are registered as chi wildcards rather than as the modeled
// `{ClusterArn}` label because the ARN reaches Overcast in two spellings. An
// AWS client percent-encodes it whole into one segment, since a Smithy
// non-greedy httpLabel binds a single segment:
//
//	GET /v1/clusters/arn%3Aaws%3Akafka%3A…%2Fprobe%2F1111…-5/nodes
//
// while CloudFormation's provisioner and Overcast's own callers send it with
// its slashes intact. Unescaping first makes the two identical, so everything
// below reasons about one shape.
//
// Where the ARN ends and a sub-resource begins is then the only question, and
// it cannot be answered by counting segments: a cluster ARN's resource part is
// "cluster/{name}/{uuid}", and a cluster may perfectly well be named "nodes".
// So the pinned model answers it instead. Each candidate boundary is offered
// back to the generated trie in the shape the model binds — the ARN re-escaped
// into one segment, the remainder as literal segments — and the shortest ARN
// that names a kafka operation wins. Shortest rather than longest is the whole
// point: "…/cluster/probe/uuid/nodes" binds ListNodes at the three-segment ARN
// and DescribeCluster at the four-segment one, and taking the longer is exactly
// how ListNodes came to answer "Cluster …/nodes not found" for a cluster nobody
// asked about. A bare ARN has no shorter boundary that binds anything, so it
// still resolves to the item operation.
//
// Returning the operation rather than a boolean is what lets the dispatchers
// answer a protocol-correct 501 for the sub-resources Overcast does not
// emulate. It also means the set of sub-resources comes from the model rather
// than from a hand-written list of suffixes, which is the property
// serviceutil.NotImplementedTarget exists for on the X-Amz-Target side.
//
// operation is "" when the tail names no kafka binding at all, which the
// callers treat as a malformed ARN.
func modeledOperation(r *http.Request, collection string) (operation, arn string) {
	tail, err := url.PathUnescape(chi.URLParam(r, "*"))
	if err != nil {
		return "", ""
	}
	for cut := 0; ; {
		slash := strings.IndexByte(tail[cut:], '/')
		candidate, subResource := tail, ""
		if slash >= 0 {
			candidate, subResource = tail[:cut+slash], tail[cut+slash+1:]
		}
		path := collection + "/" + url.PathEscape(candidate)
		if subResource != "" {
			path += "/" + subResource
		}
		if op := operationRegistry.RESTOperation(serviceName, r.Method, path, r.URL.RawQuery); op != "" {
			return op, candidate
		}
		if slash < 0 {
			return "", ""
		}
		cut += slash + 1
	}
}

// notEmulated answers an operation the pinned model binds to this method and
// URI and MSK does not implement. The envelope is restJson1's, which is what
// serviceutil.WriteNotImplemented picks for every kafka binding — MSK speaks
// restJson1 and nothing else — so this is the same answer the router's REST
// fallback gives for the MSK paths no route claims.
//
// A tail that names no binding is a malformed ARN rather than an unimplemented
// operation, and gets MSK's 400.
func (h *Handler) notEmulated(w http.ResponseWriter, r *http.Request, operation, member string) {
	if operation == "" {
		protocol.WriteJSONError(w, r, errBadRequest("invalid %s", member))
		return
	}
	protocol.NotImplementedJSON(w, r)
}

// ── Cluster subtree ───────────────────────────────────────────────────────────

// clusterGetDispatch serves GET /v1/clusters/*. Of the fourteen kafka
// operations bound to a GET beneath the cluster ARN, MSK emulates two.
func (h *Handler) clusterGetDispatch(w http.ResponseWriter, r *http.Request) {
	operation, clusterArn := modeledOperation(r, clustersPath)
	switch operation {
	case "DescribeCluster":
		h.describeCluster(w, r, clusterArn)
	case "GetBootstrapBrokers":
		h.getBootstrapBrokers(w, r, clusterArn)
	default:
		h.notEmulated(w, r, operation, "clusterArn")
	}
}

// clusterDeleteDispatch serves DELETE /v1/clusters/*. DeleteClusterPolicy and
// DeleteTopic share the subtree with DeleteCluster and are not emulated.
func (h *Handler) clusterDeleteDispatch(w http.ResponseWriter, r *http.Request) {
	operation, clusterArn := modeledOperation(r, clustersPath)
	switch operation {
	case "DeleteCluster":
		h.deleteCluster(w, r, clusterArn)
	default:
		h.notEmulated(w, r, operation, "clusterArn")
	}
}

// clusterPutDispatch serves PUT /v1/clusters/*. Nine kafka operations are bound
// to a PUT beneath the cluster ARN; MSK emulates UpdateClusterConfiguration.
func (h *Handler) clusterPutDispatch(w http.ResponseWriter, r *http.Request) {
	operation, clusterArn := modeledOperation(r, clustersPath)
	switch operation {
	case "UpdateClusterConfiguration":
		h.updateClusterConfiguration(w, r, clusterArn)
	default:
		h.notEmulated(w, r, operation, "clusterArn")
	}
}

// clusterV2GetDispatch serves GET /api/v2/clusters/* — everything beneath the
// v2 cluster ARN that the modeled `{clusterArn}` label route beside it does not
// match. ListClusterOperationsV2 is the only other GET the model binds there
// and MSK does not emulate it.
//
// The route exists so that answer is MSK's own, whether or not the caller
// signed. claimModeledPath makes the router's REST fallback recognise the
// binding, but the fallback is only reached by traffic that positively
// addresses a non-S3 service — unsigned traffic is S3's by design (see
// addressesNonS3), and the AWS CLI run against a local endpoint with
// --no-sign-request is exactly that. Leaving the 501 to the fallback would have
// left an unsigned caller still reading an <Error><Code>NoSuchKey</Code>…</Error>
// for an MSK call, which is the half of #963 that a caller can mistake for
// success. #966, #970 and #972 each registered the route for the same reason.
//
// The label route is deliberately kept rather than folded in here: it is what
// proves DescribeClusterV2 is served at the binding AWS models, and this
// wildcard is only reached when the label does not match — chi tries a
// parameter node before a catch-all.
func (h *Handler) clusterV2GetDispatch(w http.ResponseWriter, r *http.Request) {
	operation, clusterArn := modeledOperation(r, clustersV2Path)
	switch operation {
	case "DescribeClusterV2":
		h.describeClusterV2Arn(w, r, clusterArn)
	default:
		h.notEmulated(w, r, operation, "clusterArn")
	}
}

// ── Configuration subtree ─────────────────────────────────────────────────────

// configurationGetDispatch serves GET /v1/configurations/*. ListConfigurationRevisions
// and DescribeConfigurationRevision hang off the configuration ARN and are not
// emulated.
func (h *Handler) configurationGetDispatch(w http.ResponseWriter, r *http.Request) {
	operation, configArn := modeledOperation(r, configurationsPath)
	switch operation {
	case "DescribeConfiguration":
		h.describeConfiguration(w, r, configArn)
	default:
		h.notEmulated(w, r, operation, "configArn")
	}
}

// configurationDeleteDispatch serves DELETE /v1/configurations/*, where
// DeleteConfiguration is the only binding.
func (h *Handler) configurationDeleteDispatch(w http.ResponseWriter, r *http.Request) {
	operation, configArn := modeledOperation(r, configurationsPath)
	switch operation {
	case "DeleteConfiguration":
		h.deleteConfiguration(w, r, configArn)
	default:
		h.notEmulated(w, r, operation, "configArn")
	}
}
