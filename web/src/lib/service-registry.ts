/**
 * service-registry — single source of truth for all navigable AWS services.
 *
 * Every icon, color token, hex value, minimap letter, route path, nav category,
 * and description lives here. The sidebar, dashboard, global search, topology
 * map, and docs button all derive their values from this registry.
 *
 * When adding a new service:
 *   1. Add an entry to SERVICES below (visual identity + routing fields).
 *   2. That's it — sidebar, dashboard card, global search, and topology all
 *      derive from here automatically. See the field docs on ServiceEntry for
 *      which fields control which surfaces.
 */

import {
  Archive,
  MessagesSquare,
  Database,
  DatabaseZap,
  Bell,
  Zap,
  ScrollText,
  Boxes,
  Cpu,
  Mail,
  Cable,
  Waves,
  Radio,
  KeyRound,
  Users,
  UserCheck,
  Key,
  ShieldAlert,
  ShieldCheck,
  PlugZap,
  Globe,
  Braces,
  Layers,
  Activity,
  Workflow,
  Waypoints,
  SlidersHorizontal,
  Fingerprint,
  Network,
  type LucideIcon,
} from "lucide-react"

// ── Category types ─────────────────────────────────────────────────────────

export type ServiceCategory =
  | "storage"
  | "compute"
  | "messaging"
  | "security"
  | "networking"
  | "monitoring"

export const CATEGORY_LABELS: Record<ServiceCategory, string> = {
  storage: "Storage & Database",
  compute: "Compute",
  messaging: "Messaging",
  security: "Security & Identity",
  networking: "Networking & APIs",
  monitoring: "Monitoring",
}

export const CATEGORY_ORDER: ServiceCategory[] = [
  "storage",
  "compute",
  "messaging",
  "security",
  "networking",
  "monitoring",
]

export interface SubNavItem {
  to: string
  label: string
}

export interface SubNavGroup {
  group: string
  items: SubNavItem[]
}

/** A child entry is either a direct link or a labelled group of links. */
export type SubNavChild = SubNavItem | SubNavGroup

// ── ServiceEntry ───────────────────────────────────────────────────────────

export interface ServiceEntry {
  // ── Visual identity ──────────────────────────────────────────────────────
  /** Human-readable label (e.g. "S3", "DynamoDB"). */
  label: string
  /** Lucide icon component. */
  icon: LucideIcon
  /** Tailwind text color class (e.g. "text-orange-400"). */
  color: string
  /** Tailwind bg class, paired with the color (e.g. "bg-orange-400/10"). */
  bg: string
  /** Tailwind border class, paired with the color (e.g. "border-orange-400/30"). */
  border: string
  /** CSS hex color — used by canvas/SVG renderers (minimap, sweep animations). */
  hex: string
  /** Single character shown in the minimap node pill. */
  letter: string

  // ── Routing & surfaces ───────────────────────────────────────────────────
  /**
   * Primary route path. Omit for entries that exist only for visual identity
   * on the topology map (vpc, igw, etc.).
   */
  to?: string
  /**
   * Nav sidebar category. Required when this service appears in the sidebar.
   */
  category?: ServiceCategory
  /**
   * Brief description shown in the sidebar nav and global search results.
   * Also used as the dashboard card description when dashboardDescription is absent.
   */
  description?: string
  /**
   * Longer, action-oriented description for the dashboard card.
   * Falls back to description when absent.
   */
  dashboardDescription?: string
  /**
   * Dashboard card label when it differs from label (e.g. "EC2 / VPC" vs "EC2").
   * Falls back to label.
   */
  dashboardLabel?: string
  /**
   * Filename stem in docs/services/<docKey>.md. Enables the ServiceDocsButton
   * on the dashboard card and service home pages.
   */
  docKey?: string
  /** Sidebar sub-navigation items. May be flat links or labelled groups. */
  children?: SubNavChild[]
  /**
   * Show in the sidebar nav.
   * Set to false for dashboard-only services (kms, ssm, sts, etc.).
   * @default true - when {@see ServiceEntry.to} + {@see ServiceEntry.category} are set, otherwise false
   */
  nav?: boolean
  /**
   * Show on the dashboard.
   * Set to false for stub/info-only services (waf, shield) that don't warrant a card.
   * @default true - when {@see ServiceEntry.to} is set, otherwise false
   */
  dashboardCard?: boolean
  /**
   * Whether users can favourite/pin this service in the sidebar.
   * @default true - when {@see ServiceEntry.to} is set, otherwise false
   */
  favouritable?: boolean
}

// ── Registry ───────────────────────────────────────────────────────────────

export const SERVICES = {
  // ── Storage & Database ─────────────────────────────────────────────────
  s3: {
    label: "S3",
    icon: Archive,
    color: "text-orange-400",
    bg: "bg-orange-400/10",
    border: "border-orange-400/30",
    hex: "#fb923c",
    letter: "S",
    to: "/s3",
    category: "storage",
    description: "Object storage",
    dashboardDescription: "Object storage — buckets, upload, download, and browse files.",
    docKey: "s3",
  },
  dynamodb: {
    label: "DynamoDB",
    icon: Database,
    color: "text-blue-400",
    bg: "bg-blue-400/10",
    border: "border-blue-400/30",
    hex: "#60a5fa",
    letter: "D",
    to: "/dynamodb",
    category: "storage",
    description: "NoSQL key-value database",
    dashboardDescription: "NoSQL tables — manage tables, browse items, and run queries.",
    docKey: "dynamodb",
  },
  rds: {
    label: "RDS",
    icon: DatabaseZap,
    color: "text-violet-400",
    bg: "bg-violet-400/10",
    border: "border-violet-400/30",
    hex: "#a78bfa",
    letter: "R",
    to: "/rds",
    category: "storage",
    description: "Relational databases",
    dashboardLabel: "RDS / Aurora",
    dashboardDescription: "Managed relational databases — MySQL, PostgreSQL, and Aurora.",
    docKey: "rds",
  },
  elasticache: {
    label: "ElastiCache",
    icon: DatabaseZap,
    color: "text-green-500",
    bg: "bg-green-500/10",
    border: "border-green-500/30",
    hex: "#22c55e",
    letter: "EC",
    to: "/elasticache",
    category: "storage",
    description: "In-memory caching (Redis/Memcached)",
    dashboardDescription: "In-memory caching — Redis and Memcached clusters.",
    docKey: "elasticache",
  },
  msk: {
    label: "MSK",
    icon: Radio,
    color: "text-sky-500",
    bg: "bg-sky-500/10",
    border: "border-sky-500/30",
    hex: "#0ea5e9",
    letter: "MSK",
    to: "/msk",
    category: "messaging",
    description: "Managed Kafka clusters (Redpanda)",
    dashboardDescription: "Managed Kafka — clusters, bootstrap brokers, and configurations.",
    docKey: "msk",
  },

  // ── Compute ────────────────────────────────────────────────────────────
  lambda: {
    label: "Lambda",
    icon: Zap,
    color: "text-purple-400",
    bg: "bg-purple-400/10",
    border: "border-purple-400/30",
    hex: "#c084fc",
    letter: "λ",
    to: "/lambda",
    category: "compute",
    description: "Serverless functions",
    dashboardDescription: "Serverless functions — deploy, invoke, and view logs.",
    docKey: "lambda",
    children: [
      { to: "/lambda", label: "Functions" },
      { to: "/lambda/layers", label: "Layers" },
    ],
  },
  ec2: {
    label: "EC2",
    icon: Cpu,
    color: "text-sky-400",
    bg: "bg-sky-400/10",
    border: "border-sky-400/30",
    hex: "#38bdf8",
    letter: "C",
    to: "/ec2",
    category: "compute",
    description: "Virtual machines",
    dashboardLabel: "EC2 / VPC",
    dashboardDescription: "Virtual machines and networking — instances, VPCs, and subnets.",
    docKey: "ec2",
  },
  ecs: {
    label: "ECS",
    icon: Boxes,
    color: "text-emerald-400",
    bg: "bg-emerald-400/10",
    border: "border-emerald-400/30",
    hex: "#34d399",
    letter: "E",
    to: "/ecs",
    category: "compute",
    description: "Container orchestration",
    dashboardDescription: "Container orchestration — clusters, task definitions, and services.",
    docKey: "ecs",
  },
  ecr: {
    label: "ECR",
    icon: Boxes,
    color: "text-rose-400",
    bg: "bg-rose-400/10",
    border: "border-rose-400/30",
    hex: "#fb7185",
    letter: "R",
    to: "/ecr",
    category: "compute",
    description: "Container image registry",
    dashboardDescription:
      "Container registry — repositories, image tags, digests, and local docker login hints.",
    docKey: "ecr",
  },
  eks: {
    label: "EKS",
    icon: Boxes,
    color: "text-emerald-300",
    bg: "bg-emerald-300/10",
    border: "border-emerald-300/30",
    hex: "#86efac",
    letter: "K8s",
    to: "/eks",
    category: "compute",
    description: "Managed Kubernetes control plane",
    dashboardDescription:
      "Kubernetes clusters — inspect control-plane metadata and cluster status.",
    docKey: "eks",
  },
  stepfunctions: {
    label: "Step Functions",
    icon: Workflow,
    color: "text-teal-300",
    bg: "bg-teal-300/10",
    border: "border-teal-300/30",
    hex: "#5eead4",
    letter: "W",
    to: "/stepfunctions",
    category: "compute",
    description: "State machine orchestration",
    dashboardDescription: "Serverless workflows — state machines and visual orchestration.",
    docKey: "stepfunctions",
  },

  // ── Messaging ──────────────────────────────────────────────────────────
  sqs: {
    label: "SQS",
    icon: MessagesSquare,
    color: "text-yellow-400",
    bg: "bg-yellow-400/10",
    border: "border-yellow-400/30",
    hex: "#facc15",
    letter: "Q",
    to: "/sqs",
    category: "messaging",
    description: "Message queues",
    dashboardDescription: "Message queues — send, receive, and inspect messages.",
    docKey: "sqs",
  },
  sns: {
    label: "SNS",
    icon: Bell,
    color: "text-pink-400",
    bg: "bg-pink-400/10",
    border: "border-pink-400/30",
    hex: "#f472b6",
    letter: "N",
    to: "/sns",
    category: "messaging",
    description: "Pub/sub notifications",
    dashboardDescription: "Pub/sub notifications — topics, subscriptions, and publishing.",
    docKey: "sns",
  },
  ses: {
    label: "SES",
    icon: Mail,
    color: "text-amber-500",
    bg: "bg-amber-500/10",
    border: "border-amber-500/30",
    hex: "#f59e0b",
    letter: "M",
    to: "/ses",
    category: "messaging",
    description: "Email sending service",
    dashboardDescription: "Email sending — send messages and inspect delivery history.",
    docKey: "ses",
  },
  pipes: {
    label: "Pipes",
    icon: Cable,
    color: "text-cyan-400",
    bg: "bg-cyan-400/10",
    border: "border-cyan-400/30",
    hex: "#22d3ee",
    letter: "P",
    to: "/pipes",
    category: "messaging",
    description: "Event-driven pipelines",
    dashboardDescription: "EventBridge Pipes — route DynamoDB stream events to SQS queues.",
    docKey: "pipes",
  },
  kinesis: {
    label: "Kinesis",
    icon: Waves,
    color: "text-cyan-300",
    bg: "bg-cyan-300/10",
    border: "border-cyan-300/30",
    hex: "#67e8f9",
    letter: "K",
    to: "/kinesis",
    category: "messaging",
    description: "Real-time data streams",
    dashboardDescription: "Real-time data streams — create, manage, and inspect Kinesis streams.",
    docKey: "kinesis",
  },
  eventbridge: {
    label: "EventBridge",
    icon: Waypoints,
    color: "text-rose-400",
    bg: "bg-rose-400/10",
    border: "border-rose-400/30",
    hex: "#fb7185",
    letter: "Ev",
    to: "/eventbridge",
    category: "messaging",
    description: "Event bus",
    dashboardDescription: "Event bus — rules, targets, and event routing.",
    docKey: "eventbridge",
    nav: false,
  },

  // ── Security & Identity ────────────────────────────────────────────────
  secretsmanager: {
    label: "Secrets Manager",
    icon: KeyRound,
    color: "text-red-400",
    bg: "bg-red-400/10",
    border: "border-red-400/30",
    hex: "#f87171",
    letter: "Sm",
    to: "/secretsmanager",
    category: "security",
    description: "Secrets storage & rotation",
    dashboardDescription: "Secrets — create, retrieve, rotate, and manage secrets.",
    docKey: "secretsmanager",
  },
  iam: {
    label: "IAM",
    icon: Users,
    color: "text-yellow-300",
    bg: "bg-yellow-300/10",
    border: "border-yellow-300/30",
    hex: "#fde047",
    letter: "I",
    to: "/iam",
    category: "security",
    description: "Identity & access management",
    dashboardDescription: "Identity and Access Management — roles, users, and policies.",
    docKey: "iam",
  },
  cognito: {
    label: "Cognito",
    icon: UserCheck,
    color: "text-indigo-400",
    bg: "bg-indigo-400/10",
    border: "border-indigo-400/30",
    hex: "#818cf8",
    letter: "U",
    to: "/cognito",
    category: "security",
    description: "User authentication & pools",
    dashboardDescription: "User authentication — user pools and identity providers.",
    docKey: "cognito",
  },
  kms: {
    label: "KMS",
    icon: Key,
    color: "text-amber-400",
    bg: "bg-amber-400/10",
    border: "border-amber-400/30",
    hex: "#fbbf24",
    letter: "Km",
    to: "/kms",
    category: "security",
    description: "Encryption key management",
    dashboardLabel: "KMS",
    dashboardDescription:
      "Key Management Service — encryption keys, aliases, and crypto operations.",
    docKey: "kms",
    nav: false,
  },
  ssm: {
    label: "SSM",
    icon: SlidersHorizontal,
    color: "text-orange-300",
    bg: "bg-orange-300/10",
    border: "border-orange-300/30",
    hex: "#fdba74",
    letter: "Ss",
    to: "/ssm",
    category: "security",
    description: "Parameter store",
    dashboardLabel: "SSM Parameter Store",
    dashboardDescription:
      "Systems Manager — parameter store for config, secrets, and feature flags.",
    docKey: "ssm",
    nav: false,
  },
  sts: {
    label: "STS",
    icon: Fingerprint,
    color: "text-slate-300",
    bg: "bg-slate-300/10",
    border: "border-slate-300/30",
    hex: "#cbd5e1",
    letter: "St",
    to: "/sts",
    category: "security",
    description: "Temporary credentials",
    dashboardDescription: "Security Token Service — temporary credentials and caller identity.",
    docKey: "sts",
    nav: false,
  },
  waf: {
    label: "WAF",
    icon: ShieldAlert,
    color: "text-red-300",
    bg: "bg-red-300/10",
    border: "border-red-300/30",
    hex: "#fca5a5",
    letter: "Wf",
    to: "/waf",
    category: "security",
    description: "Web application firewall",
    favouritable: false,
    dashboardCard: false,
  },
  shield: {
    label: "Shield",
    icon: ShieldCheck,
    color: "text-indigo-300",
    bg: "bg-indigo-300/10",
    border: "border-indigo-300/30",
    hex: "#a5b4fc",
    letter: "Sh",
    to: "/shield",
    category: "security",
    description: "DDoS protection",
    favouritable: false,
    dashboardCard: false,
  },

  // ── Networking & APIs ──────────────────────────────────────────────────
  apigateway: {
    label: "API Gateway",
    icon: PlugZap,
    color: "text-green-300",
    bg: "bg-green-300/10",
    border: "border-green-300/30",
    hex: "#86efac",
    letter: "A",
    to: "/apigateway",
    category: "networking",
    description: "REST & WebSocket APIs",
    dashboardDescription: "HTTP and REST APIs — create, deploy, and manage endpoints.",
    docKey: "apigateway",
    children: [
      { to: "/apigateway", label: "APIs" },
      { to: "/apigateway/api-keys", label: "API Keys" },
      { to: "/apigateway/usage-plans", label: "Usage Plans" },
    ],
  },
  cloudfront: {
    label: "CloudFront",
    icon: Globe,
    color: "text-purple-300",
    bg: "bg-purple-300/10",
    border: "border-purple-300/30",
    hex: "#d8b4fe",
    letter: "Cf",
    to: "/cloudfront",
    category: "networking",
    description: "Content delivery network",
    dashboardDescription: "Content delivery network — distributions and edge caching.",
    docKey: "cloudfront",
    children: [
      { to: "/cloudfront", label: "Distributions" },
      { to: "/cloudfront/continuous-deployment-policies", label: "Continuous Deployment" },
      {
        group: "Security",
        items: [
          { to: "/cloudfront/key-groups", label: "Key Groups" },
          { to: "/cloudfront/fle-configs", label: "FLE Configs" },
          { to: "/cloudfront/fle-profiles", label: "FLE Profiles" },
        ],
      },
      {
        group: "Logging",
        items: [{ to: "/cloudfront/realtime-log-configs", label: "Realtime Log Configs" }],
      },
    ],
  },
  appsync: {
    label: "AppSync",
    icon: Braces,
    color: "text-pink-300",
    bg: "bg-pink-300/10",
    border: "border-pink-300/30",
    hex: "#f9a8d4",
    letter: "As",
    to: "/appsync",
    category: "networking",
    description: "Managed GraphQL API",
    dashboardDescription: "Managed GraphQL — APIs, resolvers, and data sources.",
    docKey: "appsync",
  },
  cloudformation: {
    label: "CloudFormation",
    icon: Layers,
    color: "text-cyan-300",
    bg: "bg-cyan-300/10",
    border: "border-cyan-300/30",
    hex: "#67e8f9",
    letter: "CF",
    to: "/cloudformation/",
    category: "networking",
    description: "Infrastructure as code",
    dashboardDescription: "Infrastructure as code — deploy and manage stacks.",
    docKey: "cloudformation",
  },
  appregistry: {
    label: "Applications",
    icon: Boxes,
    color: "text-cyan-200",
    bg: "bg-cyan-200/10",
    border: "border-cyan-200/30",
    hex: "#a5f3fc",
    letter: "App",
    to: "/applications/",
    category: "networking",
    description: "AppRegistry — resource groupings",
    dashboardCard: false,
  },

  // ── Monitoring ─────────────────────────────────────────────────────────
  cloudwatch: {
    label: "CloudWatch",
    icon: Activity,
    color: "text-green-400",
    bg: "bg-green-400/10",
    border: "border-green-400/30",
    hex: "#4ade80",
    letter: "CW",
    to: "/cloudwatch",
    category: "monitoring",
    description: "Metrics, alarms & logs",
    dashboardCard: false,
    children: [
      { to: "/cloudwatch", label: "Metrics" },
      { to: "/cloudwatch/logs", label: "Logs" },
    ],
  },

  // ── Topology / map-only (no route) ─────────────────────────────────────
  /**
   * "logs" is used by the topology map and as the dashboard card for
   * CloudWatch Logs. It deliberately points to /cloudwatch/logs rather
   * than having its own top-level route.
   */
  logs: {
    label: "CloudWatch Logs",
    icon: ScrollText,
    color: "text-teal-400",
    bg: "bg-teal-400/10",
    border: "border-teal-400/30",
    hex: "#2dd4bf",
    letter: "L",
    to: "/cloudwatch/logs",
    category: "monitoring",
    description: "Log groups and streams",
    dashboardLabel: "CloudWatch",
    dashboardDescription: "Observability — logs, metrics, and alarms.",
    docKey: "cloudwatch-logs",
    nav: false,
  },
  vpc: {
    label: "VPC",
    icon: Network,
    color: "text-teal-400",
    bg: "bg-teal-400/10",
    border: "border-teal-400/30",
    hex: "#2dd4bf",
    letter: "V",
  },
  igw: {
    label: "Internet Gateway",
    icon: Globe,
    color: "text-blue-400",
    bg: "bg-blue-400/10",
    border: "border-blue-400/30",
    hex: "#60a5fa",
    letter: "IG",
  },
} as const satisfies Record<string, ServiceEntry>
