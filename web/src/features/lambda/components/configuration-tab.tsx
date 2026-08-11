import { useState, useCallback } from "react"
import { useQuery } from "@tanstack/react-query"
import { Link } from "@tanstack/react-router"
import { AlertTriangle, ExternalLink, Trash2, Plus as PlusIcon } from "lucide-react"
import { ArnLink, ArnText } from "@/components/ui/arn-link"
import { useToast } from "@/components/ui/toast"
import { Button } from "@/components/ui/button"
import { Combobox, type ComboboxRenderContext } from "@/components/ui/combobox"
import { Definition, DefinitionList } from "@/components/ui/definition-card"
import { Spinner } from "@/components/ui/primitives"
import { Input } from "@/components/ui/input"
import { Select } from "@/components/ui/select"
import {
  lambdaKeys,
  lambdaRuntimesQueryOptions,
  layersQueryOptions,
  updateFunctionLayersMutationOptions,
  updateFunctionConfigurationMutationOptions,
  functionTagsQueryOptions,
  updateFunctionTagsMutationOptions,
} from "@/features/lambda/data"
import {
  APPLICATION_LOG_LEVELS,
  buildLoggingConfig,
  loggingConfigForm,
  LOG_FORMATS,
  SYSTEM_LOG_LEVELS,
  type LoggingConfigForm,
} from "@/features/lambda/logging-config"
import { ec2SubnetsQueryOptions, ec2SecurityGroupsQueryOptions } from "@/features/ec2/data"
import { useResourceMutation } from "@/hooks/use-resource-mutation"
import type { LambdaFunction, LambdaLayer } from "@/types"
import { fieldLabel } from "@/lib/typography"
import { cn } from "@/lib/utils"

// ─── Configuration Tab ─────────────────────────────────────────────────────

export function ConfigurationTab({ fn }: { fn: LambdaFunction }) {
  return (
    <div className="flex max-w-2xl flex-col gap-6">
      <GeneralConfigSection fn={fn} />
      <AsyncInvokeSection fn={fn} />
      <LoggingConfigSection fn={fn} />
      <VpcConfigSection fn={fn} />
      <EnvVarsSection fn={fn} />
      <TagsSection fn={fn} />
      <LayersSection functionName={fn.FunctionName ?? ""} attachedLayers={fn.Layers ?? []} />
    </div>
  )
}

// ─── Asynchronous invocation section ───────────────────────────────────────

/**
 * The dead-letter target a failed asynchronous invocation is sent to.
 *
 * The retry policy that decides when the event gets there is stated in prose
 * rather than offered as fields: the counts are AWS's defaults and the API that
 * changes them — PutFunctionEventInvokeConfig — is not emulated, so an input
 * here would be one the emulator accepts and ignores.
 */
function AsyncInvokeSection({ fn }: { fn: LambdaFunction }) {
  const [editing, setEditing] = useState(false)
  const savedTarget = fn.DeadLetterConfig?.TargetArn ?? ""
  const [targetArn, setTargetArn] = useState(savedTarget)

  const [prevFn, setPrevFn] = useState(fn)
  if (fn !== prevFn) {
    setPrevFn(fn)
    if (!editing) setTargetArn(savedTarget)
  }

  const updateMut = useResourceMutation({
    options: updateFunctionConfigurationMutationOptions(),
    invalidateKeys: [lambdaKeys.functions()],
    successTitle: "Asynchronous invocation saved",
    errorTitle: "Save failed",
    onSuccess: () => setEditing(false),
  })

  // An empty TargetArn is how AWS removes the association, so the same call
  // serves Save and Remove.
  const save = (arn: string) =>
    updateMut.mutate({
      FunctionName: fn.FunctionName,
      DeadLetterConfig: { TargetArn: arn },
    })

  const trimmed = targetArn.trim()
  const looksLikeQueueOrTopic =
    trimmed === "" || trimmed.startsWith("arn:aws:sqs:") || trimmed.startsWith("arn:aws:sns:")

  return (
    <div className="rounded-lg border border-border bg-bg-elevated p-4">
      <div className="mb-3 flex items-center justify-between">
        <h3 className="font-mono text-sm font-medium text-fg">Asynchronous invocation</h3>
        {!editing ? (
          <div className="flex gap-2">
            {savedTarget && (
              <Button
                size="sm"
                variant="ghost"
                className="text-danger hover:text-danger"
                onClick={() => save("")}
                disabled={updateMut.isPending}
              >
                Remove DLQ
              </Button>
            )}
            <Button size="sm" variant="secondary" onClick={() => setEditing(true)}>
              Edit
            </Button>
          </div>
        ) : (
          <div className="flex gap-2">
            <Button
              size="sm"
              variant="secondary"
              onClick={() => {
                setEditing(false)
                setTargetArn(savedTarget)
              }}
              disabled={updateMut.isPending}
            >
              Cancel
            </Button>
            <Button
              size="sm"
              onClick={() => save(trimmed)}
              disabled={updateMut.isPending || !looksLikeQueueOrTopic}
            >
              {updateMut.isPending ? "Saving…" : "Save changes"}
            </Button>
          </div>
        )}
      </div>

      {editing ? (
        <div className="flex flex-col gap-2">
          <label className={cn(fieldLabel, "text-fg-muted")} htmlFor="dlq-target-arn">
            Dead-letter queue ARN
          </label>
          <Input
            id="dlq-target-arn"
            value={targetArn}
            onChange={(e) => setTargetArn(e.target.value)}
            placeholder="arn:aws:sqs:us-east-1:000000000000:my-dlq"
          />
          <p className="text-xs text-fg-muted">
            An SQS queue or SNS topic. Leave empty to remove the dead-letter queue.
          </p>
          {!looksLikeQueueOrTopic && (
            <p className="text-xs text-danger">
              Lambda only accepts an SQS queue or an SNS topic as a dead-letter target.
            </p>
          )}
        </div>
      ) : (
        <div className="flex flex-col gap-3">
          <DefinitionList>
            <Definition
              label="Dead-letter queue"
              value={savedTarget ? <ArnLink arn={savedTarget} /> : null}
              full
            />
          </DefinitionList>
          <p className="text-xs text-fg-muted">
            {savedTarget
              ? "A failed invocation is retried twice before the event is sent here, matching AWS's defaults."
              : "A failed invocation is retried twice, matching AWS's defaults. With no dead-letter queue the event is then dropped."}{" "}
            The retry policy is not configurable — Overcast does not emulate
            PutFunctionEventInvokeConfig.
          </p>
        </div>
      )}
    </div>
  )
}

// ─── General config section ────────────────────────────────────────────────

function GeneralConfigSection({ fn }: { fn: LambdaFunction }) {
  const [editing, setEditing] = useState(false)

  const { data: runtimes = [], isLoading: runtimesLoading } = useQuery(lambdaRuntimesQueryOptions())

  const [form, setForm] = useState({
    description: fn.Description ?? "",
    handler: fn.Handler ?? "",
    role: fn.Role ?? "",
    runtime: fn.Runtime ?? "",
    timeout: fn.Timeout ?? 3,
    memorySize: fn.MemorySize ?? 128,
  })

  // Sync local form when server data changes (e.g., after a save).
  // Uses "adjust state during render" pattern to avoid an extra render pass
  // from useEffect. See: https://react.dev/learn/you-might-not-need-an-effect
  const [prevFn, setPrevFn] = useState(fn)
  if (fn !== prevFn) {
    setPrevFn(fn)
    if (!editing) {
      setForm({
        description: fn.Description ?? "",
        handler: fn.Handler ?? "",
        role: fn.Role ?? "",
        runtime: fn.Runtime ?? "",
        timeout: fn.Timeout ?? 3,
        memorySize: fn.MemorySize ?? 128,
      })
    }
  }

  const updateMut = useResourceMutation({
    options: updateFunctionConfigurationMutationOptions(),
    invalidateKeys: [lambdaKeys.functions()],
    successTitle: "Configuration saved",
    errorTitle: "Save failed",
    onSuccess: () => setEditing(false),
  })

  const handleSave = () => {
    updateMut.mutate({
      FunctionName: fn.FunctionName,
      Description: form.description || undefined,
      Handler: form.handler || undefined,
      Role: form.role || undefined,
      Timeout: form.timeout,
      MemorySize: form.memorySize,
    })
  }

  const handleCancel = () => {
    setEditing(false)
    setForm({
      description: fn.Description ?? "",
      handler: fn.Handler ?? "",
      role: fn.Role ?? "",
      runtime: fn.Runtime ?? "",
      timeout: fn.Timeout ?? 3,
      memorySize: fn.MemorySize ?? 128,
    })
  }

  const field =
    (k: keyof typeof form) => (e: React.ChangeEvent<HTMLInputElement | HTMLSelectElement>) =>
      setForm((prev) => ({
        ...prev,
        [k]: e.target.type === "number" ? Number(e.target.value) : e.target.value,
      }))

  return (
    <div className="rounded-lg border border-border bg-bg-elevated p-4">
      <div className="mb-3 flex items-center justify-between">
        <h3 className="font-mono text-sm font-medium text-fg">General configuration</h3>
        {!editing ? (
          <Button size="sm" variant="secondary" onClick={() => setEditing(true)}>
            Edit
          </Button>
        ) : (
          <div className="flex gap-2">
            <Button
              size="sm"
              variant="secondary"
              onClick={handleCancel}
              disabled={updateMut.isPending}
            >
              Cancel
            </Button>
            <Button size="sm" onClick={handleSave} disabled={updateMut.isPending}>
              {updateMut.isPending ? "Saving…" : "Save changes"}
            </Button>
          </div>
        )}
      </div>

      {editing ? (
        <div className="grid grid-cols-2 gap-x-6 gap-y-4 text-sm">
          <div className="col-span-2 flex flex-col gap-1">
            <label className={cn(fieldLabel, "text-fg-muted")}>Description</label>
            <Input
              value={form.description}
              onChange={field("description")}
              placeholder="Optional description"
            />
          </div>
          <div className="flex flex-col gap-1">
            <label className={cn(fieldLabel, "text-fg-muted")}>Runtime</label>
            <select
              value={form.runtime}
              onChange={field("runtime")}
              disabled={runtimesLoading}
              className="flex h-8 w-full rounded-md border border-border bg-bg px-3 py-1 text-sm text-fg focus-visible:border-accent focus-visible:ring-2 focus-visible:ring-accent focus-visible:outline-none disabled:cursor-not-allowed disabled:opacity-60"
            >
              {runtimesLoading ? (
                <option value="">Loading…</option>
              ) : (
                <>
                  <option value="">— keep current —</option>
                  {/* UpdateFunctionConfiguration is gated on AWS's block-update
                      date, not on deprecation: a deprecated runtime stays a
                      valid target until then. */}
                  {runtimes
                    .filter((r) => r.supported && !r.updateBlocked)
                    .map((r) => (
                      <option key={r.id} value={r.id}>
                        {r.name}
                        {r.deprecated ? " (deprecated)" : ""}
                      </option>
                    ))}
                </>
              )}
            </select>
          </div>
          <div className="flex flex-col gap-1">
            <label className={cn(fieldLabel, "text-fg-muted")}>Handler</label>
            <Input
              value={form.handler}
              onChange={field("handler")}
              placeholder="e.g. index.handler"
              className="font-mono"
            />
          </div>
          <div className="flex flex-col gap-1">
            <label className={cn(fieldLabel, "text-fg-muted")}>Timeout (seconds, 1–900)</label>
            <Input
              type="number"
              min={1}
              max={900}
              value={form.timeout}
              onChange={field("timeout")}
            />
          </div>
          <div className="flex flex-col gap-1">
            <label className={cn(fieldLabel, "text-fg-muted")}>Memory (MB, 128–10240)</label>
            <Input
              type="number"
              min={128}
              max={10240}
              step={64}
              value={form.memorySize}
              onChange={field("memorySize")}
            />
          </div>
          <div className="col-span-2 flex flex-col gap-1">
            <label className={cn(fieldLabel, "text-fg-muted")}>Role ARN</label>
            <Input
              value={form.role}
              onChange={field("role")}
              placeholder="arn:aws:iam::123456789012:role/…"
              className="font-mono text-xs"
            />
          </div>
        </div>
      ) : (
        <DefinitionList>
          <Definition label="Runtime" value={fn.Runtime} />
          <Definition label="Handler" value={fn.Handler} />
          <Definition label="Memory" value={`${fn.MemorySize ?? 128} MB`} />
          <Definition label="Timeout" value={`${fn.Timeout ?? 3}s`} />
          <Definition label="Package type" value={fn.PackageType ?? "Zip"} />
          <Definition label="Architectures" value={(fn.Architectures ?? []).join(", ")} />
          <Definition label="Code size" value={fn.CodeSize ? `${fn.CodeSize} bytes` : null} />
          <Definition label="Last modified" value={fn.LastModified} />
          <Definition label="State" value={fn.State} />
          <Definition
            label="Function ARN"
            value={fn.FunctionArn ? <ArnText arn={fn.FunctionArn} /> : null}
            full
          />
          <Definition label="Role" value={fn.Role} full />
          {fn.Description && (
            <Definition label="Description" value={fn.Description} variant="prose" full />
          )}
        </DefinitionList>
      )}
    </div>
  )
}

// ─── Logging configuration section ────────────────────────────────────────

/**
 * Log destination, log format, and the level filtering the JSON format unlocks.
 *
 * The whole configuration is sent on every save, log group included:
 * UpdateFunctionConfiguration rebuilds all four members from the request and
 * defaults the ones it does not find, so a payload naming only the format would
 * move a custom log group back to `/aws/lambda/<function name>`.
 */
export function LoggingConfigSection({ fn }: { fn: LambdaFunction }) {
  const [editing, setEditing] = useState(false)
  const [form, setForm] = useState<LoggingConfigForm>(() => loggingConfigForm(fn.LoggingConfig))

  // Same "adjust state during render" sync as the sections above.
  const [prevFn, setPrevFn] = useState(fn)
  if (fn !== prevFn) {
    setPrevFn(fn)
    if (!editing) setForm(loggingConfigForm(fn.LoggingConfig))
  }

  const updateMut = useResourceMutation({
    options: updateFunctionConfigurationMutationOptions(),
    invalidateKeys: [lambdaKeys.functions()],
    successTitle: "Logging configuration saved",
    errorTitle: "Save failed",
    onSuccess: () => setEditing(false),
  })

  const handleSave = () => {
    updateMut.mutate({
      FunctionName: fn.FunctionName,
      LoggingConfig: buildLoggingConfig(form),
    })
  }

  const handleCancel = () => {
    setEditing(false)
    setForm(loggingConfigForm(fn.LoggingConfig))
  }

  const isJSON = form.logFormat === "JSON"
  const saved = loggingConfigForm(fn.LoggingConfig)

  return (
    <div className="rounded-lg border border-border bg-bg-elevated p-4">
      <div className="mb-3 flex items-center justify-between">
        <h3 className="font-mono text-sm font-medium text-fg">Logging configuration</h3>
        {!editing ? (
          <Button size="sm" variant="secondary" onClick={() => setEditing(true)}>
            Edit
          </Button>
        ) : (
          <div className="flex gap-2">
            <Button
              size="sm"
              variant="secondary"
              onClick={handleCancel}
              disabled={updateMut.isPending}
            >
              Cancel
            </Button>
            <Button size="sm" onClick={handleSave} disabled={updateMut.isPending}>
              {updateMut.isPending ? "Saving…" : "Save changes"}
            </Button>
          </div>
        )}
      </div>

      {editing ? (
        // The three selects share one row: the two levels are peers governed by
        // the format beside them, and a two-column grid orphans one of them.
        <div className="flex flex-col gap-4 text-sm">
          <div className="flex flex-col gap-1">
            <label className={cn(fieldLabel, "text-fg-muted")} htmlFor="logging-log-group">
              Log group
            </label>
            <Input
              id="logging-log-group"
              value={form.logGroup}
              onChange={(e) => setForm((prev) => ({ ...prev, logGroup: e.target.value }))}
              placeholder={`/aws/lambda/${fn.FunctionName ?? "my-function"}`}
              className="font-mono text-xs"
            />
          </div>
          <div className="grid grid-cols-3 gap-x-6">
            <div className="flex flex-col gap-1">
              <label className={cn(fieldLabel, "text-fg-muted")} htmlFor="logging-log-format">
                Log format
              </label>
              <Select
                id="logging-log-format"
                value={form.logFormat}
                onChange={(e) =>
                  setForm((prev) => ({
                    ...prev,
                    logFormat: e.target.value as LoggingConfigForm["logFormat"],
                  }))
                }
              >
                {LOG_FORMATS.map((format) => (
                  <option key={format} value={format}>
                    {format}
                  </option>
                ))}
              </Select>
            </div>
            <div className="flex flex-col gap-1">
              <label className={cn(fieldLabel, "text-fg-muted")} htmlFor="logging-app-level">
                Application log level
              </label>
              <Select
                id="logging-app-level"
                value={form.applicationLogLevel}
                disabled={!isJSON}
                onChange={(e) =>
                  setForm((prev) => ({
                    ...prev,
                    applicationLogLevel: e.target.value as LoggingConfigForm["applicationLogLevel"],
                  }))
                }
              >
                {APPLICATION_LOG_LEVELS.map((level) => (
                  <option key={level} value={level}>
                    {level}
                  </option>
                ))}
              </Select>
            </div>
            <div className="flex flex-col gap-1">
              <label className={cn(fieldLabel, "text-fg-muted")} htmlFor="logging-system-level">
                System log level
              </label>
              <Select
                id="logging-system-level"
                value={form.systemLogLevel}
                disabled={!isJSON}
                onChange={(e) =>
                  setForm((prev) => ({
                    ...prev,
                    systemLogLevel: e.target.value as LoggingConfigForm["systemLogLevel"],
                  }))
                }
              >
                {SYSTEM_LOG_LEVELS.map((level) => (
                  <option key={level} value={level}>
                    {level}
                  </option>
                ))}
              </Select>
            </div>
          </div>
          <p className="text-xs text-fg-muted">
            {isJSON
              ? "Platform records are emitted as JSON events and both streams are filtered at the selected level."
              : "Log levels apply to the JSON format only — Lambda rejects them for Text."}
          </p>
        </div>
      ) : (
        <DefinitionList>
          <Definition label="Log format" value={saved.logFormat} />
          <Definition
            label="Log group"
            value={fn.LoggingConfig?.LogGroup ? <ArnLink arn={fn.LoggingConfig.LogGroup} /> : null}
          />
          {saved.logFormat === "JSON" && (
            <Definition label="Application log level" value={saved.applicationLogLevel} />
          )}
          {saved.logFormat === "JSON" && (
            <Definition label="System log level" value={saved.systemLogLevel} />
          )}
        </DefinitionList>
      )}
    </div>
  )
}

// ─── VPC configuration section ────────────────────────────────────────────

function VpcConfigSection({ fn }: { fn: LambdaFunction }) {
  const [editing, setEditing] = useState(false)

  const { data: allSubnets = [] } = useQuery(ec2SubnetsQueryOptions())
  const { data: allSecurityGroups = [] } = useQuery(ec2SecurityGroupsQueryOptions())

  const [subnetIds, setSubnetIds] = useState<string[]>(fn.VpcConfig?.SubnetIds ?? [])
  const [sgIds, setSgIds] = useState<string[]>(fn.VpcConfig?.SecurityGroupIds ?? [])

  const [prevFn, setPrevFn] = useState(fn)
  if (fn !== prevFn) {
    setPrevFn(fn)
    if (!editing) {
      setSubnetIds(fn.VpcConfig?.SubnetIds ?? [])
      setSgIds(fn.VpcConfig?.SecurityGroupIds ?? [])
    }
  }

  const updateMut = useResourceMutation({
    options: updateFunctionConfigurationMutationOptions(),
    invalidateKeys: [lambdaKeys.functions()],
    successTitle: "VPC configuration saved",
    errorTitle: "Save failed",
    onSuccess: () => setEditing(false),
  })

  const handleSave = () => {
    updateMut.mutate({
      FunctionName: fn.FunctionName,
      VpcConfig: {
        SubnetIds: subnetIds,
        SecurityGroupIds: sgIds,
      },
    })
  }

  const handleCancel = () => {
    setEditing(false)
    setSubnetIds(fn.VpcConfig?.SubnetIds ?? [])
    setSgIds(fn.VpcConfig?.SecurityGroupIds ?? [])
  }

  const handleRemoveVpc = () => {
    updateMut.mutate({
      FunctionName: fn.FunctionName,
      VpcConfig: { SubnetIds: [], SecurityGroupIds: [] },
    })
  }

  const hasVpc =
    fn.VpcConfig &&
    ((fn.VpcConfig.SubnetIds?.length ?? 0) > 0 || (fn.VpcConfig.SecurityGroupIds?.length ?? 0) > 0)

  // Available subnets/SGs not yet selected
  const availableSubnets = allSubnets.filter((s) => !subnetIds.includes(s.subnetId))
  const availableSGs = allSecurityGroups.filter((sg) => !sgIds.includes(sg.groupId))

  const [addingSubnet, setAddingSubnet] = useState(false)
  const [selectedSubnet, setSelectedSubnet] = useState("")
  const [addingSG, setAddingSG] = useState(false)
  const [selectedSG, setSelectedSG] = useState("")

  return (
    <div className="rounded-lg border border-border bg-bg-elevated p-4">
      <div className="mb-3 flex items-center justify-between">
        <h3 className="font-mono text-sm font-medium text-fg">VPC configuration</h3>
        {!editing ? (
          <div className="flex gap-2">
            {hasVpc && (
              <Button
                size="sm"
                variant="ghost"
                className="text-danger hover:text-danger"
                onClick={handleRemoveVpc}
                disabled={updateMut.isPending}
              >
                Remove VPC
              </Button>
            )}
            <Button size="sm" variant="secondary" onClick={() => setEditing(true)}>
              Edit
            </Button>
          </div>
        ) : (
          <div className="flex gap-2">
            <Button
              size="sm"
              variant="secondary"
              onClick={handleCancel}
              disabled={updateMut.isPending}
            >
              Cancel
            </Button>
            <Button size="sm" onClick={handleSave} disabled={updateMut.isPending}>
              {updateMut.isPending ? "Saving…" : "Save changes"}
            </Button>
          </div>
        )}
      </div>

      {editing ? (
        <div className="flex flex-col gap-4">
          {/* Subnets */}
          <div className="flex flex-col gap-2">
            <div className="flex items-center justify-between">
              <label className={cn(fieldLabel, "text-fg-muted")}>Subnets</label>
              <Button size="sm" variant="ghost" onClick={() => setAddingSubnet((v) => !v)}>
                <PlusIcon className="mr-1 h-3.5 w-3.5" /> Add
              </Button>
            </div>
            {addingSubnet && (
              <div className="flex items-center gap-2">
                <Combobox<{ value: string; label: string }>
                  value={selectedSubnet}
                  onChange={setSelectedSubnet}
                  items={availableSubnets.map((s) => ({
                    value: s.subnetId,
                    label: `${s.subnetId} (${s.cidrBlock} — ${s.availabilityZone})`,
                  }))}
                  filterFn={(item, q) => item.label.toLowerCase().includes(q.toLowerCase())}
                  getItemValue={(item) => item.value}
                  renderItem={(item: { value: string; label: string }) => (
                    <span className="text-sm">{item.label}</span>
                  )}
                  placeholder="Select subnet…"
                  className="flex-1"
                />
                <Button
                  size="sm"
                  disabled={!selectedSubnet}
                  onClick={() => {
                    setSubnetIds((prev) => [...prev, selectedSubnet])
                    setSelectedSubnet("")
                    setAddingSubnet(false)
                  }}
                >
                  Add
                </Button>
              </div>
            )}
            {subnetIds.length === 0 ? (
              <p className="text-xs text-fg-muted">No subnets selected.</p>
            ) : (
              <ul className="flex flex-col gap-1">
                {subnetIds.map((id) => {
                  const subnet = allSubnets.find((s) => s.subnetId === id)
                  return (
                    <li
                      key={id}
                      className="flex items-center justify-between rounded-md border border-border px-3 py-1.5 text-xs"
                    >
                      <span className="font-mono">
                        {id}
                        {subnet && (
                          <span className="ml-2 text-fg-muted">
                            {subnet.cidrBlock} — {subnet.availabilityZone}
                          </span>
                        )}
                      </span>
                      <Button
                        size="sm"
                        variant="ghost"
                        className="h-6 w-6 p-0 text-danger hover:text-danger"
                        onClick={() => setSubnetIds((prev) => prev.filter((s) => s !== id))}
                      >
                        <Trash2 className="h-3.5 w-3.5" />
                      </Button>
                    </li>
                  )
                })}
              </ul>
            )}
          </div>

          {/* Security Groups */}
          <div className="flex flex-col gap-2">
            <div className="flex items-center justify-between">
              <label className={cn(fieldLabel, "text-fg-muted")}>Security Groups</label>
              <Button size="sm" variant="ghost" onClick={() => setAddingSG((v) => !v)}>
                <PlusIcon className="mr-1 h-3.5 w-3.5" /> Add
              </Button>
            </div>
            {addingSG && (
              <div className="flex items-center gap-2">
                <Combobox<{ value: string; label: string }>
                  value={selectedSG}
                  onChange={setSelectedSG}
                  items={availableSGs.map((sg) => ({
                    value: sg.groupId,
                    label: `${sg.groupId} (${sg.groupName})`,
                  }))}
                  filterFn={(item, q) => item.label.toLowerCase().includes(q.toLowerCase())}
                  getItemValue={(item) => item.value}
                  renderItem={(item: { value: string; label: string }) => (
                    <span className="text-sm">{item.label}</span>
                  )}
                  placeholder="Select security group…"
                  className="flex-1"
                />
                <Button
                  size="sm"
                  disabled={!selectedSG}
                  onClick={() => {
                    setSgIds((prev) => [...prev, selectedSG])
                    setSelectedSG("")
                    setAddingSG(false)
                  }}
                >
                  Add
                </Button>
              </div>
            )}
            {sgIds.length === 0 ? (
              <p className="text-xs text-fg-muted">No security groups selected.</p>
            ) : (
              <ul className="flex flex-col gap-1">
                {sgIds.map((id) => {
                  const sg = allSecurityGroups.find((s) => s.groupId === id)
                  return (
                    <li
                      key={id}
                      className="flex items-center justify-between rounded-md border border-border px-3 py-1.5 text-xs"
                    >
                      <span className="font-mono">
                        {id}
                        {sg && <span className="ml-2 text-fg-muted">{sg.groupName}</span>}
                      </span>
                      <Button
                        size="sm"
                        variant="ghost"
                        className="h-6 w-6 p-0 text-danger hover:text-danger"
                        onClick={() => setSgIds((prev) => prev.filter((s) => s !== id))}
                      >
                        <Trash2 className="h-3.5 w-3.5" />
                      </Button>
                    </li>
                  )
                })}
              </ul>
            )}
          </div>
        </div>
      ) : !hasVpc ? (
        <p className="text-xs text-fg-muted">
          Not connected to a VPC. Edit to configure VPC networking. In Overcast a function reaches
          VPC resources either way — on AWS it could not.
        </p>
      ) : (
        <>
          <VpcNotEnforcedNotice />
          <DefinitionList>
            <Definition label="VPC ID" value={fn.VpcConfig!.VpcId} />
            <Definition label="Subnets" value={(fn.VpcConfig!.SubnetIds ?? []).join(", ")} />
            <Definition
              label="Security Groups"
              value={(fn.VpcConfig!.SecurityGroupIds ?? []).join(", ")}
            />
          </DefinitionList>
        </>
      )}
    </div>
  )
}

/**
 * VpcNotEnforcedNotice explains the one way Lambda VPC configuration diverges
 * from AWS here, which is the direction that makes a local test pass when the
 * deployed function will fail.
 *
 * Shown only when a VPC is actually configured. That is when someone is relying
 * on VPC semantics and stands to be misled; on the majority of functions, which
 * never touch VPC config, an always-on warning would be noise with nothing to
 * act on. The no-VPC case gets a single muted clause in its empty state
 * instead, so the person about to add a config still learns before they do.
 *
 * Not rendered while editing: the editor is a form, and a warning competing
 * with its controls is read as an error. It appears on save, which is when the
 * configuration is real.
 */
function VpcNotEnforcedNotice() {
  return (
    <div className="mb-3 flex gap-3 rounded-lg border border-warning/40 bg-warning/10 p-4 text-sm text-warning">
      <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0" />
      <div>
        <p className="font-semibold">Placement is real — isolation is not</p>
        <p className="mt-1 text-fg-muted">
          The container joins this VPC&apos;s Docker network and can reach what is in it. Overcast
          does not restrict what else it reaches: resources outside the VPC, and the internet, stay
          reachable where AWS would have no route. Security groups are stored and returned but never
          applied.
        </p>
        <Link
          to="/docs"
          search={{ path: "networking.md" }}
          hash="lambda-ecs-and-vpcs"
          className="mt-2 inline-flex w-fit items-center gap-1 text-xs underline underline-offset-2"
        >
          What this means for your tests
          <ExternalLink className="h-3 w-3" />
        </Link>
      </div>
    </div>
  )
}

// ─── Environment variables section ────────────────────────────────────────

type EnvPair = { key: string; value: string }

function EnvVarsSection({ fn }: { fn: LambdaFunction }) {
  const [editing, setEditing] = useState(false)

  const toRows = (env: Record<string, string>): EnvPair[] =>
    Object.entries(env).map(([key, value]) => ({ key, value }))

  const [rows, setRows] = useState<EnvPair[]>(() => toRows(fn.Environment?.Variables ?? {}))

  // Sync from server data when not actively editing.
  const [prevEnv, setPrevEnv] = useState(fn.Environment?.Variables)
  if (fn.Environment?.Variables !== prevEnv) {
    setPrevEnv(fn.Environment?.Variables)
    if (!editing) setRows(toRows(fn.Environment?.Variables ?? {}))
  }

  const updateMut = useResourceMutation({
    options: updateFunctionConfigurationMutationOptions(),
    invalidateKeys: [lambdaKeys.functions()],
    successTitle: "Environment variables saved",
    errorTitle: "Save failed",
    onSuccess: () => setEditing(false),
  })

  const handleSave = () => {
    const environment: Record<string, string> = {}
    for (const { key, value } of rows) {
      if (key.trim()) environment[key.trim()] = value
    }
    updateMut.mutate({ FunctionName: fn.FunctionName, Environment: { Variables: environment } })
  }

  const handleCancel = () => {
    setEditing(false)
    setRows(toRows(fn.Environment?.Variables ?? {}))
  }

  const addRow = () => setRows((r) => [...r, { key: "", value: "" }])
  const removeRow = (i: number) => setRows((r) => r.filter((_, idx) => idx !== i))
  const updateRow = (i: number, field: "key" | "value", val: string) =>
    setRows((r) => r.map((row, idx) => (idx === i ? { ...row, [field]: val } : row)))

  const currentEnv = fn.Environment?.Variables ?? {}
  const hasEnvVars = Object.keys(currentEnv).length > 0

  return (
    <div className="rounded-lg border border-border bg-bg-elevated p-4">
      <div className="mb-3 flex items-center justify-between">
        <h3 className="font-mono text-sm font-medium text-fg">Environment variables</h3>
        {!editing ? (
          <Button size="sm" variant="secondary" onClick={() => setEditing(true)}>
            Edit
          </Button>
        ) : (
          <div className="flex gap-2">
            <Button
              size="sm"
              variant="secondary"
              onClick={handleCancel}
              disabled={updateMut.isPending}
            >
              Cancel
            </Button>
            <Button size="sm" onClick={handleSave} disabled={updateMut.isPending}>
              {updateMut.isPending ? "Saving…" : "Save changes"}
            </Button>
          </div>
        )}
      </div>

      {editing ? (
        <div className="flex flex-col gap-2">
          {rows.length === 0 && (
            <p className="text-xs text-fg-muted">No environment variables. Add one below.</p>
          )}
          {rows.map((row, i) => (
            <div key={i} className="flex gap-2">
              <Input
                value={row.key}
                onChange={(e) => updateRow(i, "key", e.target.value)}
                placeholder="KEY"
                className="font-mono text-xs"
              />
              <Input
                value={row.value}
                onChange={(e) => updateRow(i, "value", e.target.value)}
                placeholder="value"
                className="font-mono text-xs"
              />
              <Button
                size="sm"
                variant="secondary"
                onClick={() => removeRow(i)}
                aria-label="Remove"
              >
                <Trash2 className="h-3.5 w-3.5" />
              </Button>
            </div>
          ))}
          <div className="mt-1">
            <Button size="sm" variant="secondary" onClick={addRow}>
              <PlusIcon className="mr-1 h-3.5 w-3.5" /> Add variable
            </Button>
          </div>
        </div>
      ) : !hasEnvVars ? (
        <p className="text-xs text-fg-muted">No environment variables configured.</p>
      ) : (
        <DefinitionList>
          {Object.entries(currentEnv).map(([key, val]) => (
            <Definition key={key} label={key} value={val} />
          ))}
        </DefinitionList>
      )}
    </div>
  )
}

// ─── Layers section ───────────────────────────────────────────────────────

function LayersSection({
  functionName,
  attachedLayers,
}: {
  functionName: string
  attachedLayers: Array<{ Arn?: string; CodeSize?: number }>
}) {
  const { toast } = useToast()
  const { data: allLayers = [] } = useQuery(layersQueryOptions())

  const updateMut = useResourceMutation({
    options: updateFunctionLayersMutationOptions(),
    invalidateKeys: [lambdaKeys.functions()],
    successTitle: "Layers updated",
    errorTitle: "Update failed",
  })

  const [showAdd, setShowAdd] = useState(false)
  const [selectedArn, setSelectedArn] = useState("")

  // Build a flat list of available version ARNs from the latest version of each layer
  const availableOptions = allLayers.map((l: LambdaLayer) => ({
    label: `${l.LayerName}:${l.LatestMatchingVersion?.Version}`,
    arn: l.LatestMatchingVersion?.LayerVersionArn ?? "",
  }))

  type LayerOption = { label: string; arn: string }

  const handleRemove = useCallback(
    (arn: string) => {
      const newArns = attachedLayers.filter((l) => l.Arn !== arn).map((l) => l.Arn ?? "")
      updateMut.mutate({ functionName, layerArns: newArns })
    },
    [functionName, attachedLayers, updateMut],
  )

  const handleAdd = useCallback(() => {
    if (!selectedArn) return
    const existing = attachedLayers.map((l) => l.Arn ?? "")
    if (existing.includes(selectedArn)) {
      toast({ title: "Already attached", description: selectedArn, variant: "danger" })
      return
    }
    updateMut.mutate({ functionName, layerArns: [...existing, selectedArn] })
    setShowAdd(false)
    setSelectedArn("")
  }, [functionName, attachedLayers, selectedArn, updateMut, toast])

  const attachedArns = new Set(attachedLayers.map((l) => l.Arn))
  const unattachedOptions = availableOptions.filter((o) => !attachedArns.has(o.arn))

  return (
    <div>
      <div className="mb-3 flex items-center justify-between">
        <h3 className="font-mono text-sm font-medium text-fg">Layers</h3>
        <Button size="sm" variant="ghost" onClick={() => setShowAdd((v) => !v)}>
          <PlusIcon className="mr-1 h-3.5 w-3.5" />
          Add layer
        </Button>
      </div>

      {showAdd && (
        <div className="mb-3 flex items-center gap-2 rounded-md border border-border bg-bg-elevated p-3">
          <Combobox<LayerOption>
            value={unattachedOptions.find((o) => o.arn === selectedArn)?.label ?? ""}
            onChange={setSelectedArn}
            items={unattachedOptions}
            filterFn={(opt, q) => opt.label.toLowerCase().includes(q.toLowerCase())}
            getItemValue={(opt) => opt.arn}
            renderItem={(opt, { selected, active }: ComboboxRenderContext) => (
              <span
                className={cn(
                  "block truncate px-3 py-1.5 text-sm text-fg",
                  active && "bg-bg-muted",
                  selected ? "font-medium" : "",
                )}
              >
                {opt.label}
              </span>
            )}
            placeholder="Search layer…"
            className="flex-1"
            popoverWidth="w-full"
          />
          <Button size="sm" disabled={!selectedArn || updateMut.isPending} onClick={handleAdd}>
            {updateMut.isPending ? <Spinner className="mr-1 h-3.5 w-3.5" /> : null}
            Attach
          </Button>
          <Button
            size="sm"
            variant="ghost"
            onClick={() => {
              setShowAdd(false)
              setSelectedArn("")
            }}
          >
            Cancel
          </Button>
        </div>
      )}

      {attachedLayers.length === 0 ? (
        <p className="text-sm text-fg-muted">No layers attached.</p>
      ) : (
        <ul className="flex flex-col gap-1">
          {attachedLayers.map((l) => (
            <li
              key={l.Arn}
              className="flex items-center justify-between rounded-md border border-border bg-bg-elevated px-3 py-2 text-xs"
            >
              <span className="mr-2 min-w-0 flex-1 truncate">
                <ArnLink arn={l.Arn ?? ""} />
              </span>
              <span className="mr-3 shrink-0 text-fg-muted">
                {l.CodeSize ? `${l.CodeSize} bytes` : ""}
              </span>
              <Button
                size="sm"
                variant="ghost"
                className="h-6 w-6 shrink-0 p-0 text-danger hover:text-danger"
                disabled={updateMut.isPending}
                onClick={() => handleRemove(l.Arn ?? "")}
              >
                <Trash2 className="h-3.5 w-3.5" />
              </Button>
            </li>
          ))}
        </ul>
      )}
    </div>
  )
}

// ─── Tags section ─────────────────────────────────────────────────────────

/**
 * Stands in for the tag set until ListTags answers.
 *
 * It has to be this one object rather than a `= {}` default in the destructure:
 * that default is re-evaluated on every render, so while the query is pending
 * it hands back a new object each time, and the render-phase sync below — which
 * compares by identity — then schedules an update on every render and never
 * converges. React gives up at 25 passes and throws "Too many re-renders",
 * which took out the whole Configuration tab on first paint.
 */
const NO_TAGS: Record<string, string> = {}

/**
 * Lambda tags live on the unqualified function ARN — AWS rejects a version or
 * alias ARN — and are read and written through ListTags/TagResource/
 * UntagResource rather than UpdateFunctionConfiguration.
 */
function TagsSection({ fn }: { fn: LambdaFunction }) {
  const resourceArn = fn.FunctionArn ?? ""
  const [editing, setEditing] = useState(false)

  const { data: tags = NO_TAGS, isLoading } = useQuery(functionTagsQueryOptions(resourceArn))

  const toRows = (source: Record<string, string>): EnvPair[] =>
    Object.entries(source).map(([key, value]) => ({ key, value }))

  const [rows, setRows] = useState<EnvPair[]>([])
  const [prevTags, setPrevTags] = useState<Record<string, string>>()
  if (tags !== prevTags) {
    setPrevTags(tags)
    if (!editing) setRows(toRows(tags))
  }

  const updateMut = useResourceMutation({
    options: updateFunctionTagsMutationOptions(),
    invalidateKeys: [lambdaKeys.tags(resourceArn), lambdaKeys.functions()],
    successTitle: "Tags saved",
    errorTitle: "Save tags failed",
    onSuccess: () => setEditing(false),
  })

  const handleSave = () => {
    const next: Record<string, string> = {}
    for (const { key, value } of rows) {
      if (key.trim()) next[key.trim()] = value
    }
    updateMut.mutate({ resourceArn, tags: next, previous: tags })
  }

  const handleCancel = () => {
    setEditing(false)
    setRows(toRows(tags))
  }

  const addRow = () => setRows((r) => [...r, { key: "", value: "" }])
  const removeRow = (i: number) => setRows((r) => r.filter((_, idx) => idx !== i))
  const updateRow = (i: number, field: "key" | "value", val: string) =>
    setRows((r) => r.map((row, idx) => (idx === i ? { ...row, [field]: val } : row)))

  const hasTags = Object.keys(tags).length > 0

  return (
    <div className="rounded-lg border border-border bg-bg-elevated p-4">
      <div className="mb-3 flex items-center justify-between">
        <h3 className="font-mono text-sm font-medium text-fg">Tags</h3>
        {!editing ? (
          <Button size="sm" variant="secondary" onClick={() => setEditing(true)}>
            Edit
          </Button>
        ) : (
          <div className="flex gap-2">
            <Button
              size="sm"
              variant="secondary"
              onClick={handleCancel}
              disabled={updateMut.isPending}
            >
              Cancel
            </Button>
            <Button size="sm" onClick={handleSave} disabled={updateMut.isPending}>
              {updateMut.isPending ? "Saving…" : "Save changes"}
            </Button>
          </div>
        )}
      </div>

      {editing ? (
        <div className="flex flex-col gap-2">
          {rows.length === 0 && <p className="text-xs text-fg-muted">No tags. Add one below.</p>}
          {rows.map((row, i) => (
            <div key={i} className="flex gap-2">
              <Input
                value={row.key}
                onChange={(e) => updateRow(i, "key", e.target.value)}
                placeholder="Key"
                className="font-mono text-xs"
              />
              <Input
                value={row.value}
                onChange={(e) => updateRow(i, "value", e.target.value)}
                placeholder="value"
                className="font-mono text-xs"
              />
              <Button
                size="sm"
                variant="secondary"
                onClick={() => removeRow(i)}
                aria-label="Remove"
              >
                <Trash2 className="h-3.5 w-3.5" />
              </Button>
            </div>
          ))}
          <div className="mt-1">
            <Button size="sm" variant="secondary" onClick={addRow}>
              <PlusIcon className="mr-1 h-3.5 w-3.5" /> Add tag
            </Button>
          </div>
        </div>
      ) : isLoading ? (
        <Spinner className="h-4 w-4" />
      ) : !hasTags ? (
        <p className="text-xs text-fg-muted">No tags on this function.</p>
      ) : (
        <DefinitionList>
          {Object.entries(tags).map(([key, val]) => (
            <Definition key={key} label={key} value={val} />
          ))}
        </DefinitionList>
      )}
    </div>
  )
}
