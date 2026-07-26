/**
 * arn-link.test.tsx — covers the ARN → route resolution used by ArnLink and
 * the embedded-ARN linkification used by LinkifiedText, both consumed by the
 * Events page to auto-link resource ARNs (see internal/events.Event.ResourceARN
 * on the Go side and event-console.tsx's JsonString on the web side).
 *
 * resolveArn/resolveService are not exported, so these tests go through the
 * public component API and assert on rendered output — an anchor with the
 * expected href for a recognised service, and plain (non-link) text for an
 * unrecognised one.
 */
import { describe, expect, it } from "vitest"
import { renderWithRouter } from "@/test/render"
import { ArnLink, LinkifiedText } from "./arn-link"

describe("ArnLink", () => {
  it("links a recognised SQS queue ARN to its detail page", () => {
    const { container } = renderWithRouter(
      () => <ArnLink arn="arn:aws:sqs:us-east-1:000000000000:my-queue" />,
      { path: "/sqs/$queue" },
    )
    const link = container.querySelector("a")
    expect(link).not.toBeNull()
    expect(link?.getAttribute("href")).toContain("/sqs/my-queue")
    expect(link?.textContent).toBe("arn:aws:sqs:us-east-1:000000000000:my-queue")
  })

  it("links a DynamoDB table ARN, ignoring a trailing GSI segment", () => {
    const { container } = renderWithRouter(
      () => (
        <ArnLink arn="arn:aws:dynamodb:us-east-1:000000000000:table/orders/index/gsi1" />
      ),
      { path: "/dynamodb/$tableName" },
    )
    const link = container.querySelector("a")
    expect(link?.getAttribute("href")).toContain("/dynamodb/orders")
  })

  it("links a Lambda function ARN to the function page, ignoring a version qualifier", () => {
    const { container } = renderWithRouter(
      () => <ArnLink arn="arn:aws:lambda:us-east-1:000000000000:function:my-fn:3" />,
      { path: "/lambda/$name" },
    )
    const link = container.querySelector("a")
    expect(link?.getAttribute("href")).toContain("/lambda/my-fn")
  })

  it("renders plain text (no link) for a service with no mapped UI route", () => {
    const { container } = renderWithRouter(
      () => <ArnLink arn="arn:aws:acm:us-east-1:000000000000:certificate/abc-123" />,
      { path: "/" },
    )
    expect(container.querySelector("a")).toBeNull()
    expect(container.textContent).toBe("arn:aws:acm:us-east-1:000000000000:certificate/abc-123")
  })

  it("renders plain text for a non-ARN string", () => {
    const { container } = renderWithRouter(() => <ArnLink arn="not-an-arn" />, { path: "/" })
    expect(container.querySelector("a")).toBeNull()
    expect(container.textContent).toBe("not-an-arn")
  })
})

describe("LinkifiedText", () => {
  it("linkifies an ARN embedded in a longer string, preserving surrounding text", () => {
    const { container } = renderWithRouter(
      () => (
        <LinkifiedText text="failed to invoke arn:aws:lambda:us-east-1:000000000000:function:my-fn: timeout" />
      ),
      { path: "/lambda/$name" },
    )
    expect(container.textContent).toBe(
      "failed to invoke arn:aws:lambda:us-east-1:000000000000:function:my-fn: timeout",
    )
    const link = container.querySelector("a")
    expect(link).not.toBeNull()
    expect(link?.getAttribute("href")).toContain("/lambda/my-fn")
  })

  it("renders text unchanged when no ARN is present", () => {
    const { container } = renderWithRouter(() => <LinkifiedText text="no arns here" />, {
      path: "/",
    })
    expect(container.querySelector("a")).toBeNull()
    expect(container.textContent).toBe("no arns here")
  })
})
