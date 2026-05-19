/**
 * useEventStream — singleton SSE subscription backed by React Query.
 *
 * A SharedWorker maintains the single EventSource to the emulator.
 * This keeps the connection alive across React HMR cycles and shares it
 * across all open tabs. The worker caches the last 1 000 events so any
 * newly-opened tab gets immediate history.
 *
 * The singleton is set up by calling useEventStreamSubscription() once in
 * the app shell. Individual components consume with useEventStream().
 *
 * Usage:
 *   const { data: events } = useEventStream({ source: "s3" })
 *   const { data: events } = useEventStream({ source: ["s3", "sqs"] })
 *   const { data: events } = useEventStream() // all sources
 */
import { useCallback, useEffect, useRef } from "react"
import { useQuery, useQueryClient, queryOptions, type QueryKey } from "@tanstack/react-query"
import { useEndpoint } from "@/hooks/use-endpoint"
import * as eventStreamClient from "@/workers/event-stream.client"
import type { WorkerMessage } from "@/workers/event-stream.protocol"
import { EventType } from "@/services/event-types"
import { s3Keys } from "@/features/s3/data"
import { sqsKeys } from "@/features/sqs/data"
import { snsKeys } from "@/features/sns/data"
import { dynamoKeys } from "@/features/dynamodb/data"
import { lambdaKeys } from "@/features/lambda/data"
import { pipeKeys } from "@/features/pipes/data"
import { inboxKeys } from "@/features/mail/data"
import { logsKeys } from "@/features/cloudwatch/data"
import { cfnKeys } from "@/features/cloudformation/data"
import { cloudfrontKeys } from "@/features/cloudfront/data"
import { ec2Keys } from "@/features/ec2/data"
import { ecsKeys } from "@/features/ecs/data"
import { rdsKeys } from "@/features/rds/data"
import { elasticacheKeys } from "@/features/elasticache/data"
import { apigwKeys } from "@/features/apigateway/data"
import { appsyncKeys } from "@/features/appsync/data"
import { cognitoKeys } from "@/features/cognito/data"
import { ebKeys } from "@/features/eventbridge/data"
import { iamKeys } from "@/features/iam/data"
import { kmsKeys } from "@/features/kms/data"
import { smKeys } from "@/features/secretsmanager/data"
import { sesKeys } from "@/features/ses/data"
import { ssmKeys } from "@/features/ssm/data"
import { sfnKeys } from "@/features/stepfunctions/data"
import { stsKeys } from "@/features/sts/data"
import { kinesisKeys } from "@/features/kinesis/data"
import { ecrKeys } from "@/features/ecr/data"
import { topologyKey } from "@/features/map/use-topology"
import { lambdaInstanceKeys } from "@/hooks/use-lambda-instances"
import type { StreamEvent } from "@/types"

export type { StreamEvent }

const MAX_EVENTS = 5_000

// ─── Query keys & options ──────────────────────────────────────────────────

const eventStreamKeys = {
  events: ["_eventStream", "events"] as const,
  status: ["_eventStream", "status"] as const,
}

interface EventStreamStatus {
  /** `null` = connecting (initial), `true` = online, `false` = offline */
  connected: boolean | null
}

/** Query options for the raw (unfiltered) event list. */
export function eventStreamQueryOptions() {
  return queryOptions({
    queryKey: eventStreamKeys.events,
    queryFn: () => [] as StreamEvent[],
    staleTime: Infinity,
    // Populated by the subscription — never refetch via HTTP
    refetchOnMount: false,
    refetchOnWindowFocus: false,
    refetchOnReconnect: false,
  })
}

/** Query options for the SSE connection status. */
export function eventStreamStatusQueryOptions() {
  return queryOptions({
    queryKey: eventStreamKeys.status,
    queryFn: (): EventStreamStatus => ({ connected: null }),
    staleTime: Infinity,
    refetchOnMount: false,
    refetchOnWindowFocus: false,
    refetchOnReconnect: false,
    networkMode: "always" as const,
  })
}

// ─── Singleton subscription ────────────────────────────────────────────────

/**
 * Sets up the single app-wide event stream via the SharedWorker.
 * Call once in the app shell. Pushes every received event into the
 * query cache so all useEventStream consumers re-render via React
 * Query's subscription model.
 */
export function useEventStreamSubscription() {
  const queryClient = useQueryClient()
  const endpoint = useEndpoint()

  const params = new URLSearchParams()
  params.set("ep", endpoint.baseUrl)
  params.set("region", endpoint.region)
  const url = `/api/events?${params.toString()}`

  // Keep a ref to the queryClient so the listener closure doesn't go stale
  // without causing the effect to re-run.
  const qcRef = useRef(queryClient)
  // eslint-disable-next-line react-hooks/refs -- intentional: keep ref fresh without re-running effect
  qcRef.current = queryClient

  useEffect(() => {
    function handleMessage(msg: WorkerMessage): void {
      const qc = qcRef.current

      switch (msg.type) {
        case "init":
          // Bootstrap the cache with the worker's stored events.
          qc.setQueryData<StreamEvent[]>(eventStreamKeys.events, msg.events)
          qc.setQueryData<EventStreamStatus>(eventStreamKeys.status, {
            connected: msg.connected,
          })
          break

        case "event":
          qc.setQueryData<StreamEvent[]>(eventStreamKeys.events, (old = []) => {
            const next = [...old, msg.event]
            next.sort((a, b) => (a.time < b.time ? -1 : a.time > b.time ? 1 : 0))
            return next.length > MAX_EVENTS ? next.slice(next.length - MAX_EVENTS) : next
          })
          invalidateForEvent(qc, msg.event)
          break

        case "status":
          qc.setQueryData<EventStreamStatus>(eventStreamKeys.status, {
            connected: msg.connected,
          })
          break

        case "cleared":
          qc.setQueryData<StreamEvent[]>(eventStreamKeys.events, [])
          break
      }
    }

    return eventStreamClient.subscribe(url, handleMessage)
  }, [url])
}

// ─── Consumer hook ─────────────────────────────────────────────────────────

export interface UseEventStreamOptions {
  /** Filter to one or more sources. Omit to receive all events. */
  source?: string | string[]
  /** Include heartbeat events in the results. Defaults to false. */
  includeHeartbeats?: boolean
}

export interface UseEventStreamResult {
  events: StreamEvent[]
  connected: boolean
  clear: () => void
}

/**
 * Consume events from the singleton EventSource, optionally filtered by
 * source. Uses React Query's `select` so filtering is memoised and only
 * the subscribers whose slice changed will re-render.
 */
export function useEventStream(opts: UseEventStreamOptions = {}): UseEventStreamResult {
  const sources = normaliseSources(opts.source)
  const includeHeartbeats = opts.includeHeartbeats ?? false

  const { data: events = [] } = useQuery({
    ...eventStreamQueryOptions(),
    select: (all) => {
      let filtered = all
      if (!includeHeartbeats) {
        filtered = filtered.filter((e) => e.type !== "heartbeat")
      }
      if (sources) {
        filtered = filtered.filter((e) => sources.includes(e.source))
      }
      return filtered
    },
  })

  const { data: status } = useQuery(eventStreamStatusQueryOptions())

  const clear = useCallback(() => {
    eventStreamClient.clear()
  }, [])

  return { events, connected: status?.connected ?? false, clear }
}

// ─── Helpers ───────────────────────────────────────────────────────────────

function normaliseSources(source: string | string[] | undefined): string[] | null {
  if (!source) return null
  return Array.isArray(source) ? source : [source]
}

// ─── Event → query-key invalidation ───────────────────────────────────────

/**
 * Returns a mapping from SSE event type to the query key prefixes to invalidate.
 * Evaluated at call-time so key factories capture the current endpoint.
 * TanStack Query uses prefix matching, so sqsKeys.messages() = [baseUrl, region, "sqs", "messages"]
 * will invalidate all keys beginning with those segments.
 */
function getEventQueryMap(): Record<string, QueryKey[] | undefined> {
  return {
    // ── S3 ──────────────────────────────────────────────────────────────────
    [EventType.s3.BucketCreated]: [s3Keys.buckets(), topologyKey],
    [EventType.s3.BucketDeleted]: [s3Keys.buckets(), topologyKey],
    [EventType.s3.NotificationConfigured]: [s3Keys.notification(), topologyKey],
    [EventType.s3.ObjectCreated]: [s3Keys.objects()],
    [EventType.s3.ObjectRemoved]: [s3Keys.objects()],

    // ── SQS ─────────────────────────────────────────────────────────────────
    [EventType.sqs.QueueCreated]: [sqsKeys.queues(), topologyKey],
    [EventType.sqs.QueueDeleted]: [sqsKeys.queues(), topologyKey],
    [EventType.sqs.QueuePurged]: [
      sqsKeys.queues(),
      sqsKeys.queue(),
      sqsKeys.messages(),
      topologyKey,
    ],
    [EventType.sqs.MessageSent]: [sqsKeys.messages(), sqsKeys.queue(), topologyKey],
    [EventType.sqs.MessageInflight]: [sqsKeys.messages(), sqsKeys.queue(), topologyKey],
    [EventType.sqs.MessageVisible]: [sqsKeys.messages(), sqsKeys.queue(), topologyKey],
    [EventType.sqs.MessageDeleted]: [sqsKeys.messages(), sqsKeys.queue(), topologyKey],
    [EventType.sqs.MessageDLQ]: [sqsKeys.messages(), sqsKeys.queue(), topologyKey],

    // ── SNS ─────────────────────────────────────────────────────────────────
    [EventType.sns.TopicCreated]: [snsKeys.topics(), topologyKey],
    [EventType.sns.TopicDeleted]: [snsKeys.topics(), topologyKey],
    [EventType.sns.SubscriptionCreated]: [snsKeys.subscriptions(), topologyKey],
    [EventType.sns.SubscriptionDeleted]: [snsKeys.subscriptions(), topologyKey],
    [EventType.sns.EmailDelivered]: [inboxKeys.messages()],
    [EventType.sns.SMSDelivered]: [inboxKeys.messages()],
    [EventType.sns.WebhookDelivered]: [inboxKeys.messages()],
    [EventType.sns.PushDelivered]: [inboxKeys.messages()],

    // ── DynamoDB ────────────────────────────────────────────────────────────
    [EventType.dynamodb.TableCreated]: [dynamoKeys.tables(), topologyKey],
    [EventType.dynamodb.TableDeleted]: [dynamoKeys.tables(), topologyKey],
    [EventType.dynamodb.StreamUpdated]: [dynamoKeys.tables(), topologyKey],
    [EventType.dynamodb.ItemMutated]: [dynamoKeys.items()],

    // ── Lambda ──────────────────────────────────────────────────────────────
    [EventType.lambda.FunctionCreated]: [lambdaKeys.functions(), topologyKey],
    [EventType.lambda.FunctionDeleted]: [lambdaKeys.functions(), topologyKey],
    [EventType.lambda.FunctionUpdated]: [lambdaKeys.functions(), lambdaKeys.source()],
    [EventType.lambda.InstanceAcquired]: [lambdaInstanceKeys.instances()],
    [EventType.lambda.InstanceReady]: [lambdaInstanceKeys.instances()],
    [EventType.lambda.InstanceInitializing]: [lambdaInstanceKeys.instances()],
    [EventType.lambda.InstanceReleased]: [lambdaInstanceKeys.instances()],
    [EventType.lambda.InstanceEvicted]: [lambdaInstanceKeys.instances()],

    // ── Pipes ────────────────────────────────────────────────────────────────
    [EventType.pipes.StateChanged]: [pipeKeys.all(), topologyKey],

    // ── Inbox ───────────────────────────────────────────────────────────────────
    [EventType.inbox.Delivered]: [inboxKeys.messages()],

    // ── Logs ─────────────────────────────────────────────────────────────────
    [EventType.logs.LogGroupCreated]: [logsKeys.all()],
    [EventType.logs.LogGroupDeleted]: [logsKeys.all()],
    [EventType.logs.LogStreamCreated]: [logsKeys.all()],
    [EventType.logs.LogStreamDeleted]: [logsKeys.all()],
    // logs:LogEventsWritten is handled specially in invalidateForEvent (scoped by logGroupName)

    // ── EC2 ─────────────────────────────────────────────────────────────────
    [EventType.ec2.VpcCreated]: [ec2Keys.vpcs(), topologyKey],
    [EventType.ec2.VpcDeleted]: [ec2Keys.vpcs(), topologyKey],
    [EventType.ec2.SubnetCreated]: [ec2Keys.subnets(), topologyKey],
    [EventType.ec2.SubnetDeleted]: [ec2Keys.subnets(), topologyKey],
    [EventType.ec2.SecurityGroupCreated]: [ec2Keys.securityGroups(), topologyKey],
    [EventType.ec2.SecurityGroupDeleted]: [ec2Keys.securityGroups(), topologyKey],
    [EventType.ec2.InstanceLaunched]: [ec2Keys.instances(), topologyKey],
    [EventType.ec2.InstanceTerminated]: [ec2Keys.instances(), topologyKey],
    [EventType.ec2.InstanceStarted]: [ec2Keys.instances()],
    [EventType.ec2.InstanceStopped]: [ec2Keys.instances()],

    // ── ECS ─────────────────────────────────────────────────────────────────
    [EventType.ecs.ClusterCreated]: [ecsKeys.clusters(), topologyKey],
    [EventType.ecs.ClusterDeleted]: [ecsKeys.clusters(), topologyKey],
    [EventType.ecs.TaskDefinitionRegistered]: [ecsKeys.taskDefinitions(), topologyKey],
    [EventType.ecs.TaskDefinitionDeregistered]: [ecsKeys.taskDefinitions(), topologyKey],
    [EventType.ecs.TaskStarted]: [ecsKeys.clusters(), ecsKeys.all(), topologyKey],
    [EventType.ecs.TaskStopped]: [ecsKeys.clusters(), ecsKeys.all(), topologyKey],
    [EventType.ecs.ServiceCreated]: [ecsKeys.clusters(), ecsKeys.all(), topologyKey],
    [EventType.ecs.ServiceUpdated]: [ecsKeys.clusters(), ecsKeys.all()],
    [EventType.ecs.ServiceDeleted]: [ecsKeys.clusters(), ecsKeys.all(), topologyKey],

    // ── RDS ─────────────────────────────────────────────────────────────────
    [EventType.rds.InstanceCreated]: [rdsKeys.instances(), topologyKey],
    [EventType.rds.InstanceDeleted]: [rdsKeys.instances(), topologyKey],
    [EventType.rds.InstanceModified]: [rdsKeys.instances()],
    [EventType.rds.InstanceStarted]: [rdsKeys.instances()],
    [EventType.rds.InstanceStopped]: [rdsKeys.instances()],
    [EventType.rds.SubnetGroupCreated]: [rdsKeys.all(), topologyKey],
    [EventType.rds.SubnetGroupDeleted]: [rdsKeys.all(), topologyKey],

    // ── CloudFormation ───────────────────────────────────────────────────────
    // Stack lifecycle: invalidate list + the specific stack detail + its resources/events
    [EventType.cloudformation.StackCreated]: [
      cfnKeys.stacks(),
      cfnKeys.stack(),
      cfnKeys.resources(),
      cfnKeys.events(),
      topologyKey,
    ],
    [EventType.cloudformation.StackUpdated]: [
      cfnKeys.stacks(),
      cfnKeys.stack(),
      cfnKeys.resources(),
      cfnKeys.events(),
      cfnKeys.template(),
    ],
    [EventType.cloudformation.StackDeleted]: [
      cfnKeys.stacks(),
      cfnKeys.stack(),
      cfnKeys.resources(),
      cfnKeys.events(),
      topologyKey,
    ],
    [EventType.cloudformation.StackFailed]: [
      cfnKeys.stacks(),
      cfnKeys.stack(),
      cfnKeys.resources(),
      cfnKeys.events(),
    ],
    // Resource events: invalidate resources + events tab (detail views polling these)
    [EventType.cloudformation.ResourceProvisioned]: [
      cfnKeys.resources(),
      cfnKeys.events(),
      cfnKeys.stack(),
    ],
    [EventType.cloudformation.ResourceDeleted]: [
      cfnKeys.resources(),
      cfnKeys.events(),
      cfnKeys.stack(),
    ],
    // ChangeSet events: not yet surfaced in getEventQueryMap but wired for future-proofing

    // ── CloudFront ──────────────────────────────────────────────────────────
    [EventType.cloudfront.DistributionCreated]: [cloudfrontKeys.distributions(), topologyKey],
    [EventType.cloudfront.DistributionUpdated]: [
      cloudfrontKeys.distributions(),
      cloudfrontKeys.distribution(),
    ],
    [EventType.cloudfront.DistributionDeleted]: [
      cloudfrontKeys.distributions(),
      cloudfrontKeys.distribution(),
      topologyKey,
    ],
    [EventType.cloudfront.InvalidationCreated]: [cloudfrontKeys.invalidations()],

    // ── API Gateway ───────────────────────────────────────────────────────
    [EventType.apigateway.RestApiCreated]: [apigwKeys.restApis(), topologyKey],
    [EventType.apigateway.RestApiDeleted]: [apigwKeys.restApis(), topologyKey],
    [EventType.apigateway.HttpApiCreated]: [apigwKeys.httpApis(), topologyKey],
    [EventType.apigateway.HttpApiDeleted]: [apigwKeys.httpApis(), topologyKey],
    [EventType.apigateway.Deployed]: [apigwKeys.all()],

    // ── AppSync ───────────────────────────────────────────────────────────
    [EventType.appsync.ApiCreated]: [appsyncKeys.apis(), topologyKey],
    [EventType.appsync.ApiUpdated]: [appsyncKeys.apis(), appsyncKeys.all()],
    [EventType.appsync.ApiDeleted]: [appsyncKeys.apis(), topologyKey],
    [EventType.appsync.SchemaUpdated]: [appsyncKeys.all()],
    [EventType.appsync.DataSourceCreated]: [appsyncKeys.all()],
    [EventType.appsync.DataSourceDeleted]: [appsyncKeys.all()],
    [EventType.appsync.ResolverCreated]: [appsyncKeys.all()],
    [EventType.appsync.ResolverDeleted]: [appsyncKeys.all()],

    // ── Cognito ───────────────────────────────────────────────────────────
    [EventType.cognito.UserPoolCreated]: [cognitoKeys.pools(), topologyKey],
    [EventType.cognito.UserPoolDeleted]: [cognitoKeys.pools(), topologyKey],
    [EventType.cognito.UserCreated]: [cognitoKeys.all()],
    [EventType.cognito.UserDeleted]: [cognitoKeys.all()],
    [EventType.cognito.UserConfirmed]: [cognitoKeys.all()],
    [EventType.cognito.UserUpdated]: [cognitoKeys.all()],
    [EventType.cognito.SignIn]: [cognitoKeys.all()],
    [EventType.cognito.SignInFailed]: [cognitoKeys.all()],
    [EventType.cognito.PasswordChanged]: [cognitoKeys.all()],
    [EventType.cognito.SignOut]: [cognitoKeys.all()],
    [EventType.cognito.GroupCreated]: [cognitoKeys.all()],
    [EventType.cognito.GroupDeleted]: [cognitoKeys.all()],
    [EventType.cognito.GroupUpdated]: [cognitoKeys.all()],
    [EventType.cognito.GroupMembershipChanged]: [cognitoKeys.all()],
    [EventType.cognito.ClientCreated]: [cognitoKeys.all()],
    [EventType.cognito.ClientDeleted]: [cognitoKeys.all()],

    // ── EventBridge ───────────────────────────────────────────────────────
    [EventType.eventbridge.BusCreated]: [ebKeys.buses(), topologyKey],
    [EventType.eventbridge.BusDeleted]: [ebKeys.buses(), topologyKey],
    [EventType.eventbridge.RuleCreated]: [ebKeys.rules(), topologyKey],
    [EventType.eventbridge.RuleDeleted]: [ebKeys.rules(), topologyKey],

    // ── IAM ───────────────────────────────────────────────────────────────
    [EventType.iam.RoleCreated]: [iamKeys.roles(), topologyKey],
    [EventType.iam.RoleDeleted]: [iamKeys.roles(), topologyKey],
    [EventType.iam.UserCreated]: [iamKeys.users()],
    [EventType.iam.UserDeleted]: [iamKeys.users()],
    [EventType.iam.PolicyCreated]: [iamKeys.policies()],
    [EventType.iam.PolicyDeleted]: [iamKeys.policies()],

    // ── KMS ───────────────────────────────────────────────────────────────
    [EventType.kms.KeyCreated]: [kmsKeys.keys(), topologyKey],
    [EventType.kms.KeyDeleted]: [kmsKeys.keys(), topologyKey],
    [EventType.kms.KeyStateChanged]: [kmsKeys.keys()],

    // ── Secrets Manager ───────────────────────────────────────────────────
    [EventType.secretsmanager.SecretCreated]: [smKeys.secrets(), topologyKey],
    [EventType.secretsmanager.SecretDeleted]: [smKeys.secrets(), topologyKey],
    [EventType.secretsmanager.SecretUpdated]: [smKeys.secrets()],
    [EventType.secretsmanager.SecretRotated]: [smKeys.secrets()],

    // ── SES ───────────────────────────────────────────────────────────────
    [EventType.ses.EmailSent]: [sesKeys.all(), inboxKeys.messages()],
    [EventType.ses.IdentityCreated]: [sesKeys.identities(), topologyKey],
    [EventType.ses.IdentityDeleted]: [sesKeys.identities(), topologyKey],
    [EventType.ses.TemplateCreated]: [sesKeys.all()],
    [EventType.ses.TemplateDeleted]: [sesKeys.all()],

    // ── SSM ───────────────────────────────────────────────────────────────
    [EventType.ssm.ParameterCreated]: [ssmKeys.parameters(), topologyKey],
    [EventType.ssm.ParameterDeleted]: [ssmKeys.parameters(), topologyKey],
    [EventType.ssm.ParameterUpdated]: [ssmKeys.parameters()],

    // ── Step Functions ────────────────────────────────────────────────────
    [EventType.stepfunctions.StateMachineCreated]: [sfnKeys.stateMachines(), topologyKey],
    [EventType.stepfunctions.StateMachineDeleted]: [sfnKeys.stateMachines(), topologyKey],
    [EventType.stepfunctions.ExecutionStarted]: [sfnKeys.all()],

    // ── STS ───────────────────────────────────────────────────────────────
    [EventType.sts.RoleAssumed]: [stsKeys.all()],

    // ── Kinesis ───────────────────────────────────────────────────────────
    [EventType.kinesis.StreamCreated]: [kinesisKeys.streams(), topologyKey],
    [EventType.kinesis.StreamDeleted]: [kinesisKeys.streams(), topologyKey],

    // ── ElastiCache ───────────────────────────────────────────────────────
    [EventType.elasticache.ClusterCreated]: [elasticacheKeys.clusters(), topologyKey],
    [EventType.elasticache.ClusterDeleted]: [elasticacheKeys.clusters(), topologyKey],
    [EventType.elasticache.ClusterModified]: [elasticacheKeys.clusters()],
    [EventType.elasticache.ReplicationGroupCreated]: [elasticacheKeys.all(), topologyKey],
    [EventType.elasticache.ReplicationGroupDeleted]: [elasticacheKeys.all(), topologyKey],
    // ── ECR ────────────────────────────────────────────────────────────
    [EventType.ecr.RepositoryCreated]: [ecrKeys.repositories(), topologyKey],
    [EventType.ecr.RepositoryDeleted]: [ecrKeys.repositories(), topologyKey],
    [EventType.ecr.ImagePushed]: [ecrKeys.repository()],
  }
}

/**
 * Invalidate React Query caches for a single SSE event.
 * Called synchronously from onMessage — no effect, no ref, no delay.
 */
function invalidateForEvent(qc: ReturnType<typeof useQueryClient>, evt: StreamEvent) {
  const keys = getEventQueryMap()[evt.type]
  if (keys) {
    for (const key of keys) {
      void qc.invalidateQueries({ queryKey: key })
    }
  }

  // logs:LogEventsWritten — scoped invalidation by log group
  if (evt.type === EventType.logs.LogEventsWritten) {
    const p = evt.payload as Record<string, unknown> | undefined
    const logGroupName = p?.logGroupName as string | undefined
    if (logGroupName) {
      void qc.invalidateQueries({ queryKey: logsKeys.filter(logGroupName) })
    }
  }
}
