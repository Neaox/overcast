import { useState, useRef } from "react";
import {
  AlertTriangle,
  CheckCircle2,
  ChevronDown,
  ExternalLink,
  Play,
} from "lucide-react";
import { tv } from "tailwind-variants";
import { cn } from "../lib/cn";
import { awsDocsUrl } from "../lib/aws-docs";
import {
  reproduceCommand,
  extractRequestIds,
  openHandler,
} from "../lib/format";
import { prevStatusKey } from "../types/state";
import {
  RunButton,
  RetryButton,
  CopyButton,
  CancelCellButton,
  RunCellButton,
} from "./run-controls";
import { useResizeHeight } from "../hooks/use-resize-height";
import { StatusBadge, StatusIcon, CountChip } from "./status";
import { PassRateBar } from "./pass-rate-bar";
import type {
  ServiceSection,
  GroupRow,
  TestCell,
  SuiteInfo,
  Status,
} from "../types/index";

// ─── Detail panel variants ────────────────────────────────────────────────────

// ─── ServiceTable ─────────────────────────────────────────────────────────────

export function ServiceTable({
  section,
  suites,
  suiteInfos,
  plannedSuites,
  headerH,
  isRunning,
  hiddenStatuses,
  prevStatuses,
  interactive,
  onCellRun,
}: {
  section: ServiceSection;
  suites: string[];
  suiteInfos: Record<string, SuiteInfo>;
  plannedSuites: { id: string; label: string }[];
  headerH: number;
  isRunning: boolean;
  hiddenStatuses: ReadonlySet<Status>;
  prevStatuses: Record<string, Status>;
  interactive?: boolean;
  onCellRun?: (filter: { suite: string; group: string; test: string }) => void;
}) {
  const [userCollapsed, setUserCollapsed] = useState(false);
  const [manuallyToggled, setManuallyToggled] = useState(false);
  const [expandedCell, setExpandedCell] = useState<string | null>(null);
  const svcBtnRef = useRef<HTMLButtonElement>(null);
  const svcBtnH = useResizeHeight(svcBtnRef, 36);

  const groups = [...section.groups.values()];
  const totals = svcTotals(section, suites);
  const isAllPass =
    totals.fail === 0 &&
    totals.skip === 0 &&
    totals.unimplemented === 0 &&
    totals.pass > 0;

  // Derived collapse state — eliminates the useEffect anti-pattern of
  // "adjusting state based on props". Auto-collapse only when not manually toggled.
  const collapsed = manuallyToggled ? userCollapsed : isAllPass && !isRunning;

  // colSpan: Group col is covered by rowspan; Operation col + one per suite + planned
  const colSpan = 1 + suites.length + plannedSuites.length;

  function toggleCell(cellKey: string) {
    setExpandedCell((prev) => (prev === cellKey ? null : cellKey));
  }

  return (
    <section
      id={`svc-${section.service}`}
      className="mb-7"
      style={{ contentVisibility: "auto", containIntrinsicSize: "auto 400px" }}
    >
      {/* Section header — click to collapse */}
      <button
        ref={svcBtnRef}
        className="box-border flex items-center gap-2.5 mb-2 group text-left sticky z-6 py-1 bg-gray-50 dark:bg-gray-900 border-b border-gray-200/60 dark:border-gray-700/60"
        style={{
          top: headerH,
          marginLeft: "-1.25rem",
          width: "calc(100% + 2.5rem)",
          paddingLeft: "1.25rem",
          paddingRight: "1.25rem",
        }}
        onClick={() => {
          setManuallyToggled(true);
          setUserCollapsed((c) => !c);
        }}
      >
        <ChevronDown
          size={14}
          className={cn(
            "text-gray-400 dark:text-gray-500 shrink-0 transition-transform duration-200",
            collapsed && "-rotate-90",
          )}
        />
        <h2 className="flex items-center gap-1.5 text-xs font-semibold text-gray-600 dark:text-gray-300 uppercase tracking-widest group-hover:text-gray-900 dark:group-hover:text-gray-100 transition-colors">
          {section.service}
          {isAllPass && (
            <CheckCircle2
              size={13}
              className="text-green-500 shrink-0"
              strokeWidth={2.5}
            />
          )}
        </h2>
        <div className="flex items-center gap-1 ml-1">
          <CountChip status="pass" count={totals.pass} />
          <CountChip status="fail" count={totals.fail} />
          <CountChip status="unimplemented" count={totals.unimplemented} />
          <CountChip status="skip" count={totals.skip} />
          <CountChip status="na" count={totals.na} />
        </div>
        {totals.pass + totals.fail > 0 && (
          <div className="ml-2 w-28">
            <PassRateBar
              pass={totals.pass}
              fail={totals.fail}
              skip={totals.skip}
              unimplemented={totals.unimplemented}
            />
          </div>
        )}
        <div className="ml-auto flex items-center gap-1 opacity-0 group-hover:opacity-100 transition-opacity">
          <RetryButton
            filter={{ service: section.service }}
            hasFailing={svcHasNonPassing(section, suites)}
            isRunning={isRunning}
            title={`Re-run non-passing ${section.service} tests`}
            className="p-1.5 text-amber-400 hover:text-amber-600 dark:text-amber-500 dark:hover:text-amber-400 hover:bg-amber-50 dark:hover:bg-amber-950"
          />
          <RunButton
            filter={{ service: section.service }}
            isRunning={isRunning}
            title={`Re-run all ${section.service} tests`}
            className="p-1.5 text-gray-400 hover:text-blue-500 dark:text-gray-500 dark:hover:text-blue-400 hover:bg-blue-50 dark:hover:bg-blue-950"
          />
        </div>
      </button>

      {!collapsed && (
        <div className="overflow-x-auto rounded-xl border border-gray-200 dark:border-gray-700 bg-white dark:bg-gray-800 shadow-sm">
          <table className="w-full text-sm border-collapse table-fixed">
            <colgroup>
              <col style={{ width: "160px" }} />
              <col />
              {suites.map((s) => (
                <col key={s} style={{ width: "100px" }} />
              ))}
              {plannedSuites.map((p) => (
                <col key={`planned-${p.id}`} style={{ width: "90px" }} />
              ))}
            </colgroup>
            <thead>
              <tr className="border-b border-gray-100 dark:border-gray-700 text-xs">
                <th className="text-left px-3 py-2.5 font-medium text-gray-500 dark:text-gray-400 tracking-wide bg-gray-50 dark:bg-gray-700/50">
                  Group
                </th>
                <th className="text-left px-3 py-2.5 font-medium text-gray-500 dark:text-gray-400 tracking-wide bg-gray-50 dark:bg-gray-700/50">
                  Operation
                </th>
                {suites.map((s) => {
                  const info = suiteInfos[s];
                  const pct =
                    info && info.total > 0 && info.completed <= info.total
                      ? Math.round((info.completed / info.total) * 100)
                      : null;
                  return (
                    <th
                      key={s}
                      className="text-center px-3 py-2 font-medium text-gray-500 dark:text-gray-400 tracking-wide bg-gray-50 dark:bg-gray-700/50"
                    >
                      <div className="flex flex-col items-center gap-0.5">
                        <span className="truncate max-w-20 text-xs">{s}</span>
                        {info?.queued && (
                          <span className="text-[9px] uppercase tracking-widest text-gray-400 dark:text-gray-500">
                            queued
                          </span>
                        )}
                        {info?.done && (
                          <span className="flex items-center gap-0.5 text-[9px] text-green-500">
                            <CheckCircle2 size={9} strokeWidth={2.5} /> done
                          </span>
                        )}
                        {info && !info.done && !info.queued && pct !== null && (
                          <span className="text-[9px] text-blue-400 tabular-nums">
                            {pct}%
                          </span>
                        )}
                      </div>
                    </th>
                  );
                })}
                {plannedSuites.map((p) => (
                  <th
                    key={`planned-${p.id}`}
                    className="text-center px-2 py-2.5 bg-gray-50 dark:bg-gray-700/50 border-l border-dashed border-gray-200 dark:border-gray-600"
                  >
                    <div className="flex flex-col items-center gap-0.5">
                      <span className="text-[11px] font-medium text-gray-300 dark:text-gray-600 truncate max-w-20">
                        {p.label}
                      </span>
                      <span className="text-[9px] uppercase tracking-widest text-gray-300 dark:text-gray-600">
                        soon
                      </span>
                    </div>
                  </th>
                ))}
              </tr>
            </thead>
            <tbody>
              {groups.map((grp) => {
                const tests = [...grp.tests.entries()].filter(
                  ([, cells]) => !isRowHidden(cells, suites, hiddenStatuses),
                );
                if (tests.length === 0) return null;
                return tests.flatMap(([testName, cells], idx) => {
                  const rowKey = `${grp.name}/${testName}`;
                  const openSuite = suites.find(
                    (s) => expandedCell === `${rowKey}/${s}`,
                  );
                  const isOpen = openSuite !== undefined;
                  const hasErrors = suites.some((s) => !!cells[s]?.error);
                  const isFailed = suites.some(
                    (s) => cells[s]?.status === "fail",
                  );

                  // Rowspan for the group cell: 1 per data row + 1 for each
                  // expanded detail row that actually has errors to show.
                  const groupRowSpan =
                    idx === 0
                      ? tests.reduce((n, [k, c]) => {
                          const rk = `${grp.name}/${k}`;
                          const hasErr = suites.some((s) => !!c[s]?.error);
                          const open =
                            expandedCell !== null &&
                            expandedCell.startsWith(rk + "/") &&
                            hasErr;
                          return n + 1 + (open ? 1 : 0);
                        }, 0)
                      : 0;

                  const dataRow = (
                    <tr
                      key={rowKey}
                      className={cn(
                        "border-b border-gray-100 dark:border-gray-700 transition-colors",
                        idx === 0 &&
                          "border-t border-gray-100 dark:border-t-gray-700",
                        hasErrors
                          ? cn(
                              "cursor-pointer",
                              isOpen
                                ? "bg-red-50/70 dark:bg-red-900/20"
                                : isFailed
                                  ? "hover:bg-red-50/40 dark:hover:bg-red-900/15"
                                  : "hover:bg-amber-50/40 dark:hover:bg-amber-900/15",
                            )
                          : "hover:bg-gray-50/70 dark:hover:bg-gray-700/40",
                      )}
                    >
                      {idx === 0 ? (
                        <td
                          rowSpan={groupRowSpan}
                          className="px-3 py-2.5 align-top border-r border-gray-100 dark:border-gray-700 bg-gray-50/60 dark:bg-gray-700/30"
                        >
                          <div className="flex flex-col gap-1 items-start group/grp">
                            <span className="text-xs font-medium text-gray-500 dark:text-gray-400 leading-tight">
                              {grp.name}
                            </span>
                            <div className="flex items-center gap-0.5 opacity-0 group-hover/grp:opacity-100 transition-opacity -ml-0.5">
                              <RetryButton
                                filter={{
                                  service: section.service,
                                  group: grp.name,
                                }}
                                hasFailing={grpHasNonPassing(grp, suites)}
                                isRunning={isRunning}
                                title={`Re-run non-passing in ${grp.name}`}
                                className="p-0.5 text-amber-400 hover:text-amber-600 dark:text-amber-500 dark:hover:text-amber-400 hover:bg-amber-50 dark:hover:bg-amber-950"
                              />
                              <RunButton
                                filter={{
                                  service: section.service,
                                  group: grp.name,
                                }}
                                isRunning={isRunning}
                                title={`Re-run ${grp.name}`}
                                className="p-0.5 text-gray-400 hover:text-blue-500 dark:text-gray-500 dark:hover:text-blue-400 hover:bg-blue-50 dark:hover:bg-blue-950"
                              />
                            </div>
                          </div>
                        </td>
                      ) : null}
                      <td className="px-3 py-2.5">
                        <span className="flex items-center gap-1.5 min-w-0 group/row">
                          {(() => {
                            const firstCell = suites
                              .map((s) => cells[s])
                              .find((c) => c !== undefined);
                            const opField = firstCell?.op;
                            const opName =
                              opField === "" ? null : (opField ?? testName);
                            const url = opName
                              ? awsDocsUrl(section.service, opName)
                              : null;
                            return url ? (
                              <a
                                href={url}
                                target="_blank"
                                rel="noopener noreferrer"
                                onClick={(e) => e.stopPropagation()}
                                className="flex items-center gap-1 text-gray-800 dark:text-gray-200 group/link hover:text-blue-600 dark:hover:text-blue-400 truncate"
                              >
                                <span className="truncate">{testName}</span>
                                <ExternalLink
                                  size={11}
                                  className="shrink-0 opacity-0 group-hover/link:opacity-50 transition-opacity"
                                />
                              </a>
                            ) : (
                              <span className="text-gray-800 dark:text-gray-200 truncate">
                                {testName}
                              </span>
                            );
                          })()}
                          <RunButton
                            filter={{
                              service: section.service,
                              group: grp.name,
                              test: testName,
                            }}
                            isRunning={isRunning}
                            title={`Re-run ${testName}`}
                            className="ml-auto opacity-0 group-hover/row:opacity-100 p-1 shrink-0 text-gray-400 hover:text-blue-500 dark:text-gray-500 dark:hover:text-blue-400 hover:bg-blue-50 dark:hover:bg-blue-950 transition-opacity"
                          />
                        </span>
                      </td>
                      {suites.map((suite) => {
                        const cell = cells[suite];
                        const cellKey = `${rowKey}/${suite}`;
                        const isCellOpen = expandedCell === cellKey;
                        const prevStatus =
                          prevStatuses[
                            prevStatusKey(
                              section.service,
                              grp.name,
                              testName,
                              suite,
                            )
                          ];
                        const flipped =
                          cell &&
                          cell.status !== "running" &&
                          prevStatus &&
                          prevStatus !== cell.status;
                        const isCancellable =
                          interactive &&
                          cell &&
                          (cell.status === "running" ||
                            cell.status === "queued");
                        return (
                          <td
                            key={suite}
                            className={cn(
                              "px-3 py-2.5 text-center group/cell relative",
                              cell?.error && "cursor-pointer",
                              interactive &&
                                !cell &&
                                "cursor-pointer hover:bg-blue-50/50 dark:hover:bg-blue-900/20",
                            )}
                            title={
                              interactive && !cell
                                ? `Run ${testName} in ${suite}`
                                : cell?.error
                                  ? "Click to view error details"
                                  : undefined
                            }
                            onClick={
                              cell?.error
                                ? (e) => {
                                    e.stopPropagation();
                                    toggleCell(cellKey);
                                  }
                                : interactive && !cell && onCellRun
                                  ? (e) => {
                                      e.stopPropagation();
                                      onCellRun({
                                        suite,
                                        group: grp.name,
                                        test: testName,
                                      });
                                    }
                                  : undefined
                            }
                          >
                            {cell ? (
                              <div className="inline-flex items-center gap-1">
                                <StatusBadge
                                  status={cell.status}
                                  expandable={!!cell.error}
                                  active={isCellOpen}
                                />
                                {flipped && (
                                  <FlipBadge
                                    from={prevStatus}
                                    to={cell.status}
                                  />
                                )}
                                {isCancellable ? (
                                  <CancelCellButton
                                    suite={suite}
                                    group={grp.name}
                                    test={testName}
                                    className="opacity-0 group-hover/cell:opacity-100 absolute top-0.5 right-0.5"
                                  />
                                ) : interactive &&
                                  cell.status !== "running" &&
                                  cell.status !== "queued" ? (
                                  <RunCellButton
                                    suite={suite}
                                    group={grp.name}
                                    test={testName}
                                    className="opacity-0 group-hover/cell:opacity-100 absolute top-0.5 right-0.5"
                                  />
                                ) : null}
                              </div>
                            ) : interactive ? (
                              <span className="inline-flex items-center justify-center w-7 h-7 text-gray-300 dark:text-gray-600 transition-colors group-hover/cell:text-blue-400 dark:group-hover/cell:text-blue-500">
                                <Play size={14} strokeWidth={2.5} />
                              </span>
                            ) : (
                              <span className="text-gray-200 dark:text-gray-600 select-none">
                                —
                              </span>
                            )}
                          </td>
                        );
                      })}
                      {plannedSuites.map((p) => (
                        <td
                          key={`planned-${p.id}`}
                          className="px-3 py-2.5 text-center border-l border-dashed border-gray-100 dark:border-gray-700"
                        >
                          <span className="text-gray-200 dark:text-gray-700 select-none">
                            ·
                          </span>
                        </td>
                      ))}
                    </tr>
                  );

                  if (!isOpen || !openSuite || !cells[openSuite]?.error)
                    return [dataRow];

                  const openCell = cells[openSuite]!;
                  const panelStatus =
                    openCell.status === "fail"
                      ? "fail"
                      : openCell.status === "unimplemented"
                        ? "unimplemented"
                        : "other";
                  const { row, icon, pre } = detailPanel({
                    status: panelStatus,
                  });

                  const reproduceCmd = reproduceCommand({
                    suite: openSuite,
                    group: grp.name,
                    test: testName,
                  });
                  const requestIds = extractRequestIds(openCell.error);
                  const detailRow = (
                    <tr key={`${rowKey}/__detail`} className={row()}>
                      <td colSpan={colSpan} className="px-5 py-4">
                        <div className="flex flex-col gap-2">
                          <div className="flex items-center gap-2 flex-wrap">
                            {suites.length > 1 && (
                              <>
                                <StatusIcon
                                  status={openCell.status}
                                  size={14}
                                />
                                <span className="text-xs font-semibold text-gray-600 dark:text-gray-300 font-mono">
                                  {openSuite}
                                </span>
                              </>
                            )}
                            <div className="ml-auto flex items-center gap-1.5">
                              <button
                                type="button"
                                onClick={() => openHandler(section.service)}
                                title={`Open internal/services/${section.service}/ in VS Code`}
                                className="inline-flex items-center gap-1 text-[10px] px-2 py-0.5 text-gray-500 hover:text-blue-600 dark:text-gray-400 dark:hover:text-blue-400 border border-gray-200 dark:border-gray-700 hover:border-blue-300 dark:hover:border-blue-700 font-mono uppercase tracking-wider"
                              >
                                <ExternalLink size={12} />
                                open handler
                              </button>
                              <CopyButton
                                value={reproduceCmd}
                                title={`Copy reproduce command: ${reproduceCmd}`}
                                label="copy reproduce command"
                                className="text-[10px] px-2 py-0.5 text-gray-500 hover:text-blue-600 dark:text-gray-400 dark:hover:text-blue-400 border border-gray-200 dark:border-gray-700 hover:border-blue-300 dark:hover:border-blue-700 font-mono uppercase tracking-wider"
                              />
                            </div>
                          </div>
                          <div className="flex items-start gap-2.5">
                            <AlertTriangle
                              size={14}
                              className={cn(icon(), "mt-0.5 shrink-0")}
                            />
                            <pre className={pre()}>{openCell.error}</pre>
                          </div>
                          {requestIds.length > 0 && (
                            <div className="flex items-center gap-1.5 flex-wrap pl-6">
                              <span className="text-[10px] uppercase tracking-wider text-gray-400 dark:text-gray-500">
                                request id
                              </span>
                              {requestIds.map((id) => (
                                <CopyButton
                                  key={id}
                                  value={id}
                                  title={`Copy request ID ${id} — grep overcast logs for this value to see the server-side handling`}
                                  label={id}
                                  className="text-[10px] px-1.5 py-0.5 font-mono text-gray-500 hover:text-blue-600 dark:text-gray-400 dark:hover:text-blue-400 border border-gray-200 dark:border-gray-700 hover:border-blue-300 dark:hover:border-blue-700"
                                />
                              ))}
                            </div>
                          )}
                        </div>
                      </td>
                    </tr>
                  );

                  return [dataRow, detailRow];
                });
              })}
            </tbody>
          </table>
        </div>
      )}
    </section>
  );
}

// ─── FlipBadge ────────────────────────────────────────────────────────────────
//
// Renders a tiny arrow pill next to a test status when that status has flipped
// since the last completed run. Green for regressions-fixed (anything→pass),
// red for new regressions (pass→anything), neutral otherwise.
function FlipBadge({ from, to }: { from: Status; to: Status }) {
  const improved = to === "pass" && from !== "pass";
  const regressed = from === "pass" && to !== "pass";
  const tone = improved
    ? "text-emerald-600 dark:text-emerald-400 bg-emerald-50 dark:bg-emerald-950/60 border-emerald-200 dark:border-emerald-800"
    : regressed
      ? "text-red-600 dark:text-red-400 bg-red-50 dark:bg-red-950/60 border-red-200 dark:border-red-800"
      : "text-gray-500 dark:text-gray-400 bg-gray-50 dark:bg-gray-800 border-gray-200 dark:border-gray-700";
  return (
    <span
      title={`Changed since last run: ${from} → ${to}`}
      className={cn(
        "text-[9px] font-mono px-1 py-px rounded border leading-none",
        tone,
      )}
    >
      Δ
    </span>
  );
}

const detailPanel = tv({
  slots: {
    row: "border-b",
    icon: "",
    pre: "text-xs border rounded-lg px-3.5 py-2.5 overflow-x-auto whitespace-pre-wrap wrap-break-word leading-relaxed font-mono flex-1 shadow-sm",
  },
  variants: {
    status: {
      fail: {
        row: "bg-red-50/60 dark:bg-red-900/20 border-red-100 dark:border-red-900",
        icon: "text-red-400",
        pre: "text-red-900 dark:text-red-200 bg-white/80 dark:bg-gray-800/80 border-red-200 dark:border-red-800",
      },
      unimplemented: {
        row: "bg-gray-50/60 dark:bg-gray-700/30 border-gray-100 dark:border-gray-700",
        icon: "text-gray-400",
        pre: "text-gray-700 dark:text-gray-200 bg-white/80 dark:bg-gray-800/80 border-gray-200 dark:border-gray-600",
      },
      other: {
        row: "bg-amber-50/60 dark:bg-amber-900/15 border-amber-100 dark:border-amber-900",
        icon: "text-amber-400",
        pre: "text-amber-900 dark:text-amber-200 bg-white/80 dark:bg-gray-800/80 border-amber-200 dark:border-amber-700",
      },
    },
  },
  defaultVariants: { status: "other" },
});

// ─── Module-private helpers ───────────────────────────────────────────────────

function svcTotals(section: ServiceSection, suites: string[]) {
  let pass = 0,
    fail = 0,
    skip = 0,
    unimplemented = 0,
    na = 0;
  for (const grp of section.groups.values()) {
    for (const cells of grp.tests.values()) {
      for (const suite of suites) {
        const s = cells[suite]?.status;
        if (s === "pass") pass++;
        else if (s === "fail") fail++;
        else if (s === "skip") skip++;
        else if (s === "unimplemented") unimplemented++;
        else if (s === "na") na++;
      }
    }
  }
  return { pass, fail, skip, unimplemented, na };
}

function grpHasNonPassing(grp: GroupRow, suites: string[]): boolean {
  for (const cells of grp.tests.values()) {
    for (const suite of suites) {
      const s = cells[suite]?.status;
      if (s === "fail" || s === "skip" || s === "unimplemented") return true;
    }
  }
  return false;
}

function svcHasNonPassing(section: ServiceSection, suites: string[]): boolean {
  for (const grp of section.groups.values()) {
    if (grpHasNonPassing(grp, suites)) return true;
  }
  return false;
}

function isRowHidden(
  cells: TestCell,
  suites: string[],
  hiddenStatuses: ReadonlySet<Status>,
): boolean {
  if (hiddenStatuses.size === 0) return false;
  const statuses = suites
    .map((s) => cells[s]?.status)
    .filter((s): s is Status => s !== undefined);
  if (statuses.length === 0) return false;
  if (statuses.some((s) => s === "running")) return false;
  return statuses.every((s) => hiddenStatuses.has(s));
}
