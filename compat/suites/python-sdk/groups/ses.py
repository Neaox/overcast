"""
groups/ses.py — SES compatibility test implementations for the Python suite.
"""

from __future__ import annotations
from lib.harness import TestContext
from lib.clients import make_clients


def _ses(ctx: TestContext):
    return make_clients(ctx.endpoint, ctx.region).ses


# ── ses-send ──────────────────────────────────────────────────────────────────

def SendEmail(ctx: TestContext) -> None:
    ses = _ses(ctx)
    resp = ses.send_email(
        Source="sender@example.com",
        Destination={"ToAddresses": ["recipient@example.com"]},
        Message={
            "Subject": {"Data": "Compat Test"},
            "Body": {"Text": {"Data": "Hello from Python compat test"}},
        },
    )
    assert resp.get("MessageId"), "SendEmail: missing MessageId"


def SendRawEmail(ctx: TestContext) -> None:
    ses = _ses(ctx)
    raw = (
        "From: sender@example.com\r\n"
        "To: recipient@example.com\r\n"
        "Subject: Raw Email Test\r\n"
        "\r\n"
        "Raw body from Python compat test\r\n"
    )
    resp = ses.send_raw_email(RawMessage={"Data": raw.encode()})
    assert resp.get("MessageId"), "SendRawEmail: missing MessageId"


def SendEmailWithReplyTo(ctx: TestContext) -> None:
    ses = _ses(ctx)
    resp = ses.send_email(
        Source="sender@example.com",
        Destination={"ToAddresses": ["recipient@example.com"]},
        Message={
            "Subject": {"Data": "Reply-To Test"},
            "Body": {"Text": {"Data": "Has reply-to header"}},
        },
        ReplyToAddresses=["noreply@example.com"],
    )
    assert resp.get("MessageId"), "SendEmailWithReplyTo: missing MessageId"


# ── ses-identities ────────────────────────────────────────────────────────────

def setup_ses_identities(ctx: TestContext) -> None:
    ses = _ses(ctx)
    ses.verify_email_identity(EmailAddress="test@example.com")
    ctx["ses_identity"] = "test@example.com"


def teardown_ses_identities(ctx: TestContext) -> None:
    identity = ctx.get("ses_identity")
    if identity:
        try:
            _ses(ctx).delete_identity(Identity=identity)
        except Exception:
            pass


def VerifyEmailIdentity(ctx: TestContext) -> None:
    ses = _ses(ctx)
    ses.verify_email_identity(EmailAddress="compat@example.com")
    try:
        resp = ses.get_identity_verification_attributes(Identities=["compat@example.com"])
        attrs = resp.get("VerificationAttributes", {})
        if "compat@example.com" not in attrs:
            raise AssertionError("VerifyEmailIdentity: identity not listed after verification")
    finally:
        ses.delete_identity(Identity="compat@example.com")


def ListIdentities(ctx: TestContext) -> None:
    ses = _ses(ctx)
    resp = ses.list_identities()
    if "test@example.com" not in resp.get("Identities", []):
        raise AssertionError("ListIdentities: test@example.com not found in identities")


def GetIdentityVerificationAttributes(ctx: TestContext) -> None:
    ses = _ses(ctx)
    resp = ses.get_identity_verification_attributes(Identities=["test@example.com"])
    attrs = resp.get("VerificationAttributes", {})
    if "test@example.com" not in attrs:
        raise AssertionError("GetIdentityVerificationAttributes: identity not in response")


def VerifyEmailAddress(ctx: TestContext) -> None:
    ses = _ses(ctx)
    ses.verify_email_address(EmailAddress="addr@example.com")
    try:
        resp = ses.list_verified_email_addresses()
        if "addr@example.com" not in resp.get("VerifiedEmailAddresses", []):
            raise AssertionError("VerifyEmailAddress: address not listed after verification")
    finally:
        ses.delete_identity(Identity="addr@example.com")


def ListVerifiedEmailAddresses(ctx: TestContext) -> None:
    ses = _ses(ctx)
    resp = ses.list_verified_email_addresses()
    if not isinstance(resp.get("VerifiedEmailAddresses"), list):
        raise AssertionError(f"ListVerifiedEmailAddresses: unexpected response {resp}")


def DeleteIdentity(ctx: TestContext) -> None:
    ses = _ses(ctx)
    ses.verify_email_identity(EmailAddress="del@example.com")
    ses.delete_identity(Identity="del@example.com")
    resp = ses.list_identities(IdentityType="EmailAddress")
    assert "del@example.com" not in resp.get("Identities", []), "DeleteIdentity: identity still present"


def GetSendQuota(ctx: TestContext) -> None:
    ses = _ses(ctx)
    resp = ses.get_send_quota()
    max_send = resp.get("Max24HourSend", 0)
    if max_send <= 0:
        raise AssertionError(f"GetSendQuota: Max24HourSend should be > 0, got {max_send}")


def SetIdentityFeedbackForwardingEnabled(ctx: TestContext) -> None:
    ses = _ses(ctx)
    identity = ctx.get("ses_identity", "test@example.com")
    ses.set_identity_feedback_forwarding_enabled(
        Identity=identity,
        ForwardingEnabled=True,
    )


# ── ses-templates ─────────────────────────────────────────────────────────────

def setup_ses_templates(ctx: TestContext) -> None:
    ctx["ses_template_name"] = f"{ctx.run_id}-tmpl"


def teardown_ses_templates(ctx: TestContext) -> None:
    name = ctx.get("ses_template_name")
    if name:
        try:
            _ses(ctx).delete_template(TemplateName=name)
        except Exception:
            pass


def CreateTemplate(ctx: TestContext) -> None:
    ses = _ses(ctx)
    name = ctx["ses_template_name"]
    ses.create_template(
        Template={
            "TemplateName": name,
            "SubjectPart": "Hello {{name}}",
            "TextPart": "Dear {{name}}, welcome!",
            "HtmlPart": "<p>Dear {{name}}, welcome!</p>",
        }
    )
    resp = ses.get_template(TemplateName=name)
    assert resp["Template"]["SubjectPart"] == "Hello {{name}}", "CreateTemplate: SubjectPart mismatch"


def GetTemplate(ctx: TestContext) -> None:
    ses = _ses(ctx)
    name = ctx["ses_template_name"]
    resp = ses.get_template(TemplateName=name)
    if resp.get("Template", {}).get("TemplateName") != name:
        raise AssertionError(f"GetTemplate: wrong name {resp.get('Template', {}).get('TemplateName')!r}")


def UpdateTemplate(ctx: TestContext) -> None:
    ses = _ses(ctx)
    name = ctx["ses_template_name"]
    ses.update_template(
        Template={
            "TemplateName": name,
            "SubjectPart": "Updated {{name}}",
            "TextPart": "Updated {{name}}!",
        }
    )
    resp = ses.get_template(TemplateName=name)
    assert resp["Template"]["SubjectPart"] == "Updated {{name}}", "UpdateTemplate: SubjectPart not updated"


def ListTemplates(ctx: TestContext) -> None:
    ses = _ses(ctx)
    name = ctx["ses_template_name"]
    resp = ses.list_templates()
    names = [t["Name"] for t in resp.get("TemplatesMetadata", [])]
    if name not in names:
        raise AssertionError(f"ListTemplates: {name!r} not found in templates")


def SendTemplatedEmail(ctx: TestContext) -> None:
    import json
    ses = _ses(ctx)
    name = ctx["ses_template_name"]
    resp = ses.send_templated_email(
        Source="sender@example.com",
        Destination={"ToAddresses": ["recipient@example.com"]},
        Template=name,
        TemplateData=json.dumps({"name": "World"}),
    )
    assert resp.get("MessageId"), "SendTemplatedEmail: missing MessageId"


def DeleteTemplate(ctx: TestContext) -> None:
    ses = _ses(ctx)
    name = ctx["ses_template_name"]
    ses.delete_template(TemplateName=name)
    ctx["ses_template_name"] = None  # prevent teardown double-delete
    resp = ses.list_templates()
    names = [t["Name"] for t in resp.get("TemplatesMetadata", [])]
    assert name not in names, f"DeleteTemplate: template {name} still present"


# ── ImplMap ───────────────────────────────────────────────────────────────────

IMPLS = {
    "SendEmail": SendEmail,
    "SendRawEmail": SendRawEmail,
    "SendEmailWithReplyTo": SendEmailWithReplyTo,
    "VerifyEmailIdentity": VerifyEmailIdentity,
    "ListIdentities": ListIdentities,
    "GetIdentityVerificationAttributes": GetIdentityVerificationAttributes,
    "VerifyEmailAddress": VerifyEmailAddress,
    "ListVerifiedEmailAddresses": ListVerifiedEmailAddresses,
    "DeleteIdentity": DeleteIdentity,
    "GetSendQuota": GetSendQuota,
    "SetIdentityFeedbackForwardingEnabled": SetIdentityFeedbackForwardingEnabled,
    "CreateTemplate": CreateTemplate,
    "GetTemplate": GetTemplate,
    "UpdateTemplate": UpdateTemplate,
    "ListTemplates": ListTemplates,
    "SendTemplatedEmail": SendTemplatedEmail,
    "DeleteTemplate": DeleteTemplate,
}

SETUP = {
    "ses-identities": setup_ses_identities,
    "ses-templates": setup_ses_templates,
}

TEARDOWN = {
    "ses-identities": teardown_ses_identities,
    "ses-templates": teardown_ses_templates,
}
