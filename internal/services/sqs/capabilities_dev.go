//go:build dev

package sqs

import "github.com/Neaox/overcast/internal/capabilities"

func init() {
	capabilities.Default.Register(
		// Queue management
		capabilities.Capability{Service: "sqs", Operation: "CreateQueue", Category: "Queue management",
			Status: capabilities.StatusSupported, Notes: "Idempotent; FIFO queues supported (.fifo suffix)"},
		capabilities.Capability{Service: "sqs", Operation: "DeleteQueue", Category: "Queue management",
			Status: capabilities.StatusSupported},
		capabilities.Capability{Service: "sqs", Operation: "GetQueueUrl", Category: "Queue management",
			Status: capabilities.StatusSupported},
		capabilities.Capability{Service: "sqs", Operation: "ListQueues", Category: "Queue management",
			Status: capabilities.StatusSupported, Notes: "Optional QueueNamePrefix filter"},
		capabilities.Capability{Service: "sqs", Operation: "GetQueueAttributes", Category: "Queue management",
			Status: capabilities.StatusSupported, Notes: "All standard attributes; All wildcard supported"},
		capabilities.Capability{Service: "sqs", Operation: "SetQueueAttributes", Category: "Queue management",
			Status: capabilities.StatusSupported},
		capabilities.Capability{Service: "sqs", Operation: "PurgeQueue", Category: "Queue management",
			Status: capabilities.StatusSupported, Notes: "Deletes all messages immediately"},
		capabilities.Capability{Service: "sqs", Operation: "ListQueueTags", Category: "Queue management",
			Status: capabilities.StatusSupported},
		capabilities.Capability{Service: "sqs", Operation: "TagQueue", Category: "Queue management",
			Status: capabilities.StatusSupported, Notes: "Merges with existing tags"},
		capabilities.Capability{Service: "sqs", Operation: "UntagQueue", Category: "Queue management",
			Status: capabilities.StatusSupported},

		// Message operations
		capabilities.Capability{Service: "sqs", Operation: "SendMessage", Category: "Message operations",
			Status: capabilities.StatusSupported, Notes: "DelaySeconds, MessageAttributes supported"},
		capabilities.Capability{Service: "sqs", Operation: "SendMessageBatch", Category: "Message operations",
			Status: capabilities.StatusSupported, Notes: "Up to 10 messages per batch"},
		capabilities.Capability{Service: "sqs", Operation: "ReceiveMessage", Category: "Message operations",
			Status: capabilities.StatusSupported, Notes: "MaxNumberOfMessages, VisibilityTimeout, WaitTimeSeconds, FIFO ReceiveRequestAttemptId"},
		capabilities.Capability{Service: "sqs", Operation: "DeleteMessage", Category: "Message operations",
			Status: capabilities.StatusSupported},
		capabilities.Capability{Service: "sqs", Operation: "DeleteMessageBatch", Category: "Message operations",
			Status: capabilities.StatusSupported, Notes: "Up to 10 messages per batch"},
		capabilities.Capability{Service: "sqs", Operation: "ChangeMessageVisibility", Category: "Message operations",
			Status: capabilities.StatusSupported, Notes: "Sets new visibility timeout on an in-flight message"},
		capabilities.Capability{Service: "sqs", Operation: "ChangeMessageVisibilityBatch", Category: "Message operations",
			Status: capabilities.StatusSupported, Notes: "Batch visibility timeout changes; per-entry success/failure response"},

		// Permissions
		capabilities.Capability{Service: "sqs", Operation: "AddPermission", Category: "Permissions",
			Status: capabilities.StatusUnsupported, Notes: "stub; returns 501"},
		capabilities.Capability{Service: "sqs", Operation: "RemovePermission", Category: "Permissions",
			Status: capabilities.StatusUnsupported, Notes: "stub; returns 501"},

		// Dead-letter queues
		capabilities.Capability{Service: "sqs", Operation: "ListDeadLetterSourceQueues", Category: "Dead-letter queues",
			Status: capabilities.StatusSupported, Notes: "Lists queues that target a given DLQ"},
		capabilities.Capability{Service: "sqs", Operation: "StartMessageMoveTask", Category: "Dead-letter queues",
			Status: capabilities.StatusSupported, Notes: "Redrives messages from a DLQ back to its source queue"},
	)
}
