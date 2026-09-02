// Accessibility audit for the console: runs axe-core (WCAG 2.2 A/AA, plus the ARIA
// rules axe files as best-practice) over every surface, in both themes, at desktop and
// narrow widths, and in the interaction states a crawler never reaches — dialogs open,
// menus open, the command palette open, the sidebar collapsed.
//
// The console is a SPA served by the daemon, so this drives a real instance rather than
// a static build:
//
//   OVERCAST_PORT=4576 OVERCAST_UI_PORT=4577 OVERCAST_DEBUG=true overcast serve
//   A11Y_BASE=http://localhost:4577 pnpm a11y
//
// Options (env):
//   A11Y_BASE=<url>    console origin (default http://localhost:4577)
//   A11Y_OUT=<dir>     where the JSON + markdown report land (default reports/a11y)
//   A11Y_ROUTES=a,b    audit exactly these routes
//   A11Y_STATES=a,b    audit exactly these interaction states
//
// Exit code is 1 when any violation is found, so CI can gate on it.
import { mkdir, writeFile } from "node:fs/promises";
import path from "node:path";
import process from "node:process";
import { fileURLToPath } from "node:url";
import AxeBuilder from "@axe-core/playwright";
import { chromium } from "playwright";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const base = (process.env.A11Y_BASE || "http://localhost:4577").replace(/\/$/, "");
const outDir = path.resolve(root, process.env.A11Y_OUT || "reports/a11y");

// WCAG 2.2 AA plus best-practice: a combobox that never announces its active option
// fails 4.1.2 in practice even where axe files the rule outside the WCAG tags.
const AXE_TAGS = ["wcag2a", "wcag2aa", "wcag21a", "wcag21aa", "wcag22aa", "best-practice"];

const VIEWPORTS = [
  { name: "desktop", width: 1440, height: 900 },
  { name: "narrow", width: 900, height: 800 },
];

const THEMES = ["light", "dark"];

/** Every surface worth auditing, with the seed data scripts/seed-a11y-state.sh creates. */
const ROUTES = [
  { path: "/", name: "dashboard" },
  { path: "/s3", name: "s3-list" },
  { path: "/s3/a11y-demo", name: "s3-bucket" },
  { path: "/dynamodb", name: "dynamodb-list" },
  { path: "/dynamodb/a11y-table", name: "dynamodb-table" },
  { path: "/sqs", name: "sqs-list" },
  { path: "/sns", name: "sns-list" },
  { path: "/lambda", name: "lambda-list" },
  { path: "/iam", name: "iam" },
  { path: "/secretsmanager", name: "secrets" },
  { path: "/inbox", name: "inbox" },
  { path: "/events", name: "events" },
  { path: "/map", name: "map" },
  { path: "/metrics", name: "metrics" },
  { path: "/debug/traces", name: "traces" },
  { path: "/settings", name: "settings" },
  { path: "/docs", name: "docs" },
  { path: "/no-such-route", name: "404" },
];

/**
 * Interaction states. `on` names the routes a state applies to; `apply` opens it and
 * resolves once the surface has settled.
 */
const STATES = [
  { name: "default", on: () => true, apply: async () => {} },
  {
    name: "search-open",
    on: (route) => route.name === "dashboard",
    apply: async (page) => {
      await page.keyboard.press("ControlOrMeta+k");
      await page.waitForSelector('[role="dialog"]', { timeout: 10_000 });
      await page.keyboard.type("a11y");
      await page.waitForTimeout(500);
    },
  },
  {
    name: "sidebar-collapsed",
    on: (route) => route.name === "dashboard",
    apply: async (page) => {
      const toggle = page.getByRole("button", { name: /collapse|expand/i }).first();
      await toggle.click({ timeout: 10_000 });
      await page.waitForTimeout(400);
    },
  },
  {
    name: "filter-menu-open",
    on: (route) => route.name === "events",
    apply: async (page) => {
      // The events page's source filter — the app's one hand-rolled dropdown, so worth
      // auditing open. Its trigger is labelled with a live count ("30 sources").
      await page.getByRole("button", { name: /\d+ sources?/i }).first().click({ timeout: 10_000 });
      await page.waitForTimeout(400);
    },
  },
  {
    name: "docs-modal-open",
    on: (route) => route.name === "s3-list",
    apply: async (page) => {
      const trigger = page.getByRole("button", { name: /docs|documentation/i }).first();
      await trigger.click({ timeout: 10_000 });
      await page.waitForSelector('[role="dialog"]', { timeout: 10_000 });
      await page.waitForTimeout(500);
    },
  },
];

const wanted = (list, name) => !list || list.split(",").map((v) => v.trim()).includes(name);

async function settle(page) {
  // The console renders skeletons first; wait for them to clear rather than a fixed
  // delay, so a slow query does not get audited as a page of loading bars.
  await page
    .waitForFunction(() => document.querySelectorAll('[aria-busy="true"]').length === 0, { timeout: 15_000 })
    .catch(() => {});
  await page.waitForTimeout(400);
}

async function run() {
  const browser = await chromium.launch();
  const findings = [];
  let checked = 0;

  try {
    for (const viewport of VIEWPORTS) {
      for (const theme of THEMES) {
        const context = await browser.newContext({
          viewport: { width: viewport.width, height: viewport.height },
          colorScheme: theme,
          reducedMotion: "no-preference",
        });
        // Both ways of choosing a theme, not just one: the colorScheme above is the OS
        // preference the "system" default follows, and this is the explicit choice
        // stamped as data-theme. Setting it before the app boots means the audited
        // render is the one the setting produces rather than a post-hydration flip.
        await context.addInitScript((value) => {
          try {
            window.localStorage.setItem("overcast:theme", JSON.stringify(value));
            // Pin two services so the sidebar's sortable rows actually render. With no
            // favourites the section shows "Star a service to pin it here" and the drag
            // handles — the only reorder affordance — are never audited.
            window.localStorage.setItem("overcast-favourites", JSON.stringify(["/s3", "/sqs"]));
          } catch {
            /* storage disabled */
          }
        }, theme);
        const page = await context.newPage();

        for (const route of ROUTES) {
          if (!wanted(process.env.A11Y_ROUTES, route.name)) continue;
          for (const state of STATES) {
            if (!wanted(process.env.A11Y_STATES, state.name)) continue;
            if (!state.on(route, viewport)) continue;
            // Interaction states are one shared component each; auditing them on one
            // route per theme × viewport is the same coverage at a quarter the cost.
            await page.goto(base + route.path, { waitUntil: "domcontentloaded" });
            await settle(page);
            try {
              await state.apply(page);
            } catch (error) {
              findings.push({
                route: route.name,
                path: route.path,
                viewport: viewport.name,
                theme,
                state: state.name,
                id: "state-setup-failed",
                impact: "serious",
                help: `Could not reach state: ${String(error).slice(0, 200)}`,
                helpUrl: "",
                nodes: [],
              });
              continue;
            }
            checked += 1;
            const results = await new AxeBuilder({ page }).withTags(AXE_TAGS).analyze();
            for (const violation of results.violations) {
              findings.push({
                route: route.name,
                path: route.path,
                viewport: viewport.name,
                theme,
                state: state.name,
                id: violation.id,
                impact: violation.impact,
                help: violation.help,
                helpUrl: violation.helpUrl,
                nodes: violation.nodes.slice(0, 4).map((node) => ({
                  target: node.target.join(" "),
                  html: node.html.slice(0, 220),
                  summary: (node.failureSummary || "").slice(0, 400),
                })),
              });
            }
          }
        }
        await context.close();
      }
    }
  } finally {
    await browser.close();
  }

  await mkdir(outDir, { recursive: true });
  await writeFile(
    path.join(outDir, "a11y-results.json"),
    JSON.stringify({ generatedAt: new Date().toISOString(), base, runs: checked, findings }, null, 2),
  );
  await writeFile(path.join(outDir, "a11y-summary.md"), renderMarkdown({ checked, findings }));

  const byRule = new Map();
  for (const finding of findings) byRule.set(finding.id, (byRule.get(finding.id) ?? 0) + 1);
  console.log(`\n${checked} audited states · ${findings.length} violation instances`);
  for (const [id, count] of [...byRule].sort((a, b) => b[1] - a[1])) console.log(`  ${String(count).padStart(4)}  ${id}`);
  console.log(`\nReport: ${outDir}`);
  process.exitCode = findings.length > 0 ? 1 : 0;
}

function renderMarkdown({ checked, findings }) {
  const lines = [`# Console accessibility audit`, ``, `- Generated: ${new Date().toISOString()}`, `- Audited states: ${checked}`, `- Violation instances: ${findings.length}`, ``];
  const group = (key) => {
    const map = new Map();
    for (const finding of findings) map.set(finding[key], (map.get(finding[key]) ?? 0) + 1);
    return [...map].sort((a, b) => b[1] - a[1]);
  };
  const impacts = new Map(findings.map((f) => [f.id, f.impact]));

  lines.push(`## By rule`, ``, `| rule | impact | count |`, `| --- | --- | --- |`);
  for (const [id, count] of group("id")) lines.push(`| ${id} | ${impacts.get(id) ?? ""} | ${count} |`);

  for (const key of ["route", "state", "theme", "viewport"]) {
    lines.push(``, `## By ${key}`, ``, `| ${key} | count |`, `| --- | --- |`);
    for (const [name, count] of group(key)) lines.push(`| ${name} | ${count} |`);
  }

  lines.push(``, `## Detail (first occurrence of each rule × route)`, ``);
  const seen = new Set();
  for (const finding of findings) {
    const key = `${finding.id}::${finding.route}`;
    if (seen.has(key)) continue;
    seen.add(key);
    lines.push(
      `### ${finding.id} — ${finding.route}`,
      ``,
      `- ${finding.help} (${finding.impact})`,
      `- ${finding.path} · ${finding.theme} · ${finding.viewport} · state: ${finding.state}`,
      `- ${finding.helpUrl}`,
      ``,
    );
    for (const node of finding.nodes) lines.push("```", node.target, node.html, node.summary, "```", ``);
  }
  return lines.join("\n");
}

run().catch((error) => {
  console.error(error);
  process.exitCode = 1;
});
