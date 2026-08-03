import { useMemo, useState } from "react"
import { useMutation, useQuery } from "@tanstack/react-query"
import { ShieldCheck, ShieldAlert, Play, AlertTriangle } from "lucide-react"
import {
  iamUsersQueryOptions,
  iamRolesQueryOptions,
  simulatePolicyMutationOptions,
} from "@/features/iam/data"
import type { IAMEvaluationResult } from "@/services/api/iam"
import { serverInfoQueryOptions } from "@/hooks/use-server-info"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Select } from "@/components/ui/select"
import { Textarea } from "@/components/ui/textarea"
import { Badge } from "@/components/ui/badge"
import { EmptyState, Spinner } from "@/components/ui/primitives"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"

const EXAMPLE_POLICY = `{
  "Version": "2012-10-17",
  "Statement": [
    { "Effect": "Allow", "Action": "s3:GetObject", "Resource": "arn:aws:s3:::my-bucket/*" }
  ]
}`

/** decisionVariant maps AWS's decision vocabulary onto badge styling. */
function decisionVariant(decision?: string) {
  if (decision === "allowed") return "success" as const
  if (decision === "explicitDeny") return "danger" as const
  return "warning" as const
}

/**
 * EnforcementNotice tells the user whether a denial here would also be a denial
 * on the wire. With enforcement off (the default) simulation is advisory only;
 * with it on, a real request that fails is the emulator's decision, not an
 * application bug.
 */
export function EnforcementNotice() {
  const { data: info } = useQuery(serverInfoQueryOptions())
  const enforcing = info?.iam_enforce === true

  if (enforcing) {
    return (
      <div
        className="flex items-start gap-3 rounded-lg border border-amber-400/30 bg-amber-400/5 px-4 py-3 text-sm text-amber-300"
        data-testid="iam-enforcement-notice"
      >
        <ShieldAlert className="mt-0.5 h-4 w-4 shrink-0" />
        <div>
          <span className="font-medium">IAM enforcement is ON.</span> Requests are evaluated
          against the calling principal&apos;s policies before they reach a service, and a call
          the policies do not allow fails with <code>AccessDenied</code> — that is the emulator
          denying it, not your application. Unset <code>OVERCAST_ENFORCE_IAM</code> to turn it
          off.
        </div>
      </div>
    )
  }

  return (
    <div
      className="flex items-start gap-3 rounded-lg border border-sky-400/30 bg-sky-400/5 px-4 py-3 text-sm text-sky-300"
      data-testid="iam-enforcement-notice"
    >
      <ShieldCheck className="mt-0.5 h-4 w-4 shrink-0" />
      <div>
        <span className="font-medium">IAM enforcement is OFF (the default).</span> Requests
        succeed regardless of policy; the simulator below reports what <em>would</em> happen. Set{" "}
        <code>OVERCAST_ENFORCE_IAM=true</code> to have requests actually checked.
      </div>
    </div>
  )
}

// ─── Simulator ─────────────────────────────────────────────────────────────

export function PolicySimulator() {
  const [principal, setPrincipal] = useState("")
  const [actions, setActions] = useState("s3:GetObject")
  const [resources, setResources] = useState("arn:aws:s3:::my-bucket/*")
  const [policy, setPolicy] = useState("")
  const [resourcePolicy, setResourcePolicy] = useState("")

  const { data: users = [] } = useQuery(iamUsersQueryOptions())
  const { data: roles = [] } = useQuery(iamRolesQueryOptions())

  const simulation = useMutation(simulatePolicyMutationOptions())

  const principals = useMemo(
    () => [
      ...users.map((u) => ({ arn: u.Arn ?? "", label: `user/${u.UserName ?? ""}` })),
      ...roles.map((r) => ({ arn: r.Arn ?? "", label: `role/${r.RoleName ?? ""}` })),
    ],
    [users, roles],
  )

  const splitList = (raw: string) =>
    raw
      .split(/[\s,]+/)
      .map((v) => v.trim())
      .filter(Boolean)

  const actionList = splitList(actions)
  const canRun = actionList.length > 0 && (principal !== "" || policy.trim() !== "")

  const run = () => {
    simulation.mutate({
      policySourceArn: principal || undefined,
      policyInputList: policy.trim() ? [policy] : undefined,
      actionNames: actionList,
      resourceArns: splitList(resources),
      resourcePolicy: resourcePolicy.trim() || undefined,
    })
  }

  const error = simulation.error

  return (
    <div className="flex flex-col gap-4 pt-4">
      <div className="grid gap-4 md:grid-cols-2">
        <div className="flex flex-col gap-1.5">
          <Label htmlFor="sim-principal">Principal</Label>
          <Select
            id="sim-principal"
            value={principal}
            onChange={(e) => setPrincipal(e.target.value)}
          >
            <option value="">— none (evaluate the policy below on its own) —</option>
            {principals.map((p) => (
              <option key={p.arn} value={p.arn}>
                {p.label}
              </option>
            ))}
          </Select>
        </div>

        <div className="flex flex-col gap-1.5">
          <Label htmlFor="sim-actions">Actions</Label>
          <Input
            id="sim-actions"
            value={actions}
            placeholder="s3:GetObject, sqs:SendMessage"
            onChange={(e) => setActions(e.target.value)}
          />
        </div>

        <div className="flex flex-col gap-1.5 md:col-span-2">
          <Label htmlFor="sim-resources">Resource ARNs</Label>
          <Input
            id="sim-resources"
            value={resources}
            placeholder="arn:aws:s3:::my-bucket/*"
            onChange={(e) => setResources(e.target.value)}
          />
        </div>

        <div className="flex flex-col gap-1.5">
          <Label htmlFor="sim-policy">Extra identity policy (optional)</Label>
          <Textarea
            id="sim-policy"
            rows={8}
            value={policy}
            placeholder={EXAMPLE_POLICY}
            onChange={(e) => setPolicy(e.target.value)}
          />
        </div>

        <div className="flex flex-col gap-1.5">
          <Label htmlFor="sim-resource-policy">Resource policy (optional)</Label>
          <Textarea
            id="sim-resource-policy"
            rows={8}
            value={resourcePolicy}
            placeholder='{"Statement":[{"Effect":"Allow","Principal":{"AWS":"*"},…}]}'
            onChange={(e) => setResourcePolicy(e.target.value)}
          />
        </div>
      </div>

      <div className="flex items-center gap-2">
        <Button size="sm" onClick={run} disabled={!canRun || simulation.isPending}>
          <Play className="mr-1.5 h-3.5 w-3.5" /> Simulate
        </Button>
        {simulation.isPending && <Spinner className="h-4 w-4" />}
      </div>

      {error && (
        <div
          className="flex items-start gap-3 rounded-lg border border-danger/30 bg-danger-muted px-4 py-3 text-sm text-danger"
          role="alert"
        >
          <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0" />
          <div>
            <span className="font-medium">{error.name || "Simulation failed"}</span> — {error.message}
          </div>
        </div>
      )}

      <SimulationResults results={simulation.data} hasRun={simulation.isSuccess} />
    </div>
  )
}

function SimulationResults({
  results,
  hasRun,
}: {
  results?: IAMEvaluationResult[]
  hasRun: boolean
}) {
  if (!hasRun) {
    return (
      <EmptyState
        icon={<ShieldCheck className="h-6 w-6" />}
        title="No simulation yet"
        description="Pick a principal or paste a policy, then run a simulation to see whether the action would be allowed."
      />
    )
  }

  if (!results?.length) {
    return (
      <EmptyState
        icon={<ShieldCheck className="h-6 w-6" />}
        title="No results"
        description="The simulation returned no evaluation results."
      />
    )
  }

  return (
    <Table>
      <TableHeader>
        <TableRow>
          <TableHead>Action</TableHead>
          <TableHead>Resource</TableHead>
          <TableHead>Decision</TableHead>
          <TableHead>Matched statements</TableHead>
          <TableHead>Missing context</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {results.map((r, i) => (
          <TableRow key={`${r.EvalActionName}-${r.EvalResourceName}-${i}`}>
            <TableCell className="font-mono">{r.EvalActionName}</TableCell>
            <TableCell className="text-fg-muted">{r.EvalResourceName}</TableCell>
            <TableCell>
              <Badge variant={decisionVariant(r.EvalDecision)}>{r.EvalDecision}</Badge>
            </TableCell>
            <TableCell className="text-fg-muted">
              {r.MatchedStatements?.length
                ? r.MatchedStatements.map(
                    (s) => `${s.SourcePolicyId ?? "?"} (${s.SourcePolicyType ?? "?"})`,
                  ).join(", ")
                : "—"}
            </TableCell>
            <TableCell className="text-fg-muted">
              {r.MissingContextValues?.length ? r.MissingContextValues.join(", ") : "—"}
            </TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  )
}
