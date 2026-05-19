package ses

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/events"
	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/protocol/op"
	"github.com/Neaox/overcast/internal/serviceutil"
	"github.com/Neaox/overcast/internal/smtp"
	"github.com/Neaox/overcast/internal/state"
)

const sesXMLNS = "http://ses.amazonaws.com/doc/2010-12-01/"

// Handler holds SES handler dependencies.
type Handler struct {
	cfg      *config.Config
	sesStore *sesStore
	log      *serviceutil.ServiceLogger
	clk      clock.Clock
	bus      *events.Bus
	mailer   smtp.Mailer
	ops      map[string]http.HandlerFunc
	typedOp  map[string]op.Operation
}

// newHandler constructs a Handler.
func newHandler(cfg *config.Config, store state.Store, log *serviceutil.ServiceLogger, clk clock.Clock) *Handler {
	h := &Handler{
		cfg:      cfg,
		sesStore: newSESStore(store, clk, cfg.Region),
		log:      log,
		clk:      clk,
	}
	h.initOps()
	return h
}

// initOps registers every known SES v1 operation to its handler.
// Stubs live in handler_stubs.go.
func (h *Handler) initOps() {
	h.ops = map[string]http.HandlerFunc{
		// Send
		"SendEmail":    h.SendEmail,
		"SendRawEmail": h.SendRawEmail,
		// Identity management
		"VerifyEmailIdentity":               h.VerifyEmailIdentity,
		"VerifyEmailAddress":                h.VerifyEmailIdentity, // legacy alias
		"VerifyDomainIdentity":              h.VerifyDomainIdentity,
		"ListIdentities":                    h.ListIdentities,
		"ListVerifiedEmailAddresses":        h.ListVerifiedEmailAddresses,
		"GetIdentityVerificationAttributes": h.GetIdentityVerificationAttributes,
		"DeleteIdentity":                    h.DeleteIdentity,
		"DeleteVerifiedEmailAddress":        h.DeleteIdentity, // legacy alias
		// Templates
		"CreateTemplate":     h.CreateTemplate,
		"GetTemplate":        h.GetTemplate,
		"UpdateTemplate":     h.UpdateTemplate,
		"ListTemplates":      h.ListTemplates,
		"DeleteTemplate":     h.DeleteTemplate,
		"SendTemplatedEmail": h.SendTemplatedEmail,
		// Quota / stats
		"GetSendQuota":      h.GetSendQuota,
		"GetSendStatistics": h.GetSendStatisticsStub,
		// Stubs (return 501)
		"SetIdentityNotificationTopic":         h.stub,
		"SetIdentityFeedbackForwardingEnabled": h.SetIdentityFeedbackForwardingEnabled,
		"SetIdentityMailFromDomain":            h.stub,
		"GetIdentityDkimAttributes":            h.stub,
		"SetIdentityDkimEnabled":               h.stub,
		"GetIdentityNotificationAttributes":    h.stub,
		"GetIdentityMailFromDomainAttributes":  h.stub,
		"VerifyDomainDkim":                     h.stub,
		"CreateReceiptRule":                    h.stub,
		"CreateReceiptRuleSet":                 h.stub,
		"DeleteReceiptRule":                    h.stub,
		"DeleteReceiptRuleSet":                 h.stub,
		"ListReceiptRuleSets":                  h.stub,
		"DescribeReceiptRule":                  h.stub,
		"DescribeReceiptRuleSet":               h.stub,
		"CreateConfigurationSet":               h.stub,
		"DeleteConfigurationSet":               h.stub,
		"ListConfigurationSets":                h.stub,
	}
	h.typedOp = h.typedOps()
}

// ownsAction reports whether this handler recognises the given Action.
func (h *Handler) ownsAction(action string) bool {
	_, ok := h.ops[action]
	return ok
}

// setMailer sets the mailer used for email delivery.
func (h *Handler) setMailer(m smtp.Mailer) { h.mailer = m }

// publish emits an event if the bus is wired.
func (h *Handler) publish(r *http.Request, t events.Type, payload any) {
	if h.bus != nil {
		h.bus.Publish(r.Context(), events.Event{Type: t, Payload: payload})
	}
}

// dispatch routes to the correct SES v1 handler based on the Action form value.
// r.ParseForm() is called by the router before this method is invoked.
func (h *Handler) dispatch(w http.ResponseWriter, r *http.Request) {
	action := r.FormValue("Action")
	if fn, ok := h.ops[action]; ok {
		fn(w, r)
		return
	}
	protocol.WriteQueryXMLError(w, r, &protocol.AWSError{
		Code:       "InvalidAction",
		Message:    "The action " + action + " is not valid for this web service.",
		HTTPStatus: http.StatusBadRequest,
	})
}

// ─── v1 handlers ────────────────────────────────────────────────────────────

// SendEmail implements the SES v1 SendEmail operation.
// Request fields (form-encoded):
//
//	Source, Destination.ToAddresses.member.N,
//	Message.Subject.Data, Message.Body.Text.Data, Message.Body.Html.Data
func (h *Handler) SendEmail(w http.ResponseWriter, r *http.Request) {
	from := r.FormValue("Source")
	if from == "" {
		protocol.WriteQueryXMLError(w, r, protocol.ErrMissingParameter("Source"))
		return
	}
	to := collectMembers(r, "Destination.ToAddresses.member")
	cc := collectMembers(r, "Destination.CcAddresses.member")
	bcc := collectMembers(r, "Destination.BccAddresses.member")
	all := append(append(to, cc...), bcc...)
	if len(all) == 0 {
		protocol.WriteQueryXMLError(w, r, errInvalidParam)
		return
	}
	subject := r.FormValue("Message.Subject.Data")
	text := r.FormValue("Message.Body.Text.Data")
	html := r.FormValue("Message.Body.Html.Data")

	msgID, aerr := h.deliverEmail(from, all, subject, text, html)
	if aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}

	h.publish(r, events.SESEmailSent, events.ResourcePayload{Name: msgID})

	writeQueryXML(w, r, "SendEmailResponse", "SendEmailResult", struct {
		MessageId string
	}{MessageId: msgID})
}

// SendRawEmail implements the SES v1 SendRawEmail operation.
func (h *Handler) SendRawEmail(w http.ResponseWriter, r *http.Request) {
	from := r.FormValue("Source")
	to := collectMembers(r, "Destinations.member")
	rawB64 := r.FormValue("RawMessage.Data")
	if rawB64 == "" {
		protocol.WriteQueryXMLError(w, r, protocol.ErrMissingParameter("RawMessage.Data"))
		return
	}
	rawMsg, err := base64.StdEncoding.DecodeString(rawB64)
	if err != nil {
		protocol.WriteQueryXMLError(w, r, &protocol.AWSError{
			Code:       "InvalidParameterValue",
			Message:    "RawMessage.Data is not valid base64.",
			HTTPStatus: 400,
		})
		return
	}

	// If no explicit Source/Destinations, parse from the raw message headers.
	if from == "" {
		from = parseHeaderFrom(rawMsg)
	}
	if len(to) == 0 {
		to = parseHeaderRecipients(rawMsg)
	}
	if from == "" || len(to) == 0 {
		protocol.WriteQueryXMLError(w, r, errInvalidParam)
		return
	}

	msgID, aerr := h.deliverRaw(from, to, rawMsg)
	if aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}

	h.publish(r, events.SESEmailSent, events.ResourcePayload{Name: msgID})

	writeQueryXML(w, r, "SendRawEmailResponse", "SendRawEmailResult", struct {
		MessageId string
	}{MessageId: msgID})
}

// VerifyEmailIdentity marks an email address as verified.
func (h *Handler) VerifyEmailIdentity(w http.ResponseWriter, r *http.Request) {
	email := r.FormValue("EmailAddress")
	if email == "" {
		protocol.WriteQueryXMLError(w, r, protocol.ErrMissingParameter("EmailAddress"))
		return
	}
	aerr := h.sesStore.putIdentity(r.Context(), &VerifiedIdentity{
		Identity:  email,
		Type:      "email",
		CreatedAt: h.clk.Now(),
	})
	if aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}
	h.publish(r, events.SESIdentityCreated, events.ResourcePayload{Name: email})
	writeQueryXML(w, r, "VerifyEmailIdentityResponse", "VerifyEmailIdentityResult", struct{}{})
}

// VerifyDomainIdentity marks a domain as verified, returning a fake token.
func (h *Handler) VerifyDomainIdentity(w http.ResponseWriter, r *http.Request) {
	domain := r.FormValue("Domain")
	if domain == "" {
		protocol.WriteQueryXMLError(w, r, protocol.ErrMissingParameter("Domain"))
		return
	}
	token := "overcast-verify-" + strings.ReplaceAll(domain, ".", "-")
	aerr := h.sesStore.putIdentity(r.Context(), &VerifiedIdentity{
		Identity:  domain,
		Type:      "domain",
		Token:     token,
		CreatedAt: h.clk.Now(),
	})
	if aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}
	h.publish(r, events.SESIdentityCreated, events.ResourcePayload{Name: domain})
	writeQueryXML(w, r, "VerifyDomainIdentityResponse", "VerifyDomainIdentityResult", struct {
		VerificationToken string
	}{VerificationToken: token})
}

// ListIdentities returns all verified identities.
func (h *Handler) ListIdentities(w http.ResponseWriter, r *http.Request) {
	identities, aerr := h.sesStore.listIdentities(r.Context())
	if aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}
	// Optional IdentityType filter ("EmailAddress" or "Domain").
	filter := r.FormValue("IdentityType")
	members := make([]string, 0, len(identities))
	for _, id := range identities {
		if filter == "EmailAddress" && id.Type != "email" {
			continue
		}
		if filter == "Domain" && id.Type != "domain" {
			continue
		}
		members = append(members, id.Identity)
	}

	type listIdentitiesResult struct {
		Identities struct {
			Member []string `xml:"member"`
		} `xml:"Identities"`
	}
	result := listIdentitiesResult{}
	result.Identities.Member = members
	writeQueryXML(w, r, "ListIdentitiesResponse", "ListIdentitiesResult", result)
}

// ListVerifiedEmailAddresses returns all email (not domain) verified identities.
// This is the legacy v1 API — clients should prefer ListIdentities.
func (h *Handler) ListVerifiedEmailAddresses(w http.ResponseWriter, r *http.Request) {
	identities, aerr := h.sesStore.listIdentities(r.Context())
	if aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}
	members := make([]string, 0, len(identities))
	for _, id := range identities {
		if id.Type == "email" {
			members = append(members, id.Identity)
		}
	}
	type listVerifiedResult struct {
		VerifiedEmailAddresses struct {
			Member []string `xml:"member"`
		} `xml:"VerifiedEmailAddresses"`
	}
	result := listVerifiedResult{}
	result.VerifiedEmailAddresses.Member = members
	writeQueryXML(w, r, "ListVerifiedEmailAddressesResponse", "ListVerifiedEmailAddressesResult", result)
}

// GetIdentityVerificationAttributes returns verification status for identities.
func (h *Handler) GetIdentityVerificationAttributes(w http.ResponseWriter, r *http.Request) {
	requested := collectMembers(r, "Identities.member")
	type entry struct {
		Key   string `xml:"key"`
		Value struct {
			VerificationStatus string
			VerificationToken  string `xml:",omitempty"`
		} `xml:"value"`
	}
	entries := make([]entry, 0, len(requested))
	for _, id := range requested {
		v, _ := h.sesStore.getIdentity(r.Context(), id)
		e := entry{Key: id}
		if v != nil {
			e.Value.VerificationStatus = "Success"
			e.Value.VerificationToken = v.Token
		} else {
			e.Value.VerificationStatus = "Pending"
		}
		entries = append(entries, e)
	}
	type getVerificationAttrsResult struct {
		VerificationAttributes struct {
			Entry []entry `xml:"entry"`
		} `xml:"VerificationAttributes"`
	}
	r2 := getVerificationAttrsResult{}
	r2.VerificationAttributes.Entry = entries
	writeQueryXML(w, r, "GetIdentityVerificationAttributesResponse",
		"GetIdentityVerificationAttributesResult", r2)
}

// DeleteIdentity removes a verified identity.
func (h *Handler) DeleteIdentity(w http.ResponseWriter, r *http.Request) {
	identity := r.FormValue("Identity")
	if identity == "" {
		protocol.WriteQueryXMLError(w, r, protocol.ErrMissingParameter("Identity"))
		return
	}
	if aerr := h.sesStore.deleteIdentity(r.Context(), identity); aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}
	h.publish(r, events.SESIdentityDeleted, events.ResourcePayload{Name: identity})
	writeQueryXML(w, r, "DeleteIdentityResponse", "DeleteIdentityResult", struct{}{})
}

// GetSendQuota returns a generous fake quota.
func (h *Handler) GetSendQuota(w http.ResponseWriter, r *http.Request) {
	writeQueryXML(w, r, "GetSendQuotaResponse", "GetSendQuotaResult", struct {
		Max24HourSend   string
		MaxSendRate     string
		SentLast24Hours string
	}{
		Max24HourSend:   "50000.0",
		MaxSendRate:     "14.0",
		SentLast24Hours: "0.0",
	})
}

// ─── SES v2 handlers ─────────────────────────────────────────────────────────

// V2SendEmail handles POST /v2/email/outbound-emails.
func (h *Handler) V2SendEmail(w http.ResponseWriter, r *http.Request) {
	var req struct {
		FromEmailAddress string `json:"FromEmailAddress"`
		Destination      *struct {
			ToAddresses  []string `json:"ToAddresses"`
			CcAddresses  []string `json:"CcAddresses"`
			BccAddresses []string `json:"BccAddresses"`
		} `json:"Destination"`
		Content *struct {
			Simple *struct {
				Subject struct{ Data string } `json:"Subject"`
				Body    struct {
					Text *struct{ Data string } `json:"Text"`
					Html *struct{ Data string } `json:"Html"`
				} `json:"Body"`
			} `json:"Simple"`
			Raw *struct {
				Data []byte `json:"Data"` // base64 in JSON
			} `json:"Raw"`
		} `json:"Content"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}

	if req.Content == nil {
		writeV2JSONError(w, r, &protocol.AWSError{Code: "BadRequestException", Message: "Content is required.", HTTPStatus: 400})
		return
	}

	// Handle raw message.
	if req.Content.Raw != nil {
		from := req.FromEmailAddress
		var to []string
		if req.Destination != nil {
			to = append(append(req.Destination.ToAddresses, req.Destination.CcAddresses...), req.Destination.BccAddresses...)
		}
		if from == "" {
			from = parseHeaderFrom(req.Content.Raw.Data)
		}
		if len(to) == 0 {
			to = parseHeaderRecipients(req.Content.Raw.Data)
		}
		if from == "" || len(to) == 0 {
			writeV2JSONError(w, r, &protocol.AWSError{Code: "BadRequestException", Message: "Source and Destination are required.", HTTPStatus: 400})
			return
		}
		msgID, aerr := h.deliverRaw(from, to, req.Content.Raw.Data)
		if aerr != nil {
			writeV2JSONError(w, r, aerr)
			return
		}
		h.publish(r, events.SESEmailSent, events.ResourcePayload{Name: msgID})
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, `{"MessageId":%q}`, msgID)
		return
	}

	// Handle simple message.
	if req.Content.Simple == nil {
		writeV2JSONError(w, r, &protocol.AWSError{Code: "BadRequestException", Message: "Content.Simple or Content.Raw is required.", HTTPStatus: 400})
		return
	}
	from := req.FromEmailAddress
	if from == "" {
		writeV2JSONError(w, r, &protocol.AWSError{Code: "BadRequestException", Message: "FromEmailAddress is required.", HTTPStatus: 400})
		return
	}
	var all []string
	if req.Destination != nil {
		all = append(append(req.Destination.ToAddresses, req.Destination.CcAddresses...), req.Destination.BccAddresses...)
	}
	if len(all) == 0 {
		writeV2JSONError(w, r, &protocol.AWSError{Code: "BadRequestException", Message: "Destination is required.", HTTPStatus: 400})
		return
	}
	subject := req.Content.Simple.Subject.Data
	var text, html string
	if req.Content.Simple.Body.Text != nil {
		text = req.Content.Simple.Body.Text.Data
	}
	if req.Content.Simple.Body.Html != nil {
		html = req.Content.Simple.Body.Html.Data
	}
	msgID, aerr := h.deliverEmail(from, all, subject, text, html)
	if aerr != nil {
		writeV2JSONError(w, r, aerr)
		return
	}
	h.publish(r, events.SESEmailSent, events.ResourcePayload{Name: msgID})
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, `{"MessageId":%q}`, msgID)
}

// V2CreateEmailIdentity handles PUT /v2/email/identities.
func (h *Handler) V2CreateEmailIdentity(w http.ResponseWriter, r *http.Request) {
	var req struct {
		EmailIdentity string `json:"EmailIdentity"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	if req.EmailIdentity == "" {
		writeV2JSONError(w, r, &protocol.AWSError{Code: "BadRequestException", Message: "EmailIdentity is required.", HTTPStatus: 400})
		return
	}
	idType := "email"
	if !strings.Contains(req.EmailIdentity, "@") {
		idType = "domain"
	}
	aerr := h.sesStore.putIdentity(r.Context(), &VerifiedIdentity{
		Identity:  req.EmailIdentity,
		Type:      idType,
		CreatedAt: h.clk.Now(),
	})
	if aerr != nil {
		writeV2JSONError(w, r, aerr)
		return
	}
	h.publish(r, events.SESIdentityCreated, events.ResourcePayload{Name: req.EmailIdentity})
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	iTypeAPI := "EMAIL_ADDRESS"
	if idType == "domain" {
		iTypeAPI = "DOMAIN"
	}
	fmt.Fprintf(w, `{"IdentityType":%q,"VerifiedForSendingStatus":true}`, iTypeAPI)
}

// V2ListEmailIdentities handles GET /v2/email/identities.
func (h *Handler) V2ListEmailIdentities(w http.ResponseWriter, r *http.Request) {
	identities, aerr := h.sesStore.listIdentities(r.Context())
	if aerr != nil {
		writeV2JSONError(w, r, aerr)
		return
	}
	type item struct {
		IdentityName       string `json:"IdentityName"`
		IdentityType       string `json:"IdentityType"`
		SendingEnabled     bool   `json:"SendingEnabled"`
		VerificationStatus string `json:"VerificationStatus"`
	}
	items := make([]item, 0, len(identities))
	for _, id := range identities {
		iType := "EMAIL_ADDRESS"
		if id.Type == "domain" {
			iType = "DOMAIN"
		}
		items = append(items, item{
			IdentityName:       id.Identity,
			IdentityType:       iType,
			SendingEnabled:     true,
			VerificationStatus: "SUCCESS",
		})
	}
	w.Header().Set("Content-Type", "application/json")
	j, _ := json.Marshal(map[string]any{"EmailIdentities": items})
	w.WriteHeader(http.StatusOK)
	w.Write(j) //nolint:errcheck
}

// V2GetEmailIdentity handles GET /v2/email/identities/{EmailIdentity}.
func (h *Handler) V2GetEmailIdentity(w http.ResponseWriter, r *http.Request) {
	raw := chi.URLParam(r, "EmailIdentity")
	identity, err := url.PathUnescape(raw)
	if err != nil {
		identity = raw
	}
	v, aerr := h.sesStore.getIdentity(r.Context(), identity)
	if aerr != nil {
		writeV2JSONError(w, r, aerr)
		return
	}
	iType := "EMAIL_ADDRESS"
	if v.Type == "domain" {
		iType = "DOMAIN"
	}
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"IdentityType":%q,"VerifiedForSendingStatus":true,"VerificationStatus":"SUCCESS","IdentityName":%q}`,
		iType, v.Identity)
}

// V2DeleteEmailIdentity handles DELETE /v2/email/identities/{EmailIdentity}.
func (h *Handler) V2DeleteEmailIdentity(w http.ResponseWriter, r *http.Request) {
	raw := chi.URLParam(r, "EmailIdentity")
	identity, err := url.PathUnescape(raw)
	if err != nil {
		identity = raw
	}
	if aerr := h.sesStore.deleteIdentity(r.Context(), identity); aerr != nil {
		writeV2JSONError(w, r, aerr)
		return
	}
	h.publish(r, events.SESIdentityDeleted, events.ResourcePayload{Name: identity})
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("{}")) //nolint:errcheck
}

// ─── Delivery helpers ────────────────────────────────────────────────────────

func (h *Handler) deliverEmail(from string, to []string, subject, text, html string) (string, *protocol.AWSError) {
	msgID := protocol.NewRequestID()
	if h.mailer == nil {
		return msgID, nil
	}
	if err := h.mailer.Send(context.Background(), from, to, subject, text, html); err != nil {
		h.log.Error("ses: send email failed", zap.Error(err))
		return "", protocol.Wrap(protocol.ErrInternalError, err)
	}
	return msgID, nil
}

func (h *Handler) deliverRaw(from string, to []string, raw []byte) (string, *protocol.AWSError) {
	msgID := protocol.NewRequestID()
	if h.mailer == nil {
		return msgID, nil
	}
	if err := h.mailer.SendRaw(context.Background(), from, to, raw); err != nil {
		h.log.Error("ses: send raw email failed", zap.Error(err))
		return "", protocol.Wrap(protocol.ErrInternalError, err)
	}
	return msgID, nil
}

// ─── Template handlers ───────────────────────────────────────────────────────

// CreateTemplate creates a new SES email template.
func (h *Handler) CreateTemplate(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("Template.TemplateName")
	if name == "" {
		protocol.WriteQueryXMLError(w, r, protocol.ErrMissingParameter("Template.TemplateName"))
		return
	}
	// Reject if already exists.
	if _, aerr := h.sesStore.getTemplate(r.Context(), name); aerr == nil {
		protocol.WriteQueryXMLError(w, r, errTemplateAlreadyExists(name))
		return
	}
	tmpl := &Template{
		TemplateName: name,
		SubjectPart:  r.FormValue("Template.SubjectPart"),
		TextPart:     r.FormValue("Template.TextPart"),
		HtmlPart:     r.FormValue("Template.HtmlPart"),
		CreatedAt:    h.clk.Now(),
	}
	if aerr := h.sesStore.putTemplate(r.Context(), tmpl); aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}
	h.publish(r, events.SESTemplateCreated, events.ResourcePayload{Name: name})
	writeQueryXML(w, r, "CreateTemplateResponse", "CreateTemplateResult", struct{}{})
}

// GetTemplate retrieves an SES email template by name.
func (h *Handler) GetTemplate(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("TemplateName")
	if name == "" {
		protocol.WriteQueryXMLError(w, r, protocol.ErrMissingParameter("TemplateName"))
		return
	}
	tmpl, aerr := h.sesStore.getTemplate(r.Context(), name)
	if aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}
	type templateXML struct {
		TemplateName string `xml:"TemplateName"`
		SubjectPart  string `xml:"SubjectPart,omitempty"`
		TextPart     string `xml:"TextPart,omitempty"`
		HtmlPart     string `xml:"HtmlPart,omitempty"`
	}
	writeQueryXML(w, r, "GetTemplateResponse", "GetTemplateResult", struct {
		Template templateXML `xml:"Template"`
	}{Template: templateXML{
		TemplateName: tmpl.TemplateName,
		SubjectPart:  tmpl.SubjectPart,
		TextPart:     tmpl.TextPart,
		HtmlPart:     tmpl.HtmlPart,
	}})
}

// UpdateTemplate replaces an SES email template.
func (h *Handler) UpdateTemplate(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("Template.TemplateName")
	if name == "" {
		protocol.WriteQueryXMLError(w, r, protocol.ErrMissingParameter("Template.TemplateName"))
		return
	}
	// Must already exist.
	tmpl, aerr := h.sesStore.getTemplate(r.Context(), name)
	if aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}
	tmpl.SubjectPart = r.FormValue("Template.SubjectPart")
	tmpl.TextPart = r.FormValue("Template.TextPart")
	tmpl.HtmlPart = r.FormValue("Template.HtmlPart")
	if aerr := h.sesStore.putTemplate(r.Context(), tmpl); aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}
	writeQueryXML(w, r, "UpdateTemplateResponse", "UpdateTemplateResult", struct{}{})
}

// ListTemplates lists all SES email templates (name and creation time only).
func (h *Handler) ListTemplates(w http.ResponseWriter, r *http.Request) {
	templates, aerr := h.sesStore.listTemplates(r.Context())
	if aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}
	type metaXML struct {
		Name             string `xml:"Name"`
		CreatedTimestamp string `xml:"CreatedTimestamp"`
	}
	meta := make([]metaXML, 0, len(templates))
	for _, t := range templates {
		meta = append(meta, metaXML{
			Name:             t.TemplateName,
			CreatedTimestamp: t.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
		})
	}
	writeQueryXML(w, r, "ListTemplatesResponse", "ListTemplatesResult", struct {
		TemplatesMetadata []metaXML `xml:"TemplatesMetadata>member"`
	}{TemplatesMetadata: meta})
}

// DeleteTemplate deletes an SES email template.
func (h *Handler) DeleteTemplate(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("TemplateName")
	if name == "" {
		protocol.WriteQueryXMLError(w, r, protocol.ErrMissingParameter("TemplateName"))
		return
	}
	if aerr := h.sesStore.deleteTemplate(r.Context(), name); aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}
	h.publish(r, events.SESTemplateDeleted, events.ResourcePayload{Name: name})
	writeQueryXML(w, r, "DeleteTemplateResponse", "DeleteTemplateResult", struct{}{})
}

// SendTemplatedEmail renders a template and sends the message.
// Template variable substitution uses Go's strings.NewReplacer to replace
// {{key}} tokens — sufficient for the compat test suite.
func (h *Handler) SendTemplatedEmail(w http.ResponseWriter, r *http.Request) {
	from := r.FormValue("Source")
	if from == "" {
		protocol.WriteQueryXMLError(w, r, protocol.ErrMissingParameter("Source"))
		return
	}
	to := collectMembers(r, "Destination.ToAddresses.member")
	cc := collectMembers(r, "Destination.CcAddresses.member")
	bcc := collectMembers(r, "Destination.BccAddresses.member")
	all := append(append(to, cc...), bcc...)
	if len(all) == 0 {
		protocol.WriteQueryXMLError(w, r, errInvalidParam)
		return
	}
	templateName := r.FormValue("Template")
	if templateName == "" {
		protocol.WriteQueryXMLError(w, r, protocol.ErrMissingParameter("Template"))
		return
	}
	tmpl, aerr := h.sesStore.getTemplate(r.Context(), templateName)
	if aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}

	// Parse TemplateData JSON to build replacer pairs.
	var data map[string]any
	if td := r.FormValue("TemplateData"); td != "" {
		_ = json.Unmarshal([]byte(td), &data)
	}
	pairs := make([]string, 0, len(data)*2)
	for k, v := range data {
		pairs = append(pairs, "{{"+k+"}}", fmt.Sprintf("%v", v))
	}
	rep := strings.NewReplacer(pairs...)
	subject := rep.Replace(tmpl.SubjectPart)
	text := rep.Replace(tmpl.TextPart)
	htmlBody := rep.Replace(tmpl.HtmlPart)

	msgID, deliverErr := h.deliverEmail(from, all, subject, text, htmlBody)
	if deliverErr != nil {
		protocol.WriteQueryXMLError(w, r, deliverErr)
		return
	}
	h.publish(r, events.SESEmailSent, events.ResourcePayload{Name: msgID})
	writeQueryXML(w, r, "SendTemplatedEmailResponse", "SendTemplatedEmailResult", struct {
		MessageId string
	}{MessageId: msgID})
}

// ─── Wire format helpers ─────────────────────────────────────────────────────

// writeQueryXML writes an SES v1 Query-protocol XML response envelope.
// resultTag is the element name to wrap resultBody in (e.g. "SendEmailResult").
// resultBody can be any struct — its fields are serialised inside resultTag.
func writeQueryXML(w http.ResponseWriter, r *http.Request, rootTag, resultTag string, resultBody any) {
	var inner bytes.Buffer
	enc := xml.NewEncoder(&inner)
	if err := enc.EncodeElement(resultBody, xml.StartElement{Name: xml.Name{Local: resultTag}}); err == nil {
		enc.Flush()
	}
	type response struct {
		XMLName          xml.Name                  `xml:""`
		Xmlns            string                    `xml:"xmlns,attr"`
		Inner            []byte                    `xml:",innerxml"`
		ResponseMetadata protocol.ResponseMetadata `xml:"ResponseMetadata"`
	}
	protocol.WriteQueryXML(w, r, http.StatusOK, &response{
		XMLName:          xml.Name{Local: rootTag},
		Xmlns:            sesXMLNS,
		Inner:            inner.Bytes(),
		ResponseMetadata: protocol.QueryResponseMetadata(r),
	})
}

// writeV2JSONError writes an SES v2 JSON error response.
func writeV2JSONError(w http.ResponseWriter, r *http.Request, aerr *protocol.AWSError) {
	protocol.WriteJSONError(w, r, aerr)
}

// ─── Form-value helpers ──────────────────────────────────────────────────────

// collectMembers extracts form values like "Prefix.1", "Prefix.2", … and
// returns them as a slice (max 50 to prevent abuse).
func collectMembers(r *http.Request, prefix string) []string {
	var out []string
	for i := 1; i <= 50; i++ {
		v := r.FormValue(fmt.Sprintf("%s.%d", prefix, i))
		if v == "" {
			break
		}
		out = append(out, v)
	}
	return out
}

// parseHeaderFrom extracts the From address from a raw RFC 2822 message.
func parseHeaderFrom(msg []byte) string {
	for _, line := range strings.SplitN(string(msg), "\n", 100) {
		line = strings.TrimRight(line, "\r")
		if strings.EqualFold(strings.SplitN(line, ":", 2)[0], "From") {
			parts := strings.SplitN(line, ":", 2)
			if len(parts) == 2 {
				addr := strings.TrimSpace(parts[1])
				// Strip display name: "Name <addr>" → "addr"
				if i := strings.LastIndex(addr, "<"); i >= 0 {
					addr = strings.TrimRight(addr[i+1:], ">")
				}
				return strings.TrimSpace(addr)
			}
		}
	}
	return ""
}

// SetIdentityFeedbackForwardingEnabled records the forwarding preference.
// For emulation purposes this is a no-op — the call just needs to succeed.
func (h *Handler) SetIdentityFeedbackForwardingEnabled(w http.ResponseWriter, r *http.Request) {
	if r.FormValue("Identity") == "" {
		protocol.WriteQueryXMLError(w, r, protocol.ErrMissingParameter("Identity"))
		return
	}
	writeQueryXML(w, r, "SetIdentityFeedbackForwardingEnabledResponse",
		"SetIdentityFeedbackForwardingEnabledResult", struct{}{})
}

// parseHeaderRecipients extracts To/Cc recipients from a raw RFC 2822 message.
func parseHeaderRecipients(msg []byte) []string {
	var out []string
	for _, line := range strings.SplitN(string(msg), "\n", 100) {
		line = strings.TrimRight(line, "\r")
		hdr := strings.SplitN(line, ":", 2)
		if len(hdr) != 2 {
			continue
		}
		if strings.EqualFold(hdr[0], "To") || strings.EqualFold(hdr[0], "Cc") {
			for _, addr := range strings.Split(hdr[1], ",") {
				addr = strings.TrimSpace(addr)
				if i := strings.LastIndex(addr, "<"); i >= 0 {
					addr = strings.TrimRight(addr[i+1:], ">")
				}
				addr = strings.TrimSpace(addr)
				if addr != "" {
					out = append(out, addr)
				}
			}
		}
	}
	return out
}
