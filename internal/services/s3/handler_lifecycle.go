package s3

// handler_lifecycle.go implements GetBucketLifecycleConfiguration,
// PutBucketLifecycleConfiguration and DeleteBucketLifecycle, plus the
// validation that decides which rule constructs Overcast will accept.
//
// Every action AWS models here is now evaluated: Expiration (including
// ExpiredObjectDeleteMarker), Transition, AbortIncompleteMultipartUpload,
// NoncurrentVersionExpiration and NoncurrentVersionTransition. Nothing is
// stored-and-ignored — if a construct were ever accepted that the sweeper
// cannot act on, it would have to be refused here instead, because a rule that
// silently never runs is worse than one that is rejected.

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

	// transitionDefaultMinimumHeader carries
	// com.amazonaws.s3#TransitionDefaultMinimumObjectSize: a request header on
	// PutBucketLifecycleConfiguration and a response header on both that
	// operation and GetBucketLifecycleConfiguration.
	transitionDefaultMinimumHeader = "x-amz-transition-default-minimum-object-size"
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
	NoncurrentDays          *int `xml:"NoncurrentDays"`
	NewerNoncurrentVersions *int `xml:"NewerNoncurrentVersions,omitempty"`
}

type lifecycleNoncurrentTransitionXML struct {
	NoncurrentDays          *int   `xml:"NoncurrentDays"`
	NewerNoncurrentVersions *int   `xml:"NewerNoncurrentVersions,omitempty"`
	StorageClass            string `xml:"StorageClass"`
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

	w.Header().Set(transitionDefaultMinimumHeader, cfg.transitionDefaultMinimum())
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

	minimum, aerr := parseTransitionDefaultMinimum(r.Header.Get(transitionDefaultMinimumHeader))
	if aerr != nil {
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
	cfg.TransitionDefaultMinimumObjectSize = minimum

	if aerr := h.store.putLifecycle(r.Context(), bucket, cfg); aerr != nil {
		protocol.WriteXMLError(w, r, aerr)
		return
	}
	h.invalidateLifecycleIndex(r)

	// PutBucketLifecycleConfigurationOutput carries the behaviour in force, so
	// a caller that omitted the header still learns which default applies.
	w.Header().Set(transitionDefaultMinimumHeader, cfg.transitionDefaultMinimum())
	w.WriteHeader(http.StatusOK)
}

// parseTransitionDefaultMinimum validates the
// x-amz-transition-default-minimum-object-size header. An absent header is
// legal and stores nothing, which reads back as AWS's default; a value outside
// com.amazonaws.s3#TransitionDefaultMinimumObjectSize is refused rather than
// stored, because storing it would claim a transition behaviour the sweeper
// cannot apply.
func parseTransitionDefaultMinimum(raw string) (string, *protocol.AWSError) {
	switch strings.TrimSpace(raw) {
	case "":
		return "", nil
	case transitionDefaultMinimumAll128K:
		return transitionDefaultMinimumAll128K, nil
	case transitionDefaultMinimumVaries:
		return transitionDefaultMinimumVaries, nil
	default:
		return "", protocol.ErrInvalidArgument(fmt.Sprintf(
			"The %s header value must be %s or %s",
			transitionDefaultMinimumHeader, transitionDefaultMinimumAll128K, transitionDefaultMinimumVaries))
	}
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

	noncurrentExpiration, aerr := parseLifecycleNoncurrentExpiration(in.NoncurrentVersionExpiration, in.Filter != nil)
	if aerr != nil {
		return nil, aerr
	}
	out.NoncurrentVersionExpiration = noncurrentExpiration

	for i := range in.Transition {
		transition, tErr := parseLifecycleTransition(&in.Transition[i])
		if tErr != nil {
			return nil, tErr
		}
		out.Transitions = append(out.Transitions, *transition)
	}

	for i := range in.NoncurrentVersionTransition {
		transition, tErr := parseLifecycleNoncurrentTransition(&in.NoncurrentVersionTransition[i], in.Filter != nil)
		if tErr != nil {
			return nil, tErr
		}
		out.NoncurrentVersionTransitions = append(out.NoncurrentVersionTransitions, *transition)
	}

	if in.AbortIncompleteMultipartUpload != nil {
		days := in.AbortIncompleteMultipartUpload.DaysAfterInitiation
		if days == nil || *days <= 0 {
			return nil, protocol.ErrInvalidArgument(
				"'DaysAfterInitiation' for AbortIncompleteMultipartUpload action must be a positive integer")
		}
		out.AbortIncompleteMultipartUpload = &LifecycleAbortMPU{DaysAfterInitiation: *days}
	}

	if out.Expiration == nil && out.NoncurrentVersionExpiration == nil && len(out.Transitions) == 0 &&
		len(out.NoncurrentVersionTransitions) == 0 && out.AbortIncompleteMultipartUpload == nil {
		return nil, &protocol.AWSError{
			Code:       "InvalidRequest",
			Message:    "At least one action needs to be specified in a rule",
			HTTPStatus: http.StatusBadRequest,
		}
	}
	return out, nil
}

func parseLifecycleNoncurrentExpiration(in *lifecycleNoncurrentExpirationXML, hasFilter bool) (*LifecycleNoncurrentVersionExpiration, *protocol.AWSError) {
	if in == nil {
		return nil, nil
	}
	if in.NoncurrentDays == nil || *in.NoncurrentDays <= 0 {
		return nil, protocol.ErrInvalidArgument(
			"'NoncurrentDays' for NoncurrentVersionExpiration action must be a positive integer")
	}
	if newer := in.NewerNoncurrentVersions; newer != nil {
		if aerr := validateNewerNoncurrentVersions(*newer, hasFilter, "NoncurrentVersionExpiration"); aerr != nil {
			return nil, aerr
		}
	}
	return &LifecycleNoncurrentVersionExpiration{
		NoncurrentDays:          *in.NoncurrentDays,
		NewerNoncurrentVersions: in.NewerNoncurrentVersions,
	}, nil
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
	// ExpiredObjectDeleteMarker is a third, mutually exclusive form of
	// Expiration: it names no age at all, it only says "clean up a delete
	// marker that is hiding nothing". AWS refuses it alongside Days or Date.
	if in.ExpiredObjectDeleteMarker != nil && *in.ExpiredObjectDeleteMarker {
		if in.Days != nil || hasDate {
			return nil, &protocol.AWSError{
				Code:       "InvalidRequest",
				Message:    "'ExpiredObjectDeleteMarker' cannot be specified with 'Days' or 'Date' in a Lifecycle Expiration Policy",
				HTTPStatus: http.StatusBadRequest,
			}
		}
		return &LifecycleExpiration{ExpiredObjectDeleteMarker: true}, nil
	}

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

// parseLifecycleNoncurrentTransition validates a NoncurrentVersionTransition:
// a storage class from the transition set, a positive NoncurrentDays, and the
// same NewerNoncurrentVersions bounds AWS puts on NoncurrentVersionExpiration.
func parseLifecycleNoncurrentTransition(in *lifecycleNoncurrentTransitionXML, hasFilter bool) (*LifecycleNoncurrentVersionTransition, *protocol.AWSError) {
	if !lifecycleTransitionStorageClasses[in.StorageClass] {
		return nil, errMalformedLifecycleXML(
			"NoncurrentVersionTransition StorageClass is not a valid value: " + in.StorageClass)
	}
	if in.NoncurrentDays == nil || *in.NoncurrentDays <= 0 {
		return nil, protocol.ErrInvalidArgument(
			"'NoncurrentDays' for NoncurrentVersionTransition action must be a positive integer")
	}
	if newer := in.NewerNoncurrentVersions; newer != nil {
		if aerr := validateNewerNoncurrentVersions(*newer, hasFilter, "NoncurrentVersionTransition"); aerr != nil {
			return nil, aerr
		}
	}
	return &LifecycleNoncurrentVersionTransition{
		NoncurrentDays:          *in.NoncurrentDays,
		NewerNoncurrentVersions: in.NewerNoncurrentVersions,
		StorageClass:            in.StorageClass,
	}, nil
}

// validateNewerNoncurrentVersions applies the bounds AWS puts on the retained-
// version count, shared by both noncurrent actions that accept it.
func validateNewerNoncurrentVersions(newer int, hasFilter bool, action string) *protocol.AWSError {
	if newer < 1 || newer > 100 {
		return protocol.ErrInvalidArgument(
			"'NewerNoncurrentVersions' for " + action + " action must be between 1 and 100")
	}
	if !hasFilter {
		return &protocol.AWSError{
			Code:       "InvalidRequest",
			Message:    "A Filter must be specified when NewerNoncurrentVersions is specified for " + action,
			HTTPStatus: http.StatusBadRequest,
		}
	}
	return nil
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
			switch {
			case rule.Expiration.ExpiredObjectDeleteMarker:
				marker := true
				xmlRule.Expiration.ExpiredObjectDeleteMarker = &marker
			case rule.Expiration.Date != nil:
				xmlRule.Expiration.Date = rule.Expiration.Date.UTC().Format("2006-01-02T15:04:05.000Z")
			default:
				days := rule.Expiration.Days
				xmlRule.Expiration.Days = &days
			}
		}
		if rule.NoncurrentVersionExpiration != nil {
			days := rule.NoncurrentVersionExpiration.NoncurrentDays
			xmlRule.NoncurrentVersionExpiration = &lifecycleNoncurrentExpirationXML{
				NoncurrentDays:          &days,
				NewerNoncurrentVersions: rule.NoncurrentVersionExpiration.NewerNoncurrentVersions,
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
		for j := range rule.NoncurrentVersionTransitions {
			t := rule.NoncurrentVersionTransitions[j]
			days := t.NoncurrentDays
			xmlRule.NoncurrentVersionTransition = append(xmlRule.NoncurrentVersionTransition, lifecycleNoncurrentTransitionXML{
				NoncurrentDays:          &days,
				NewerNoncurrentVersions: t.NewerNoncurrentVersions,
				StorageClass:            t.StorageClass,
			})
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
