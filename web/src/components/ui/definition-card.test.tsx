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
  it("inherits the inline layout from the list so pairs need not repeat it", () => {
    render(
      <DefinitionList layout="inline">
        <Definition label="From" value="noreply@example.com" />
      </DefinitionList>,
    )
    expect(screen.getByText("From")).toHaveClass("w-28")
  })

  it("leaves labels unconstrained in the default stacked layout", () => {
    render(
      <DefinitionList>
        <Definition label="From" value="noreply@example.com" />
      </DefinitionList>,
    )
    expect(screen.getByText("From")).not.toHaveClass("w-28")
  })

  it("spans a full-width pair across the whole grid", () => {
    render(
      <DefinitionList>
        <Definition label="ARN" value="arn:aws:s3:::bucket" full />
      </DefinitionList>,
    )
    expect(screen.getByText("ARN").parentElement).toHaveClass("col-span-full")
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
