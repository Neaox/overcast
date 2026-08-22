# shadcn React Aria migration — plan

> Status: **closed — not adopted (2026-08-22)**, recorded as
> [#1182](https://github.com/Neaox/overcast/issues/1182) (closed, not planned). The web UI
> stays on Radix; #1101/#1102/#1103 proceed on it. Cost/benefit: no user-facing value for an
> AWS emulator, a11y goals reachable within Radix, and a base migration would have to precede
> the #1101 scaffolds or they get built twice. **Reopen trigger:** a concrete primitive the
> console needs that Radix lacks or does materially worse. The design below is kept as the
> incremental approach to use if that trigger fires.
> (As of 2026-08-21 no migration had started: no `web/components.json`, no `components/aria/`,
> no React Aria dependency; direct-Radix usage in §1 had grown to include alert-dialog,
> dropdown-menu, scroll-area, select, separator and tabs.)
> Goal: adopt shadcn's React Aria component base for new complex UI primitives,
> while preserving Overcast's stable app-facing component APIs and avoiding a
> broad feature-page rewrite.

## 1. Context

shadcn/ui now supports React Aria as a first-class component base alongside Base
UI and Radix. This gives us access to a larger registry of accessible,
composable components, but it is not a drop-in replacement for the current web
UI.

The current web app is documented as using Radix primitives
([web/README.md](../../web/README.md)), but direct Radix usage is relatively
small:

- `@radix-ui/react-dialog`
- `@radix-ui/react-popover`
- `@radix-ui/react-tooltip`
- `@radix-ui/react-toast`
- `@radix-ui/react-label`
- `@radix-ui/react-slot`

Most feature pages already import through local wrappers in
`web/src/components/ui`. That indirection is the migration lever: switch internals
behind stable wrappers first, then optionally simplify call sites later.

## 2. Recommendation

Adopt React Aria shadcn incrementally, not as a big-bang base switch.

1. Generate React Aria shadcn components into a separate namespace, not directly
   into `web/src/components/ui`.
2. Keep feature pages importing from `@/components/ui/*`.
3. Use `@/components/ui/*` as Overcast's stable adapter layer.
4. Prefer React Aria shadcn for new complex primitives.
5. Migrate existing primitives one family at a time, with behavior tests before
   each migration.

## 3. Target layout

```text
web/src/components/
  aria/       # generated shadcn React Aria components, kept close to registry output
  ui/         # Overcast app-facing wrappers and design-system components
```

Rules:

- Generated registry code goes in `components/aria`.
- App and feature code imports from `components/ui` unless a component is still
  experimental.
- `components/ui` may wrap, adapt, or re-export `components/aria` components.
- Do not overwrite existing `components/ui` files with generated output.
- Keep generated components close to shadcn output; put Overcast-specific API
  compatibility in wrappers.

This gives us the registry benefits without making every feature page depend on
React Aria's native API shapes immediately.

## 4. Setup work

1. Add `web/components.json` configured for React Aria base.
2. Point shadcn's generated UI alias at `@/components/aria`, not
   `@/components/ui`.
3. Add initial React Aria dependencies through the shadcn CLI rather than by
   hand where possible.
4. Generate a small initial component set:
   - `button`
   - `dialog`
   - `tooltip`
   - `tabs`
   - `combobox`
   - `popover`
   - `field`
   - `select`
   - `checkbox`
   - `switch`
5. Normalize generated styles to Overcast tokens:
   - `bg-bg`
   - `bg-bg-elevated`
   - `bg-bg-muted`
   - `text-fg`
   - `text-fg-muted`
   - `text-fg-subtle`
   - `border-border`
   - `text-accent`
   - `bg-accent`
6. Run `cd web && npm run lint && npm run typecheck && npm test` after the
   initial generation and after each migrated component family.

## 5. Migration phases

### Phase 0 — inventory and guardrails

1. Add this plan and update `web/AGENTS.md` with the namespace rule before code
   migration starts.
2. Add or strengthen tests around existing behavior for the target component.
3. Confirm no generated route file changes are needed; never edit
   `web/src/routeTree.gen.ts`.

### Phase 1 — low-risk adapters

Migrate small, contained primitives first to prove the approach.

1. `Tooltip`: preserve the current `<Tooltip content side>{children}</Tooltip>`
   API while using React Aria internally.
2. `Switch`: preserve `checked`, `onCheckedChange`, `disabled`, `id`, and
   `className`.
3. `Tabs`: preserve `Tabs`, `TabList`, `Tab`, and `TabPanel` exports.

These are good first targets because the APIs are small and regressions are easy
to test.

### Phase 2 — Combobox pilot

Replace the internals of `web/src/components/ui/combobox.tsx` behind the current
generic API.

Do not rewrite every combobox call site in the same PR. Existing usages include:

- region selection (`RegionSelect`, `RegionSelectCompact`)
- resource ARN selection (`ResourceArnCombobox`)
- RDS engine and instance class selection
- Lambda runtime, subnet, security group, and layer selection
- EC2 internet gateway and VPC-related selectors
- S3/SQS/SNS/Pipes ARN selectors with free-text fallback

This is the highest-value migration because the current combobox hand-rolls
keyboard/listbox behavior and popover focus handling. It is also the highest-risk
component because it supports custom values, disabled item reasons, custom
footers, multi-select chips, and label-vs-stored-value behavior.

### Phase 3 — Dialog

Migrate `web/src/components/ui/dialog.tsx` after smaller adapters are proven.

Preserve exports:

- `Dialog`
- `DialogTrigger`
- `DialogContent`
- `DialogHeader`
- `DialogBody`
- `DialogFooter`
- `DialogTitle`
- `DialogDescription`
- `DialogClose`

Handle direct Radix imports separately:

- `web/src/components/layout/global-search.tsx`
- `web/src/features/map/log-stream-peek.tsx`
- `web/src/features/map/lambda-invocations-drawer.tsx`

### Phase 4 — new components first

Use React Aria shadcn as the default source for components we do not already
have, especially:

- `Checkbox`
- richer `Field`
- richer `Select`
- `Date Picker`
- `Calendar`
- `Popover`
- `Dropdown Menu`

Avoid migrating presentational components unless there is a concrete benefit.

### Phase 5 — cleanup

1. Remove unused Radix dependencies only after no imports remain.
2. Update `web/README.md` from "Radix UI primitives" to the new component
   architecture.
3. Consider optional call-site cleanup where React Aria's native composition is
   clearer than compatibility adapters.

## 6. Component disposition

| Component | Current status | Plan |
|---|---|---|
| `Button` | Local wrapper using Radix `Slot` for `asChild` | Keep initially. Revisit only if `asChild` replacement is clear. |
| `Input` | Native input wrapper | Keep. |
| `Textarea` | Native textarea wrapper | Keep. |
| `Select` | Native select wrapper | Keep for simple native selects. Add richer React Aria select separately if needed. |
| `Table` | Semantic table wrappers | Keep. |
| `Badge` | Presentational | Keep. |
| `Card` | Presentational | Keep. |
| `Spinner` | Presentational | Keep. |
| `EmptyState` | Presentational | Keep. |
| `Tooltip` | Radix wrapper, few call sites | Early adapter migration. |
| `Tabs` | Hand-rolled | Early React Aria migration. |
| `Switch` | Hand-rolled button with `role="switch"` | Early React Aria migration. |
| `Combobox` | Custom, complex, accessibility-sensitive | Main pilot after tests. |
| `Dialog` | Radix wrapper, many call sites | Migrate after smaller adapters. |
| `Toast` | Custom context plus Radix toast | Defer unless there is a specific problem. |
| `Label` | Radix label wrapper | Low priority; native label is often enough. |

## 7. Prop conversion map

### Tooltip

| Current API | React Aria / shadcn Aria concept |
|---|---|
| `content` | tooltip children/content |
| `side` | `placement` |
| `children` | trigger child |
| `TooltipProvider delayDuration=300` | adapter-level default delay |

Keep the current app API:

```tsx
<Tooltip content="Help" side="top">
  <Button>Hover</Button>
</Tooltip>
```

### Switch

| Current API | React Aria concept |
|---|---|
| `checked` | `isSelected` |
| `onCheckedChange` | `onChange` |
| `disabled` | `isDisabled` |
| `id` | `id` |
| `className` | `className` |

### Tabs

| Current API | React Aria / shadcn Aria concept |
|---|---|
| `selectedKey` | `selectedKey` |
| `onSelectionChange(key: string)` | `onSelectionChange(key: Key)` with string normalization |
| `<Tab id>` | trigger/tab `id` |
| `isDisabled` | `isDisabled` |
| `<TabPanel id>` | content/panel `id` |
| `className` | pass through |

Keep current names to avoid rewriting the existing tab call sites.

### Dialog

| Current API | React Aria / shadcn Aria concept |
|---|---|
| `<Dialog open onOpenChange>` | controlled modal/dialog state |
| `<DialogTrigger asChild>` | trigger composition; may need adapter or call-site rewrite |
| `<DialogContent className aria-describedby>` | modal/dialog content wrapper |
| `<DialogClose>` | close button/render-prop close action |
| `<DialogTitle>` | title/heading slot |
| `<DialogDescription>` | description element and `aria-describedby` wiring |
| default close button | preserve in `DialogContent` adapter |
| `onClick={(e) => e.stopPropagation()}` on content | verify whether still needed |

Risk: React Aria dialog composition differs from Radix. Preserve the current API
until every existing dialog has behavior tests.

### Combobox

| Current API | React Aria / shadcn Aria concept |
|---|---|
| `items` | collection/list items |
| `value: string` | `selectedKey` and/or `inputValue` depending on usage |
| `onChange(value: string)` | selection and input-value change bridge |
| `filterFn(item, query)` | adapter-managed filtering or React Aria filtering |
| `getItemValue(item)` | item `id`/key |
| `renderItem(item, ctx)` | item children/render prop |
| `renderSeparator(item, prev)` | grouped sections or separator rendering |
| `isItemDisabled(item)` | `disabledKeys` plus disabled reason rendering |
| `allowCustom` | custom/free-text value support |
| `renderCustomFooter(query, select)` | custom list/popover footer |
| `emptyMessage` | empty state renderer |
| `isLoading` | disabled/loading state |
| `popoverWidth` | content class/style |
| `multiple` | multi-selection plus chip list |
| `inputClassName` | input slot class |

Combobox should remain an adapter initially. Converting every call site to native
React Aria collection syntax should be optional follow-up work.

## 8. Feature gaps and compatibility risks

- `asChild` is Radix-specific. `Button asChild`, `DialogTrigger asChild`, and
  direct `Dialog.Close asChild` uses need adapter support or call-site rewrites.
- React Aria shadcn does not appear to cover every Radix/Base category in the
  same way. If we need components such as hover card, menubar, or navigation
  menu, we may still use Radix/Base/custom components.
- Current combobox usages often display a friendly label while storing an ARN or
  ID. React Aria's `selectedKey` and `inputValue` distinction must be handled
  deliberately.
- Free-text ARN entry is mandatory for AWS workflows. Any React Aria combobox
  migration must preserve `allowCustom` and custom footer behavior.
- Disabled combobox items currently display disabled reasons. React Aria can
  disable keys, but rendering the reason remains our responsibility.
- Dialog focus behavior, close behavior, and `aria-describedby` wiring may differ
  from Radix.
- Generated shadcn styles may not match Overcast's Tailwind v4 token system out
  of the box.
- React Aria uses `Key` types in several APIs; Overcast code currently assumes
  strings.

## 9. Regression prevention

### General rules

1. Add failing/characterization tests before changing a component's internals.
2. Migrate one component family per PR.
3. Preserve public `@/components/ui/*` APIs first.
4. Only rewrite feature-page call sites after the adapter is proven.
5. Prefer semantic tests with Testing Library roles and user events.
6. Run `cd web && npm run lint && npm run typecheck && npm test` for every
   migrated family.
7. Run `cd web && npm run build` for larger migrations such as Combobox or
   Dialog.

### Combobox test checklist

- Opens on focus/click.
- Filters by typed query.
- Arrow keys move the active item.
- Enter selects the active item.
- Escape closes without changing the value.
- Disabled items cannot be selected.
- Disabled item reasons remain visible.
- Custom free-text value can be selected.
- Custom footer can select a value.
- Multi-select chips add and remove values.
- Backspace removes the last chip when the query is empty.
- Popover inside `Dialog` does not close the dialog when selecting an item.
- `RegionSelect` keeps custom region fallback.
- `ResourceArnCombobox` keeps ARN display, free-text fallback, and loading state.

### Dialog test checklist

- Controlled `open` / `onOpenChange` works.
- Trigger opens the dialog.
- Escape closes the dialog.
- Close button closes the dialog.
- Dialog title is exposed by role/name.
- `aria-describedby` behavior does not regress.
- Forms inside dialogs submit and cancel correctly.
- Dialog content clicks do not trigger parent row/card handlers unexpectedly.
- Global search still focuses the input and closes on navigation.

### Tabs test checklist

- Correct panel renders for `selectedKey`.
- Clicking a tab calls `onSelectionChange` with a string key.
- Disabled tabs cannot be selected.
- Keyboard navigation works after the React Aria conversion.

### Tooltip test checklist

- Tooltip appears on hover.
- Tooltip appears on keyboard focus.
- `side` maps to expected placement.
- Disabled-button tooltip patterns still work where used.

## 10. Rollback strategy

Because app code continues importing from `@/components/ui/*`, each component can
be rolled back by reverting only that wrapper's internals and dependency changes.

Do not mix registry generation, adapter migration, and unrelated feature UI work
in the same PR. Keep each migration small enough that reverting it does not undo
unrelated product changes.

## 11. Definition of done for the migration

The migration is complete when:

1. New complex shared UI components are generated from React Aria shadcn by
   default.
2. Existing high-value primitives (`Tooltip`, `Tabs`, `Switch`, `Combobox`, and
   optionally `Dialog`) are React Aria-backed or have an explicit reason to stay
   Radix/custom.
3. Feature pages still import through `@/components/ui/*` unless there is a
   deliberate exception.
4. Unused Radix dependencies are removed.
5. `web/README.md` and `web/AGENTS.md` describe the final component strategy.
6. `cd web && npm run lint && npm run typecheck && npm test && npm run build`
   passes.
