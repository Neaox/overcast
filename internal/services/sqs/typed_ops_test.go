package sqs

import "testing"

func TestTypedOps_queueOperationsRegistered(t *testing.T) {
	// Given: an SQS handler with its typed operation registry.
	h := &Handler{}

	// When: the typed operations are listed.
	ops := h.typedOps()

	// Then: every queue lifecycle operation migrated in this step is present.
	for _, name := range []string{
		"CreateQueue",
		"GetQueueUrl",
		"GetQueueAttributes",
		"SetQueueAttributes",
		"DeleteQueue",
		"ListQueues",
		"PurgeQueue",
		"SendMessage",
		"ReceiveMessage",
		"DeleteMessage",
		"SendMessageBatch",
		"DeleteMessageBatch",
		"ChangeMessageVisibility",
		"ChangeMessageVisibilityBatch",
		"ListDeadLetterSourceQueues",
		"AddPermission",
		"RemovePermission",
		"ListQueueTags",
		"TagQueue",
		"UntagQueue",
	} {
		if _, ok := ops[name]; !ok {
			t.Fatalf("expected typed operation %q to be registered", name)
		}
	}
}
