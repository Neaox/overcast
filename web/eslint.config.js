// oxlint is the primary linter for web/ — see https://github.com/Neaox/overcast/issues/1330.
// `.oxlintrc.json` owns the rule set and its rationale; `pnpm run lint` runs
// `oxlint .` first, then `eslint .`.
//
// This file is the *remainder*: the rules oxlint has no equivalent for.
//
//   react-hooks/component-hook-factories — the named accepted loss from #1330.
//     Flags components and hooks defined inside other functions. No oxlint
//     equivalent, and none filed upstream. This rule is why ESLint is still in
//     the pipeline at all.
//   react-hooks/config, react-hooks/gating — validate eslint-plugin-react-hooks'
//     own config objects; meaningless outside that plugin.
//   no-octal — no oxlint equivalent. Unreachable in practice (octal literals are
//     a syntax error in ESM/strict mode), kept on rather than dropped silently.
//
// All four are syntactic, so this config no longer sets `projectService` and
// ESLint no longer starts a TypeScript program — no `tsc` in the lint path, and
// `@typescript-eslint/*` contributes no rules at all any more.
//
// It does NOT unblock the TypeScript 7 bump (#1259), which was the hope. Measured
// on this config with `typescript@7.0.2` installed: ESLint still dies with
// `TypeError: Cannot read properties of undefined (reading 'Cjs')` from
// `@typescript-eslint/typescript-estree/dist/create-program/shared.js`. The
// failure is at *module load* of the parser, not in any rule, so switching the
// type-aware rules off does not reach it. TS 7 needs either typescript-eslint
// support (typescript-eslint#10940, targeted at TS 7.1) or a TypeScript-capable
// ESLint parser that is not typescript-estree.
//
// The presets are still extended rather than deleted, deliberately: they are the
// safety net. If a future @eslint/js, typescript-eslint, react-hooks or
// @tanstack/query release adds a rule to its recommended set, oxlint will not
// know about it and ESLint will start reporting it here — loudly — instead of
// it being silently lost. If that new rule needs type information, ESLint will
// fail with typescript-eslint's "requires type information" error; the fix is to
// add the rule to .oxlintrc.json (preferred) or re-enable `projectService` below.
import { readFileSync } from "node:fs"
import { fileURLToPath } from "node:url"
import js from "@eslint/js"
import globals from "globals"
import reactHooks from "eslint-plugin-react-hooks"
import reactRefresh from "eslint-plugin-react-refresh"
import tseslint from "typescript-eslint"
import tanstackQuery from "@tanstack/eslint-plugin-query"
import oxlint from "eslint-plugin-oxlint"
import { parse as parseJsonc } from "jsonc-parser"
import { defineConfig, globalIgnores } from "eslint/config"
import classnames from "./eslint-plugin-classnames/index.js"

const oxlintConfigPath = fileURLToPath(new URL("./.oxlintrc.json", import.meta.url))

// Every ESLint rule name reachable from this config, so the derivation below
// can never name a rule that does not exist (ESLint rejects unknown rule names).
const knownRules = new Set([
  ...Object.keys(js.configs.all.rules),
  ...Object.keys(tseslint.plugin.rules).map((r) => `@typescript-eslint/${r}`),
  ...Object.keys(reactHooks.rules).map((r) => `react-hooks/${r}`),
  ...Object.keys(reactRefresh.rules).map((r) => `react-refresh/${r}`),
  ...Object.keys(tanstackQuery.rules).map((r) => `@tanstack/query/${r}`),
  ...Object.keys(classnames.rules).map((r) => `classnames/${r}`),
])

// oxlint rule name -> the ESLint rule name(s) it stands in for.
//
// `eslint-plugin-oxlint` already maps oxlint's *native* rules (including the
// aliasing cases, e.g. oxlint's TS-aware `no-unused-vars` covering both the core
// rule and `@typescript-eslint/no-unused-vars`). What it does not map is our two
// JS plugins, and the React Compiler rules — which oxlint files under `react/*`
// while eslint-plugin-react-hooks files them under `react-hooks/*`. Deriving the
// whole set from .oxlintrc.json rather than listing the gaps by hand is what
// makes drift impossible: a rule added there is switched off here automatically.
function eslintNamesFor(oxlintRule) {
  if (oxlintRule.startsWith("classnames/") || oxlintRule.startsWith("@tanstack/query/")) {
    return [oxlintRule]
  }
  if (oxlintRule.startsWith("typescript/")) {
    return [`@typescript-eslint/${oxlintRule.slice("typescript/".length)}`]
  }
  if (oxlintRule === "react/only-export-components") {
    return ["react-refresh/only-export-components"]
  }
  if (oxlintRule.startsWith("react/")) {
    return [`react-hooks/${oxlintRule.slice("react/".length)}`]
  }
  // A bare oxlint core rule. oxlint parses TypeScript natively, so its core
  // rules also stand in for the `@typescript-eslint/` extension rule of the
  // same name where one exists (no-unused-vars, no-array-constructor,
  // no-unused-expressions).
  return [oxlintRule, `@typescript-eslint/${oxlintRule}`]
}

// Every rule named in .oxlintrc.json goes off here, whatever its severity there.
// The enabled ones are off because oxlint reports them; the ones .oxlintrc.json
// explicitly sets to "off" are off because we do not want them reported at all
// (the six `no-unsafe-*` / `no-base-to-string` rules, and
// react-hooks/incompatible-library — .oxlintrc.json carries the reasoning).
const oxlintRules = parseJsonc(readFileSync(oxlintConfigPath, "utf8")).rules ?? {}
const oxlintOwned = Object.fromEntries(
  Object.keys(oxlintRules)
    .flatMap(eslintNamesFor)
    .filter((name) => knownRules.has(name))
    .map((name) => [name, "off"]),
)

export default defineConfig([
  globalIgnores(["dist"]),
  {
    files: ["**/*.{ts,tsx}"],
    extends: [
      js.configs.recommended,
      // Not `recommendedTypeChecked`: every type-aware rule it enables is owned
      // by oxlint now (oxlint-tsgolint embeds its own typescript-go), and the
      // non-type-aware half of the preset is identical either way. Keeping the
      // type-checked variant would only add rules this config immediately turns
      // off, at the cost of pinning ESLint to typescript-eslint's TS support.
      tseslint.configs.recommended,
      reactHooks.configs.flat.recommended,
      reactRefresh.configs.vite,
      ...tanstackQuery.configs["flat/recommended"],
    ],
    plugins: {
      classnames,
    },
    languageOptions: {
      ecmaVersion: "latest",
      globals: globals.browser,
    },
    linterOptions: {
      // oxlint owns this now (`options.reportUnusedDisableDirectives` in
      // .oxlintrc.json). ESLint cannot judge it any more: almost every rule
      // named by a suppression comment in src/ is switched off below, so ESLint
      // would report every one of those comments as unused.
      reportUnusedDisableDirectives: "off",
    },
  },
  // Must come last: switches off every rule oxlint owns.
  ...oxlint.buildFromOxlintConfigFile(oxlintConfigPath),
  {
    name: "oxlint/owned-derived-from-oxlintrc",
    rules: oxlintOwned,
  },
])
