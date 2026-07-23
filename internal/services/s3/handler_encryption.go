package s3

import (
	"encoding/xml"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/Neaox/overcast/internal/protocol"
)

type bucketEncryptionXML struct {
	XMLName xml.Name                  `xml:"ServerSideEncryptionConfiguration"`
	Xmlns   string                    `xml:"xmlns,attr,omitempty"`
	Rules   []bucketEncryptionRuleXML `xml:"Rule"`
}

type bucketEncryptionRuleXML struct {
	ApplyServerSideEncryptionByDefault bucketEncryptionDefaultXML `xml:"ApplyServerSideEncryptionByDefault"`
	BucketKeyEnabled                   *bool                      `xml:"BucketKeyEnabled,omitempty"`
}

type bucketEncryptionDefaultXML struct {
	SSEAlgorithm   string `xml:"SSEAlgorithm"`
	KMSMasterKeyID string `xml:"KMSMasterKeyID,omitempty"`
}

func defaultBucketEncryptionRules() []BucketEncryptionRule {
	return []BucketEncryptionRule{{SSEAlgorithm: "AES256"}}
}

// GetBucketEncryption handles GET /{bucket}?encryption.
func (h *Handler) GetBucketEncryption(w http.ResponseWriter, r *http.Request) {
	bucket := chi.URLParam(r, "bucket")
	b, aerr := h.store.getBucket(r.Context(), bucket)
	if aerr != nil {
		protocol.WriteXMLError(w, r, aerr)
		return
	}
	rules := b.EncryptionRules
	if len(rules) == 0 {
		// S3 now applies SSE-S3 by default for new objects. Returning an explicit
		// AES256 config keeps CDK asset-bucket checks on the happy path.
		rules = defaultBucketEncryptionRules()
	}
	protocol.WriteXML(w, r, http.StatusOK, bucketEncryptionXML{Xmlns: "http://s3.amazonaws.com/doc/2006-03-01/", Rules: bucketEncryptionRulesToXML(rules)})
}

// PutBucketEncryption handles PUT /{bucket}?encryption.
func (h *Handler) PutBucketEncryption(w http.ResponseWriter, r *http.Request) {
	bucket := chi.URLParam(r, "bucket")
	b, aerr := h.store.getBucket(r.Context(), bucket)
	if aerr != nil {
		protocol.WriteXMLError(w, r, aerr)
		return
	}
	var cfg bucketEncryptionXML
	if err := xml.NewDecoder(r.Body).Decode(&cfg); err != nil {
		protocol.WriteXMLError(w, r, &protocol.AWSError{Code: "MalformedXML", Message: "The XML you provided was not well-formed", HTTPStatus: http.StatusBadRequest})
		return
	}
	if len(cfg.Rules) == 0 {
		protocol.WriteXMLError(w, r, &protocol.AWSError{Code: "MalformedXML", Message: "The server side encryption configuration was not found", HTTPStatus: http.StatusBadRequest})
		return
	}
	rules := make([]BucketEncryptionRule, 0, len(cfg.Rules))
	for _, rule := range cfg.Rules {
		alg := rule.ApplyServerSideEncryptionByDefault.SSEAlgorithm
		if alg != "AES256" && alg != "aws:kms" && alg != "aws:kms:dsse" {
			protocol.WriteXMLError(w, r, &protocol.AWSError{Code: "InvalidArgument", Message: "The server side encryption algorithm is invalid", HTTPStatus: http.StatusBadRequest})
			return
		}
		rules = append(rules, BucketEncryptionRule{
			SSEAlgorithm:     alg,
			KMSMasterKeyID:   rule.ApplyServerSideEncryptionByDefault.KMSMasterKeyID,
			BucketKeyEnabled: rule.BucketKeyEnabled,
		})
	}
	b.EncryptionRules = rules
	if aerr := h.store.putBucket(r.Context(), b); aerr != nil {
		protocol.WriteXMLError(w, r, aerr)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// DeleteBucketEncryption handles DELETE /{bucket}?encryption.
func (h *Handler) DeleteBucketEncryption(w http.ResponseWriter, r *http.Request) {
	bucket := chi.URLParam(r, "bucket")
	b, aerr := h.store.getBucket(r.Context(), bucket)
	if aerr != nil {
		protocol.WriteXMLError(w, r, aerr)
		return
	}
	b.EncryptionRules = nil
	if aerr := h.store.putBucket(r.Context(), b); aerr != nil {
		protocol.WriteXMLError(w, r, aerr)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func bucketEncryptionRulesToXML(rules []BucketEncryptionRule) []bucketEncryptionRuleXML {
	out := make([]bucketEncryptionRuleXML, 0, len(rules))
	for _, rule := range rules {
		out = append(out, bucketEncryptionRuleXML{
			ApplyServerSideEncryptionByDefault: bucketEncryptionDefaultXML{
				SSEAlgorithm:   rule.SSEAlgorithm,
				KMSMasterKeyID: rule.KMSMasterKeyID,
			},
			BucketKeyEnabled: rule.BucketKeyEnabled,
		})
	}
	return out
}
