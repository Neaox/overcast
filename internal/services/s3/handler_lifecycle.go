package s3

// handler_lifecycle.go implements GetBucketLifecycleConfiguration,
// PutBucketLifecycleConfiguration and DeleteBucketLifecycle, plus the
// validation that decides which rule constructs Overcast will accept.
//
// The validation is the load-bearing part. Overcast's object store keeps
// exactly one live object per key: it has no version history and no delete
// markers (see ListObjectVersions in handler_bucket.go). The three rule
// constructs that depend on versioning therefore can never be acted on, so
// they are refused here rather than stored and quietly ignored — the failure
// mode docs/plans/full-emulation-priority.md §2.1 exists to prevent. Every
// other construct in the schema is evaluated by the sweeper in lifecycle.go.

import (
	"encoding/xml"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Neaox/overcast/internal/protocol"
)

const (
	lifecycleStatusEnabled  = "Enabled"
	lifecycleStatusDisabled = "Disabled"

	// maxLifecycleRuleIDLength is AWS's documented limit for a rule ID.
	maxLifecycleRuleIDLength = 255
)

// lifecycleTransitionStorageClasses are the classes S3 accepts on a
// Transition. Overcast records the class as a marker and moves no bytes
// (docs/plans/full-emulation-priority.md §7).
var lifecycleTransitionStorageClasses = map[string]bool{
	"GLACIER":             true,
	"GLACIER_IR":          true,
	"DEEP_ARCHIVE":        true,
	"STANDARD_IA":         true,
	"ONEZONE_IA":          true,
	"INTELLIGENT_TIERING": true,
}

// ---- Wire format -----------------------------------------------------------

// lifecycleConfigurationXML is the LifecycleConfiguration element, used for
// both the Put request body and the Get response.
type lifecycleConfigurationXML struct {
	XMLName xml.Name           `xml:"LifecycleConfiguration"`
	Xmlns   string             `xml:"xmlns,attr,omitempty"`
	Rules   []lifecycleRuleXML `xml:"Rule"`
}

type lifecycleRuleXML struct {
	ID     string `xml:"ID,omitempty"`
	Status string `xml:"Status"`

	// Prefix is the deprecated rule-level filter. A pointer so an omitted
	// element is distinguishable from <Prefix></Prefix>, which AWS accepts as
	// "the whole bucket".
	Prefix *string             `xml:"Prefix"`
	Filter *lifecycleFilterXML `xml:"Filter"`

	Expiration                     *lifecycleExpirationXML  `xml:"Expiration"`
	Transition                     []lifecycleTransitionXML `xml:"Transition"`
	AbortIncompleteMultipartUpload *lifecycleAbortMPUXML    `xml:"AbortIncompleteMultipartUpload"`

	// Refused constructs. They are parsed so that supplying one produces a
	// specific, named error instead of being silently dropped by the decoder.
	NoncurrentVersionExpiration *lifecycleNoncurrentExpirationXML  `xml:"NoncurrentVersionExpiration"`
	NoncurrentVersionTransition []lifecycleNoncurrentTransitionXML `xml:"NoncurrentVersionTransition"`
}

type lifecycleFilterXML struct {
	Prefix                string           `xml:"Prefix,omitempty"`
	Tag                   *lifecycleTagXML `xml:"Tag,omitempty"`
	ObjectSizeGreaterThan *int64           `xml:"ObjectSizeGreaterThan,omitempty"`
	ObjectSizeLessThan    *int64           `xml:"ObjectSizeLessThan,omitempty"`
	And                   *lifecycleAndXML `xml:"And,omitempty"`
}

type lifecycleAndXML struct {
	Prefix                string            `xml:"Prefix,omitempty"`
	Tags                  []lifecycleTagXML `xml:"Tag,omitempty"`
	ObjectSizeGreaterThan *int64            `xml:"ObjectSizeGreaterThan,omitempty"`
	ObjectSizeLessThan    *int64            `xml:"ObjectSizeLessThan,omitempty"`
}

type lifecycleTagXML struct {
	Key   string `xml:"Key"`
	Value string `xml:"Value"`
}

// Date carries omitempty because a Days rule must not serialise an empty
// <Date></Date>: every real SDK parses the element as a timestamp and rejects
// "" as an invalid RFC3339 value, so an empty element breaks the response for
// every client rather than being ignored. AWS omits it. Same for Transition.
type lifecycleExpirationXML struct {
	Days                      *int   `xml:"Days,omitempty"`
	Date                      string `xml:"Date,omitempty"`
	ExpiredObjectDeleteMarker *bool  `xml:"ExpiredObjectDeleteMarker,omitempty"`
}

type lifecycleTransitionXML struct {
	Days         *int   `xml:"Days,omitempty"`
	Date         string `xml:"Date,omitempty"`
	StorageClass string `xml:"StorageClass"`
}

type lifecycleAbortMPUXML struct {
	DaysAfterInitiation *int `xml:"DaysAfterInitiation,omitempty"`
}

type lifecycleNoncurrentExpirationXML struct {
	NoncurrentDays *int `xml:"NoncurrentDays"`
}

type lifecycleNoncurrentTransitionXML struct {
	NoncurrentDays *int   `xml:"NoncurrentDays"`
	StorageClass   string `xml:"StorageClass"`
}

// ---- Errors ----------------------------------------------------------------

func errMalformedLifecycleXML(detail string) *protocol.AWSError {
	msg := "The XML you provided was not well-formed or did not validate against our published schema"
	if detail != "" {
		msg += ": " + detail
	}
	return &protocol.AWSError{Code: "MalformedXML", Message: msg, HTTPStatus: http.StatusBadRequest}
}

func errNoSuchLifecycleConfiguration() *protocol.AWSError {
	return &protocol.AWSError{
		Code:       "NoSuchLifecycleConfiguration",
		Message:    "The lifecycle configuration does not exist",
		HTTPStatus: http.StatusNotFound,
	}
}

// errUnevaluatedLifecycleConstruct refuses a rule element the sweeper cannot
// act on. The message names the element and says why, so a user reading a
// 400 knows this is an Overcast boundary rather than an AWS schema violation.
func errUnevaluatedLifecycleConstruct(element, reason string) *protocol.AWSError {
	return protocol.ErrInvalidArgument(fmt.Sprintf(
		"%s is not evaluated by this emulator (%s). It is refused rather than stored, "+
			"so a rule that would never run cannot be mistaken for one that does.",
		element, reason))
}

// ---- Handlers --------------------------------------------------------------

// getBucketLifecycleConfiguration handles GET /{bucket}?lifecycle.
// AWS docs: https://docs.aws.amazon.com/AmazonS3/latest/API/API_GetBucketLifecycleConfiguration.html
func (h *Handler) getBucketLifecycleConfiguration(w http.ResponseWriter, r *http.Request) {
	bucket := chi.URLParam(r, "bucket")
	if aerr := h.requireBucket(r, bucket); aerr != nil {
		protocol.WriteXMLError(w, r, aerr)
		return
	}

	cfg, found, aerr := h.store.getLifecycle(r.Context(), bucket)
	if aerr != nil {
		protocol.WriteXMLError(w, r, aerr)
		return
	}
	if !found || len(cfg.Rules) == 0 {
		protocol.WriteXMLError(w, r, errNoSuchLifecycleConfiguration())
		return
	}

	protocol.WriteXML(w, r, http.StatusOK, lifecycleConfigurationXML{
		Xmlns: "http://s3.amazonaws.com/doc/2006-03-01/",
		Rules: lifecycleRulesToXML(cfg.Rules),
	})
}

// putBucketLifecycleConfiguration handles PUT /{bucket}?lifecycle.
// AWS docs: https://docs.aws.amazon.com/AmazonS3/latest/API/API_PutBucketLifecycleConfiguration.html
//
// The configuration replaces any previous one wholesale, as on AWS.
func (h *Handler) putBucketLifecycleConfiguration(w http.ResponseWriter, r *http.Request) {
	bucket := chi.URLParam(r, "bucket")
	if aerr := h.requireBucket(r, bucket); aerr != nil {
		protocol.WriteXMLError(w, r, aerr)
		return
	}

	var body lifecycleConfigurationXML
	if err := xml.NewDecoder(r.Body).Decode(&body); err != nil {
		protocol.WriteXMLError(w, r, errMalformedLifecycleXML(""))
		return
	}

	cfg, aerr := parseLifecycleConfiguration(&body)
	if aerr != nil {
		protocol.WriteXMLError(w, r, aerr)
		return
	}

	if aerr := h.store.putLifecycle(r.Context(), bucket, cfg); aerr != nil {
		protocol.WriteXMLError(w, r, aerr)
		return
	}
	h.invalidateLifecycleIndex(r)

	w.WriteHeader(http.StatusOK)
}

// deleteBucketLifecycle handles DELETE /{bucket}?lifecycle.
// AWS docs: https://docs.aws.amazon.com/AmazonS3/latest/API/API_DeleteBucketLifecycle.html
//
// Idempotent: AWS answers 204 whether or not a configuration was there.
func (h *Handler) deleteBucketLifecycle(w http.ResponseWriter, r *http.Request) {
	bucket := chi.URLParam(r, "bucket")
	if aerr := h.requireBucket(r, bucket); aerr != nil {
		protocol.WriteXMLError(w, r, aerr)
		return
	}

	if aerr := h.store.deleteLifecycle(r.Context(), bucket); aerr != nil {
		protocol.WriteXMLError(w, r, aerr)
		return
	}
	h.invalidateLifecycleIndex(r)

	w.WriteHeader(http.StatusNoContent)
}

// Every lifecycle handler calls requireBucket (handler_multipart.go) first, as
// AWS does — a missing bucket outranks a missing configuration.

// invalidateLifecycleIndex republishes the snapshot the object paths read, so
// a configuration change is visible to the very next request rather than at
// the next sweep.
func (h *Handler) invalidateLifecycleIndex(r *http.Request) {
	if _, err := h.refreshLifecycleIndex(r.Context()); err != nil {
		h.log.LogStateError(r, "lifecycle index refresh", protocol.Wrap(protocol.ErrInternalError, err))
		return
	}
	_ = h.lifecycle.init.Do(func() error { return nil })
}

// ---- Parsing and validation ------------------------------------------------

// parseLifecycleConfiguration validates the decoded body and converts it to
// the stored model. Validation order follows AWS: structural problems
// (MalformedXML) before value problems (InvalidArgument/InvalidRequest), and
// per-rule checks before cross-rule ones.
func parseLifecycleConfiguration(body *lifecycleConfigurationXML) (*LifecycleConfiguration, *protocol.AWSError) {
	if len(body.Rules) == 0 {
		return nil, errMalformedLifecycleXML("at least one Rule is required")
	}

	cfg := &LifecycleConfiguration{Rules: make([]LifecycleRule, 0, len(body.Rules))}
	seenIDs := make(map[string]bool, len(body.Rules))

	for i := range body.Rules {
		rule, aerr := parseLifecycleRule(&body.Rules[i], i)
		if aerr != nil {
			return nil, aerr
		}
		if seenIDs[rule.ID] {
			return nil, protocol.ErrInvalidArgument(
				"Rule ID must be unique. Found same ID for more than one rule: " + rule.ID)
		}
		seenIDs[rule.ID] = true
		cfg.Rules = append(cfg.Rules, *rule)
	}
	return cfg, nil
}

func parseLifecycleRule(in *lifecycleRuleXML, index int) (*LifecycleRule, *protocol.AWSError) {
	if aerr := refuseUnevaluatedConstructs(in); aerr != nil {
		return nil, aerr
	}

	switch in.Status {
	case lifecycleStatusEnabled, lifecycleStatusDisabled:
	default:
		return nil, errMalformedLifecycleXML(
			"Status must be " + lifecycleStatusEnabled + " or " + lifecycleStatusDisabled)
	}

	if len(in.ID) > maxLifecycleRuleIDLength {
		return nil, protocol.ErrInvalidArgument(fmt.Sprintf(
			"ID length should not exceed allowed limit of %d", maxLifecycleRuleIDLength))
	}

	// A rule must carry exactly one of the legacy Prefix and the Filter form.
	switch {
	case in.Prefix != nil && in.Filter != nil:
		return nil, errMalformedLifecycleXML("a Rule cannot carry both Prefix and Filter")
	case in.Prefix == nil && in.Filter == nil:
		return nil, errMalformedLifecycleXML("a Rule must carry either Prefix or Filter")
	}

	out := &LifecycleRule{
		ID:     in.ID,
		Status: in.Status,
		Prefix: in.Prefix,
	}
	if out.ID == "" {
		// AWS generates an ID when one is not supplied. Deriving it from the
		// rule's position keeps Get's answer stable across restarts.
		out.ID = fmt.Sprintf("rule-%d", index+1)
	}

	if in.Filter != nil {
		filter, aerr := parseLifecycleFilter(in.Filter)
		if aerr != nil {
			return nil, aerr
		}
		out.Filter = filter
	}

	expiration, aerr := parseLifecycleExpiration(in.Expiration)
	if aerr != nil {
		return nil, aerr
	}
	out.Expiration = expiration

	for i := range in.Transition {
		transition, tErr := parseLifecycleTransition(&in.Transition[i])
		if tErr != nil {
			return nil, tErr
		}
		out.Transitions = append(out.Transitions, *transition)
	}

	if in.AbortIncompleteMultipartUpload != nil {
		days := in.AbortIncompleteMultipartUpload.DaysAfterInitiation
		if days == nil || *days <= 0 {
			return nil, protocol.ErrInvalidArgument(
				"'DaysAfterInitiation' for AbortIncompleteMultipartUpload action must be a positive integer")
		}
		out.AbortIncompleteMultipartUpload = &LifecycleAbortMPU{DaysAfterInitiation: *days}
	}

	if out.Expiration == nil && len(out.Transitions) == 0 && out.AbortIncompleteMultipartUpload == nil {
		return nil, &protocol.AWSError{
			Code:       "InvalidRequest",
			Message:    "At least one action needs to be specified in a rule",
			HTTPStatus: http.StatusBadRequest,
		}
	}
	return out, nil
}

// refuseUnevaluatedConstructs rejects the rule elements Overcast's object
// store cannot support. All three depend on object version history, which the
// store does not model: every key holds exactly one live object, so there are
// no noncurrent versions and no delete markers for a rule to act on.
func refuseUnevaluatedConstructs(in *lifecycleRuleXML) *protocol.AWSError {
	const noVersions = "this emulator's object store keeps one live object per key, " +
		"with no version history or delete markers"

	if in.NoncurrentVersionExpiration != nil {
		return errUnevaluatedLifecycleConstruct("NoncurrentVersionExpiration", noVersions)
	}
	if len(in.NoncurrentVersionTransition) > 0 {
		return errUnevaluatedLifecycleConstruct("NoncurrentVersionTransition", noVersions)
	}
	if in.Expiration != nil && in.Expiration.ExpiredObjectDeleteMarker != nil {
		return errUnevaluatedLifecycleConstruct("ExpiredObjectDeleteMarker", noVersions)
	}
	return nil
}

// parseLifecycleFilter validates AWS's union semantics: a Filter carries at
// most one predicate, with And holding the conjunction of several.
func parseLifecycleFilter(in *lifecycleFilterXML) (*LifecycleFilter, *protocol.AWSError) {
	set := 0
	if in.Prefix != "" {
		set++
	}
	if in.Tag != nil {
		set++
	}
	if in.ObjectSizeGreaterThan != nil {
		set++
	}
	if in.ObjectSizeLessThan != nil {
		set++
	}
	if in.And != nil {
		set++
	}
	if set > 1 {
		return nil, errMalformedLifecycleXML(
			"Filter must carry at most one of Prefix, Tag, ObjectSizeGreaterThan, ObjectSizeLessThan and And")
	}

	out := &LifecycleFilter{
		Prefix:                in.Prefix,
		ObjectSizeGreaterThan: in.ObjectSizeGreaterThan,
		ObjectSizeLessThan:    in.ObjectSizeLessThan,
	}
	if in.Tag != nil {
		if in.Tag.Key == "" {
			return nil, errMalformedLifecycleXML("Tag requires a Key")
		}
		out.Tag = &LifecycleTag{Key: in.Tag.Key, Value: in.Tag.Value}
	}
	if in.And != nil {
		and := &LifecycleAnd{
			Prefix:                in.And.Prefix,
			ObjectSizeGreaterThan: in.And.ObjectSizeGreaterThan,
			ObjectSizeLessThan:    in.And.ObjectSizeLessThan,
		}
		for _, tag := range in.And.Tags {
			if tag.Key == "" {
				return nil, errMalformedLifecycleXML("Tag requires a Key")
			}
			and.Tags = append(and.Tags, LifecycleTag(tag))
		}
		out.And = and
	}
	return out, nil
}

func parseLifecycleExpiration(in *lifecycleExpirationXML) (*LifecycleExpiration, *protocol.AWSError) {
	if in == nil {
		return nil, nil
	}
	hasDate := in.Date != ""
	switch {
	case in.Days != nil && hasDate:
		return nil, errMalformedLifecycleXML("Expiration cannot carry both Days and Date")
	case in.Days == nil && !hasDate:
		return nil, errMalformedLifecycleXML("Expiration requires either Days or Date")
	}

	if in.Days != nil {
		if *in.Days <= 0 {
			return nil, protocol.ErrInvalidArgument(
				"'Days' for Expiration action must be a positive integer")
		}
		return &LifecycleExpiration{Days: *in.Days}, nil
	}

	date, aerr := parseLifecycleDate(in.Date, "Expiration")
	if aerr != nil {
		return nil, aerr
	}
	return &LifecycleExpiration{Date: &date}, nil
}

func parseLifecycleTransition(in *lifecycleTransitionXML) (*LifecycleTransition, *protocol.AWSError) {
	if !lifecycleTransitionStorageClasses[in.StorageClass] {
		return nil, errMalformedLifecycleXML(
			"Transition StorageClass is not a valid value: " + in.StorageClass)
	}
	hasDate := in.Date != ""
	switch {
	case in.Days != nil && hasDate:
		return nil, errMalformedLifecycleXML("Transition cannot carry both Days and Date")
	case in.Days == nil && !hasDate:
		return nil, errMalformedLifecycleXML("Transition requires either Days or Date")
	}

	if in.Days != nil {
		if *in.Days < 0 {
			return nil, protocol.ErrInvalidArgument(
				"'Days' for Transition action must not be a negative integer")
		}
		return &LifecycleTransition{Days: *in.Days, StorageClass: in.StorageClass}, nil
	}

	date, aerr := parseLifecycleDate(in.Date, "Transition")
	if aerr != nil {
		return nil, aerr
	}
	return &LifecycleTransition{Date: &date, StorageClass: in.StorageClass}, nil
}

// parseLifecycleDate accepts the ISO-8601 forms S3 accepts and enforces AWS's
// rule that a lifecycle Date must be midnight UTC.
func parseLifecycleDate(raw, action string) (time.Time, *protocol.AWSError) {
	var (
		parsed time.Time
		err    error
	)
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02"} {
		parsed, err = time.Parse(layout, strings.TrimSpace(raw))
		if err == nil {
			break
		}
	}
	if err != nil {
		return time.Time{}, errMalformedLifecycleXML("'Date' for " + action + " action is not a valid ISO-8601 date")
	}
	utc := parsed.UTC()
	if !utc.Equal(utc.Truncate(24 * time.Hour)) {
		return time.Time{}, protocol.ErrInvalidArgument(
			"'Date' must be at midnight GMT in '" + action + "' action")
	}
	return utc, nil
}

// ---- Serialisation ---------------------------------------------------------

func lifecycleRulesToXML(rules []LifecycleRule) []lifecycleRuleXML {
	out := make([]lifecycleRuleXML, 0, len(rules))
	for i := range rules {
		rule := &rules[i]
		xmlRule := lifecycleRuleXML{
			ID:     rule.ID,
			Status: rule.Status,
			Prefix: rule.Prefix,
		}
		if rule.Filter != nil {
			xmlRule.Filter = lifecycleFilterToXML(rule.Filter)
		}
		if rule.Expiration != nil {
			xmlRule.Expiration = &lifecycleExpirationXML{}
			if rule.Expiration.Date != nil {
				xmlRule.Expiration.Date = rule.Expiration.Date.UTC().Format("2006-01-02T15:04:05.000Z")
			} else {
				days := rule.Expiration.Days
				xmlRule.Expiration.Days = &days
			}
		}
		for j := range rule.Transitions {
			t := rule.Transitions[j]
			xmlTransition := lifecycleTransitionXML{StorageClass: t.StorageClass}
			if t.Date != nil {
				xmlTransition.Date = t.Date.UTC().Format("2006-01-02T15:04:05.000Z")
			} else {
				days := t.Days
				xmlTransition.Days = &days
			}
			xmlRule.Transition = append(xmlRule.Transition, xmlTransition)
		}
		if rule.AbortIncompleteMultipartUpload != nil {
			days := rule.AbortIncompleteMultipartUpload.DaysAfterInitiation
			xmlRule.AbortIncompleteMultipartUpload = &lifecycleAbortMPUXML{DaysAfterInitiation: &days}
		}
		out = append(out, xmlRule)
	}
	return out
}

func lifecycleFilterToXML(f *LifecycleFilter) *lifecycleFilterXML {
	out := &lifecycleFilterXML{
		Prefix:                f.Prefix,
		ObjectSizeGreaterThan: f.ObjectSizeGreaterThan,
		ObjectSizeLessThan:    f.ObjectSizeLessThan,
	}
	if f.Tag != nil {
		out.Tag = &lifecycleTagXML{Key: f.Tag.Key, Value: f.Tag.Value}
	}
	if f.And != nil {
		and := &lifecycleAndXML{
			Prefix:                f.And.Prefix,
			ObjectSizeGreaterThan: f.And.ObjectSizeGreaterThan,
			ObjectSizeLessThan:    f.And.ObjectSizeLessThan,
		}
		for _, tag := range f.And.Tags {
			and.Tags = append(and.Tags, lifecycleTagXML(tag))
		}
		out.And = and
	}
	return out
}
