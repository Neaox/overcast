package sns

// handler_stubs.go contains every SNS handler that is not yet implemented.
// Each method returns HTTP 501 Not Implemented with x-emulator-unsupported: true.
//
// Convention: when an operation is implemented, move its method body out of this
// file and into handler.go (or handler_<group>.go for large feature groups).
// handler.go is the authoritative inventory of what actually works.

import (
	"net/http"

	"github.com/your-org/overcast/internal/protocol"
)

// CreateTopic handles the SNS CreateTopic operation.
// AWS docs: https://docs.aws.amazon.com/sns/latest/api/API_CreateTopic.html
func (h *Handler) CreateTopic(w http.ResponseWriter, r *http.Request) {
	protocol.NotImplementedJSON(w, r)
}

// DeleteTopic handles the SNS DeleteTopic operation.
// AWS docs: https://docs.aws.amazon.com/sns/latest/api/API_DeleteTopic.html
func (h *Handler) DeleteTopic(w http.ResponseWriter, r *http.Request) {
	protocol.NotImplementedJSON(w, r)
}

// ListTopics handles the SNS ListTopics operation.
// AWS docs: https://docs.aws.amazon.com/sns/latest/api/API_ListTopics.html
func (h *Handler) ListTopics(w http.ResponseWriter, r *http.Request) {
	protocol.NotImplementedJSON(w, r)
}

// Subscribe handles the SNS Subscribe operation.
// AWS docs: https://docs.aws.amazon.com/sns/latest/api/API_Subscribe.html
func (h *Handler) Subscribe(w http.ResponseWriter, r *http.Request) {
	protocol.NotImplementedJSON(w, r)
}

// Unsubscribe handles the SNS Unsubscribe operation.
// AWS docs: https://docs.aws.amazon.com/sns/latest/api/API_Unsubscribe.html
func (h *Handler) Unsubscribe(w http.ResponseWriter, r *http.Request) {
	protocol.NotImplementedJSON(w, r)
}

// ListSubscriptionsByTopic handles the SNS ListSubscriptionsByTopic operation.
// AWS docs: https://docs.aws.amazon.com/sns/latest/api/API_ListSubscriptionsByTopic.html
func (h *Handler) ListSubscriptionsByTopic(w http.ResponseWriter, r *http.Request) {
	protocol.NotImplementedJSON(w, r)
}

// Publish handles the SNS Publish operation.
// AWS docs: https://docs.aws.amazon.com/sns/latest/api/API_Publish.html
func (h *Handler) Publish(w http.ResponseWriter, r *http.Request) {
	protocol.NotImplementedJSON(w, r)
}

// PublishBatch handles the SNS PublishBatch operation.
// AWS docs: https://docs.aws.amazon.com/sns/latest/api/API_PublishBatch.html
func (h *Handler) PublishBatch(w http.ResponseWriter, r *http.Request) {
	protocol.NotImplementedJSON(w, r)
}

// GetTopicAttributes handles the SNS GetTopicAttributes operation.
// AWS docs: https://docs.aws.amazon.com/sns/latest/api/API_GetTopicAttributes.html
func (h *Handler) GetTopicAttributes(w http.ResponseWriter, r *http.Request) {
	protocol.NotImplementedJSON(w, r)
}

// SetTopicAttributes handles the SNS SetTopicAttributes operation.
// AWS docs: https://docs.aws.amazon.com/sns/latest/api/API_SetTopicAttributes.html
func (h *Handler) SetTopicAttributes(w http.ResponseWriter, r *http.Request) {
	protocol.NotImplementedJSON(w, r)
}
