/**
 * client.ts — construct an AWS SDK v3 client and a command from the IR.
 *
 * The scenario file carries `sdkId`, `endpointPrefix` and `signingName` and
 * nothing SDK-specific, so each backend derives what it needs. For this one
 * (compat/model/README.md § Naming):
 *
 *   package  @aws-sdk/client-<kebab(sdkId)>
 *   client   new <PascalCase(sdkId)>Client(config)
 *   command  new <Op>Command(params)
 *
 * which is exactly what a hand-written group in src/groups/ writes by hand.
 * The client configuration is `clientConfig()` from lib/clients.ts — the same
 * endpoint, credentials, region and HTTP/1.1 handler every other group uses,
 * not a second copy of it. The endpoint override is the only deviation from
 * production configuration; there is no Overcast-specific code path here.
 */

import { clientConfig } from "../clients.ts";
import type { TestContext } from "../harness.ts";
import type { ClientSpec } from "./ir.ts";

/** Make one call. The interpreter's only view of the SDK. */
export type Sender = (
  op: string,
  params: Record<string, unknown>,
) => Promise<unknown>;

/** Where the clients point. */
export type ClientTarget = Pick<TestContext, "endpoint" | "region">;

interface ServiceClient {
  send(command: object): Promise<unknown>;
}

type ClientConstructor = new (config: unknown) => ServiceClient;
type CommandConstructor = new (input: Record<string, unknown>) => object;

/**
 * The package name for an sdkId: lowercase, spaces to hyphens.
 *
 * Deliberately *not* a camel-case split: the sdkId already carries the word
 * boundaries as spaces ("Cognito Identity Provider", "CloudWatch Logs",
 * "Secrets Manager"), and splitting on capitals would turn "DynamoDB" into
 * "dynamo-db" and "ApiGatewayV2" into "api-gateway-v2", neither of which is a
 * package. Every client lib/clients.ts imports round-trips through this rule.
 */
export function packageName(sdkId: string): string {
  return `@aws-sdk/client-${sdkId.trim().toLowerCase().replace(/[\s_]+/g, "-")}`;
}

/** The client class name: the sdkId with its separators removed, + "Client". */
export function clientClassName(sdkId: string): string {
  return `${sdkId.replace(/[^A-Za-z0-9]/g, "")}Client`;
}

/** The command class name for an operation. */
export function commandClassName(op: string): string {
  return `${op}Command`;
}

/**
 * One dynamic import per service, cached by package name. The promise itself
 * is cached, so two groups of the same service that start together share one
 * import rather than racing.
 */
const modules = new Map<string, Promise<Record<string, unknown>>>();

/** One client per (service, endpoint, region). Clients are stateless. */
const clients = new Map<string, ServiceClient>();

async function loadModule(sdkId: string): Promise<Record<string, unknown>> {
  const pkg = packageName(sdkId);
  let pending = modules.get(pkg);
  if (pending === undefined) {
    pending = importModule(pkg, sdkId);
    modules.set(pkg, pending);
  }
  return pending;
}

async function importModule(
  pkg: string,
  sdkId: string,
): Promise<Record<string, unknown>> {
  // The JSON boundary's twin: a specifier only known at runtime. The result
  // is taken as `unknown` rather than the `any` a dynamic import is typed as,
  // and every export is looked up with a runtime check below.
  let mod: unknown;
  try {
    mod = await import(pkg);
  } catch (err) {
    throw new Error(
      `scenario client for sdkId ${JSON.stringify(sdkId)} needs ${pkg}, ` +
        `which is not installed — add it to compat/suites/node-js-sdk/` +
        `package.json at the suite's pinned SDK version (${String(err)})`,
    );
  }
  if (typeof mod !== "object" || mod === null) {
    throw new Error(`${pkg} did not resolve to a module namespace`);
  }
  return mod as Record<string, unknown>;
}

function requireConstructor(
  mod: Record<string, unknown>,
  name: string,
  pkg: string,
): CommandConstructor {
  const ctor = mod[name];
  if (typeof ctor !== "function") {
    throw new Error(`${pkg} exports no ${name}`);
  }
  return ctor as CommandConstructor;
}

async function getClient(
  spec: ClientSpec,
  target: ClientTarget,
): Promise<ServiceClient> {
  const pkg = packageName(spec.sdkId);
  const key = `${pkg}|${target.endpoint}|${target.region}`;
  const cached = clients.get(key);
  if (cached !== undefined) return cached;

  const mod = await loadModule(spec.sdkId);
  const name = clientClassName(spec.sdkId);
  const Ctor = requireConstructor(mod, name, pkg) as unknown as ClientConstructor;
  const client = new Ctor(clientConfig(target));
  clients.set(key, client);
  return client;
}

/**
 * A Sender that uses the AWS SDK exactly as production code would:
 * `client.send(new <Op>Command(params))`.
 */
export function makeSdkSender(spec: ClientSpec, target: ClientTarget): Sender {
  return async (op, params) => {
    const mod = await loadModule(spec.sdkId);
    const client = await getClient(spec, target);
    const Command = requireConstructor(
      mod,
      commandClassName(op),
      packageName(spec.sdkId),
    );
    return client.send(new Command(params));
  };
}
