package ec2

// describe.go — one body per Describe*, reached two ways.
//
// EC2 is reached over the Query protocol through two dispatchers: the legacy
// `http.HandlerFunc` map (`Handler.ops`) and the typed operation registry
// (`Handler.typedOp`). Until #754 the registry was never dispatched to, and the
// reason it could not be was that every operation had been written twice —
// once reading `r.FormValue` and once reading a decoded request struct — and
// the second copy had fallen behind. Filters were the visible half of that:
// #1032 taught the legacy path to refuse a filter name it does not implement
// and to read `*`/`?` as AWS reads them, and none of it reached the typed
// twins, which ignored filters altogether and answered with the whole region.
//
// The fix is structural rather than a second catch-up. A Describe* now has one
// body, taking a describeQuery: whatever the caller selected on, already
// decoded. The legacy handler builds one off the form, the typed operation
// builds one off its request struct, and both call the same function. The two
// paths cannot answer differently because there is only one answer — the
// property #754 needs, held by construction instead of by a test somebody has
// to remember to extend.
//
// The seam is deliberately narrow: describeQuery carries the `<Resource>Id.N`
// selection and the filters, which is all any EC2 describe selects on.
// Operations with a request parameter beyond those (DescribeVpcAttribute's
// Attribute, DescribeInstanceTypes' InstanceType.N) take it as an ordinary
// argument alongside.

import (
	"net/http"

	"github.com/Neaox/overcast/internal/protocol"
)

// describeQuery is everything a Describe* selects on, decoded: the IDs the
// caller named and the filters it sent.
type describeQuery struct {
	ids     idSelection
	filters filterSeq
}

// requestQuery reads a describe's selection off the form.
func requestQuery(r *http.Request, idParam string) describeQuery {
	return describeQuery{ids: requestedIDs(r, idParam), filters: eachFilter(r)}
}

// unselectedQuery reads the filters of a describe with no `<Resource>Id.N`
// parameter to select on — DescribeRegions, DescribeTags and the like.
func unselectedQuery(r *http.Request) describeQuery {
	return describeQuery{ids: idSelection{}, filters: eachFilter(r)}
}

// typedQuery is requestQuery's twin for an operation reached through the typed
// registry, where the codec has already decoded both halves.
func typedQuery(ids []string, filters []ec2Filter) describeQuery {
	return describeQuery{ids: selectedIDs(ids), filters: eachDecodedFilter(filters)}
}

// writeDescribe renders a shared describe body's result on the legacy path.
//
// Errors go out in EC2's own `<Errors><Error>` envelope rather than the generic
// Query one, which is the same distinction ec2ErrorCodec makes for the typed
// path — see Service.DispatchQuery.
func writeDescribe(w http.ResponseWriter, r *http.Request, resp any, aerr *protocol.AWSError) {
	if aerr != nil {
		protocol.WriteEC2QueryXMLError(w, r, aerr)
		return
	}
	protocol.WriteQueryXML(w, r, http.StatusOK, resp)
}
