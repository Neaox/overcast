import { Fragment, useMemo, useState } from "react";
import {
  AlertTriangle,
  ChevronRight,
  Hammer,
  RefreshCw,
  Rocket,
  Search,
  Trash2,
} from "lucide-react";
import { cn } from "../lib/cn";
import { StatusBadge, StatusIcon, CountChip } from "./status";
import { PassRateBar } from "./pass-rate-bar";
import { RunButton, RetryButton, CopyButton } from "./run-controls";
import {
  reproduceCommand,
  extractRequestIds,
  openHandler,
} from "../lib/format";
import { prevStatusKey } from "../types/state";
import type { ServiceSection, Status, QueueEntry } from "../types/index";

const SUITE_ID = "cdk";

type StageKind = "sequential" | "parallel";

interface StageDef {
  id: string;
  label: string;
  kind: StageKind;
  icon: typeof Hammer;
  accent: string;
  maxHeight?: string;
  tests: { name: string; label: string; hint: string }[];
}

// The CDK lifecycle flow, broken into stages. Test names
// here must match the raw test names emitted by the cdk suite (registry.json
// + compat/suites/cdk/src/groups/lifecycle.ts).
const STAGES: StageDef[] = [
  {
    id: "prepare",
    label: "Prepare",
    kind: "sequential",
    icon: Hammer,
    accent: "blue",
    tests: [
      {
        name: "Bootstrap",
        label: "Bootstrap CDK",
        hint: "cdk bootstrap — provisions the CDKToolkit stack",
      },
      {
        name: "Synth",
        label: "Synthesize template",
        hint: "cdk synth — generates the CloudFormation template",
      },
    ],
  },
  {
    id: "deploy",
    label: "Deploy",
    kind: "sequential",
    icon: Rocket,
    accent: "violet",
    tests: [
      {
        name: "Deploy",
        label: "Deploy stack",
        hint: "cdk deploy — submits template to CloudFormation",
      },
      {
        name: "VerifyStackStatus",
        label: "CREATE_COMPLETE",
        hint: "DescribeStacks returns CREATE_COMPLETE / UPDATE_COMPLETE",
      },
    ],
  },
  {
    id: "verify",
    label: "Verify resources",
    kind: "parallel",
    icon: Search,
    accent: "emerald",
    maxHeight: "max-h-[460px] overflow-y-auto",
    tests: [
      {
        name: "VerifyBucket",
        label: "S3 bucket",
        hint: "ListBuckets shows the CompatBucket created by the stack",
      },
      {
        name: "VerifyQueues",
        label: "SQS main + DLQ",
        hint: "Queue exists and RedrivePolicy references the DLQ ARN",
      },
      {
        name: "VerifyTopicSubscription",
        label: "SNS → SQS delivery",
        hint: "Publish to the topic and receive the message on the queue",
      },
      {
        name: "VerifyTable",
        label: "DynamoDB + GSI",
        hint: "DescribeTable exposes the gsi1 global secondary index",
      },
      {
        name: "VerifyRole",
        label: "IAM role trust",
        hint: "GetRole trust policy allows lambda.amazonaws.com",
      },
      {
        name: "VerifyFunctionConfig",
        label: "Lambda config",
        hint: "Handler, runtime, role ARN, and env vars match stack props",
      },
      {
        name: "VerifyEventSourceMapping",
        label: "Lambda ESM (SQS)",
        hint: "ListEventSourceMappings links the queue to the function",
      },
      {
        name: "VerifyDynamoDBStream",
        label: "DynamoDB stream",
        hint: "Table has StreamEnabled and LatestStreamArn populated",
      },
      {
        name: "VerifyDynamoDBEsm",
        label: "Lambda ESM (DDB stream)",
        hint: "Event source mapping exists for the DynamoDB stream",
      },
      {
        name: "PutStreamTriggerItem",
        label: "Put stream trigger item",
        hint: "PutItem into the table to trigger the DynamoDB stream",
      },
      {
        name: "VerifyLambdaInvokedByStream",
        label: "Stream → Lambda",
        hint: "GetEventSourceMapping LastProcessingResult reflects the trigger",
      },
      {
        name: "VerifyLogGroup",
        label: "CloudWatch Logs",
        hint: "DescribeLogGroups finds the stack-created log group",
      },
      {
        name: "VerifyKmsKey",
        label: "KMS key",
        hint: "DescribeKey returns enabled key metadata",
      },
      {
        name: "VerifySecret",
        label: "Secrets Manager",
        hint: "DescribeSecret returns the auto-generated secret",
      },
      {
        name: "VerifyParameter",
        label: "SSM Parameter",
        hint: "GetParameter returns the string value",
      },
      {
        name: "VerifyPolicy",
        label: "IAM managed policy",
        hint: "GetPolicy returns the S3 list-bucket policy",
      },
      {
        name: "VerifyVpc",
        label: "EC2 VPC",
        hint: "DescribeVpcs finds the stack-created VPC",
      },
      {
        name: "VerifySecurityGroup",
        label: "EC2 security group",
        hint: "DescribeSecurityGroups finds the stack-created SG",
      },
      {
        name: "VerifyRestApi",
        label: "API Gateway REST API",
        hint: "GetRestApis finds the mock endpoint REST API",
      },
      {
        name: "VerifyEventBus",
        label: "EventBridge bus",
        hint: "DescribeEventBus returns name and ARN match",
      },
      {
        name: "VerifyStateMachine",
        label: "Step Functions SM",
        hint: "DescribeStateMachine returns the state machine ARN",
      },
      {
        name: "VerifyStateMachineStatus",
        label: "State machine ACTIVE",
        hint: "DescribeStateMachine status is ACTIVE",
      },
      {
        name: "VerifyNestedStack",
        label: "Nested stack",
        hint: "DescribeStacks finds the nested CloudFormation stack",
      },
      {
        name: "VerifyNestedQueue",
        label: "Nested SQS queue",
        hint: "GetQueueUrl finds the queue inside the nested stack",
      },
    ],
  },
  {
    id: "update",
    label: "Update",
    kind: "sequential",
    icon: RefreshCw,
    accent: "amber",
    tests: [
      {
        name: "UpdateLambdaTimeout",
        label: "Redeploy with change",
        hint: "cdk deploy with CDK_COMPAT_LAMBDA_TIMEOUT=15",
      },
      {
        name: "VerifyUpdateStatus",
        label: "UPDATE_COMPLETE",
        hint: "DescribeStacks returns UPDATE_COMPLETE after redeploy",
      },
      {
        name: "VerifyUpdatedFunctionConfig",
        label: "Timeout changed",
        hint: "GetFunctionConfiguration timeout is now 15s",
      },
    ],
  },
  {
    id: "teardown",
    label: "Teardown",
    kind: "sequential",
    icon: Trash2,
    accent: "rose",
    tests: [
      {
        name: "Destroy",
        label: "Destroy stack",
        hint: "cdk destroy — DeleteStack rolls back all provisioned resources",
      },
      {
        name: "VerifyDestroyed",
        label: "Stack removed",
        hint: "ListStacks no longer returns the stack (other than DELETE_COMPLETE)",
      },
    ],
  },
];

// Tailwind has to see full class strings to generate them at build time, so we
// map accent keys to concrete classes rather than interpolating.
const accentClasses: Record<
  string,
  { ring: string; icon: string; tint: string }
> = {
  blue: {
    ring: "border-blue-200 dark:border-blue-900",
    icon: "text-blue-500",
    tint: "bg-blue-50/60 dark:bg-blue-950/30",
  },
  violet: {
    ring: "border-violet-200 dark:border-violet-900",
    icon: "text-violet-500",
    tint: "bg-violet-50/60 dark:bg-violet-950/30",
  },
  emerald: {
    ring: "border-emerald-200 dark:border-emerald-900",
    icon: "text-emerald-500",
    tint: "bg-emerald-50/60 dark:bg-emerald-950/30",
  },
  amber: {
    ring: "border-amber-200 dark:border-amber-900",
    icon: "text-amber-500",
    tint: "bg-amber-50/60 dark:bg-amber-950/30",
  },
  rose: {
    ring: "border-rose-200 dark:border-rose-900",
    icon: "text-rose-500",
    tint: "bg-rose-50/60 dark:bg-rose-950/30",
  },
};

interface CdkCell {
  status: Status;
  error?: string;
  group?: string;
}

function lookupCell(
  section: ServiceSection | undefined,
  testName: string,
): CdkCell | undefined {
  if (!section) return undefined;
  for (const grp of section.groups.values()) {
    const cells = grp.tests.get(testName);
    if (!cells) continue;
    const cell = cells[SUITE_ID];
    if (cell)
      return { status: cell.status, error: cell.error, group: grp.name };
  }
  return undefined;
}

function aggregateTotals(section: ServiceSection | undefined) {
  const totals = { pass: 0, fail: 0, skip: 0, unimplemented: 0, na: 0 };
  for (const stage of STAGES) {
    for (const t of stage.tests) {
      const cell = lookupCell(section, t.name);
      if (!cell) continue;
      if (cell.status in totals) {
        totals[cell.status as keyof typeof totals]++;
      }
    }
  }
  return totals;
}

export function CdkFlow({
  section,
  active,
  isRunning,
  prevStatuses,
  queue = [],
}: {
  section: ServiceSection | undefined;
  active: boolean;
  isRunning: boolean;
  prevStatuses: Record<string, Status>;
  queue?: QueueEntry[];
}) {
  const [expanded, setExpanded] = useState<string | null>(null);

  const totals = aggregateTotals(section);
  const hasRun =
    active &&
    totals.pass + totals.fail + totals.skip + totals.unimplemented > 0;

  return (
    <section id="svc-cdk" className="mt-10 mb-6 scroll-mt-24">
      <div className="flex items-center gap-2 mb-4 flex-wrap">
        <h2 className="text-[11px] font-bold text-gray-500 dark:text-gray-400 uppercase tracking-widest shrink-0">
          Deployment Tools &amp; IaC
        </h2>
        <div className="h-px flex-1 bg-gray-200 dark:bg-gray-700 min-w-4" />
      </div>

      {/* CDK flow card */}
      <div className="rounded-xl border border-gray-200 dark:border-gray-700 bg-white dark:bg-gray-800 shadow-sm overflow-hidden">
        {/* Header */}
        <div className="flex items-center gap-3 px-5 py-3 border-b border-gray-100 dark:border-gray-700 bg-gray-50/70 dark:bg-gray-700/30 flex-wrap">
          <div className="flex items-center gap-2">
            <span className="text-sm font-semibold text-gray-800 dark:text-gray-100">
              CDK
            </span>
            <span className="text-[11px] text-gray-400 dark:text-gray-500 font-mono">
              TypeScript&nbsp;v2
            </span>
          </div>
          <span className="text-[11px] text-gray-500 dark:text-gray-400">
            Full-stack lifecycle — bootstrap → deploy → verify → teardown
          </span>

          {hasRun && (
            <div className="flex items-center gap-1 ml-auto">
              <CountChip status="pass" count={totals.pass} />
              <CountChip status="fail" count={totals.fail} />
              <CountChip status="unimplemented" count={totals.unimplemented} />
              <CountChip status="skip" count={totals.skip} />
            </div>
          )}
          {hasRun && totals.pass + totals.fail > 0 && (
            <PassRateBar
              pass={totals.pass}
              fail={totals.fail}
              skip={totals.skip}
              unimplemented={totals.unimplemented}
            />
          )}
          {active && (
            <div className="flex items-center gap-1 ml-1">
              <RetryButton
                filter={{ suite: SUITE_ID }}
                hasFailing={
                  totals.fail + totals.skip + totals.unimplemented > 0
                }
                isRunning={isRunning}
                title="Re-run non-passing CDK tests"
                className="p-1.5 text-amber-400 hover:text-amber-600 dark:text-amber-500 dark:hover:text-amber-400 hover:bg-amber-50 dark:hover:bg-amber-950"
              />
              <RunButton
                filter={{ suite: SUITE_ID }}
                isRunning={isRunning}
                title="Re-run the full CDK lifecycle"
                className="p-1.5 text-gray-400 hover:text-blue-500 dark:text-gray-500 dark:hover:text-blue-400 hover:bg-blue-50 dark:hover:bg-blue-950"
              />
            </div>
          )}
          {!active && (
            <span className="ml-auto text-[10px] uppercase tracking-widest text-gray-300 dark:text-gray-600">
              Not run yet
            </span>
          )}
        </div>

        {/* Stages — horizontally scrollable so the full flow stays
            side-by-side on narrow viewports instead of stacking. */}
        <div className="overflow-x-auto">
          <div className="grid grid-cols-[minmax(180px,1fr)_auto_minmax(180px,1fr)_auto_minmax(360px,2.2fr)_auto_minmax(180px,1fr)_auto_minmax(180px,1fr)] items-stretch min-w-290">
            {STAGES.map((stage, idx) => (
              <Fragment key={stage.id}>
                <StageColumn
                  stage={stage}
                  section={section}
                  expanded={expanded}
                  setExpanded={setExpanded}
                  active={active}
                  isRunning={isRunning}
                  prevStatuses={prevStatuses}
                  queue={queue}
                />
                {idx < STAGES.length - 1 && (
                  <div
                    className="flex items-center justify-center px-1"
                    aria-hidden
                  >
                    <ChevronRight
                      size={18}
                      className="text-gray-300 dark:text-gray-600"
                    />
                  </div>
                )}
              </Fragment>
            ))}
          </div>
        </div>

        {/* Error detail panel */}
        {expanded &&
          (() => {
            const cell = lookupCell(section, expanded);
            if (!cell?.error) return null;
            const reproduceCmd = cell.group
              ? reproduceCommand({
                  suite: SUITE_ID,
                  group: cell.group,
                  test: expanded,
                })
              : null;
            return (
              <div className="border-t border-red-100 dark:border-red-900 bg-red-50/60 dark:bg-red-900/20 px-5 py-4">
                <div className="flex items-center gap-2 mb-2 flex-wrap">
                  <StatusIcon status={cell.status} size={14} />
                  <span className="text-xs font-semibold text-gray-700 dark:text-gray-200 font-mono">
                    {expanded}
                  </span>
                  <div className="ml-auto flex items-center gap-1.5">
                    <button
                      type="button"
                      onClick={() => openHandler("cloudformation")}
                      title="Open internal/services/cloudformation/ in VS Code — CDK dispatches via CloudFormation"
                      className="inline-flex items-center gap-1 text-[10px] px-2 py-0.5 text-gray-500 hover:text-blue-600 dark:text-gray-400 dark:hover:text-blue-400 border border-gray-200 dark:border-gray-700 hover:border-blue-300 dark:hover:border-blue-700 font-mono uppercase tracking-wider"
                    >
                      open cfn handler
                    </button>
                    {reproduceCmd && (
                      <CopyButton
                        value={reproduceCmd}
                        title={`Copy reproduce command: ${reproduceCmd}`}
                        label="copy reproduce command"
                        className="text-[10px] px-2 py-0.5 text-gray-500 hover:text-blue-600 dark:text-gray-400 dark:hover:text-blue-400 border border-gray-200 dark:border-gray-700 hover:border-blue-300 dark:hover:border-blue-700 font-mono uppercase tracking-wider"
                      />
                    )}
                  </div>
                </div>
                <div className="flex items-start gap-2.5">
                  <AlertTriangle
                    size={14}
                    className="text-red-400 mt-0.5 shrink-0"
                  />
                  <pre className="text-xs border rounded-lg px-3.5 py-2.5 overflow-x-auto whitespace-pre-wrap wrap-break-word leading-relaxed font-mono flex-1 shadow-sm text-red-900 dark:text-red-200 bg-white/80 dark:bg-gray-800/80 border-red-200 dark:border-red-800">
                    {cell.error}
                  </pre>
                </div>
                {(() => {
                  const ids = extractRequestIds(cell.error);
                  if (ids.length === 0) return null;
                  return (
                    <div className="flex items-center gap-1.5 flex-wrap pl-6 mt-2">
                      <span className="text-[10px] uppercase tracking-wider text-gray-400 dark:text-gray-500">
                        request id
                      </span>
                      {ids.map((id) => (
                        <CopyButton
                          key={id}
                          value={id}
                          title={`Copy request ID ${id} — grep overcast logs for this value`}
                          label={id}
                          className="text-[10px] px-1.5 py-0.5 font-mono text-gray-500 hover:text-blue-600 dark:text-gray-400 dark:hover:text-blue-400 border border-gray-200 dark:border-gray-700 hover:border-blue-300 dark:hover:border-blue-700"
                        />
                      ))}
                    </div>
                  );
                })()}
              </div>
            );
          })()}
      </div>

      {/* Coming-soon placeholders for other IaC tools */}
      <div className="grid grid-cols-2 sm:grid-cols-3 lg:grid-cols-3 gap-4 mt-4">
        {[
          { id: "tofu", label: "OpenTofu", sublabel: "AWS provider" },
          { id: "terraform", label: "Terraform", sublabel: "AWS provider" },
          { id: "pulumi", label: "Pulumi", sublabel: "AWS provider" },
        ].map((p) => (
          <div
            key={p.id}
            className="flex flex-col items-center justify-center gap-1.5 rounded-xl border border-dashed border-gray-200 dark:border-gray-700 px-5 py-6 text-center bg-white/50 dark:bg-gray-800/50"
          >
            <span className="text-sm font-semibold text-gray-400 dark:text-gray-500">
              {p.label}
            </span>
            <span className="text-xs text-gray-400 dark:text-gray-500">
              {p.sublabel}
            </span>
            <span className="mt-1 text-[10px] uppercase tracking-widest text-gray-300 dark:text-gray-600">
              Coming soon
            </span>
          </div>
        ))}
      </div>
    </section>
  );
}

function StageColumn({
  stage,
  section,
  expanded,
  setExpanded,
  active,
  isRunning,
  prevStatuses,
  queue,
}: {
  stage: StageDef;
  section: ServiceSection | undefined;
  expanded: string | null;
  setExpanded: (v: string | null) => void;
  active: boolean;
  isRunning: boolean;
  prevStatuses: Record<string, Status>;
  queue: QueueEntry[];
}) {
  const accent = accentClasses[stage.accent]!;
  const Icon = stage.icon;

  // Build a quick lookup for test-level queue states so cells with no result
  // yet (first-ever CDK run) still show queued/running indicators.
  const { queuedTests, runningTests } = useMemo(() => {
    const queuedTests = new Set<string>();
    const runningTests = new Set<string>();
    for (const q of queue) {
      if (q.suite === SUITE_ID) {
        if (q.test) {
          if (q.state === "running") runningTests.add(q.test);
          else queuedTests.add(q.test);
        } else {
          // Suite-level entry — all tests in this suite are queued/running.
          for (const s of stage.tests) {
            if (q.state === "running") runningTests.add(s.name);
            else queuedTests.add(s.name);
          }
        }
      }
    }
    return { queuedTests, runningTests };
  }, [queue, stage.tests]);

  return (
    <div className={cn("flex flex-col gap-2 px-4 py-4", accent.tint)}>
      <div className="flex items-center gap-1.5 mb-1">
        <Icon size={13} className={accent.icon} strokeWidth={2.2} />
        <span className="text-[11px] font-semibold uppercase tracking-widest text-gray-600 dark:text-gray-300">
          {stage.label}
        </span>
        {stage.kind === "parallel" && (
          <span className="text-[9px] uppercase tracking-widest text-gray-400 dark:text-gray-500">
            parallel
          </span>
        )}
      </div>

      <ol className={cn("flex flex-col gap-1.5", stage.maxHeight)}>
        {stage.tests.map((t, i) => {
          const cell = lookupCell(section, t.name);
          // Prefer the authoritative cell status. Fall back to queue-derived
          // status so tests with no result yet show queued/running indicators.
          const queueStatus: Status | undefined = runningTests.has(t.name)
            ? "running"
            : queuedTests.has(t.name)
              ? "queued"
              : undefined;
          const status: Status = cell?.status ?? queueStatus ?? "na";
          const hasError = !!cell?.error;
          const isExpanded = expanded === t.name;
          const prev = cell?.group
            ? prevStatuses[prevStatusKey("cdk", cell.group, t.name, SUITE_ID)]
            : undefined;
          const flipped =
            !!prev &&
            !!cell &&
            prev !== cell.status &&
            cell.status !== "running";
          return (
            <li
              key={t.name}
              className={cn(
                "relative flex items-center gap-2.5 rounded-lg border px-2.5 py-1.5 bg-white dark:bg-gray-800 transition-colors group",
                accent.ring,
                hasError && "cursor-pointer",
                isExpanded && "ring-2 ring-red-300 dark:ring-red-700",
              )}
              onClick={
                hasError
                  ? () => setExpanded(isExpanded ? null : t.name)
                  : undefined
              }
              title={t.hint}
            >
              {/* step index */}
              {stage.kind === "sequential" && (
                <span className="text-[9px] font-mono text-gray-300 dark:text-gray-600 w-3 text-right">
                  {i + 1}
                </span>
              )}

              <StatusBadge
                status={cell ? status : (queueStatus ?? "na")}
                expandable={hasError}
                active={isExpanded}
              />
              {flipped && prev && <CdkFlipBadge from={prev} to={status} />}

              <div className="flex flex-col min-w-0 flex-1">
                <span className="text-xs font-medium text-gray-800 dark:text-gray-100 truncate">
                  {t.label}
                </span>
                <span className="text-[10px] text-gray-400 dark:text-gray-500 truncate">
                  {t.hint}
                </span>
              </div>

              {active && (
                <RunButton
                  filter={{ suite: SUITE_ID, test: t.name }}
                  isRunning={isRunning}
                  title={`Re-run ${t.label}`}
                  className="opacity-0 group-hover:opacity-100 p-1 shrink-0 text-gray-400 hover:text-blue-500 dark:text-gray-500 dark:hover:text-blue-400 hover:bg-blue-50 dark:hover:bg-blue-950 transition-opacity"
                />
              )}
            </li>
          );
        })}
      </ol>
    </div>
  );
}

function CdkFlipBadge({ from, to }: { from: Status; to: Status }) {
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
