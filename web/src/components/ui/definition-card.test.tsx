import { describe, it, expect } from "vitest"
import { render, screen } from "@/test/render"
import { Definition, DefinitionCard, DefinitionList } from "@/components/ui/definition-card"
import { fieldLabel } from "@/lib/typography"

const labelClasses = fieldLabel.split(" ")

describe("Definition > typography", () => {
  it("sets every label in the field-label spec so it matches a column header", () => {
    render(
      <DefinitionList>
        <Definition label="Created" value="25 Jul 2026" />
      </DefinitionList>,
    )
    expect(screen.getByText("Created")).toHaveClass(...labelClasses)
  })

  it("sets a value in mono, because a detail value is machine output", () => {
    render(
      <DefinitionList>
        <Definition label="ARN" value="arn:aws:iam::000000000000:role/app" />
      </DefinitionList>,
    )
    expect(screen.getByText("arn:aws:iam::000000000000:role/app")).toHaveClass("font-mono")
  })

  it("sets a prose value in sans, because a description is not machine output", () => {
    render(
      <DefinitionList>
        <Definition label="Description" value="The queue the worker drains." variant="prose" />
      </DefinitionList>,
    )
    expect(screen.getByText("The queue the worker drains.")).not.toHaveClass("font-mono")
  })

  it("keeps the mono value font when the caller passes its own value classes", () => {
    render(
      <DefinitionList>
        <Definition label="Status" value="ACTIVE" valueClassName="text-success" />
      </DefinitionList>,
    )
    const value = screen.getByText("ACTIVE")
    expect(value).toHaveClass("font-mono")
    expect(value).toHaveClass("text-success")
  })
})

describe("Definition > empty values", () => {
  it.each([
    ["undefined", undefined],
    ["null", null],
    ["an empty string", ""],
  ])("renders an em dash when the value is %s", (_name, value) => {
    render(
      <DefinitionList>
        <Definition label="Subnet ID" value={value} />
      </DefinitionList>,
    )
    expect(screen.getByText("—")).toBeInTheDocument()
  })

  it("renders a zero rather than treating it as absent", () => {
    render(
      <DefinitionList>
        <Definition label="Rules" value={0} />
      </DefinitionList>,
    )
    expect(screen.getByText("0")).toBeInTheDocument()
  })
})

describe("Definition > semantics", () => {
  it("marks the label as a dt so the pair is a real definition list", () => {
    render(
      <DefinitionList>
        <Definition label="Region" value="us-east-1" />
      </DefinitionList>,
    )
    expect(screen.getByText("Region").tagName).toBe("DT")
    expect(screen.getByText("us-east-1").tagName).toBe("DD")
  })
})

describe("DefinitionList > layout", () => {
  // The container must wrap the list rather than be it: `@container` scopes
  // queries to descendants, so a `<dl>` that declared itself the container
  // would size its own columns against some ancestor instead of its own width.
  it("wraps the list in a container so its shape follows the card, not the viewport", () => {
    const { container } = render(
      <DefinitionList>
        <Definition label="Runtime" value="nodejs20.x" />
      </DefinitionList>,
    )
    const dl = container.querySelector("dl")
    expect(dl).not.toHaveClass("@container")
    expect(dl?.parentElement).toHaveClass("@container")
  })

  it("reflows a pair from inline to stacked at the container breakpoint by default", () => {
    render(
      <DefinitionList>
        <Definition label="Runtime" value="nodejs20.x" />
      </DefinitionList>,
    )
    const label = screen.getByText("Runtime")
    // Narrow card: a fixed label column beside the value.
    expect(label).toHaveClass("w-28")
    // Wide card: the column is released and the value sits underneath.
    expect(label).toHaveClass("@3xl:w-auto")
    expect(label.parentElement).toHaveClass("@3xl:flex-col")
  })

  it("pins a list to inline so it never releases the label column", () => {
    render(
      <DefinitionList layout="inline">
        <Definition label="From" value="noreply@example.com" />
      </DefinitionList>,
    )
    const label = screen.getByText("From")
    expect(label).toHaveClass("w-28")
    expect(label).not.toHaveClass("@3xl:w-auto")
  })

  it("pins a list to stacked so it never takes a label column", () => {
    render(
      <DefinitionList layout="stacked">
        <Definition label="From" value="noreply@example.com" />
      </DefinitionList>,
    )
    const label = screen.getByText("From")
    expect(label).not.toHaveClass("w-28")
    expect(label.parentElement).toHaveClass("flex-col")
  })

  it("lets a pair override the layout its list set", () => {
    render(
      <DefinitionList layout="inline">
        <Definition label="Description" value="A queue." layout="stacked" />
      </DefinitionList>,
    )
    expect(screen.getByText("Description")).not.toHaveClass("w-28")
  })

  it("spans a full-width pair across the whole grid", () => {
    render(
      <DefinitionList>
        <Definition label="ARN" value="arn:aws:s3:::bucket" full />
      </DefinitionList>,
    )
    expect(screen.getByText("ARN").parentElement).toHaveClass("col-span-full")
  })

  it("scales its column count with the container, not the window", () => {
    const { container } = render(
      <DefinitionList>
        <Definition label="Runtime" value="nodejs20.x" />
      </DefinitionList>,
    )
    const dl = container.querySelector("dl")
    expect(dl).toHaveClass("@3xl:grid-cols-2")
    expect(dl).toHaveClass("@5xl:grid-cols-3")
  })
})

describe("DefinitionCard", () => {
  it("renders its pairs inside a card", () => {
    render(
      <DefinitionCard>
        <Definition label="Rotation" value="Disabled" />
      </DefinitionCard>,
    )
    expect(screen.getByText("Disabled")).toBeInTheDocument()
  })

  it("shows a heading when one is given", () => {
    render(
      <DefinitionCard title="Caller Identity">
        <Definition label="Account" value="000000000000" />
      </DefinitionCard>,
    )
    expect(screen.getByRole("heading", { name: "Caller Identity" })).toBeInTheDocument()
  })

  it("omits the heading when no title is given", () => {
    render(
      <DefinitionCard>
        <Definition label="Account" value="000000000000" />
      </DefinitionCard>,
    )
    expect(screen.queryByRole("heading")).not.toBeInTheDocument()
  })
})
