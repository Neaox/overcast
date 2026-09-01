// Link resolution for the service docs modal.
//
// Service docs are no longer one file per service: a landing page at
// docs/services/<key>.md links down into docs/services/<key>/operations.md and
// its hand-written siblings. The modal therefore has to resolve a Markdown
// href the way a browser would — against the directory of the doc it appears
// in — instead of reading the filename stem and hoping it names a service.
//
// Kept out of the component so it can be tested without rendering one.

/** Service name → console route. Matches docs/services/<name>.md stems. */
export const SERVICE_ROUTES: Record<string, string> = {
  s3: "/s3",
  sqs: "/sqs",
  dynamodb: "/dynamodb",
  sns: "/sns",
  ses: "/ses",
  secretsmanager: "/secretsmanager",
  lambda: "/lambda",
  kinesis: "/kinesis",
  pipes: "/pipes",
  iam: "/iam",
  cloudformation: "/cloudformation",
  ec2: "/ec2",
  ecs: "/ecs",
  cognito: "/cognito",
  appsync: "/appsync",
  apigateway: "/apigateway",
  cloudfront: "/cloudfront",
  rds: "/rds",
  stepfunctions: "/stepfunctions",
  waf: "/waf",
  shield: "/shield",
  kms: "/kms",
  ssm: "/ssm",
  sts: "/sts",
  cloudwatch: "/cloudwatch",
  "cloudwatch-logs": "/cloudwatch",
  appregistry: "/applications",
}

/**
 * Where a link in a service doc goes.
 *
 * - `route`  — another service's console page, opened with #docs so its own
 *   modal comes up. This is the long-standing behaviour for `lambda.md`-style
 *   cross-service links and it is worth keeping: the reader almost always
 *   wants that service's resources, not just its prose.
 * - `doc`    — a doc the modal can load in place: a sub-page, or a service
 *   landing page with no console route of its own.
 * - `external` — everything else, opened in a new tab.
 */
export type DocLink =
  | { kind: "route"; href: string }
  | { kind: "doc"; path: string; label: string }
  | { kind: "external" }

/** The docs path a modal starts on for a given service key. */
export function landingPath(service: string): string {
  return `services/${service}.md`
}

/**
 * Resolve `href`, written inside the doc at `from`, to a docs-root-relative
 * path — the same resolution a browser does, so `../s3.md` from
 * `services/s3/operations.md` lands on `services/s3.md`.
 *
 * Returns null for an absolute URL, an anchor-only link, or a path that
 * escapes the docs root.
 */
export function resolveDocPath(from: string, href: string): string | null {
  if (!href || /^[a-z][a-z0-9+.-]*:/i.test(href) || href.startsWith("#") || href.startsWith("//")) {
    return null
  }
  const [target] = href.split("#")
  if (!target) return null

  const segments = target.startsWith("/")
    ? target.replace(/^\/+/, "").split("/")
    : [...from.split("/").slice(0, -1), ...target.split("/")]

  const stack: string[] = []
  for (const segment of segments) {
    if (segment === "" || segment === ".") continue
    if (segment === "..") {
      if (stack.length === 0) return null
      stack.pop()
      continue
    }
    stack.push(segment)
  }
  return stack.length > 0 ? stack.join("/") : null
}

/** Human label for a resolved service doc path, used in the modal breadcrumb. */
export function docTitle(path: string, displayName?: string): string {
  const rest = path.replace(/^services\//, "").replace(/\.md$/i, "")
  const [service, page] = rest.split("/")
  const name = displayName || service
  return page ? `${name} ${page}` : name
}

/**
 * Classify a link written in the doc at `from`.
 *
 * A `.md` link that resolves under `services/` is something the modal can
 * show; a service landing page that has a console route is handed to the app
 * instead. Anything else is a real outbound link.
 */
export function classifyDocLink(from: string, href: string): DocLink {
  const path = resolveDocPath(from, href)
  if (!path || !path.startsWith("services/") || !/\.md$/i.test(path)) {
    return { kind: "external" }
  }
  const rest = path.slice("services/".length)
  if (!rest.includes("/")) {
    const route = SERVICE_ROUTES[rest.replace(/\.md$/i, "")]
    if (route) return { kind: "route", href: `${route}#docs` }
  }
  return { kind: "doc", path, label: docTitle(path) }
}
