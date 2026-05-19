package io.overcast.compat.groups;

import io.overcast.compat.clients.AwsClients;
import io.overcast.compat.harness.Assertions;
import io.overcast.compat.harness.TestContext;
import io.overcast.compat.harness.TestFn;
import software.amazon.awssdk.services.ses.SesClient;
import software.amazon.awssdk.services.ses.model.*;

import java.util.Map;

/**
 * SES (Simple Email Service) compatibility test group.
 *
 * <p>Groups: ses-send, ses-identities, ses-templates.
 */
public final class SesGroup implements ServiceGroup {

    private final AwsClients clients;

    public SesGroup(AwsClients clients) {
        this.clients = clients;
    }

    private SesClient ses() { return clients.ses(); }

    @Override
    public Map<String, TestFn> impls() {
        return Map.ofEntries(
                Map.entry("SendEmail",               this::sendEmail),
                Map.entry("SendRawEmail",            this::sendRawEmail),
                Map.entry("SendEmailWithReplyTo",    this::sendEmailWithReplyTo),
                Map.entry("SendTemplatedEmail",      this::sendTemplatedEmail),
                Map.entry("VerifyEmailIdentity",     this::verifyEmailIdentity),
                Map.entry("ListIdentities",          this::listIdentities),
                Map.entry("GetIdentityVerificationAttributes", this::getIdentityVerificationAttributes),
                Map.entry("VerifyEmailAddress",      this::verifyEmailAddress),
                Map.entry("ListVerifiedEmailAddresses", this::listVerifiedEmailAddresses),
                Map.entry("SetIdentityFeedbackForwardingEnabled", this::setIdentityFeedbackForwardingEnabled),
                Map.entry("DeleteIdentity",          this::deleteIdentity),
                Map.entry("GetSendQuota",            this::getSendQuota),
                Map.entry("CreateTemplate",          this::createTemplate),
                Map.entry("GetTemplate",             this::getTemplate),
                Map.entry("ListTemplates",           this::listTemplates),
                Map.entry("UpdateTemplate",          this::updateTemplate),
                Map.entry("DeleteTemplate",          this::deleteTemplate)
        );
    }

    @Override
    public Map<String, TestFn> setups() {
        return Map.ofEntries(
                Map.entry("ses-send",        this::setupSend),
                Map.entry("ses-identities",  this::setupIdentities),
                Map.entry("ses-templates",   this::setupTemplates)
        );
    }

    @Override
    public Map<String, TestFn> teardowns() {
        return Map.ofEntries(
                Map.entry("ses-send",        ctx -> {}),
                Map.entry("ses-identities",  this::teardownIdentities),
                Map.entry("ses-templates",   this::teardownTemplates)
        );
    }

    // ── ses-send ──────────────────────────────────────────────────────────────

    private void setupSend(TestContext ctx) throws Exception {
        // Verify identities used for sending.
        String from = "compat-from@example.com";
        String to   = "compat-to@example.com";
        ses().verifyEmailIdentity(r -> r.emailAddress(from));
        ses().verifyEmailIdentity(r -> r.emailAddress(to));
        ctx.set("sesFrom", from);
        ctx.set("sesTo",   to);
    }

    private void sendEmail(TestContext ctx) throws Exception {
        String from = ctx.getString("sesFrom");
        String to   = ctx.getString("sesTo");
        var resp = ses().sendEmail(r -> r
                .source(from)
                .destination(d -> d.toAddresses(to))
                .message(m -> m
                        .subject(c -> c.data("Compat test").charset("UTF-8"))
                        .body(b -> b.text(c -> c.data("Hello from compat tests").charset("UTF-8")))));
        Assertions.assertNotBlank(resp.messageId(), "SendEmail: messageId is blank");
    }

    private void sendRawEmail(TestContext ctx) throws Exception {
        String from = ctx.getString("sesFrom");
        String to   = ctx.getString("sesTo");
        String raw = "From: " + from + "\r\n"
                + "To: " + to + "\r\n"
                + "Subject: Raw compat\r\n"
                + "MIME-Version: 1.0\r\n"
                + "Content-Type: text/plain\r\n\r\n"
                + "raw body";
        ses().sendRawEmail(r -> r.rawMessage(
                RawMessage.builder()
                        .data(software.amazon.awssdk.core.SdkBytes.fromUtf8String(raw))
                        .build()));
    }

    private void sendEmailWithReplyTo(TestContext ctx) throws Exception {
        String from = ctx.getString("sesFrom");
        String to   = ctx.getString("sesTo");
        var resp = ses().sendEmail(r -> r
                .source(from)
                .destination(d -> d.toAddresses(to))
                .message(m -> m
                        .subject(c -> c.data("ReplyTo Test").charset("UTF-8"))
                        .body(b -> b.text(c -> c.data("body with reply-to").charset("UTF-8"))))
                .replyToAddresses(from));
        Assertions.assertNotBlank(resp.messageId(), "SendEmailWithReplyTo: messageId is blank");
    }

    private void sendTemplatedEmail(TestContext ctx) throws Exception {
        String from = ctx.getString("sesFrom");
        String to   = ctx.getString("sesTo");
        if (from == null || to == null) {
            from = "compat-tmpl-from@example.com";
            to   = "compat-tmpl-to@example.com";
            final String f = from, t = to;
            ses().verifyEmailIdentity(r -> r.emailAddress(f));
            ses().verifyEmailIdentity(r -> r.emailAddress(t));
        }
        // Re-use the template created in ses-templates setup if available.
        String tmpl = ctx.getString("sesTemplateName");
        if (tmpl == null) {
            tmpl = "compat-tmpl-" + ctx.runId();
            createTemplateIfAbsent(tmpl);
        }
        final String t = tmpl;
        final String source = from;
        final String dest = to;
        ses().sendTemplatedEmail(r -> r
                .source(source)
                .destination(d -> d.toAddresses(dest))
                .template(t)
                .templateData("{\"name\":\"compat\"}"));
    }

    // ── ses-identities ────────────────────────────────────────────────────────

    private void setupIdentities(TestContext ctx) throws Exception {
        String email = "compat-id-" + ctx.runId() + "@example.com";
        ses().verifyEmailIdentity(r -> r.emailAddress(email));
        ctx.set("sesIdentityEmail", email);
    }

    private void teardownIdentities(TestContext ctx) {
        String email = ctx.getString("sesIdentityEmail");
        if (email != null)
            try { ses().deleteIdentity(r -> r.identity(email)); } catch (Exception ignored) {}
    }

    private void verifyEmailIdentity(TestContext ctx) throws Exception {
        // Created during setup — just assert it was set.
        Assertions.assertNotBlank(ctx.getString("sesIdentityEmail"), "sesIdentityEmail");
    }

    private void listIdentities(TestContext ctx) throws Exception {
        String email = ctx.getString("sesIdentityEmail");
        var resp = ses().listIdentities(r -> r.identityType(IdentityType.EMAIL_ADDRESS).maxItems(100));
        boolean found = resp.identities().contains(email);
        Assertions.assertTrue(found, "ListIdentities: identity not found for " + email);
    }

    private void getIdentityVerificationAttributes(TestContext ctx) throws Exception {
        String email = ctx.getString("sesIdentityEmail");
        var resp = ses().getIdentityVerificationAttributes(r -> r.identities(email));
        Assertions.assertTrue(resp.verificationAttributes().containsKey(email),
                "GetIdentityVerificationAttributes: identity key missing");
    }

    private void setIdentityFeedbackForwardingEnabled(TestContext ctx) throws Exception {
        // Use a dedicated identity — DeleteIdentity may have already removed
        // the shared one and nulled the context variable.
        String email = "compat-fwd-" + ctx.runId() + "@example.com";
        ses().verifyEmailIdentity(r -> r.emailAddress(email));
        ses().setIdentityFeedbackForwardingEnabled(r -> r.identity(email).forwardingEnabled(true));
    }

    private void deleteIdentity(TestContext ctx) throws Exception {
        String email = ctx.getString("sesIdentityEmail");
        ses().deleteIdentity(r -> r.identity(email));
        ctx.set("sesIdentityEmail", null);
    }

    private void verifyEmailAddress(TestContext ctx) throws Exception {
        String email = "verify-addr-" + ctx.runId() + "@example.com";
        ses().verifyEmailAddress(r -> r.emailAddress(email));
        try {
            ses().deleteIdentity(r -> r.identity(email));
        } catch (Exception ignored) {}
    }

    private void listVerifiedEmailAddresses(TestContext ctx) throws Exception {
        var resp = ses().listVerifiedEmailAddresses();
        Assertions.assertNotNull(resp.verifiedEmailAddresses(), "ListVerifiedEmailAddresses: list is null");
    }

    private void getSendQuota(TestContext ctx) throws Exception {
        var resp = ses().getSendQuota();
        Assertions.assertTrue(resp.max24HourSend() > 0, "GetSendQuota: max24HourSend should be > 0");
    }

    // ── ses-templates ─────────────────────────────────────────────────────────

    private void setupTemplates(TestContext ctx) {
        ctx.set("sesTemplateName", "compat-" + ctx.runId());
    }

    private void teardownTemplates(TestContext ctx) {
        String name = ctx.getString("sesTemplateName");
        if (name != null)
            try { ses().deleteTemplate(r -> r.templateName(name)); } catch (Exception ignored) {}
    }

    private void createTemplate(TestContext ctx) throws Exception {
        String name = ctx.getString("sesTemplateName");
        ses().createTemplate(r -> r.template(
                Template.builder()
                        .templateName(name)
                        .subjectPart("Hello {{name}}")
                        .htmlPart("<p>Hello {{name}}</p>")
                        .textPart("Hello {{name}}")
                        .build()));
    }

    private void getTemplate(TestContext ctx) throws Exception {
        String name = ctx.getString("sesTemplateName");
        var resp = ses().getTemplate(r -> r.templateName(name));
        Assertions.assertEquals(name, resp.template().templateName(), "GetTemplate: name mismatch");
    }

    private void listTemplates(TestContext ctx) throws Exception {
        var resp = ses().listTemplates(r -> r.maxItems(100));
        String name = ctx.getString("sesTemplateName");
        boolean found = resp.templatesMetadata().stream()
                .anyMatch(m -> m.name().equals(name));
        Assertions.assertTrue(found, "ListTemplates: created template not found");
    }

    private void updateTemplate(TestContext ctx) throws Exception {
        String name = ctx.getString("sesTemplateName");
        ses().updateTemplate(r -> r.template(
                Template.builder()
                        .templateName(name)
                        .subjectPart("Updated {{name}}")
                        .textPart("Updated {{name}}")
                        .build()));
    }

    private void deleteTemplate(TestContext ctx) throws Exception {
        String name = ctx.getString("sesTemplateName");
        ses().deleteTemplate(r -> r.templateName(name));
        ctx.set("sesTemplateName", null);
    }

    // ── Helpers ───────────────────────────────────────────────────────────────

    private void createTemplateIfAbsent(String name) {
        try {
            ses().createTemplate(r -> r.template(
                    Template.builder()
                            .templateName(name)
                            .subjectPart("Hello {{name}}")
                            .textPart("Hello {{name}}")
                            .build()));
        } catch (Exception ignored) {}
    }
}
