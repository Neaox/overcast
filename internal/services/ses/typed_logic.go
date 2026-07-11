package ses

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Neaox/overcast/internal/events"
	"github.com/Neaox/overcast/internal/protocol"
)

// ---- Request types ----

type sendEmailReq struct {
	Source      string       `json:"Source"`
	Destination sesEmailDest `json:"Destination"`
	Message     sesEmailMsg  `json:"Message"`
}

type sesEmailDest struct {
	ToAddresses  []string `json:"ToAddresses"`
	CcAddresses  []string `json:"CcAddresses"`
	BccAddresses []string `json:"BccAddresses"`
}

type sesEmailMsg struct {
	Subject sesEmailContent `json:"Subject"`
	Body    sesEmailBody    `json:"Body"`
}

type sesEmailContent struct {
	Data    string `json:"Data"`
	Charset string `json:"Charset"`
}

type sesEmailBody struct {
	Text *sesEmailContent `json:"Text"`
	Html *sesEmailContent `json:"Html"`
}

type sendRawEmailReq struct {
	Source       string        `json:"Source"`
	Destinations []string      `json:"Destinations"`
	RawMessage   sesRawMsgData `json:"RawMessage"`
}

type sesRawMsgData struct {
	Data string `json:"Data"`
}

type verifyEmailIdentityReq struct {
	EmailAddress string `json:"EmailAddress"`
}

type verifyDomainIdentityReq struct {
	Domain string `json:"Domain"`
}

type listIdentitiesReq struct {
	IdentityType string `json:"IdentityType"`
}

type listVerifiedEmailAddressesReq struct{}

type getIdentityVerificationAttributesReq struct {
	Identities []string `json:"Identities"`
}

type deleteIdentityReq struct {
	Identity string `json:"Identity"`
}

type createTemplateReq struct {
	Template sesTemplateData `json:"Template"`
}

type sesTemplateData struct {
	TemplateName string `json:"TemplateName"`
	SubjectPart  string `json:"SubjectPart"`
	TextPart     string `json:"TextPart"`
	HtmlPart     string `json:"HtmlPart"`
}

type getTemplateReq struct {
	TemplateName string `json:"TemplateName"`
}

type updateTemplateReq struct {
	Template sesTemplateData `json:"Template"`
}

type listTemplatesReq struct{}

type deleteTemplateReq struct {
	TemplateName string `json:"TemplateName"`
}

type sendTemplatedEmailReq struct {
	Source       string       `json:"Source"`
	Destination  sesEmailDest `json:"Destination"`
	Template     string       `json:"Template"`
	TemplateData string       `json:"TemplateData"`
}

type getSendQuotaReq struct{}

type getSendStatisticsReq struct{}

type setIdentityFeedbackForwardingEnabledReq struct {
	Identity          string `json:"Identity"`
	ForwardingEnabled bool   `json:"ForwardingEnabled"`
}

// ---- Response types ----

type sesRespMeta struct {
	RequestId string `xml:"RequestId"`
}

type sendEmailResp struct {
	XMLName struct{}        `xml:"SendEmailResponse"`
	Xmlns   string          `xml:"xmlns,attr"`
	Result  sendEmailResult `xml:"SendEmailResult"`
	Meta    sesRespMeta     `xml:"ResponseMetadata"`
}

type sendEmailResult struct {
	MessageId string `xml:"MessageId"`
}

type sendRawEmailResp struct {
	XMLName struct{}           `xml:"SendRawEmailResponse"`
	Xmlns   string             `xml:"xmlns,attr"`
	Result  sendRawEmailResult `xml:"SendRawEmailResult"`
	Meta    sesRespMeta        `xml:"ResponseMetadata"`
}

type sendRawEmailResult struct {
	MessageId string `xml:"MessageId"`
}

type verifyEmailIdentityResp struct {
	XMLName struct{}                  `xml:"VerifyEmailIdentityResponse"`
	Xmlns   string                    `xml:"xmlns,attr"`
	Result  verifyEmailIdentityResult `xml:"VerifyEmailIdentityResult"`
	Meta    sesRespMeta               `xml:"ResponseMetadata"`
}

type verifyEmailIdentityResult struct{}

type verifyDomainIdentityResp struct {
	XMLName struct{}                   `xml:"VerifyDomainIdentityResponse"`
	Xmlns   string                     `xml:"xmlns,attr"`
	Result  verifyDomainIdentityResult `xml:"VerifyDomainIdentityResult"`
	Meta    sesRespMeta                `xml:"ResponseMetadata"`
}

type verifyDomainIdentityResult struct {
	VerificationToken string `xml:"VerificationToken"`
}

type listIdentitiesResp struct {
	XMLName struct{}             `xml:"ListIdentitiesResponse"`
	Xmlns   string               `xml:"xmlns,attr"`
	Result  listIdentitiesResult `xml:"ListIdentitiesResult"`
	Meta    sesRespMeta          `xml:"ResponseMetadata"`
}

type listIdentitiesResult struct {
	Identities sesIdentityList `xml:"Identities"`
}

type sesIdentityList struct {
	Members []string `xml:"member"`
}

type listVerifiedEmailAddressesResp struct {
	XMLName struct{}                         `xml:"ListVerifiedEmailAddressesResponse"`
	Xmlns   string                           `xml:"xmlns,attr"`
	Result  listVerifiedEmailAddressesResult `xml:"ListVerifiedEmailAddressesResult"`
	Meta    sesRespMeta                      `xml:"ResponseMetadata"`
}

type listVerifiedEmailAddressesResult struct {
	VerifiedEmailAddresses sesIdentityList `xml:"VerifiedEmailAddresses"`
}

type getIdentityVerificationAttributesResp struct {
	XMLName struct{}                                `xml:"GetIdentityVerificationAttributesResponse"`
	Xmlns   string                                  `xml:"xmlns,attr"`
	Result  getIdentityVerificationAttributesResult `xml:"GetIdentityVerificationAttributesResult"`
	Meta    sesRespMeta                             `xml:"ResponseMetadata"`
}

type getIdentityVerificationAttributesResult struct {
	VerificationAttributes sesVerificationAttrs `xml:"VerificationAttributes"`
}

type sesVerificationAttrs struct {
	Entries []sesVerificationEntry `xml:"entry"`
}

type sesVerificationEntry struct {
	Key   string               `xml:"key"`
	Value sesVerificationValue `xml:"value"`
}

type sesVerificationValue struct {
	VerificationStatus string `xml:"VerificationStatus"`
	VerificationToken  string `xml:"VerificationToken,omitempty"`
}

type deleteIdentityResp struct {
	XMLName struct{}             `xml:"DeleteIdentityResponse"`
	Xmlns   string               `xml:"xmlns,attr"`
	Result  deleteIdentityResult `xml:"DeleteIdentityResult"`
	Meta    sesRespMeta          `xml:"ResponseMetadata"`
}

type deleteIdentityResult struct{}

type createTemplateResp struct {
	XMLName struct{}             `xml:"CreateTemplateResponse"`
	Xmlns   string               `xml:"xmlns,attr"`
	Result  createTemplateResult `xml:"CreateTemplateResult"`
	Meta    sesRespMeta          `xml:"ResponseMetadata"`
}

type createTemplateResult struct{}

type getTemplateResp struct {
	XMLName struct{}          `xml:"GetTemplateResponse"`
	Xmlns   string            `xml:"xmlns,attr"`
	Result  getTemplateResult `xml:"GetTemplateResult"`
	Meta    sesRespMeta       `xml:"ResponseMetadata"`
}

type getTemplateResult struct {
	Template sesTemplateXML `xml:"Template"`
}

type sesTemplateXML struct {
	TemplateName string `xml:"TemplateName"`
	SubjectPart  string `xml:"SubjectPart,omitempty"`
	TextPart     string `xml:"TextPart,omitempty"`
	HtmlPart     string `xml:"HtmlPart,omitempty"`
}

type updateTemplateResp struct {
	XMLName struct{}             `xml:"UpdateTemplateResponse"`
	Xmlns   string               `xml:"xmlns,attr"`
	Result  updateTemplateResult `xml:"UpdateTemplateResult"`
	Meta    sesRespMeta          `xml:"ResponseMetadata"`
}

type updateTemplateResult struct{}

type listTemplatesResp struct {
	XMLName struct{}            `xml:"ListTemplatesResponse"`
	Xmlns   string              `xml:"xmlns,attr"`
	Result  listTemplatesResult `xml:"ListTemplatesResult"`
	Meta    sesRespMeta         `xml:"ResponseMetadata"`
}

type listTemplatesResult struct {
	TemplatesMetadata []sesTemplateMetadataXML `xml:"TemplatesMetadata>member"`
}

type sesTemplateMetadataXML struct {
	Name             string `xml:"Name"`
	CreatedTimestamp string `xml:"CreatedTimestamp"`
}

type deleteTemplateResp struct {
	XMLName struct{}             `xml:"DeleteTemplateResponse"`
	Xmlns   string               `xml:"xmlns,attr"`
	Result  deleteTemplateResult `xml:"DeleteTemplateResult"`
	Meta    sesRespMeta          `xml:"ResponseMetadata"`
}

type deleteTemplateResult struct{}

type sendTemplatedEmailResp struct {
	XMLName struct{}                 `xml:"SendTemplatedEmailResponse"`
	Xmlns   string                   `xml:"xmlns,attr"`
	Result  sendTemplatedEmailResult `xml:"SendTemplatedEmailResult"`
	Meta    sesRespMeta              `xml:"ResponseMetadata"`
}

type sendTemplatedEmailResult struct {
	MessageId string `xml:"MessageId"`
}

type getSendQuotaResp struct {
	XMLName struct{}           `xml:"GetSendQuotaResponse"`
	Xmlns   string             `xml:"xmlns,attr"`
	Result  getSendQuotaResult `xml:"GetSendQuotaResult"`
	Meta    sesRespMeta        `xml:"ResponseMetadata"`
}

type getSendQuotaResult struct {
	Max24HourSend   string `xml:"Max24HourSend"`
	MaxSendRate     string `xml:"MaxSendRate"`
	SentLast24Hours string `xml:"SentLast24Hours"`
}

type getSendStatisticsResp struct {
	XMLName struct{}                `xml:"GetSendStatisticsResponse"`
	Xmlns   string                  `xml:"xmlns,attr"`
	Result  getSendStatisticsResult `xml:"GetSendStatisticsResult"`
	Meta    sesRespMeta             `xml:"ResponseMetadata"`
}

type getSendStatisticsResult struct {
	SendDataPoints struct{} `xml:"SendDataPoints"`
}

type setIdentityFeedbackForwardingEnabledResp struct {
	XMLName struct{}                                   `xml:"SetIdentityFeedbackForwardingEnabledResponse"`
	Xmlns   string                                     `xml:"xmlns,attr"`
	Result  setIdentityFeedbackForwardingEnabledResult `xml:"SetIdentityFeedbackForwardingEnabledResult"`
	Meta    sesRespMeta                                `xml:"ResponseMetadata"`
}

type setIdentityFeedbackForwardingEnabledResult struct{}

// ---- Helpers ----

func sesMetaFromCtx(ctx context.Context) sesRespMeta {
	return sesRespMeta{RequestId: protocol.RequestIDFromContext(ctx)}
}

// ---- Typed handler functions ----

func (h *Handler) sendEmailTyped(ctx context.Context, req *sendEmailReq) (*sendEmailResp, *protocol.AWSError) {
	if req.Source == "" {
		return nil, protocol.ErrMissingParameter("Source")
	}
	all := append(append(req.Destination.ToAddresses, req.Destination.CcAddresses...), req.Destination.BccAddresses...)
	if len(all) == 0 {
		return nil, errInvalidParam
	}
	var text, html string
	if req.Message.Body.Text != nil {
		text = req.Message.Body.Text.Data
	}
	if req.Message.Body.Html != nil {
		html = req.Message.Body.Html.Data
	}
	msgID, aerr := h.deliverEmail(req.Source, all, req.Message.Subject.Data, text, html)
	if aerr != nil {
		return nil, aerr
	}
	if h.bus != nil {
		h.bus.Publish(ctx, events.Event{Type: events.SESEmailSent, Time: h.clk.Now(), Source: "ses", Payload: events.ResourcePayload{Name: msgID}})
	}
	return &sendEmailResp{Xmlns: sesXMLNS, Result: sendEmailResult{MessageId: msgID}, Meta: sesMetaFromCtx(ctx)}, nil
}

func (h *Handler) sendRawEmailTyped(ctx context.Context, req *sendRawEmailReq) (*sendRawEmailResp, *protocol.AWSError) {
	if req.RawMessage.Data == "" {
		return nil, protocol.ErrMissingParameter("RawMessage.Data")
	}
	rawMsg, err := base64.StdEncoding.DecodeString(req.RawMessage.Data)
	if err != nil {
		return nil, &protocol.AWSError{Code: "InvalidParameterValue", Message: "RawMessage.Data is not valid base64.", HTTPStatus: 400}
	}
	from := req.Source
	to := req.Destinations
	if from == "" {
		from = parseHeaderFrom(rawMsg)
	}
	if len(to) == 0 {
		to = parseHeaderRecipients(rawMsg)
	}
	if from == "" || len(to) == 0 {
		return nil, errInvalidParam
	}
	msgID, aerr := h.deliverRaw(from, to, rawMsg)
	if aerr != nil {
		return nil, aerr
	}
	if h.bus != nil {
		h.bus.Publish(ctx, events.Event{Type: events.SESEmailSent, Time: h.clk.Now(), Source: "ses", Payload: events.ResourcePayload{Name: msgID}})
	}
	return &sendRawEmailResp{Xmlns: sesXMLNS, Result: sendRawEmailResult{MessageId: msgID}, Meta: sesMetaFromCtx(ctx)}, nil
}

func (h *Handler) verifyEmailIdentityTyped(ctx context.Context, req *verifyEmailIdentityReq) (*verifyEmailIdentityResp, *protocol.AWSError) {
	if req.EmailAddress == "" {
		return nil, protocol.ErrMissingParameter("EmailAddress")
	}
	aerr := h.sesStore.putIdentity(ctx, &VerifiedIdentity{Identity: req.EmailAddress, Type: "email", CreatedAt: h.clk.Now()})
	if aerr != nil {
		return nil, aerr
	}
	if h.bus != nil {
		h.bus.Publish(ctx, events.Event{Type: events.SESIdentityCreated, Time: h.clk.Now(), Source: "ses", Payload: events.ResourcePayload{Name: req.EmailAddress}})
	}
	return &verifyEmailIdentityResp{Xmlns: sesXMLNS, Result: verifyEmailIdentityResult{}, Meta: sesMetaFromCtx(ctx)}, nil
}

func (h *Handler) verifyDomainIdentityTyped(ctx context.Context, req *verifyDomainIdentityReq) (*verifyDomainIdentityResp, *protocol.AWSError) {
	if req.Domain == "" {
		return nil, protocol.ErrMissingParameter("Domain")
	}
	token := "overcast-verify-" + strings.ReplaceAll(req.Domain, ".", "-")
	aerr := h.sesStore.putIdentity(ctx, &VerifiedIdentity{Identity: req.Domain, Type: "domain", Token: token, CreatedAt: h.clk.Now()})
	if aerr != nil {
		return nil, aerr
	}
	if h.bus != nil {
		h.bus.Publish(ctx, events.Event{Type: events.SESIdentityCreated, Time: h.clk.Now(), Source: "ses", Payload: events.ResourcePayload{Name: req.Domain}})
	}
	return &verifyDomainIdentityResp{Xmlns: sesXMLNS, Result: verifyDomainIdentityResult{VerificationToken: token}, Meta: sesMetaFromCtx(ctx)}, nil
}

func (h *Handler) listIdentitiesTyped(ctx context.Context, req *listIdentitiesReq) (*listIdentitiesResp, *protocol.AWSError) {
	identities, aerr := h.sesStore.listIdentities(ctx)
	if aerr != nil {
		return nil, aerr
	}
	members := make([]string, 0, len(identities))
	for _, id := range identities {
		if req.IdentityType == "EmailAddress" && id.Type != "email" {
			continue
		}
		if req.IdentityType == "Domain" && id.Type != "domain" {
			continue
		}
		members = append(members, id.Identity)
	}
	return &listIdentitiesResp{Xmlns: sesXMLNS, Result: listIdentitiesResult{Identities: sesIdentityList{Members: members}}, Meta: sesMetaFromCtx(ctx)}, nil
}

func (h *Handler) listVerifiedEmailAddressesTyped(ctx context.Context, _ *listVerifiedEmailAddressesReq) (*listVerifiedEmailAddressesResp, *protocol.AWSError) {
	identities, aerr := h.sesStore.listIdentities(ctx)
	if aerr != nil {
		return nil, aerr
	}
	members := make([]string, 0, len(identities))
	for _, id := range identities {
		if id.Type == "email" {
			members = append(members, id.Identity)
		}
	}
	return &listVerifiedEmailAddressesResp{Xmlns: sesXMLNS, Result: listVerifiedEmailAddressesResult{VerifiedEmailAddresses: sesIdentityList{Members: members}}, Meta: sesMetaFromCtx(ctx)}, nil
}

func (h *Handler) getIdentityVerificationAttributesTyped(ctx context.Context, req *getIdentityVerificationAttributesReq) (*getIdentityVerificationAttributesResp, *protocol.AWSError) {
	entries := make([]sesVerificationEntry, 0, len(req.Identities))
	for _, id := range req.Identities {
		v, _ := h.sesStore.getIdentity(ctx, id)
		e := sesVerificationEntry{Key: id}
		if v != nil {
			e.Value.VerificationStatus = "Success"
			e.Value.VerificationToken = v.Token
		} else {
			e.Value.VerificationStatus = "Pending"
		}
		entries = append(entries, e)
	}
	return &getIdentityVerificationAttributesResp{Xmlns: sesXMLNS, Result: getIdentityVerificationAttributesResult{VerificationAttributes: sesVerificationAttrs{Entries: entries}}, Meta: sesMetaFromCtx(ctx)}, nil
}

func (h *Handler) deleteIdentityTyped(ctx context.Context, req *deleteIdentityReq) (*deleteIdentityResp, *protocol.AWSError) {
	if req.Identity == "" {
		return nil, protocol.ErrMissingParameter("Identity")
	}
	if aerr := h.sesStore.deleteIdentity(ctx, req.Identity); aerr != nil {
		return nil, aerr
	}
	if h.bus != nil {
		h.bus.Publish(ctx, events.Event{Type: events.SESIdentityDeleted, Time: h.clk.Now(), Source: "ses", Payload: events.ResourcePayload{Name: req.Identity}})
	}
	return &deleteIdentityResp{Xmlns: sesXMLNS, Result: deleteIdentityResult{}, Meta: sesMetaFromCtx(ctx)}, nil
}

func (h *Handler) createTemplateTyped(ctx context.Context, req *createTemplateReq) (*createTemplateResp, *protocol.AWSError) {
	name := req.Template.TemplateName
	if name == "" {
		return nil, protocol.ErrMissingParameter("Template.TemplateName")
	}
	if _, aerr := h.sesStore.getTemplate(ctx, name); aerr == nil {
		return nil, errTemplateAlreadyExists(name)
	}
	tmpl := &Template{
		TemplateName: name,
		SubjectPart:  req.Template.SubjectPart,
		TextPart:     req.Template.TextPart,
		HtmlPart:     req.Template.HtmlPart,
		CreatedAt:    h.clk.Now(),
	}
	if aerr := h.sesStore.putTemplate(ctx, tmpl); aerr != nil {
		return nil, aerr
	}
	if h.bus != nil {
		h.bus.Publish(ctx, events.Event{Type: events.SESTemplateCreated, Time: h.clk.Now(), Source: "ses", Payload: events.ResourcePayload{Name: name}})
	}
	return &createTemplateResp{Xmlns: sesXMLNS, Result: createTemplateResult{}, Meta: sesMetaFromCtx(ctx)}, nil
}

func (h *Handler) getTemplateTyped(ctx context.Context, req *getTemplateReq) (*getTemplateResp, *protocol.AWSError) {
	if req.TemplateName == "" {
		return nil, protocol.ErrMissingParameter("TemplateName")
	}
	tmpl, aerr := h.sesStore.getTemplate(ctx, req.TemplateName)
	if aerr != nil {
		return nil, aerr
	}
	return &getTemplateResp{Xmlns: sesXMLNS, Result: getTemplateResult{Template: sesTemplateXML{
		TemplateName: tmpl.TemplateName, SubjectPart: tmpl.SubjectPart, TextPart: tmpl.TextPart, HtmlPart: tmpl.HtmlPart,
	}}, Meta: sesMetaFromCtx(ctx)}, nil
}

func (h *Handler) updateTemplateTyped(ctx context.Context, req *updateTemplateReq) (*updateTemplateResp, *protocol.AWSError) {
	name := req.Template.TemplateName
	if name == "" {
		return nil, protocol.ErrMissingParameter("Template.TemplateName")
	}
	tmpl, aerr := h.sesStore.getTemplate(ctx, name)
	if aerr != nil {
		return nil, aerr
	}
	tmpl.SubjectPart = req.Template.SubjectPart
	tmpl.TextPart = req.Template.TextPart
	tmpl.HtmlPart = req.Template.HtmlPart
	if aerr := h.sesStore.putTemplate(ctx, tmpl); aerr != nil {
		return nil, aerr
	}
	return &updateTemplateResp{Xmlns: sesXMLNS, Result: updateTemplateResult{}, Meta: sesMetaFromCtx(ctx)}, nil
}

func (h *Handler) listTemplatesTyped(ctx context.Context, _ *listTemplatesReq) (*listTemplatesResp, *protocol.AWSError) {
	templates, aerr := h.sesStore.listTemplates(ctx)
	if aerr != nil {
		return nil, aerr
	}
	meta := make([]sesTemplateMetadataXML, 0, len(templates))
	for _, t := range templates {
		meta = append(meta, sesTemplateMetadataXML{
			Name:             t.TemplateName,
			CreatedTimestamp: t.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
		})
	}
	return &listTemplatesResp{Xmlns: sesXMLNS, Result: listTemplatesResult{TemplatesMetadata: meta}, Meta: sesMetaFromCtx(ctx)}, nil
}

func (h *Handler) deleteTemplateTyped(ctx context.Context, req *deleteTemplateReq) (*deleteTemplateResp, *protocol.AWSError) {
	if req.TemplateName == "" {
		return nil, protocol.ErrMissingParameter("TemplateName")
	}
	if aerr := h.sesStore.deleteTemplate(ctx, req.TemplateName); aerr != nil {
		return nil, aerr
	}
	if h.bus != nil {
		h.bus.Publish(ctx, events.Event{Type: events.SESTemplateDeleted, Time: h.clk.Now(), Source: "ses", Payload: events.ResourcePayload{Name: req.TemplateName}})
	}
	return &deleteTemplateResp{Xmlns: sesXMLNS, Result: deleteTemplateResult{}, Meta: sesMetaFromCtx(ctx)}, nil
}

func (h *Handler) sendTemplatedEmailTyped(ctx context.Context, req *sendTemplatedEmailReq) (*sendTemplatedEmailResp, *protocol.AWSError) {
	if req.Source == "" {
		return nil, protocol.ErrMissingParameter("Source")
	}
	all := append(append(req.Destination.ToAddresses, req.Destination.CcAddresses...), req.Destination.BccAddresses...)
	if len(all) == 0 {
		return nil, errInvalidParam
	}
	if req.Template == "" {
		return nil, protocol.ErrMissingParameter("Template")
	}
	tmpl, aerr := h.sesStore.getTemplate(ctx, req.Template)
	if aerr != nil {
		return nil, aerr
	}
	var data map[string]any
	if req.TemplateData != "" {
		_ = json.Unmarshal([]byte(req.TemplateData), &data)
	}
	pairs := make([]string, 0, len(data)*2)
	for k, v := range data {
		pairs = append(pairs, "{{"+k+"}}", fmt.Sprintf("%v", v))
	}
	rep := strings.NewReplacer(pairs...)
	subject := rep.Replace(tmpl.SubjectPart)
	text := rep.Replace(tmpl.TextPart)
	htmlBody := rep.Replace(tmpl.HtmlPart)
	msgID, deliverErr := h.deliverEmail(req.Source, all, subject, text, htmlBody)
	if deliverErr != nil {
		return nil, deliverErr
	}
	if h.bus != nil {
		h.bus.Publish(ctx, events.Event{Type: events.SESEmailSent, Time: h.clk.Now(), Source: "ses", Payload: events.ResourcePayload{Name: msgID}})
	}
	return &sendTemplatedEmailResp{Xmlns: sesXMLNS, Result: sendTemplatedEmailResult{MessageId: msgID}, Meta: sesMetaFromCtx(ctx)}, nil
}

func (h *Handler) getSendQuotaTyped(ctx context.Context, _ *getSendQuotaReq) (*getSendQuotaResp, *protocol.AWSError) {
	return &getSendQuotaResp{Xmlns: sesXMLNS, Result: getSendQuotaResult{
		Max24HourSend: "50000.0", MaxSendRate: "14.0", SentLast24Hours: "0.0",
	}, Meta: sesMetaFromCtx(ctx)}, nil
}

func (h *Handler) getSendStatisticsTyped(ctx context.Context, _ *getSendStatisticsReq) (*getSendStatisticsResp, *protocol.AWSError) {
	return &getSendStatisticsResp{Xmlns: sesXMLNS, Result: getSendStatisticsResult{}, Meta: sesMetaFromCtx(ctx)}, nil
}

func (h *Handler) setIdentityFeedbackForwardingEnabledTyped(ctx context.Context, req *setIdentityFeedbackForwardingEnabledReq) (*setIdentityFeedbackForwardingEnabledResp, *protocol.AWSError) {
	if req.Identity == "" {
		return nil, protocol.ErrMissingParameter("Identity")
	}
	return &setIdentityFeedbackForwardingEnabledResp{Xmlns: sesXMLNS, Result: setIdentityFeedbackForwardingEnabledResult{}, Meta: sesMetaFromCtx(ctx)}, nil
}
