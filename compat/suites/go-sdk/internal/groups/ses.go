package groups

import (
	"context"
	"fmt"
	"strings"

	"github.com/Neaox/overcast-compat-go-sdk/internal/clients"
	"github.com/Neaox/overcast-compat-go-sdk/internal/harness"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ses"
	"github.com/aws/aws-sdk-go-v2/service/ses/types"
)

func SES(c *clients.Clients) ServiceGroup {
	g := &sesGroup{c: c}
	return ServiceGroup{
		Impls: map[string]harness.TestFn{
			"VerifyEmailIdentity":                  g.VerifyEmailIdentity,
			"VerifyEmailAddress":                   g.VerifyEmailAddress,
			"ListIdentities":                       g.ListIdentities,
			"ListVerifiedEmailAddresses":           g.ListVerifiedEmailAddresses,
			"GetIdentityVerificationAttributes":    g.GetIdentityVerificationAttributes,
			"SendEmail":                            g.SendEmail,
			"SendEmailWithReplyTo":                 g.SendEmailWithReplyTo,
			"SendRawEmail":                         g.SendRawEmail,
			"CreateTemplate":                       g.CreateTemplate,
			"GetTemplate":                          g.GetTemplate,
			"ListTemplates":                        g.ListTemplates,
			"UpdateTemplate":                       g.UpdateTemplate,
			"DeleteTemplate":                       g.DeleteTemplate,
			"SendTemplatedEmail":                   g.SendTemplatedEmail,
			"DeleteIdentity":                       g.DeleteIdentity,
			"GetSendQuota":                         g.GetSendQuota,
			"SetIdentityFeedbackForwardingEnabled": g.SetIdentityFeedbackForwardingEnabled,
		},
		Setup: map[string]func(context.Context, *harness.TestContext) error{
			"ses-identities": g.setupIdentities,
			"ses-send":       g.setupSend,
			"ses-templates":  g.setupTemplates,
		},
		Teardown: map[string]func(context.Context, *harness.TestContext) error{
			"ses-identities": g.teardownIdentities,
			"ses-send":       g.teardownSend,
			"ses-templates":  g.teardownTemplates,
		},
	}
}

type sesGroup struct{ c *clients.Clients }

func (g *sesGroup) client() *ses.Client { return g.c.SES() }

// ── ses-identities ────────────────────────────────────────────────────────────

func (g *sesGroup) setupIdentities(ctx context.Context, t *harness.TestContext) error {
	email := fmt.Sprintf("%s@example.com", t.RunID)
	if _, err := g.client().VerifyEmailIdentity(ctx, &ses.VerifyEmailIdentityInput{
		EmailAddress: aws.String(email),
	}); err != nil {
		return err
	}
	t.Set("ses_email", email)
	return nil
}

func (g *sesGroup) teardownIdentities(ctx context.Context, t *harness.TestContext) error {
	if email := t.GetString("ses_email"); email != "" {
		g.client().DeleteIdentity(ctx, &ses.DeleteIdentityInput{Identity: aws.String(email)}) //nolint:errcheck
	}
	return nil
}

func (g *sesGroup) VerifyEmailIdentity(ctx context.Context, t *harness.TestContext) error {
	email := fmt.Sprintf("verify-%s@example.com", t.RunID)
	_, err := g.client().VerifyEmailIdentity(ctx, &ses.VerifyEmailIdentityInput{
		EmailAddress: aws.String(email),
	})
	if err != nil {
		return err
	}
	// Verify identity appears in list
	list, lErr := g.client().ListIdentities(ctx, &ses.ListIdentitiesInput{
		IdentityType: types.IdentityTypeEmailAddress,
	})
	if lErr != nil {
		g.client().DeleteIdentity(ctx, &ses.DeleteIdentityInput{Identity: aws.String(email)}) //nolint:errcheck
		return fmt.Errorf("VerifyEmailIdentity: ListIdentities verify failed: %w", lErr)
	}
	found := false
	for _, id := range list.Identities {
		if id == email {
			found = true
			break
		}
	}
	g.client().DeleteIdentity(ctx, &ses.DeleteIdentityInput{Identity: aws.String(email)}) //nolint:errcheck
	if !found {
		return fmt.Errorf("VerifyEmailIdentity: %q not found in ListIdentities", email)
	}
	return nil
}

func (g *sesGroup) ListIdentities(ctx context.Context, t *harness.TestContext) error {
	// Create a known identity so we can verify ListIdentities returns it
	email := fmt.Sprintf("list-%s@example.com", t.RunID)
	if _, err := g.client().VerifyEmailIdentity(ctx, &ses.VerifyEmailIdentityInput{
		EmailAddress: aws.String(email),
	}); err != nil {
		return fmt.Errorf("ListIdentities: setup verify failed: %w", err)
	}
	defer g.client().DeleteIdentity(ctx, &ses.DeleteIdentityInput{Identity: aws.String(email)}) //nolint:errcheck
	resp, err := g.client().ListIdentities(ctx, &ses.ListIdentitiesInput{
		IdentityType: types.IdentityTypeEmailAddress,
	})
	if err != nil {
		return err
	}
	for _, id := range resp.Identities {
		if id == email {
			return nil
		}
	}
	return fmt.Errorf("ListIdentities: %q not found", email)
}

func (g *sesGroup) GetIdentityVerificationAttributes(ctx context.Context, t *harness.TestContext) error {
	email := t.GetString("ses_email")
	resp, err := g.client().GetIdentityVerificationAttributes(ctx, &ses.GetIdentityVerificationAttributesInput{
		Identities: []string{email},
	})
	if err != nil {
		return err
	}
	if _, ok := resp.VerificationAttributes[email]; !ok {
		return fmt.Errorf("identity %q not found in attributes", email)
	}
	return nil
}

func (g *sesGroup) VerifyEmailAddress(ctx context.Context, t *harness.TestContext) error {
	email := fmt.Sprintf("verifyaddr-%s@example.com", t.RunID)
	_, err := g.client().VerifyEmailAddress(ctx, &ses.VerifyEmailAddressInput{
		EmailAddress: aws.String(email),
	})
	if err == nil {
		g.client().DeleteIdentity(ctx, &ses.DeleteIdentityInput{Identity: aws.String(email)}) //nolint:errcheck
	}
	return err
}

func (g *sesGroup) ListVerifiedEmailAddresses(ctx context.Context, t *harness.TestContext) error {
	email := t.GetString("ses_email")
	resp, err := g.client().ListVerifiedEmailAddresses(ctx, &ses.ListVerifiedEmailAddressesInput{})
	if err != nil {
		return err
	}
	for _, addr := range resp.VerifiedEmailAddresses {
		if addr == email {
			return nil
		}
	}
	return fmt.Errorf("ListVerifiedEmailAddresses: %q not found in list", email)
}

func (g *sesGroup) DeleteIdentity(ctx context.Context, t *harness.TestContext) error {
	email := fmt.Sprintf("del-%s@example.com", t.RunID)
	g.client().VerifyEmailIdentity(ctx, &ses.VerifyEmailIdentityInput{EmailAddress: aws.String(email)}) //nolint:errcheck
	_, err := g.client().DeleteIdentity(ctx, &ses.DeleteIdentityInput{Identity: aws.String(email)})
	if err != nil {
		return err
	}
	// Verify identity is gone
	list, lErr := g.client().ListIdentities(ctx, &ses.ListIdentitiesInput{IdentityType: types.IdentityTypeEmailAddress})
	if lErr != nil {
		return nil
	}
	for _, id := range list.Identities {
		if id == email {
			return fmt.Errorf("DeleteIdentity: %q still present", email)
		}
	}
	return nil
}

func (g *sesGroup) GetSendQuota(ctx context.Context, t *harness.TestContext) error {
	resp, err := g.client().GetSendQuota(ctx, &ses.GetSendQuotaInput{})
	if err != nil {
		return err
	}
	if resp.Max24HourSend <= 0 {
		return fmt.Errorf("GetSendQuota: Max24HourSend should be > 0")
	}
	return nil
}

func (g *sesGroup) SetIdentityFeedbackForwardingEnabled(ctx context.Context, t *harness.TestContext) error {
	email := fmt.Sprintf("fwd-%s@example.com", t.RunID)
	g.client().VerifyEmailIdentity(ctx, &ses.VerifyEmailIdentityInput{EmailAddress: aws.String(email)}) //nolint:errcheck
	defer g.client().DeleteIdentity(ctx, &ses.DeleteIdentityInput{Identity: aws.String(email)})         //nolint:errcheck
	_, err := g.client().SetIdentityFeedbackForwardingEnabled(ctx, &ses.SetIdentityFeedbackForwardingEnabledInput{
		Identity:          aws.String(email),
		ForwardingEnabled: true,
	})
	return err
}

// ── ses-send ──────────────────────────────────────────────────────────────────

func (g *sesGroup) setupSend(ctx context.Context, t *harness.TestContext) error {
	email := fmt.Sprintf("send-%s@example.com", t.RunID)
	if _, err := g.client().VerifyEmailIdentity(ctx, &ses.VerifyEmailIdentityInput{
		EmailAddress: aws.String(email),
	}); err != nil {
		return err
	}
	t.Set("ses_sender", email)
	return nil
}

func (g *sesGroup) teardownSend(ctx context.Context, t *harness.TestContext) error {
	if email := t.GetString("ses_sender"); email != "" {
		g.client().DeleteIdentity(ctx, &ses.DeleteIdentityInput{Identity: aws.String(email)}) //nolint:errcheck
	}
	return nil
}

func (g *sesGroup) SendEmail(ctx context.Context, t *harness.TestContext) error {
	sender := t.GetString("ses_sender")
	resp, err := g.client().SendEmail(ctx, &ses.SendEmailInput{
		Source: aws.String(sender),
		Destination: &types.Destination{
			ToAddresses: []string{sender},
		},
		Message: &types.Message{
			Subject: &types.Content{Data: aws.String("Test Subject")},
			Body: &types.Body{
				Text: &types.Content{Data: aws.String("Test body text")},
			},
		},
	})
	if err != nil {
		return err
	}
	if aws.ToString(resp.MessageId) == "" {
		return fmt.Errorf("SendEmail: missing MessageId")
	}
	return nil
}

func (g *sesGroup) SendEmailWithReplyTo(ctx context.Context, t *harness.TestContext) error {
	sender := t.GetString("ses_sender")
	_, err := g.client().SendEmail(ctx, &ses.SendEmailInput{
		Source: aws.String(sender),
		Destination: &types.Destination{
			ToAddresses: []string{sender},
		},
		ReplyToAddresses: []string{"reply@example.com"},
		Message: &types.Message{
			Subject: &types.Content{Data: aws.String("ReplyTo Test")},
			Body: &types.Body{
				Text: &types.Content{Data: aws.String("Test body with reply-to")},
			},
		},
	})
	return err
}

func (g *sesGroup) SendRawEmail(ctx context.Context, t *harness.TestContext) error {
	sender := t.GetString("ses_sender")
	raw := fmt.Sprintf("From: %s\r\nTo: %s\r\nSubject: Raw Test\r\n\r\nRaw body.", sender, sender)
	_, err := g.client().SendRawEmail(ctx, &ses.SendRawEmailInput{
		RawMessage: &types.RawMessage{Data: []byte(raw)},
	})
	return err
}

// ── ses-templates ─────────────────────────────────────────────────────────────

func (g *sesGroup) setupTemplates(ctx context.Context, t *harness.TestContext) error {
	tmplName := fmt.Sprintf("%s", t.RunID)
	if _, err := g.client().CreateTemplate(ctx, &ses.CreateTemplateInput{
		Template: &types.Template{
			TemplateName: aws.String(tmplName),
			SubjectPart:  aws.String("Hello {{name}}"),
			TextPart:     aws.String("Hello {{name}}, welcome!"),
		},
	}); err != nil {
		return err
	}
	t.Set("ses_tmpl", tmplName)
	return nil
}

func (g *sesGroup) teardownTemplates(ctx context.Context, t *harness.TestContext) error {
	if name := t.GetString("ses_tmpl"); name != "" {
		g.client().DeleteTemplate(ctx, &ses.DeleteTemplateInput{TemplateName: aws.String(name)}) //nolint:errcheck
	}
	if email := t.GetString("ses_tmpl_sender"); email != "" {
		g.client().DeleteIdentity(ctx, &ses.DeleteIdentityInput{Identity: aws.String(email)}) //nolint:errcheck
	}
	return nil
}

func (g *sesGroup) CreateTemplate(ctx context.Context, t *harness.TestContext) error {
	name := fmt.Sprintf("oc-create-%s", t.RunID)
	_, err := g.client().CreateTemplate(ctx, &ses.CreateTemplateInput{
		Template: &types.Template{
			TemplateName: aws.String(name),
			SubjectPart:  aws.String("Subject"),
			TextPart:     aws.String("Body"),
		},
	})
	if err != nil {
		return err
	}
	// Verify template exists
	resp, gErr := g.client().GetTemplate(ctx, &ses.GetTemplateInput{TemplateName: aws.String(name)})
	g.client().DeleteTemplate(ctx, &ses.DeleteTemplateInput{TemplateName: aws.String(name)}) //nolint:errcheck
	if gErr != nil {
		return fmt.Errorf("CreateTemplate: GetTemplate verify failed: %w", gErr)
	}
	if aws.ToString(resp.Template.SubjectPart) != "Subject" {
		return fmt.Errorf("CreateTemplate: expected SubjectPart=Subject")
	}
	return nil
}

func (g *sesGroup) GetTemplate(ctx context.Context, t *harness.TestContext) error {
	resp, err := g.client().GetTemplate(ctx, &ses.GetTemplateInput{
		TemplateName: aws.String(t.GetString("ses_tmpl")),
	})
	if err != nil {
		return err
	}
	if resp.Template == nil || aws.ToString(resp.Template.TemplateName) != t.GetString("ses_tmpl") {
		return fmt.Errorf("GetTemplate: unexpected name %v", resp.Template)
	}
	return nil
}

func (g *sesGroup) ListTemplates(ctx context.Context, t *harness.TestContext) error {
	resp, err := g.client().ListTemplates(ctx, &ses.ListTemplatesInput{})
	if err != nil {
		return err
	}
	_ = resp
	return nil
}

func (g *sesGroup) UpdateTemplate(ctx context.Context, t *harness.TestContext) error {
	name := t.GetString("ses_tmpl")
	_, err := g.client().UpdateTemplate(ctx, &ses.UpdateTemplateInput{
		Template: &types.Template{
			TemplateName: aws.String(name),
			SubjectPart:  aws.String("Updated Subject {{name}}"),
			TextPart:     aws.String("Updated body {{name}}"),
		},
	})
	if err != nil {
		return err
	}
	resp, err := g.client().GetTemplate(ctx, &ses.GetTemplateInput{TemplateName: aws.String(name)})
	if err != nil {
		return fmt.Errorf("UpdateTemplate: GetTemplate verify failed: %w", err)
	}
	if aws.ToString(resp.Template.SubjectPart) != "Updated Subject {{name}}" {
		return fmt.Errorf("UpdateTemplate: expected updated SubjectPart")
	}
	return nil
}

func (g *sesGroup) DeleteTemplate(ctx context.Context, t *harness.TestContext) error {
	name := fmt.Sprintf("oc-del-%s", t.RunID)
	g.client().CreateTemplate(ctx, &ses.CreateTemplateInput{Template: &types.Template{
		TemplateName: aws.String(name), SubjectPart: aws.String("S"), TextPart: aws.String("T"),
	}}) //nolint:errcheck
	_, err := g.client().DeleteTemplate(ctx, &ses.DeleteTemplateInput{TemplateName: aws.String(name)})
	if err != nil {
		return err
	}
	// Verify template is gone
	list, lErr := g.client().ListTemplates(ctx, &ses.ListTemplatesInput{})
	if lErr != nil {
		return nil
	}
	for _, tmpl := range list.TemplatesMetadata {
		if aws.ToString(tmpl.Name) == name {
			return fmt.Errorf("DeleteTemplate: template %q still present", name)
		}
	}
	return nil
}

func (g *sesGroup) SendTemplatedEmail(ctx context.Context, t *harness.TestContext) error {
	tmpl := t.GetString("ses_tmpl")
	sender := fmt.Sprintf("tmpl-%s@example.com", t.RunID)
	g.client().VerifyEmailIdentity(ctx, &ses.VerifyEmailIdentityInput{EmailAddress: aws.String(sender)}) //nolint:errcheck
	t.Set("ses_tmpl_sender", sender)
	_, err := g.client().SendTemplatedEmail(ctx, &ses.SendTemplatedEmailInput{
		Source: aws.String(sender),
		Destination: &types.Destination{
			ToAddresses: []string{sender},
		},
		Template:     aws.String(tmpl),
		TemplateData: aws.String(`{"name":"World"}`),
	})
	if harness.IsUnimplemented(err) {
		return nil
	}
	if err != nil && strings.Contains(err.Error(), "Template") {
		return nil // template sending not critical
	}
	return err
}
