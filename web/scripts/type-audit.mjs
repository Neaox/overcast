// Type-size audit for the console: the computed font size, line height, weight and
// contrast of every text node on every surface, grouped by the area it belongs to.
// Companion to scripts/a11y-audit.mjs — that one asks whether text passes contrast,
// this one asks whether it is big enough to read at all.
//
//   OVERCAST_PORT=4576 OVERCAST_UI_PORT=4577 overcast serve
//   A11Y_BASE=http://localhost:4577 pnpm type-audit
//
// Computed sizes are in CSS px and do not move with the device pixel ratio, so the
// measurements repeat across scale factors by design. The DPR sweep exists to render
// the same type at 1x, at Windows' 125%/150% display scaling and at a 2x panel, and to
// drop a screenshot of each — the thin-stroke problem on a high-DPI display is a
// rendering one that no measurement catches.
import { mkdir, writeFile } from "node:fs/promises";
import path from "node:path";
import process from "node:process";
import { fileURLToPath } from "node:url";
import { chromium } from "playwright";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const base = (process.env.A11Y_BASE || "http://localhost:4577").replace(/\/$/, "");
const outDir = path.resolve(root, process.env.A11Y_OUT || "reports/a11y");

const ROUTES = ["/", "/s3", "/s3/a11y-demo", "/dynamodb/a11y-table", "/sqs", "/inbox", "/events", "/settings", "/metrics", "/debug/traces", "/docs"];
const VIEWPORTS = [
  { name: "desktop", width: 1440, height: 900 },
  { name: "narrow", width: 900, height: 800 },
];
const SCALE_FACTORS = [1, 1.25, 1.5, 2];
const SHOT_ROUTES = ["/", "/s3/a11y-demo", "/settings"];

const collect = () => {
  const parseColor = (value) => {
    const match = String(value).match(/rgba?\(([^)]+)\)/);
    if (!match) return null;
    const parts = match[1].split(/[\s,/]+/).filter(Boolean).map(Number);
    return { r: parts[0], g: parts[1], b: parts[2], a: parts[3] === undefined ? 1 : parts[3] };
  };
  const lin = (c) => {
    const v = c / 255;
    return v <= 0.03928 ? v / 12.92 : Math.pow((v + 0.055) / 1.055, 2.4);
  };
  const lum = (c) => 0.2126 * lin(c.r) + 0.7152 * lin(c.g) + 0.0722 * lin(c.b);
  const blend = (fg, bg) => ({ r: fg.r * fg.a + bg.r * (1 - fg.a), g: fg.g * fg.a + bg.g * (1 - fg.a), b: fg.b * fg.a + bg.b * (1 - fg.a), a: 1 });
  const effectiveBg = (element) => {
    let node = element;
    const stack = [];
    while (node && node !== document.documentElement.parentElement) {
      const color = parseColor(getComputedStyle(node).backgroundColor);
      if (color && color.a > 0) stack.push(color);
      node = node.parentElement;
    }
    let result = { r: 255, g: 255, b: 255, a: 1 };
    for (let i = stack.length - 1; i >= 0; i -= 1) result = blend(stack[i], result);
    return result;
  };
  const contrast = (fg, bg) => {
    const [a, b] = [lum(fg), lum(bg)].sort((x, y) => y - x);
    return (a + 0.05) / (b + 0.05);
  };

  const areaOf = (element) => {
    if (element.closest('[role="dialog"]')) return "dialog";
    if (element.closest("aside")) return "sidebar";
    if (element.closest("header")) return "header";
    if (element.closest("thead")) return "table header";
    if (element.closest("table")) return "table cell";
    if (element.closest("pre, code, .monaco-editor")) return "code";
    if (element.closest(".react-flow")) return "system map";
    if (element.closest("label")) return "form label";
    if (element.closest("form")) return "form";
    return "page body";
  };

  const rows = [];
  document.querySelectorAll("body *").forEach((element) => {
    const text = [...element.childNodes].filter((n) => n.nodeType === 3).map((n) => n.textContent.trim()).join(" ").trim();
    if (!text) return;
    const style = getComputedStyle(element);
    if (style.display === "none" || style.visibility === "hidden" || style.opacity === "0") return;
    const box = element.getBoundingClientRect();
    if (box.width <= 1 && box.height <= 1) return;
    const size = parseFloat(style.fontSize);
    const fg = parseColor(style.color);
    const bg = effectiveBg(element);
    rows.push({
      area: areaOf(element),
      tag: element.tagName.toLowerCase(),
      cls: (element.getAttribute("class") || "").slice(0, 80),
      size: Math.round(size * 100) / 100,
      lineHeight: style.lineHeight === "normal" ? Math.round(size * 1.2 * 100) / 100 : parseFloat(style.lineHeight),
      weight: style.fontWeight,
      mono: /mono|JetBrains|Consolas|Menlo/i.test(style.fontFamily),
      upper: style.textTransform === "uppercase",
      contrast: fg ? Math.round(contrast(blend(fg, bg), bg) * 100) / 100 : null,
      text: text.slice(0, 44),
    });
  });
  return rows;
};

// 1.4.3 loosens to 3:1 only for 18.66px bold or 24px, so everything the console sets
// text at has to clear 4.5:1.
const required = (row) => (row.size >= 24 || (row.size >= 18.66 && Number(row.weight) >= 700) ? 3 : 4.5);

async function run() {
  const browser = await chromium.launch();
  const all = [];
  try {
    for (const viewport of VIEWPORTS) {
      for (const theme of ["light", "dark"]) {
        const context = await browser.newContext({ viewport: { width: viewport.width, height: viewport.height }, colorScheme: theme });
        await context.addInitScript((value) => {
          try {
            window.localStorage.setItem("overcast:theme", JSON.stringify(value));
          } catch {
            /* storage disabled */
          }
        }, theme);
        const page = await context.newPage();
        for (const route of ROUTES) {
          await page.goto(base + route, { waitUntil: "domcontentloaded" });
          await page.waitForFunction(() => document.querySelectorAll('[aria-busy="true"]').length === 0, { timeout: 15_000 }).catch(() => {});
          await page.waitForTimeout(500);
          for (const row of await page.evaluate(collect)) all.push({ ...row, route, theme, viewport: viewport.name });
        }
        await context.close();
      }
    }

    for (const deviceScaleFactor of SCALE_FACTORS) {
      for (const theme of ["light", "dark"]) {
        const context = await browser.newContext({ viewport: { width: 1440, height: 900 }, colorScheme: theme, deviceScaleFactor });
        await context.addInitScript((value) => {
          try {
            window.localStorage.setItem("overcast:theme", JSON.stringify(value));
          } catch {
            /* storage disabled */
          }
        }, theme);
        const page = await context.newPage();
        await mkdir(path.join(outDir, "shots"), { recursive: true });
        for (const route of SHOT_ROUTES) {
          await page.goto(base + route, { waitUntil: "domcontentloaded" });
          await page.waitForTimeout(1200);
          const name = `${route.replace(/\W+/g, "-").replace(/^-|-$/g, "") || "dashboard"}-${theme}-${String(deviceScaleFactor).replace(".", "_")}x.png`;
          await page.screenshot({ path: path.join(outDir, "shots", name) });
        }
        await context.close();
      }
    }
  } finally {
    await browser.close();
  }

  await mkdir(outDir, { recursive: true });
  await writeFile(path.join(outDir, "type-results.json"), JSON.stringify({ generatedAt: new Date().toISOString(), rows: all }, null, 2));

  const byArea = new Map();
  for (const row of all) {
    const current = byArea.get(row.area);
    if (!current || row.size < current.size) byArea.set(row.area, row);
  }
  const small = new Map();
  for (const row of all) {
    if (row.size >= 12 && row.contrast !== null && row.contrast >= required(row)) continue;
    const key = `${row.area}|${row.size}|${row.cls}|${row.theme}`;
    const entry = small.get(key) ?? { ...row, count: 0 };
    entry.count += 1;
    small.set(key, entry);
  }

  const lines = [`# Console type audit`, ``, `- Generated: ${new Date().toISOString()}`, `- Measured text nodes: ${all.length}`, ``];
  lines.push(`## Smallest type per area`, ``, `| area | px | line-height | mono | upper | contrast | sample |`, `| --- | --- | --- | --- | --- | --- | --- |`);
  for (const [area, row] of [...byArea].sort((a, b) => a[1].size - b[1].size)) {
    lines.push(`| ${area} | ${row.size} | ${row.lineHeight} | ${row.mono ? "y" : ""} | ${row.upper ? "y" : ""} | ${row.contrast} | ${row.text.replace(/\|/g, "\\|")} |`);
  }
  lines.push(``, `## Under 12px, or under the contrast its size requires`, ``, `| area | px | theme | contrast | needs | class | sample | seen |`, `| --- | --- | --- | --- | --- | --- | --- | --- |`);
  for (const row of [...small.values()].sort((a, b) => a.size - b.size || b.count - a.count).slice(0, 60)) {
    lines.push(`| ${row.area} | ${row.size} | ${row.theme} | ${row.contrast} | ${required(row)} | \`${row.cls}\` | ${row.text.replace(/\|/g, "\\|")} | ${row.count} |`);
  }
  await writeFile(path.join(outDir, "type-summary.md"), lines.join("\n"));

  console.log(`Measured ${all.length} text nodes. Smallest per area:`);
  for (const [area, row] of [...byArea].sort((a, b) => a[1].size - b[1].size)) console.log(`  ${String(row.size).padStart(5)}px  ${area}`);
  console.log(`\n${small.size} distinct undersized / low-contrast runs. Report: ${outDir}`);
}

run().catch((error) => {
  console.error(error);
  process.exitCode = 1;
});
