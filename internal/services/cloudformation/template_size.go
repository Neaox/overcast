package cloudformation

import (
	"fmt"
)

// maxInlineTemplateBytes is the largest TemplateBody CloudFormation accepts on
// the request itself. AWS caps the inline parameter at 51,200 bytes and directs
// anything larger to S3 via TemplateURL, which has its own, higher limit.
//
// The cap belongs to the parameter, not to CreateStack: every operation taking
// a TemplateBody applies it.
const maxInlineTemplateBytes = 51_200

// checkInlineTemplateSize validates a TemplateBody supplied inline on the
// request, returning the message AWS gives when it is too large. Every caller
// reports it as a ValidationError with HTTP 400, which is the shape real
// CloudFormation uses, so the error carries the message alone.
//
// It is deliberately not applied to a template fetched from TemplateURL. That
// path is the documented escape hatch from this limit, and capping it here
// would leave a caller who did exactly what AWS tells them to do with nowhere
// left to go.
//
// Real CloudFormation echoes the offending value into this message. That is
// omitted: the value is by definition at least 51,200 bytes, so returning it
// would make the error body an order of magnitude larger than the diagnosis it
// carries. The parts a client acts on — the code, the status, the member name
// and the limit — are unchanged, and the value-less form of this message is
// already used elsewhere in the codebase (internal/services/ecs).
func checkInlineTemplateSize(templateBody string) error {
	if len(templateBody) <= maxInlineTemplateBytes {
		return nil
	}
	return fmt.Errorf(
		"1 validation error detected: Value at 'templateBody' failed to satisfy constraint: "+
			"Member must have length less than or equal to %d", maxInlineTemplateBytes)
}

// maxResolvedTemplateBytes is the largest template CloudFormation accepts from
// a TemplateURL — the limit TemplateBody's callers are sent to S3 to get under.
//
// It is a decimal megabyte, not a mebibyte. The quotas table says only "1 MB",
// but AWS's own error names the number: "Template may not exceed 1000000 bytes
// in size." Reading it as 1 MiB would accept 48,576 bytes' worth of templates
// that AWS refuses, which is the divergence direction that costs a user a
// deployment rather than a local error.
const maxResolvedTemplateBytes = 1_000_000

// checkResolvedTemplateSize validates a template after it has been fetched from
// a TemplateURL. Without it the inline cap would not close the gap it was added
// for — it would relocate it, because the message that cap returns tells the
// caller to use TemplateURL, and a template of any size at all deployed there.
//
// The message is AWS's, verbatim.
func checkResolvedTemplateSize(templateBody string) error {
	if len(templateBody) <= maxResolvedTemplateBytes {
		return nil
	}
	return fmt.Errorf("Template may not exceed %d bytes in size.", maxResolvedTemplateBytes) //nolint:staticcheck // AWS's wording, capitalised and punctuated as it sends it.
}
