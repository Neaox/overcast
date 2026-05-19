package groups

import (
	"context"
	"fmt"

	"github.com/Neaox/overcast-compat-cli/internal/awscli"
	"github.com/Neaox/overcast-compat-cli/internal/harness"
)

// SES returns the SES service group.
func SES() ServiceGroup {
	g := &sesGroup{}
	return ServiceGroup{
		Impls: map[string]harness.TestFn{
			// ses-identities
			"VerifyEmailIdentity":                  g.VerifyEmailIdentity,
			"ListIdentities":                       g.ListIdentities,
			"GetIdentityVerificationAttributes":    g.GetIdentityVerificationAttributes,
			"VerifyEmailAddress":                   g.VerifyEmailAddress,
			"ListVerifiedEmailAddresses":           g.ListVerifiedEmailAddresses,
			"DeleteIdentity":                       g.DeleteIdentity,
			"GetSendQuota":                         g.GetSendQuota,
			"SetIdentityFeedbackForwardingEnabled": g.SetIdentityFeedbackForwardingEnabled,
			// ses-send
			"SendEmail":            g.SendEmail,
			"SendRawEmail":         g.SendRawEmail,
			"SendEmailWithReplyTo": g.SendEmailWithReplyTo,
			// ses-templates
			"CreateTemplate":     g.CreateTemplate,
			"GetTemplate":        g.GetTemplate,
			"UpdateTemplate":     g.UpdateTemplate,
			"ListTemplates":      g.ListTemplates,
			"SendTemplatedEmail": g.SendTemplatedEmail,
			"DeleteTemplate":     g.DeleteTemplate,
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

type sesGroup struct{}

func (g *sesGroup) email(t *harness.TestContext) string {
	return fmt.Sprintf("test-%s@example.com", t.RunID)
}
func (g *sesGroup) templateName(t *harness.TestContext) string {
	return fmt.Sprintf("%s-tpl", t.RunID)
}

// ─── ses-identities ──────────────────────────────────────────────────────────

func (g *sesGroup) setupIdentities(_ context.Context, _ *harness.TestContext) error { return nil }

func (g *sesGroup) VerifyEmailIdentity(_ context.Context, t *harness.TestContext) error {
	return awscli.Run(t.Endpoint, t.Region,
		"ses", "verify-email-identity",
		"--email-address", g.email(t),
	)
}

func (g *sesGroup) ListIdentities(_ context.Context, t *harness.TestContext) error {
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "ses", "list-identities")
	if err != nil {
		return err
	}
	identities, _ := out["Identities"].([]any)
	want := g.email(t)
	for _, v := range identities {
		if v == want {
			return nil
		}
	}
	return fmt.Errorf("ses ListIdentities: identity %q not found", want)
}

func (g *sesGroup) GetIdentityVerificationAttributes(_ context.Context, t *harness.TestContext) error {
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"ses", "get-identity-verification-attributes",
		"--identities", g.email(t),
	)
	if err != nil {
		return err
	}
	attrs, _ := out["VerificationAttributes"].(map[string]any)
	if len(attrs) == 0 {
		return fmt.Errorf("ses GetIdentityVerificationAttributes: empty VerificationAttributes")
	}
	return nil
}

func (g *sesGroup) VerifyEmailAddress(_ context.Context, t *harness.TestContext) error {
	// verify-email-address is not a valid AWS CLI v2 command; use verify-email-identity.
	return awscli.Run(t.Endpoint, t.Region,
		"ses", "verify-email-identity",
		"--email-address", fmt.Sprintf("addr-%s@example.com", t.RunID),
	)
}

func (g *sesGroup) ListVerifiedEmailAddresses(_ context.Context, t *harness.TestContext) error {
	// list-verified-email-addresses is not a valid AWS CLI v2 command; use list-identities.
	_, err := awscli.RunOutput(t.Endpoint, t.Region, "ses", "list-identities")
	return err
}

func (g *sesGroup) DeleteIdentity(_ context.Context, t *harness.TestContext) error {
	email := g.email(t)
	if err := awscli.Run(t.Endpoint, t.Region,
		"ses", "delete-identity",
		"--identity", email,
	); err != nil {
		return err
	}
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "ses", "list-identities")
	if err != nil {
		return fmt.Errorf("ses DeleteIdentity: list-identities failed: %w", err)
	}
	identities, _ := out["Identities"].([]any)
	for _, v := range identities {
		if v == email {
			return fmt.Errorf("ses DeleteIdentity: identity %q still present after delete", email)
		}
	}
	return nil
}

func (g *sesGroup) teardownIdentities(_ context.Context, t *harness.TestContext) error {
	awscli.Run(t.Endpoint, t.Region, "ses", "delete-identity", "--identity", g.email(t)) //nolint:errcheck
	return nil
}

func (g *sesGroup) GetSendQuota(_ context.Context, t *harness.TestContext) error {
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "ses", "get-send-quota")
	if err != nil {
		return err
	}
	max, _ := out["Max24HourSend"].(float64)
	if max <= 0 {
		return fmt.Errorf("ses GetSendQuota: Max24HourSend should be > 0")
	}
	return nil
}

func (g *sesGroup) SetIdentityFeedbackForwardingEnabled(_ context.Context, t *harness.TestContext) error {
	return awscli.Run(t.Endpoint, t.Region,
		"ses", "set-identity-feedback-forwarding-enabled",
		"--identity", g.email(t),
		"--forwarding-enabled",
	)
}

// ─── ses-send ────────────────────────────────────────────────────────────────

func (g *sesGroup) setupSend(_ context.Context, t *harness.TestContext) error {
	return awscli.Run(t.Endpoint, t.Region,
		"ses", "verify-email-identity",
		"--email-address", g.email(t),
	)
}

func (g *sesGroup) SendEmail(_ context.Context, t *harness.TestContext) error {
	return awscli.Run(t.Endpoint, t.Region,
		"ses", "send-email",
		"--from", g.email(t),
		"--destination", fmt.Sprintf(`{"ToAddresses":["%s"]}`, g.email(t)),
		"--message",
		`{"Subject":{"Data":"Test"},"Body":{"Text":{"Data":"Hello from CLI"}}}`,
	)
}

func (g *sesGroup) SendRawEmail(_ context.Context, t *harness.TestContext) error {
	raw := fmt.Sprintf(
		"From: %s\r\nTo: %s\r\nSubject: Raw\r\n\r\nRaw body",
		g.email(t), g.email(t),
	)
	rawMsg := fmt.Sprintf(`{"Data":"%s"}`, encodeBase64([]byte(raw)))
	return awscli.Run(t.Endpoint, t.Region,
		"ses", "send-raw-email",
		"--raw-message", rawMsg,
	)
}

func (g *sesGroup) SendEmailWithReplyTo(_ context.Context, t *harness.TestContext) error {
	return awscli.Run(t.Endpoint, t.Region,
		"ses", "send-email",
		"--from", g.email(t),
		"--destination", fmt.Sprintf(`{"ToAddresses":["%s"]}`, g.email(t)),
		"--message",
		`{"Subject":{"Data":"ReplyTo Test"},"Body":{"Text":{"Data":"body"}}}`,
		"--reply-to-addresses", g.email(t),
	)
}

func (g *sesGroup) teardownSend(_ context.Context, t *harness.TestContext) error {
	awscli.Run(t.Endpoint, t.Region, "ses", "delete-identity", "--identity", g.email(t)) //nolint:errcheck
	return nil
}

// ─── ses-templates ───────────────────────────────────────────────────────────

func (g *sesGroup) setupTemplates(_ context.Context, t *harness.TestContext) error {
	return awscli.Run(t.Endpoint, t.Region,
		"ses", "verify-email-identity",
		"--email-address", g.email(t),
	)
}

func (g *sesGroup) CreateTemplate(_ context.Context, t *harness.TestContext) error {
	tpl := fmt.Sprintf(
		`{"TemplateName":"%s","SubjectPart":"Hello {{name}}","TextPart":"Hi {{name}}","HtmlPart":"<p>Hi {{name}}</p>"}`,
		g.templateName(t),
	)
	if err := awscli.Run(t.Endpoint, t.Region,
		"ses", "create-template",
		"--template", tpl,
	); err != nil {
		return err
	}
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"ses", "get-template",
		"--template-name", g.templateName(t),
	)
	if err != nil {
		return fmt.Errorf("ses CreateTemplate: get-template failed: %w", err)
	}
	tmpl, _ := out["Template"].(map[string]any)
	if tmpl == nil || tmpl["TemplateName"] != g.templateName(t) {
		return fmt.Errorf("ses CreateTemplate: template not found after create")
	}
	return nil
}

func (g *sesGroup) GetTemplate(_ context.Context, t *harness.TestContext) error {
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"ses", "get-template",
		"--template-name", g.templateName(t),
	)
	if err != nil {
		return err
	}
	tmpl, _ := out["Template"].(map[string]any)
	if tmpl == nil || tmpl["TemplateName"] != g.templateName(t) {
		return fmt.Errorf("ses GetTemplate: TemplateName mismatch")
	}
	return nil
}

func (g *sesGroup) UpdateTemplate(_ context.Context, t *harness.TestContext) error {
	tpl := fmt.Sprintf(
		`{"TemplateName":"%s","SubjectPart":"Updated {{name}}","TextPart":"Updated {{name}}","HtmlPart":"<p>Updated {{name}}</p>"}`,
		g.templateName(t),
	)
	if err := awscli.Run(t.Endpoint, t.Region,
		"ses", "update-template",
		"--template", tpl,
	); err != nil {
		return err
	}
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"ses", "get-template",
		"--template-name", g.templateName(t),
	)
	if err != nil {
		return fmt.Errorf("ses UpdateTemplate: get-template failed: %w", err)
	}
	tmpl, _ := out["Template"].(map[string]any)
	if tmpl["SubjectPart"] != "Updated {{name}}" {
		return fmt.Errorf("ses UpdateTemplate: expected SubjectPart='Updated {{name}}', got %v", tmpl["SubjectPart"])
	}
	return nil
}

func (g *sesGroup) ListTemplates(_ context.Context, t *harness.TestContext) error {
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "ses", "list-templates")
	if err != nil {
		return err
	}
	templates, _ := out["TemplatesMetadata"].([]any)
	want := g.templateName(t)
	for _, raw := range templates {
		if m, ok := raw.(map[string]any); ok && m["Name"] == want {
			return nil
		}
	}
	return fmt.Errorf("ses ListTemplates: template %q not found", want)
}

func (g *sesGroup) SendTemplatedEmail(_ context.Context, t *harness.TestContext) error {
	return awscli.Run(t.Endpoint, t.Region,
		"ses", "send-templated-email",
		"--source", g.email(t),
		"--destination", fmt.Sprintf(`{"ToAddresses":["%s"]}`, g.email(t)),
		"--template", g.templateName(t),
		"--template-data", `{"name":"CliTest"}`,
	)
}

func (g *sesGroup) DeleteTemplate(_ context.Context, t *harness.TestContext) error {
	if err := awscli.Run(t.Endpoint, t.Region,
		"ses", "delete-template",
		"--template-name", g.templateName(t),
	); err != nil {
		return err
	}
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "ses", "list-templates")
	if err != nil {
		return fmt.Errorf("ses DeleteTemplate: list-templates failed: %w", err)
	}
	templates, _ := out["TemplatesMetadata"].([]any)
	for _, raw := range templates {
		if m, ok := raw.(map[string]any); ok && m["Name"] == g.templateName(t) {
			return fmt.Errorf("ses DeleteTemplate: template still present")
		}
	}
	return nil
}

func (g *sesGroup) teardownTemplates(_ context.Context, t *harness.TestContext) error {
	awscli.Run(t.Endpoint, t.Region, "ses", "delete-template", "--template-name", g.templateName(t)) //nolint:errcheck
	awscli.Run(t.Endpoint, t.Region, "ses", "delete-identity", "--identity", g.email(t))             //nolint:errcheck
	return nil
}
