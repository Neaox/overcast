import { Fragment } from "react"
import { ProvenanceTag } from "@/components/ui/provenance-tag"
import { formatDate, formatTimeOfDay } from "@/lib/format"
import { cn } from "@/lib/utils"
import type {
  StackDiagnostics,
  StackDiagnosticsEvent,
  StackDiagnosticsFact,
  StackDiagnosticsLog,
  StackDiagnosticsResource,
  StackDiagnosticsSection,
} from "@/services/api/cloudformation"

/**
 * The Diagnostics tab: why this deploy failed, and how much of the answer real
 * AWS would also have given you.
 *
 * A rollback deletes the resources whose logs held the answer, so what is on
 * screen here mostly no longer exists — Overcast copied it on the way out. That
 * makes the panel useful and, handled carelessly, misleading, which is why the
 * provenance tag sits on every section and the counterfactual closes the page
 * rather than being tucked into a tooltip: someone reading a captured container
 * log needs to know, before they write the fix, whether production would ever
 * have shown it to them.
 *
 * Nothing rendered here is on the AWS surface. The Events tab remains a 1:1
 * mirror of `DescribeStackEvents` and must stay one — see
 * docs/plans/cfn-deploy-diagnostics.md § "The two hard rules".
 */
export function StackDiagnosticsPanel({ diagnostics }: { diagnostics: StackDiagnostics }) {
  return (
    <div className="flex flex-col gap-4">
      <Headline diagnostics={diagnostics} />

      {diagnostics.resources.map((resource) => (
        <ResourceDiagnosis key={resource.logicalId} resource={resource} />
      ))}

      <Counterfactual text={diagnostics.counterfactual} />
    </div>
  )
}

// ─── Headline ────────────────────────────────────────────────────────────────

/**
 * The one-sentence answer, with the sentence CloudFormation recorded beside it.
 *
 * The two are shown together on purpose. Overcast's reading is the useful one
 * and AWS's is the one that would have survived in production, so putting them
 * side by side teaches the gap instead of hiding it — and each carries its own
 * provenance tag, because they come from opposite sides of the line.
 *
 * `headline` is optional: a collector that found evidence but could not draw a
 * conclusion from it emits none, and the panel then leads with the sections
 * rather than inventing a summary.
 */
function Headline({ diagnostics }: { diagnostics: StackDiagnostics }) {
  return (
    <section className="flex flex-col gap-3 rounded-md border border-border bg-bg-subtle p-4">
      {diagnostics.headline && (
        <div className="flex flex-col gap-2">
          <div className="flex flex-wrap items-center gap-2">
            <h2 className="font-mono text-sm font-medium text-fg">What Overcast found</h2>
            <ProvenanceTag provenance="overcast-inference" />
          </div>
          <p className="text-[15px] leading-relaxed font-medium text-fg">{diagnostics.headline}</p>
        </div>
      )}

      {diagnostics.awsReason && (
        <div className="flex flex-col gap-2">
          <div className="flex flex-wrap items-center gap-2">
            <h2 className="font-mono text-sm font-medium text-fg">What CloudFormation recorded</h2>
            <ProvenanceTag provenance="aws-api" />
          </div>
          <p className="text-[13px] leading-relaxed text-fg-muted">{diagnostics.awsReason}</p>
        </div>
      )}

      <p className="text-xs text-fg-subtle">
        {diagnostics.operation} · {diagnostics.stackStatus} · captured{" "}
        {formatDate(diagnostics.capturedAt)}
      </p>
    </section>
  )
}

// ─── Per-resource ────────────────────────────────────────────────────────────

function ResourceDiagnosis({ resource }: { resource: StackDiagnosticsResource }) {
  return (
    <section className="flex flex-col gap-3 rounded-md border border-border p-4">
      <header className="flex min-w-0 flex-col gap-1">
        <div className="flex flex-wrap items-baseline gap-2">
          <h3 className="font-mono text-sm font-medium text-fg">{resource.logicalId}</h3>
          <span className="font-mono text-xs text-fg-muted">{resource.type}</span>
        </div>
        {resource.physicalId && (
          <span className="truncate font-mono text-xs text-fg-subtle" title={resource.physicalId}>
            {resource.physicalId}
          </span>
        )}
        {resource.statusReason && (
          <p className="text-[13px] text-danger">{resource.statusReason}</p>
        )}
      </header>

      {resource.sections.map((section) => (
        <Section key={section.id} section={section} />
      ))}
    </section>
  )
}

function Section({ section }: { section: StackDiagnosticsSection }) {
  return (
    <div className="flex min-w-0 flex-col gap-2">
      <div className="flex flex-wrap items-center gap-2">
        <h4 className="font-mono text-xs font-medium text-fg">{section.title}</h4>
        <ProvenanceTag provenance={section.provenance} />
      </div>
      {section.note && <p className="text-[13px] text-fg-muted">{section.note}</p>}
      <SectionBody section={section} />
    </div>
  )
}

/**
 * One renderer per section kind, and the switch is exhaustive by construction.
 *
 * The `never` assignment in the default arm is the point of modelling the
 * payload as a discriminated union: when a collector server-side grows a fourth
 * kind, this stops compiling instead of quietly rendering an empty section
 * whose absence nobody would notice until someone needed it.
 */
function SectionBody({ section }: { section: StackDiagnosticsSection }) {
  switch (section.kind) {
    case "facts":
      return <FactsBody facts={section.facts} />
    case "events":
      return <EventsBody events={section.events} />
    case "log":
      return <LogBody log={section.log} />
    default: {
      const unhandled: never = section
      return unhandled
    }
  }
}

// ─── Section bodies ──────────────────────────────────────────────────────────

/**
 * Label/value pairs as a definition list.
 *
 * A definition list rather than a table because these are attributes of one
 * thing, not rows of comparable things — and it degrades to a readable stack on
 * a narrow viewport, which a two-column table does not.
 */
function FactsBody({ facts }: { facts: StackDiagnosticsFact[] }) {
  if (facts.length === 0) return null
  return (
    <div className="overflow-x-auto rounded-md border border-border bg-bg-muted">
      <dl className="grid grid-cols-[max-content_1fr] gap-x-4 gap-y-1 p-3 text-[13px]">
        {facts.map((fact, i) => (
          // Labels legitimately repeat within a section — three containers in
          // one task each have an "Exit code" — so the index is what makes the
          // key unique, and the list is static for a given payload.
          <Fragment key={`${fact.label}-${i}`}>
            <dt className="font-mono whitespace-nowrap text-fg-muted">{fact.label}</dt>
            <dd className="min-w-0 font-mono break-words text-fg">
              {fact.value}
              {fact.hint && <span className="ml-2 text-fg-subtle">({fact.hint})</span>}
            </dd>
          </Fragment>
        ))}
      </dl>
    </div>
  )
}

/**
 * A service's own event feed, in the order the collector recorded it.
 *
 * Times are wall-clock to the second, not dated: every event in one of these
 * sections belongs to the same failing deploy, whose date is already on the
 * capture stamp above, and a deploy that wedges usually produces several events
 * inside the same minute — which minute-precision would collapse into a column
 * of identical stamps.
 */
function EventsBody({ events }: { events: StackDiagnosticsEvent[] }) {
  if (events.length === 0) return null
  return (
    <div className="max-h-80 overflow-auto rounded-md border border-border bg-bg-muted">
      <ul className="flex flex-col gap-1 p-3 text-[13px]">
        {events.map((event, i) => (
          <li key={`${event.at}-${i}`} className="flex gap-3">
            <span className="shrink-0 font-mono text-xs whitespace-nowrap text-fg-subtle">
              {formatTimeOfDay(event.at)}
            </span>
            <span className="min-w-0 break-words text-fg">{event.message}</span>
          </li>
        ))}
      </ul>
    </div>
  )
}

/**
 * Captured output, monospace, in its own scroll container.
 *
 * `whitespace-pre` with the overflow on the wrapper rather than `pre-wrap`: a
 * stack trace's indentation is load-bearing, and reflowing it to the panel
 * width destroys the shape that makes it readable. The container scrolls in
 * both axes so a long line never widens the page — the tab must not introduce
 * horizontal scroll on the document.
 */
function LogBody({ log }: { log: StackDiagnosticsLog }) {
  return (
    <div className="flex flex-col gap-1">
      {log.label && <span className="font-mono text-xs text-fg-subtle">{log.label}</span>}
      <pre
        role="region"
        aria-label={log.label ? `Captured output — ${log.label}` : "Captured output"}
        tabIndex={0}
        className={cn(
          "max-h-96 overflow-auto rounded-md border border-border bg-bg-muted p-3",
          "font-mono text-xs leading-relaxed whitespace-pre text-fg",
        )}
      >
        {log.text}
      </pre>
      {(log.truncated || log.capturedAt) && (
        <p className="text-xs text-fg-subtle">
          {log.truncated && "Truncated — only the tail was kept. "}
          {log.capturedAt && `Captured ${formatDate(log.capturedAt)}`}
        </p>
      )}
    </div>
  )
}

// ─── Counterfactual ──────────────────────────────────────────────────────────

/**
 * What real AWS would have left you with, at the foot of the tab.
 *
 * This sentence does more anti-misleading work than any badge, because it
 * teaches the difference rather than disclaiming it — and it is directly
 * useful: it tells the reader whether the fix they are about to write depends
 * on a signal they will actually have in production. Visually distinct from the
 * evidence above it so it does not read as one more finding.
 */
function Counterfactual({ text }: { text: string }) {
  return (
    <footer className="flex flex-col gap-1 rounded-md border border-dashed border-border bg-bg-subtle p-4">
      <span className="font-mono text-xs font-medium tracking-wide text-fg-muted uppercase">
        In real AWS
      </span>
      <p className="text-[13px] leading-relaxed text-fg-muted">{text}</p>
    </footer>
  )
}
