package ec2

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"net/http"
	"strings"

	"github.com/Neaox/overcast/internal/protocol"
)

// ── XML response types ───────────────────────────────────────────────────────

type xmlCreateKeyPairResponse struct {
	XMLName        xml.Name `xml:"CreateKeyPairResponse"`
	Xmlns          string   `xml:"xmlns,attr"`
	RequestID      string   `xml:"requestId"`
	KeyName        string   `xml:"keyName"`
	KeyFingerprint string   `xml:"keyFingerprint"`
	KeyMaterial    string   `xml:"keyMaterial"`
	KeyPairID      string   `xml:"keyPairId"`
}

type xmlDescribeKeyPairsResponse struct {
	XMLName   xml.Name         `xml:"DescribeKeyPairsResponse"`
	Xmlns     string           `xml:"xmlns,attr"`
	RequestID string           `xml:"requestId"`
	KeySet    []xmlKeyPairItem `xml:"keySet>item"`
}

type xmlKeyPairItem struct {
	KeyName        string `xml:"keyName"`
	KeyFingerprint string `xml:"keyFingerprint"`
	KeyPairID      string `xml:"keyPairId"`
}

type xmlDeleteKeyPairResponse struct {
	XMLName   xml.Name `xml:"DeleteKeyPairResponse"`
	Xmlns     string   `xml:"xmlns,attr"`
	RequestID string   `xml:"requestId"`
	Return    bool     `xml:"return"`
}

// randomFingerprint generates a dummy SSH key fingerprint.
func randomFingerprint() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	parts := make([]string, 16)
	for i, v := range b {
		parts[i] = hex.EncodeToString([]byte{v})
	}
	return strings.Join(parts, ":")
}

// dummyKeyMaterial returns a placeholder PEM-formatted key.
func dummyKeyMaterial() string {
	b := make([]byte, 64)
	_, _ = rand.Read(b)
	return fmt.Sprintf("-----BEGIN RSA PRIVATE KEY-----\n%s\n-----END RSA PRIVATE KEY-----",
		hex.EncodeToString(b))
}

// CreateKeyPair creates a new key pair and returns key material.
func (h *Handler) CreateKeyPair(w http.ResponseWriter, r *http.Request) {
	keyName := r.FormValue("KeyName")
	if keyName == "" {
		protocol.WriteEC2QueryXMLError(w, r, &protocol.AWSError{
			Code:       "MissingParameter",
			Message:    "KeyName is required",
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}

	// Check for duplicate.
	if _, aerr := h.store.getKeyPair(r.Context(), keyName); aerr == nil {
		protocol.WriteEC2QueryXMLError(w, r, &protocol.AWSError{
			Code:       "InvalidKeyPair.Duplicate",
			Message:    fmt.Sprintf("The keypair '%s' already exists", keyName),
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}

	fingerprint, material := h.keyMaterialFor()
	kpID := fmt.Sprintf("key-%s", shortID())

	kp := &KeyPair{
		KeyName:        keyName,
		KeyFingerprint: fingerprint,
		KeyPairID:      kpID,
		KeyMaterial:    material,
	}
	if aerr := h.store.putKeyPair(r.Context(), kp); aerr != nil {
		protocol.WriteEC2QueryXMLError(w, r, aerr)
		return
	}

	protocol.WriteQueryXML(w, r, http.StatusOK, &xmlCreateKeyPairResponse{
		Xmlns:          ec2XMLNS,
		RequestID:      protocol.RequestIDFromContext(r.Context()),
		KeyName:        keyName,
		KeyFingerprint: fingerprint,
		KeyMaterial:    material,
		KeyPairID:      kpID,
	})
}

// DescribeKeyPairs lists key pairs, optionally selected by KeyName.N or
// filtered.
func (h *Handler) DescribeKeyPairs(w http.ResponseWriter, r *http.Request) {
	resp, aerr := h.describeKeyPairs(r.Context(), requestQuery(r, "KeyName"))
	writeDescribe(w, r, resp, aerr)
}

func (h *Handler) describeKeyPairs(ctx context.Context, q describeQuery) (*xmlDescribeKeyPairsResponse, *protocol.AWSError) {
	filters, aerr := keyPairFilters.parse(q.filters)
	if aerr != nil {
		return nil, aerr
	}
	requested := q.ids

	all, aerr := h.store.listKeyPairs(ctx)
	if aerr != nil {
		return nil, aerr
	}

	items := make([]xmlKeyPairItem, 0, len(all))
	for _, kp := range all {
		if !requested.has(kp.KeyName) || !filters.matches(kp) {
			continue
		}
		items = append(items, xmlKeyPairItem{
			KeyName:        kp.KeyName,
			KeyFingerprint: kp.KeyFingerprint,
			KeyPairID:      kp.KeyPairID,
		})
	}

	return &xmlDescribeKeyPairsResponse{
		Xmlns:     ec2XMLNS,
		RequestID: protocol.RequestIDFromContext(ctx),
		KeySet:    items,
	}, nil
}

// DeleteKeyPair deletes a key pair by name.
func (h *Handler) DeleteKeyPair(w http.ResponseWriter, r *http.Request) {
	keyName := r.FormValue("KeyName")
	if keyName == "" {
		protocol.WriteEC2QueryXMLError(w, r, &protocol.AWSError{
			Code:       "MissingParameter",
			Message:    "KeyName is required",
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}

	// AWS DeleteKeyPair is idempotent — no error if not found.
	h.store.deleteKeyPair(r.Context(), keyName) //nolint:errcheck

	protocol.WriteQueryXML(w, r, http.StatusOK, &xmlDeleteKeyPairResponse{
		Xmlns:     ec2XMLNS,
		RequestID: protocol.RequestIDFromContext(r.Context()),
		Return:    true,
	})
}
