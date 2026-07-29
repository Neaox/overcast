// Fetches a secret the same way the AWS Parameters and Secrets Lambda
// Extension is meant to be used: an HTTP GET to the extension's local server,
// authenticated with the function's session token.
//
// Every step is logged with a timestamp so a hang shows up as a gap rather
// than as a bare timeout.

const PORT = process.env.PARAMETERS_SECRETS_EXTENSION_HTTP_PORT ?? "2773"
const SECRET_NAME = process.env.SECRET_NAME

function log(step, extra = {}) {
  console.log(JSON.stringify({ t: new Date().toISOString(), step, ...extra }))
}

export const handler = async (event) => {
  log("handler:start", { event })

  const url = `http://localhost:${PORT}/secretsmanager/get?secretId=${encodeURIComponent(SECRET_NAME)}`
  log("extension:request", { url })

  const started = Date.now()
  const res = await fetch(url, {
    headers: { "X-Aws-Parameters-Secrets-Token": process.env.AWS_SESSION_TOKEN },
  })
  log("extension:headers", { status: res.status, ms: Date.now() - started })

  const body = await res.text()
  log("extension:body", { bytes: body.length, ms: Date.now() - started })

  const parsed = JSON.parse(body)
  log("secret:ok", { arn: parsed.ARN, secretBytes: parsed.SecretString?.length })

  // What a real handler does next: resolve and call an external API. Overcast
  // points containers at its own resolver, so this is the first thing after the
  // secret that depends on DNS working for names Overcast does not own.
  const dns = await import("node:dns/promises")
  for (const host of ["oauth2.googleapis.com", "example.com"]) {
    const t0 = Date.now()
    try {
      const addrs = await dns.lookup(host, { all: true })
      log("dns:ok", { host, ms: Date.now() - t0, addrs: addrs.map((a) => a.address) })
    } catch (err) {
      log("dns:fail", { host, ms: Date.now() - t0, code: err.code, message: err.message })
    }
  }

  const t1 = Date.now()
  try {
    const ext = await fetch("https://oauth2.googleapis.com/token", {
      method: "POST",
      signal: AbortSignal.timeout(20000),
    })
    log("egress:ok", { status: ext.status, ms: Date.now() - t1 })
  } catch (err) {
    log("egress:fail", { ms: Date.now() - t1, name: err.name, message: err.message })
  }

  log("handler:done", { ms: Date.now() - started })
  return { ok: true, ms: Date.now() - started, secretBytes: parsed.SecretString?.length }
}
