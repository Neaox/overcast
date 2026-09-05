package s3

// handler_notifications.go — PutBucketNotificationConfiguration and
// GetBucketNotificationConfiguration.
//
// AWS docs:
//   https://docs.aws.amazon.com/AmazonS3/latest/API/API_PutBucketNotificationConfiguration.html
//   https://docs.aws.amazon.com/AmazonS3/latest/API/API_GetBucketNotificationConfiguration.html

import (
	"encoding/xml"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/overcast-sh/overcast/internal/events"
	"github.com/overcast-sh/overcast/internal/middleware"
	"github.com/overcast-sh/overcast/internal/protocol"
	"github.com/overcast-sh/overcast/internal/serviceutil"
)

// ---- XML wire types --------------------------------------------------------
// These mirror the AWS S3 XML schema exactly.
// Separate from the storage types so wire format and storage remain decoupled.

type xmlNotificationConfiguration struct {
	XMLName              xml.Name                         `xml:"NotificationConfiguration"`
	Xmlns                string                           `xml:"xmlns,attr,omitempty"`
	QueueConfigurations  []xmlQueueConfiguration          `xml:"QueueConfiguration"`
	TopicConfigurations  []xmlTopicConfiguration          `xml:"TopicConfiguration"`
	LambdaConfigurations []xmlLambdaFunctionConfiguration `xml:"CloudFunctionConfiguration"`

	// EventBridgeConfiguration is an empty element; a pointer distinguishes
	// "present, so EventBridge delivery is on" from absent. omitempty keeps an
	// unset one out of the response — emitting it would turn delivery on for
	// any client that read the configuration back and put it again.
	EventBridgeConfiguration *xmlEventBridgeConfiguration `xml:"EventBridgeConfiguration,omitempty"`
}

// xmlEventBridgeConfiguration is com.amazonaws.s3#EventBridgeConfiguration: a
// structure with no members.
type xmlEventBridgeConfiguration struct{}

type xmlQueueConfiguration struct {
	ID     string     `xml:"Id,omitempty"`
	Queue  string     `xml:"Queue"`
	Events []string   `xml:"Event"`
	Filter *xmlFilter `xml:"Filter"`
}

type xmlTopicConfiguration struct {
	ID     string     `xml:"Id,omitempty"`
	Topic  string     `xml:"Topic"`
	Events []string   `xml:"Event"`
	Filter *xmlFilter `xml:"Filter"`
}

type xmlLambdaFunctionConfiguration struct {
	ID       string     `xml:"Id,omitempty"`
	Function string     `xml:"CloudFunction"`
	Events   []string   `xml:"Event"`
	Filter   *xmlFilter `xml:"Filter"`
}

type xmlFilter struct {
	S3Key xmlS3Key `xml:"S3Key"`
}

type xmlS3Key struct {
	FilterRules []xmlFilterRule `xml:"FilterRule"`
}

type xmlFilterRule struct {
	Name  string `xml:"Name"`
	Value string `xml:"Value"`
}

// ---- Handlers --------------------------------------------------------------

// GetBucketNotificationConfiguration handles GET /{bucket}?notification.
func (h *Handler) GetBucketNotificationConfiguration(w http.ResponseWriter, r *http.Request) {
	bucket := chi.URLParam(r, "bucket")

	if _, aerr := h.store.getBucket(r.Context(), bucket); aerr != nil {
		protocol.WriteXMLError(w, r, aerr)
		return
	}

	cfg, aerr := h.store.getNotificationConfig(r.Context(), bucket)
	if aerr != nil {
		protocol.WriteXMLError(w, r, aerr)
		return
	}

	out := notificationConfigToXML(cfg)
	protocol.WriteXML(w, r, http.StatusOK, out)
}

// PutBucketNotificationConfiguration handles PUT /{bucket}?notification.
func (h *Handler) PutBucketNotificationConfiguration(w http.ResponseWriter, r *http.Request) {
	bucket := chi.URLParam(r, "bucket")

	if _, aerr := h.store.getBucket(r.Context(), bucket); aerr != nil {
		protocol.WriteXMLError(w, r, aerr)
		return
	}

	var in xmlNotificationConfiguration
	if err := xml.NewDecoder(r.Body).Decode(&in); err != nil {
		protocol.WriteXMLError(w, r, &protocol.AWSError{
			Code:       "MalformedXML",
			Message:    "The XML you provided was not well-formed",
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}

	// Real AWS requires Lambda notification destinations to be in the same
	// region as the bucket.
	bucketRegion := middleware.RegionFromContext(r.Context(), h.cfg.Region)
	for _, lc := range in.LambdaConfigurations {
		if fnRegion := serviceutil.ARNRegion(lc.Function); fnRegion != "" && fnRegion != bucketRegion {
			protocol.WriteXMLError(w, r, destinationValidationFailed(
				"Lambda function ARN must be in the same region as the S3 bucket."))
			return
		}
	}

	// "In the case of AWS Lambda destinations, Amazon S3 verifies that the
	// Lambda function permissions grant Amazon S3 permission to invoke the
	// function from the Amazon S3 bucket." The PUT is atomic, so one
	// unverifiable destination refuses the whole configuration.
	// https://docs.aws.amazon.com/AmazonS3/latest/API/API_PutBucketNotificationConfiguration.html
	if h.lambdaAuth != nil && !skipDestinationValidation(r) {
		bucketARN := protocol.ARN("", "", "s3", bucket)
		for _, lc := range in.LambdaConfigurations {
			if aerr := h.lambdaAuth.AuthorizeServiceInvoke(
				r.Context(), lc.Function, s3ServicePrincipal, bucketARN, h.cfg.AccountID,
			); aerr != nil {
				protocol.WriteXMLError(w, r, destinationValidationFailed(
					"Not authorized to invoke function ["+lc.Function+"]"))
				return
			}
		}
	}

	cfg := notificationConfigFromXML(&in)
	if aerr := h.store.putNotificationConfig(r.Context(), bucket, cfg); aerr != nil {
		protocol.WriteXMLError(w, r, aerr)
		return
	}

	if h.bus != nil {
		h.bus.Publish(r.Context(), events.Event{
			Type:    events.S3NotificationConfigured,
			Time:    h.clk.Now(),
			Source:  "s3",
			Payload: events.ResourcePayload{Name: bucket, ARN: protocol.ARN("", "", "s3", bucket)},
		})
	}
	protocol.WriteEmpty(w, r, http.StatusOK)
}

// destinationValidationFailed is what S3 answers when it cannot verify a
// notification destination. The configuration is not stored — the PUT
// notification is an atomic operation, so if one destination fails the entire
// PUT fails and S3 adds nothing to the bucket.
func destinationValidationFailed(detail string) *protocol.AWSError {
	return &protocol.AWSError{
		Code:       "InvalidArgument",
		Message:    "Unable to validate the following destination configurations: " + detail,
		HTTPStatus: http.StatusBadRequest,
	}
}

// skipDestinationValidation reports whether the caller asked S3 to store the
// configuration without checking that its destinations are reachable.
func skipDestinationValidation(r *http.Request) bool {
	return strings.EqualFold(r.Header.Get("x-amz-skip-destination-validation"), "true")
}

// ---- Conversion helpers ----------------------------------------------------

func notificationConfigFromXML(in *xmlNotificationConfiguration) *NotificationConfig {
	cfg := &NotificationConfig{}

	if in.EventBridgeConfiguration != nil {
		cfg.EventBridgeConfiguration = &EventBridgeNotificationConfig{}
	}

	for _, qc := range in.QueueConfigurations {
		q := QueueNotificationConfig{
			ID:     qc.ID,
			ARN:    qc.Queue,
			Events: qc.Events,
		}
		if qc.Filter != nil {
			q.Filter = filterFromXML(qc.Filter)
		}
		cfg.QueueConfigurations = append(cfg.QueueConfigurations, q)
	}

	for _, tc := range in.TopicConfigurations {
		t := TopicNotificationConfig{
			ID:     tc.ID,
			ARN:    tc.Topic,
			Events: tc.Events,
		}
		if tc.Filter != nil {
			t.Filter = filterFromXML(tc.Filter)
		}
		cfg.TopicConfigurations = append(cfg.TopicConfigurations, t)
	}

	for _, lc := range in.LambdaConfigurations {
		l := LambdaNotificationConfig{
			ID:     lc.ID,
			ARN:    lc.Function,
			Events: lc.Events,
		}
		if lc.Filter != nil {
			l.Filter = filterFromXML(lc.Filter)
		}
		cfg.LambdaConfigurations = append(cfg.LambdaConfigurations, l)
	}

	return cfg
}

func notificationConfigToXML(cfg *NotificationConfig) *xmlNotificationConfiguration {
	out := &xmlNotificationConfiguration{
		Xmlns: "http://s3.amazonaws.com/doc/2006-03-01/",
	}

	if cfg.EventBridgeConfiguration != nil {
		out.EventBridgeConfiguration = &xmlEventBridgeConfiguration{}
	}

	for _, qc := range cfg.QueueConfigurations {
		q := xmlQueueConfiguration{
			ID:     qc.ID,
			Queue:  qc.ARN,
			Events: qc.Events,
		}
		if qc.Filter != nil {
			q.Filter = filterToXML(qc.Filter)
		}
		out.QueueConfigurations = append(out.QueueConfigurations, q)
	}

	for _, tc := range cfg.TopicConfigurations {
		t := xmlTopicConfiguration{
			ID:     tc.ID,
			Topic:  tc.ARN,
			Events: tc.Events,
		}
		if tc.Filter != nil {
			t.Filter = filterToXML(tc.Filter)
		}
		out.TopicConfigurations = append(out.TopicConfigurations, t)
	}

	for _, lc := range cfg.LambdaConfigurations {
		l := xmlLambdaFunctionConfiguration{
			ID:       lc.ID,
			Function: lc.ARN,
			Events:   lc.Events,
		}
		if lc.Filter != nil {
			l.Filter = filterToXML(lc.Filter)
		}
		out.LambdaConfigurations = append(out.LambdaConfigurations, l)
	}

	return out
}

func filterFromXML(f *xmlFilter) *NotificationFilter {
	nf := &NotificationFilter{}
	for _, r := range f.S3Key.FilterRules {
		nf.Key.Rules = append(nf.Key.Rules, NotificationFilterRule(r))
	}
	return nf
}

func filterToXML(f *NotificationFilter) *xmlFilter {
	xf := &xmlFilter{}
	for _, r := range f.Key.Rules {
		xf.S3Key.FilterRules = append(xf.S3Key.FilterRules, xmlFilterRule(r))
	}
	return xf
}
