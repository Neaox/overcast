/**
 * groups/ses.ts — SES compatibility test groups for the node-js-sdk suite.
 *
 * Status: Partially implemented in Overcast (SMTP server + SendRawEmail).
 *         SESv2 operations are expected to fail with 501 until implemented.
 *
 * Groups:
 *   ses-send       — SendEmail + SendRawEmail (SES v1)
 *   ses-identities — identity verification lifecycle
 *   ses-templates  — email template CRUD (not implemented → expect fail)
 */

import {
  SendEmailCommand,
  SendRawEmailCommand,
  VerifyEmailIdentityCommand,
  VerifyEmailAddressCommand,
  ListIdentitiesCommand,
  GetIdentityVerificationAttributesCommand,
  DeleteIdentityCommand,
  ListVerifiedEmailAddressesCommand,
  CreateTemplateCommand,
  UpdateTemplateCommand,
  GetTemplateCommand,
  ListTemplatesCommand,
  DeleteTemplateCommand,
  SendTemplatedEmailCommand,
  GetSendQuotaCommand,
  SetIdentityFeedbackForwardingEnabledCommand,
  IdentityType,
} from "@aws-sdk/client-ses";
import { makeClients } from "../lib/clients.js";
import type { TestGroup } from "../lib/harness.js";
import * as assert from "node:assert/strict";

export function makeSESGroups(suite: string): TestGroup[] {
  return [
    // ── ses-send ───────────────────────────────────────────────────────────
    {
      suite,
      service: "ses",
      name: "ses-send",
      tests: [
        {
          name: "SendEmail",
          fn: async (ctx) => {
            const { ses } = makeClients(ctx);
            const resp = await ses.send(
              new SendEmailCommand({
                Source: `compat+${ctx.runId}@example.com`,
                Destination: { ToAddresses: ["recipient@example.com"] },
                Message: {
                  Subject: { Data: `Compat test ${ctx.runId}` },
                  Body: {
                    Text: { Data: "Hello from Overcast compat tests." },
                    Html: { Data: "<p>Hello from Overcast compat tests.</p>" },
                  },
                },
              }),
            );
            assert.ok(resp.MessageId, "SendEmail: missing MessageId");
          },
        },
        {
          name: "SendRawEmail",
          fn: async (ctx) => {
            const { ses } = makeClients(ctx);
            // Minimal RFC 2822 raw message.
            const raw = [
              `From: compat+${ctx.runId}@example.com`,
              "To: recipient@example.com",
              `Subject: Raw compat test ${ctx.runId}`,
              "MIME-Version: 1.0",
              "Content-Type: text/plain",
              "",
              "Hello from raw email.",
            ].join("\r\n");
            const resp = await ses.send(
              new SendRawEmailCommand({
                RawMessage: { Data: Buffer.from(raw) },
              }),
            );
            assert.ok(resp.MessageId, "SendRawEmail: missing MessageId");
          },
        },
        {
          name: "SendEmailWithReplyTo",
          op: "SendEmail",
          fn: async (ctx) => {
            const { ses } = makeClients(ctx);
            const resp = await ses.send(
              new SendEmailCommand({
                Source: `compat+${ctx.runId}@example.com`,
                Destination: { ToAddresses: ["recipient@example.com"] },
                ReplyToAddresses: [`reply-${ctx.runId}@example.com`],
                ReturnPath: `bounce-${ctx.runId}@example.com`,
                Message: {
                  Subject: { Data: "Reply-To test" },
                  Body: { Text: { Data: "Test" } },
                },
              }),
            );
            assert.ok(
              resp.MessageId,
              "SendEmailWithReplyTo: missing MessageId",
            );
          },
        },
        {
          name: "GetSendQuota",
          fn: async (ctx) => {
            const { ses } = makeClients(ctx);
            const resp = await ses.send(new GetSendQuotaCommand({}));
            assert.notStrictEqual(
              resp.Max24HourSend,
              undefined,
              "GetSendQuota: missing Max24HourSend",
            );
            assert.notStrictEqual(
              resp.MaxSendRate,
              undefined,
              "GetSendQuota: missing MaxSendRate",
            );
          },
        },
      ],
    },

    // ── ses-identities ─────────────────────────────────────────────────────
    {
      suite,
      service: "ses",
      name: "ses-identities",
      tests: [
        {
          name: "VerifyEmailIdentity",
          fn: async (ctx) => {
            const { ses } = makeClients(ctx);
            // In Overcast, verification is instant (no real DNS check).
            await ses.send(
              new VerifyEmailIdentityCommand({
                EmailAddress: `verified-${ctx.runId}@example.com`,
              }),
            );
            const resp = await ses.send(
              new ListIdentitiesCommand({
                IdentityType: IdentityType.EmailAddress,
              }),
            );
            if (
              !resp.Identities?.includes(`verified-${ctx.runId}@example.com`)
            ) {
              throw new Error(
                "VerifyEmailIdentity: identity not found after verify",
              );
            }
          },
        },
        {
          name: "ListIdentities",
          fn: async (ctx) => {
            const { ses } = makeClients(ctx);
            const resp = await ses.send(
              new ListIdentitiesCommand({
                IdentityType: IdentityType.EmailAddress,
              }),
            );
            if (
              !resp.Identities?.includes(`verified-${ctx.runId}@example.com`)
            ) {
              throw new Error("ListIdentities: verified address not found");
            }
          },
        },
        {
          name: "GetIdentityVerificationAttributes",
          fn: async (ctx) => {
            const { ses } = makeClients(ctx);
            const resp = await ses.send(
              new GetIdentityVerificationAttributesCommand({
                Identities: [`verified-${ctx.runId}@example.com`],
              }),
            );
            const attr =
              resp.VerificationAttributes?.[
                `verified-${ctx.runId}@example.com`
              ];
            assert.ok(
              attr,
              "GetIdentityVerificationAttributes: missing attributes",
            );
            assert.strictEqual(
              attr.VerificationStatus,
              "Success",
              `GetIdentityVerificationAttributes: unexpected status: ${attr.VerificationStatus}`,
            );
          },
        },
        {
          name: "VerifyEmailAddress",
          fn: async (ctx) => {
            const { ses } = makeClients(ctx);
            const addr = `addr-${ctx.runId}@example.com`;
            await ses.send(
              new VerifyEmailAddressCommand({
                EmailAddress: addr,
              }),
            );
            const resp = await ses.send(
              new ListIdentitiesCommand({ IdentityType: "EmailAddress" }),
            );
            assert.ok(
              resp.Identities?.includes(addr),
              `VerifyEmailAddress: ${addr} not found in identities`,
            );
          },
        },
        {
          name: "ListVerifiedEmailAddresses",
          fn: async (ctx) => {
            const { ses } = makeClients(ctx);
            const resp = await ses.send(
              new ListVerifiedEmailAddressesCommand({}),
            );
            if (
              !resp.VerifiedEmailAddresses?.includes(
                `addr-${ctx.runId}@example.com`,
              )
            ) {
              throw new Error("ListVerifiedEmailAddresses: address not found");
            }
          },
        },
        {
          name: "DeleteIdentity",
          fn: async (ctx) => {
            const { ses } = makeClients(ctx);
            const identity = `verified-${ctx.runId}@example.com`;
            await ses.send(
              new DeleteIdentityCommand({
                Identity: identity,
              }),
            );
            const resp = await ses.send(
              new ListIdentitiesCommand({ IdentityType: "EmailAddress" }),
            );
            assert.ok(
              !resp.Identities?.includes(identity),
              `DeleteIdentity: ${identity} still present after delete`,
            );
          },
        },
        {
          name: "SetIdentityFeedbackForwardingEnabled",
          fn: async (ctx) => {
            const { ses } = makeClients(ctx);
            // Re-create identity since DeleteIdentity may have removed it.
            await ses.send(
              new VerifyEmailIdentityCommand({
                EmailAddress: `fwd-${ctx.runId}@example.com`,
              }),
            );
            await ses.send(
              new SetIdentityFeedbackForwardingEnabledCommand({
                Identity: `fwd-${ctx.runId}@example.com`,
                ForwardingEnabled: true,
              }),
            );
          },
        },
      ],
      teardown: async (ctx) => {
        const { ses } = makeClients(ctx);
        for (const addr of [
          `verified-${ctx.runId}@example.com`,
          `addr-${ctx.runId}@example.com`,
          `fwd-${ctx.runId}@example.com`,
        ]) {
          try {
            await ses.send(new DeleteIdentityCommand({ Identity: addr }));
          } catch {}
        }
      },
    },

    // ── ses-templates ──────────────────────────────────────────────────────
    {
      suite,
      service: "ses",
      name: "ses-templates",
      tests: [
        {
          name: "CreateTemplate",
          fn: async (ctx) => {
            const { ses } = makeClients(ctx);
            await ses.send(
              new CreateTemplateCommand({
                Template: {
                  TemplateName: `${ctx.runId}-tmpl`,
                  SubjectPart: "Hello {{name}}",
                  TextPart: "Hi {{name}}, welcome!",
                  HtmlPart: "<p>Hi {{name}}, welcome!</p>",
                },
              }),
            );
            const resp = await ses.send(
              new GetTemplateCommand({ TemplateName: `${ctx.runId}-tmpl` }),
            );
            assert.strictEqual(
              resp.Template?.SubjectPart,
              "Hello {{name}}",
              `CreateTemplate: expected SubjectPart="Hello {{name}}", got "${resp.Template?.SubjectPart}"`,
            );
          },
        },
        {
          name: "GetTemplate",
          fn: async (ctx) => {
            const { ses } = makeClients(ctx);
            const resp = await ses.send(
              new GetTemplateCommand({ TemplateName: `${ctx.runId}-tmpl` }),
            );
            assert.ok(
              resp.Template?.TemplateName,
              "GetTemplate: missing TemplateName",
            );
          },
        },
        {
          name: "UpdateTemplate",
          fn: async (ctx) => {
            const { ses } = makeClients(ctx);
            await ses.send(
              new UpdateTemplateCommand({
                Template: {
                  TemplateName: `${ctx.runId}-tmpl`,
                  SubjectPart: "Updated {{name}}",
                  TextPart: "Updated Hi {{name}}!",
                  HtmlPart: "<p>Updated Hi {{name}}!</p>",
                },
              }),
            );
            const resp = await ses.send(
              new GetTemplateCommand({ TemplateName: `${ctx.runId}-tmpl` }),
            );
            assert.strictEqual(
              resp.Template?.SubjectPart,
              "Updated {{name}}",
              `UpdateTemplate: expected SubjectPart="Updated {{name}}", got "${resp.Template?.SubjectPart}"`,
            );
          },
        },
        {
          name: "ListTemplates",
          fn: async (ctx) => {
            const { ses } = makeClients(ctx);
            const resp = await ses.send(new ListTemplatesCommand({}));
            if (
              !resp.TemplatesMetadata?.some(
                (t) => t.Name === `${ctx.runId}-tmpl`,
              )
            ) {
              throw new Error("ListTemplates: template not found");
            }
          },
        },
        {
          name: "SendTemplatedEmail",
          fn: async (ctx) => {
            const { ses } = makeClients(ctx);
            const resp = await ses.send(
              new SendTemplatedEmailCommand({
                Source: `compat+${ctx.runId}@example.com`,
                Destination: { ToAddresses: ["recipient@example.com"] },
                Template: `${ctx.runId}-tmpl`,
                TemplateData: JSON.stringify({ name: "World" }),
              }),
            );
            assert.ok(resp.MessageId, "SendTemplatedEmail: missing MessageId");
          },
        },
        {
          name: "DeleteTemplate",
          fn: async (ctx) => {
            const { ses } = makeClients(ctx);
            await ses.send(
              new DeleteTemplateCommand({ TemplateName: `${ctx.runId}-tmpl` }),
            );
            const resp = await ses.send(new ListTemplatesCommand({}));
            if (
              resp.TemplatesMetadata?.some(
                (t) => t.Name === `${ctx.runId}-tmpl`,
              )
            ) {
              throw new Error(
                "DeleteTemplate: template still present after delete",
              );
            }
          },
        },
      ],
      teardown: async (ctx) => {
        const { ses } = makeClients(ctx);
        try {
          await ses.send(
            new DeleteTemplateCommand({ TemplateName: `${ctx.runId}-tmpl` }),
          );
        } catch {}
      },
    },
  ];
}
