import js from "@eslint/js"
import globals from "globals"
import reactHooks from "eslint-plugin-react-hooks"
import reactRefresh from "eslint-plugin-react-refresh"
import tseslint from "typescript-eslint"
import tanstackQuery from "@tanstack/eslint-plugin-query"
import { defineConfig, globalIgnores } from "eslint/config"
import classnames from "./eslint-plugin-classnames/index.js"

export default defineConfig([
  globalIgnores(["dist"]),
  {
    files: ["**/*.{ts,tsx}"],
    extends: [
      js.configs.recommended,
      // recommendedTypeChecked enables type-aware rules (no-floating-promises etc.)
      // It runs the TypeScript compiler in the background — slower but catches more bugs.
      tseslint.configs.recommendedTypeChecked,
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
      parserOptions: {
        projectService: true,
        tsconfigRootDir: import.meta.dirname,
      },
    },
    rules: {
      "@typescript-eslint/no-unused-vars": [
        "error",
        {
          argsIgnorePattern: "^_",
          varsIgnorePattern: "^_",
          caughtErrorsIgnorePattern: "^_",
          destructuredArrayIgnorePattern: "^_",
          ignoreRestSiblings: true,
        },
      ],

      // Type-aware promise rules. Use `void somePromise` to explicitly signal
      // "fire and forget — this decision is intentional".
      "@typescript-eslint/no-floating-promises": "error",
      "@typescript-eslint/no-misused-promises": [
        "error",
        // Allow async event handlers — common in React. The footgun is returning
        // a Promise from a void function (e.g. setTimeout callback), not handlers.
        // properties: false — config-object props like onClick/onTrigger expecting
        // () => void are the same pattern as JSX attributes; navigate/fetchNextPage
        // return Promises but callers never await them (fire-and-forget navigation).
        { checksVoidReturn: { arguments: false, attributes: false, properties: false } },
      ],

      // any silences the compiler for real issues. Warn for now while we drive
      // existing any usages toward unknown / never / proper types. Footgun: any
      // infects callers — the type unsafety spreads silently.
      "@typescript-eslint/no-explicit-any": "warn",

      // These fire because of existing `any` in the codebase (no-unsafe-* rules
      // from recommendedTypeChecked). They'll resolve as no-explicit-any is fixed.
      // Suppressed until then to keep the signal clean.
      "@typescript-eslint/no-unsafe-assignment": "off",
      "@typescript-eslint/no-unsafe-argument": "off",
      "@typescript-eslint/no-unsafe-member-access": "off",
      "@typescript-eslint/no-unsafe-call": "off",
      "@typescript-eslint/no-unsafe-return": "off",

      // no-base-to-string: fires on any-typed values too — suppress until
      // no-explicit-any cleanup is done.
      "@typescript-eslint/no-base-to-string": "off",

      // Type-safe no-ops — these are low-noise wins from recommendedTypeChecked.
      "@typescript-eslint/no-unnecessary-type-assertion": "error",
      "@typescript-eslint/no-unnecessary-condition": [
        "warn",
        { allowConstantLoopConditions: true },
      ],

      // Prefer `import type` for type-only imports — Vite's bundler benefits from
      // knowing at parse time which imports are types vs values.
      "@typescript-eslint/consistent-type-imports": [
        "warn",
        { prefer: "type-imports", fixStyle: "inline-type-imports" },
      ],

      // React Compiler compatibility warning — TanStack Virtual's useVirtualizer()
      // returns a mutable class instance the compiler can't auto-memoize. Components
      // using it carry a `'use no memo'` directive to explicitly opt out of compilation
      // when the compiler is eventually enabled. Until then this is noise.
      "react-hooks/incompatible-library": "off",

      // Enforce idiomatic className construction with cn()
      "classnames/no-template-literal": "warn",
      "classnames/no-concatenation": "warn",
      "classnames/no-bare-ternary": "warn",
      "classnames/no-redundant-cn": "warn",
      "classnames/no-dup-ternary": "warn",
      "classnames/prefer-cva": "warn",

      // TanStack Query — prefer-query-options is in recommendedStrict; adding it
      // here enforces the queryOptions() wrapper which gives type-safe inference.
      "@tanstack/query/prefer-query-options": "warn",
    },
  },
  // TanStack Router route files export `Route` (a config object) alongside
  // local component definitions. react-refresh flags this as a fast-refresh
  // incompatibility, but it's the framework's required pattern.
  {
    files: ["src/routes/**/*.tsx"],
    rules: {
      "react-refresh/only-export-components": "off",
    },
  },
])
