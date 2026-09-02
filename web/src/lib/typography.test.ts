import { readFileSync, readdirSync, statSync } from "node:fs"
import { join } from "node:path"
import { fieldLabel, sectionLabel } from "./typography"

describe("label specs", () => {
  it("sets a field label at the scale floor and the narrower .14em", () => {
    expect(fieldLabel).toContain("text-2xs")
    expect(fieldLabel).toContain("tracking-[0.14em]")
  })

  it("sets a section heading at the scale floor and the wider .16em", () => {
    expect(sectionLabel).toContain("text-2xs")
    expect(sectionLabel).toContain("tracking-[0.16em]")
  })

  it("puts neither below the floor — an arbitrary px value would not scale with the root", () => {
    for (const spec of [fieldLabel, sectionLabel]) {
      expect(spec).not.toMatch(/text-\[\d+px\]/)
    }
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

/* A guard over hand-written sizes, for a failure that has already happened: the sizes
   kept drifting below the scale. An arbitrary `text-[Npx]` does not scale with the root
   font — which steps up on large displays — so the smaller the value, the wider the gap
   between the body text and the labels naming it on a 4K panel. 8-10px mono uppercase
   in the muted colour is also the exact case a high-DPI display renders thin and grey
   rather than sharp, and the webfont has no weight between 400 and 700 to lean on. */
describe("label specs > hand-written sizes", () => {
  it("finds nothing set at or below the floor as a raw px value", () => {
    const offenders: string[] = []
    for (const file of sourceFiles("src")) {
      if (file.endsWith("typography.test.ts")) continue
      readFileSync(file, "utf8")
        .split("\n")
        .forEach((line, i) => {
          // 11px and below: everything at or under the floor belongs to `text-2xs`,
          // which is a rem and therefore scales with the root font on large displays.
          if (/text-\[(?:[0-9]|10|11)px\]/.test(line)) offenders.push(`${file}:${i + 1}`)
        })
    }

    expect(offenders, `below the 11px floor:\n${offenders.join("\n")}`).toEqual([])
  })
})
