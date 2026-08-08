package s3

// handler_website.go implements PutBucketWebsite, GetBucketWebsite and
// DeleteBucketWebsite, plus the validation that decides which website
// documents S3 accepts.
//
// AWS docs:
//   https://docs.aws.amazon.com/AmazonS3/latest/API/API_PutBucketWebsite.html
//   https://docs.aws.amazon.com/AmazonS3/latest/API/API_GetBucketWebsite.html
//   https://docs.aws.amazon.com/AmazonS3/latest/API/API_DeleteBucketWebsite.html
//
// The wire shapes mirror com.amazonaws.s3#WebsiteConfiguration in the pinned
// model (models/aws/VERSION): ErrorDocument, IndexDocument,
// RedirectAllRequestsTo and RoutingRules, with RoutingRules an unflattened
// list of RoutingRule elements.
//
// Overcast stores and returns the whole document but does not serve a website
// endpoint, so no request is ever redirected by it. That keeps the emulator
// truthful about the configuration a stack deployed without pretending to host
// the site — see docs/services/s3.md.

import (
	"encoding/xml"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/Neaox/overcast/internal/protocol"
)

// websiteProtocols is com.amazonaws.s3#Protocol.
var websiteProtocols = map[string]bool{"http": true, "https": true}

// ---- Wire format -----------------------------------------------------------

// websiteConfigurationXML is the WebsiteConfiguration element, used for both
// the Put request body and the Get response. Every optional element is a
// pointer or carries omitempty so an unset one is omitted rather than
// serialised empty — real SDKs reject an empty enum or required child.
type websiteConfigurationXML struct {
	XMLName               xml.Name                  `xml:"WebsiteConfiguration"`
	Xmlns                 string                    `xml:"xmlns,attr,omitempty"`
	RedirectAllRequestsTo *redirectAllRequestsToXML `xml:"RedirectAllRequestsTo,omitempty"`
	IndexDocument         *indexDocumentXML         `xml:"IndexDocument,omitempty"`
	ErrorDocument         *errorDocumentXML         `xml:"ErrorDocument,omitempty"`
	RoutingRules          *websiteRoutingRulesXML   `xml:"RoutingRules,omitempty"`
}

type indexDocumentXML struct {
	Suffix string `xml:"Suffix"`
}

type errorDocumentXML struct {
	Key string `xml:"Key"`
}

type redirectAllRequestsToXML struct {
	HostName string `xml:"HostName"`
	Protocol string `xml:"Protocol,omitempty"`
}

// websiteRoutingRulesXML is the RoutingRules wrapper. AWS does not flatten the
// list, so the RoutingRule elements are nested inside it.
type websiteRoutingRulesXML struct {
	Rules []websiteRoutingRuleXML `xml:"RoutingRule"`
}

type websiteRoutingRuleXML struct {
	Condition *websiteConditionXML `xml:"Condition,omitempty"`
	Redirect  websiteRedirectXML   `xml:"Redirect"`
}

type websiteConditionXML struct {
	HTTPErrorCodeReturnedEquals string `xml:"HttpErrorCodeReturnedEquals,omitempty"`
	KeyPrefixEquals             string `xml:"KeyPrefixEquals,omitempty"`
}

type websiteRedirectXML struct {
	HostName             string `xml:"HostName,omitempty"`
	HTTPRedirectCode     string `xml:"HttpRedirectCode,omitempty"`
	Protocol             string `xml:"Protocol,omitempty"`
	ReplaceKeyPrefixWith string `xml:"ReplaceKeyPrefixWith,omitempty"`
	ReplaceKeyWith       string `xml:"ReplaceKeyWith,omitempty"`
}

// ---- Errors ----------------------------------------------------------------

func errNoSuchWebsiteConfiguration() *protocol.AWSError {
	return &protocol.AWSError{
		Code:       "NoSuchWebsiteConfiguration",
		Message:    "The specified bucket does not have a website configuration",
		HTTPStatus: http.StatusNotFound,
	}
}

// ---- Handlers --------------------------------------------------------------

// putBucketWebsite handles PUT /{bucket}?website.
//
// The configuration replaces any previous one wholesale, as on AWS: there is
// no merge, so a redirect-only document clears an index document the bucket
// used to have.
func (h *Handler) putBucketWebsite(w http.ResponseWriter, r *http.Request) {
	bucket := chi.URLParam(r, "bucket")
	b, aerr := h.store.getBucket(r.Context(), bucket)
	if aerr != nil {
		protocol.WriteXMLError(w, r, aerr)
		return
	}

	var req websiteConfigurationXML
	if err := xml.NewDecoder(r.Body).Decode(&req); err != nil {
		protocol.WriteXMLError(w, r, &protocol.AWSError{
			Code:       "MalformedXML",
			Message:    "The XML you provided was not well-formed or did not validate against our published schema",
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}

	cfg, aerr := parseWebsiteConfiguration(&req)
	if aerr != nil {
		protocol.WriteXMLError(w, r, aerr)
		return
	}

	b.WebsiteConfig = cfg
	if aerr := h.store.putBucket(r.Context(), b); aerr != nil {
		protocol.WriteXMLError(w, r, aerr)
		return
	}
	protocol.WriteEmpty(w, r, http.StatusOK)
}

// getBucketWebsite handles GET /{bucket}?website.
func (h *Handler) getBucketWebsite(w http.ResponseWriter, r *http.Request) {
	bucket := chi.URLParam(r, "bucket")
	b, aerr := h.store.getBucket(r.Context(), bucket)
	if aerr != nil {
		protocol.WriteXMLError(w, r, aerr)
		return
	}
	if b.WebsiteConfig == nil {
		protocol.WriteXMLError(w, r, errNoSuchWebsiteConfiguration())
		return
	}
	protocol.WriteXML(w, r, http.StatusOK, websiteConfigurationToXML(b.WebsiteConfig, s3XMLNamespace))
}

// deleteBucketWebsite handles DELETE /{bucket}?website.
//
// Idempotent: AWS answers 204 whether or not a configuration was there.
func (h *Handler) deleteBucketWebsite(w http.ResponseWriter, r *http.Request) {
	bucket := chi.URLParam(r, "bucket")
	b, aerr := h.store.getBucket(r.Context(), bucket)
	if aerr != nil {
		protocol.WriteXMLError(w, r, aerr)
		return
	}
	b.WebsiteConfig = nil
	if aerr := h.store.putBucket(r.Context(), b); aerr != nil {
		protocol.WriteXMLError(w, r, aerr)
		return
	}
	protocol.WriteEmpty(w, r, http.StatusNoContent)
}

// ---- Parsing and validation ------------------------------------------------

// parseWebsiteConfiguration validates the decoded body and converts it to the
// stored model.
//
// AWS documents two mutually exclusive top-level forms: RedirectAllRequestsTo
// on its own ("If you specify this property, you can't specify any other
// property"), or an IndexDocument with an optional ErrorDocument and optional
// RoutingRules.
func parseWebsiteConfiguration(in *websiteConfigurationXML) (*WebsiteConfiguration, *protocol.AWSError) {
	if in.RedirectAllRequestsTo != nil {
		if in.IndexDocument != nil || in.ErrorDocument != nil || in.RoutingRules != nil {
			return nil, protocol.ErrInvalidArgument(
				"RedirectAllRequestsTo cannot be combined with IndexDocument, ErrorDocument or RoutingRules")
		}
		redirect, aerr := parseWebsiteRedirectAll(in.RedirectAllRequestsTo)
		if aerr != nil {
			return nil, aerr
		}
		return &WebsiteConfiguration{RedirectAllRequestsTo: redirect}, nil
	}

	if in.IndexDocument == nil {
		return nil, protocol.ErrInvalidArgument(
			"A value must be provided for either IndexDocument or RedirectAllRequestsTo")
	}
	suffix := in.IndexDocument.Suffix
	switch {
	case suffix == "":
		return nil, protocol.ErrInvalidArgument("The IndexDocument Suffix is not well formed")
	case strings.Contains(suffix, "/"):
		return nil, protocol.ErrInvalidArgument("The IndexDocument Suffix must not include a slash character")
	}

	cfg := &WebsiteConfiguration{IndexDocument: suffix}
	if in.ErrorDocument != nil {
		if in.ErrorDocument.Key == "" {
			return nil, protocol.ErrInvalidArgument("The ErrorDocument Key is required")
		}
		cfg.ErrorDocument = in.ErrorDocument.Key
	}
	if in.RoutingRules != nil {
		rules, aerr := parseWebsiteRoutingRules(in.RoutingRules.Rules)
		if aerr != nil {
			return nil, aerr
		}
		cfg.RoutingRules = rules
	}
	return cfg, nil
}

func parseWebsiteRedirectAll(in *redirectAllRequestsToXML) (*WebsiteRedirectAll, *protocol.AWSError) {
	if in.HostName == "" {
		return nil, protocol.ErrInvalidArgument("RedirectAllRequestsTo requires a HostName")
	}
	if aerr := validateWebsiteProtocol(in.Protocol); aerr != nil {
		return nil, aerr
	}
	return &WebsiteRedirectAll{HostName: in.HostName, Protocol: in.Protocol}, nil
}

func parseWebsiteRoutingRules(in []websiteRoutingRuleXML) ([]WebsiteRoutingRule, *protocol.AWSError) {
	if len(in) == 0 {
		return nil, protocol.ErrInvalidArgument("RoutingRules requires at least one RoutingRule")
	}
	out := make([]WebsiteRoutingRule, 0, len(in))
	for i := range in {
		rule, aerr := parseWebsiteRoutingRule(&in[i])
		if aerr != nil {
			return nil, aerr
		}
		out = append(out, *rule)
	}
	return out, nil
}

func parseWebsiteRoutingRule(in *websiteRoutingRuleXML) (*WebsiteRoutingRule, *protocol.AWSError) {
	out := &WebsiteRoutingRule{}

	if in.Condition != nil {
		// A Condition with neither predicate matches nothing and is not one of
		// the two forms AWS documents.
		if in.Condition.HTTPErrorCodeReturnedEquals == "" && in.Condition.KeyPrefixEquals == "" {
			return nil, protocol.ErrInvalidArgument(
				"A RoutingRule Condition requires HttpErrorCodeReturnedEquals or KeyPrefixEquals")
		}
		out.Condition = &WebsiteRoutingCondition{
			HTTPErrorCodeReturnedEquals: in.Condition.HTTPErrorCodeReturnedEquals,
			KeyPrefixEquals:             in.Condition.KeyPrefixEquals,
		}
	}

	redirect := in.Redirect
	// Every Redirect field is "not required if one of the siblings is
	// present", so a Redirect with no sibling at all names no destination.
	if redirect.HostName == "" && redirect.HTTPRedirectCode == "" && redirect.Protocol == "" &&
		redirect.ReplaceKeyPrefixWith == "" && redirect.ReplaceKeyWith == "" {
		return nil, protocol.ErrInvalidArgument(
			"A RoutingRule Redirect requires at least one of HostName, HttpRedirectCode, Protocol, ReplaceKeyPrefixWith and ReplaceKeyWith")
	}
	// "Can be present only if ReplaceKeyWith is not provided."
	if redirect.ReplaceKeyPrefixWith != "" && redirect.ReplaceKeyWith != "" {
		return nil, protocol.ErrInvalidArgument(
			"A RoutingRule Redirect cannot carry both ReplaceKeyWith and ReplaceKeyPrefixWith")
	}
	if aerr := validateWebsiteProtocol(redirect.Protocol); aerr != nil {
		return nil, aerr
	}

	// The wire and stored Redirect shapes are field-for-field identical, which
	// is exactly what AWS's model says: every member is an optional string.
	out.Redirect = WebsiteRedirect(redirect)
	return out, nil
}

// validateWebsiteProtocol enforces com.amazonaws.s3#Protocol. An empty value
// means the element was absent, which is legal everywhere it appears.
func validateWebsiteProtocol(value string) *protocol.AWSError {
	if value == "" || websiteProtocols[value] {
		return nil
	}
	return protocol.ErrInvalidArgument("Protocol must be http or https")
}

// ---- Serialisation ---------------------------------------------------------

func websiteConfigurationToXML(cfg *WebsiteConfiguration, xmlns string) *websiteConfigurationXML {
	out := &websiteConfigurationXML{Xmlns: xmlns}
	if cfg.RedirectAllRequestsTo != nil {
		out.RedirectAllRequestsTo = &redirectAllRequestsToXML{
			HostName: cfg.RedirectAllRequestsTo.HostName,
			Protocol: cfg.RedirectAllRequestsTo.Protocol,
		}
	}
	if cfg.IndexDocument != "" {
		out.IndexDocument = &indexDocumentXML{Suffix: cfg.IndexDocument}
	}
	if cfg.ErrorDocument != "" {
		out.ErrorDocument = &errorDocumentXML{Key: cfg.ErrorDocument}
	}
	if len(cfg.RoutingRules) > 0 {
		rules := &websiteRoutingRulesXML{Rules: make([]websiteRoutingRuleXML, 0, len(cfg.RoutingRules))}
		for i := range cfg.RoutingRules {
			rule := &cfg.RoutingRules[i]
			xmlRule := websiteRoutingRuleXML{Redirect: websiteRedirectXML(rule.Redirect)}
			if rule.Condition != nil {
				xmlRule.Condition = &websiteConditionXML{
					HTTPErrorCodeReturnedEquals: rule.Condition.HTTPErrorCodeReturnedEquals,
					KeyPrefixEquals:             rule.Condition.KeyPrefixEquals,
				}
			}
			rules.Rules = append(rules.Rules, xmlRule)
		}
		out.RoutingRules = rules
	}
	return out
}
