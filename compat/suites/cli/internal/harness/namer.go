package harness

import "fmt"

// Namer derives consistent, collision-free resource names scoped to a test
// group and run. Declare one Namer per group with a distinct short tag so that
// groups running in parallel within the same run never share resource names.
//
// Usage — declare a package-level var per group:
//
//	var sqsQueuesNamer = harness.NewNamer("sqs-q")
//
//	func setup(ctx context.Context, t *harness.TestContext) error {
//	    name := sqsQueuesNamer.Name(t) // → "oc-a3f9b12c-sqs-q"
//	    ...
//	}
//
// The tag should be short, lowercase, and hyphenated (e.g. "sqs-q", "ddb-tbl").
type Namer struct {
	tag string
}

// NewNamer creates a Namer for the given short group tag.
func NewNamer(tag string) Namer {
	return Namer{tag: tag}
}

// Name returns a unique resource name: "{runID}-{tag}".
func (n Namer) Name(t *TestContext) string {
	return fmt.Sprintf("%s-%s", t.RunID, n.tag)
}

// Suffixed returns "{runID}-{tag}{suffix}".
// Use for names that require a mandatory suffix, e.g. ".fifo" for SQS FIFO queues.
func (n Namer) Suffixed(t *TestContext, suffix string) string {
	return fmt.Sprintf("%s-%s%s", t.RunID, n.tag, suffix)
}
