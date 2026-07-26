import { readFileSync, readdirSync, statSync } from "node:fs"
import { join } from "node:path"
import { fieldLabel, sectionLabel } from "./typography"

describe("label specs", () => {
  it("sets a field label at the narrower 9px/.14em", () => {
    expect(fieldLabel).toContain("text-[9px]")
    expect(fieldLabel).toContain("tracking-[0.14em]")
  })

  it("sets a section heading at the wider 10px/.16em", () => {
    expect(sectionLabel).toContain("text-[10px]")
    expect(sectionLabel).toContain("tracking-[0.16em]")
  })

  it("keeps the two apart, because the tracking is what makes a heading a heading", () => {
    expect(sectionLabel).not.toContain("tracking-[0.14em]")
    expect(fieldLabel).not.toContain("tracking-[0.16em]")
  })
})

function sourceFiles(dir: string, acc: string[] = []): string[] {
  for (const entry of readdirSync(dir)) {
    const path = join(dir, entry)
    if (statSync(path).isDirectory()) sourceFiles(path, acc)
    else if (/\.tsx?$/.test(path)) acc.push(path)
  }
  return acc
}

/* The failure this catches has already happened twice — `not-emulated-chips`
   and `service-list-view` both shipped heading tracking at column-header size,
   which is exactly the combination that erases the two roles' difference. */
describe("label specs > hand-written combinations", () => {
  it("finds no 9px label carrying section-heading tracking", () => {
    const offenders: string[] = []
    for (const file of sourceFiles("src")) {
      if (file.endsWith("typography.test.ts")) continue
      readFileSync(file, "utf8")
        .split("\n")
        .forEach((line, i) => {
          if (
            /text-\[9px\][^"']*tracking-\[0\.16em\]|tracking-\[0\.16em\][^"']*text-\[9px\]/.test(
              line,
            )
          ) {
            offenders.push(`${file}:${i + 1}`)
          }
        })
    }

    expect(offenders, `9px at heading tracking:\n${offenders.join("\n")}`).toEqual([])
  })
})
