package lambda_test

// telemetry_init_phase_test.go — what an extension subscribed to the Telemetry
// API sees of the INIT phase.
//
// The awkward part of these three records is *when* they happen.
// platform.initStart is published before the extensions are even started, and
// the pair that closes the phase can beat an extension that is still
// registering — yet an extension's whole job is to be told about the phase it
// was started in, and AWS delivers them. So the records are held per
// environment and replayed to a subscription made during INIT.
//
// The function here logs in Text format on purpose: the CloudWatch copy of
// these records exists only under LogFormat: JSON, and a subscriber's copy is
// not affected by the log format at all. AWS: "Configuring the format of the
// system logs Lambda sends to CloudWatch doesn't affect Lambda Telemetry API
// behavior."

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Neaox/overcast/tests/helpers"
)

func TestInvoke_extensionSubscribedToTelemetryReceivesTheInitPhaseRecords(t *testing.T) {
	skipIfNoDocker(t)
	requireLambdaInit(t)

	image := buildLambdaImage(t, `FROM public.ecr.aws/lambda/nodejs:20
COPY app.js /var/task/app.js
COPY ext.js /opt/extension-src/ext.js
COPY collector.sh /opt/extensions/collector
RUN chmod 0755 /opt/extensions/collector
CMD ["app.handler"]
`, map[string]string{
		"app.js": `
exports.handler = async () => {
  console.log("handler ran marker-handler");
  return { ok: true };
};
`,
		"collector.sh": "#!/bin/sh\nexec /var/lang/bin/node /opt/extension-src/ext.js\n",
		// A minimal Telemetry API consumer: register, stand up the destination
		// the records will be POSTed to, subscribe to the platform stream, then
		// long-poll for events the way AWS requires so the environment is
		// reported ready. Everything it is sent it prints, one line per record,
		// which is how the test gets to see it — an extension's stdout is an
		// `extension` record and reaches CloudWatch like any other output.
		"ext.js": `
const http = require("http");
const [host, port] = process.env.AWS_LAMBDA_RUNTIME_API.split(":");

function call(method, path, headers, body) {
  return new Promise((resolve, reject) => {
    const req = http.request({ host, port, method, path, headers }, res => {
      res.resume();
      res.on("end", () => resolve(res.headers));
    });
    req.on("error", reject);
    if (body) req.write(body);
    req.end();
  });
}

(async () => {
  const headers = await call(
    "POST",
    "/2020-01-01/extension/register",
    { "Lambda-Extension-Name": "collector", "Content-Type": "application/json" },
    JSON.stringify({ events: ["INVOKE", "SHUTDOWN"] }),
  );
  const id = headers["lambda-extension-identifier"];

  const server = http.createServer((req, res) => {
    let body = "";
    req.on("data", chunk => { body += chunk; });
    req.on("end", () => {
      res.writeHead(200);
      res.end();
      try {
        for (const event of JSON.parse(body)) {
          console.log("TELEMETRY " + event.type + " " + JSON.stringify(event.record));
        }
      } catch (err) {
        console.log("TELEMETRY undecodable " + err);
      }
    });
  });
  await new Promise(resolve => server.listen(9999, "0.0.0.0", resolve));

  await call(
    "PUT",
    "/2020-08-15/logs",
    { "Lambda-Extension-Identifier": id, "Content-Type": "application/json" },
    JSON.stringify({
      types: ["platform"],
      buffering: { timeoutMs: 25, maxBytes: 262144, maxItems: 1000 },
      destination: { protocol: "HTTP", URI: "http://127.0.0.1:9999" },
    }),
  );
  console.log("collector subscribed marker-subscribed");

  for (;;) {
    await call("GET", "/2020-01-01/extension/event/next", { "Lambda-Extension-Identifier": id });
  }
})().catch(err => console.error("collector failed: " + err));
`,
	})

	srv := helpers.NewTestServer(t, helpers.WithLambdaDocker())
	createImageFunction(t, srv, "telemetry-init-fn", image, nil)
	waitForFunctionActive(t, srv, "telemetry-init-fn")

	tail := string(invokeForLogTail(t, srv, "telemetry-init-fn", []byte("{}")))
	if !strings.Contains(tail, "handler ran marker-handler") {
		t.Fatalf("the function did not run:\n%s", tail)
	}
	// A platform record never enters a request's log buffer, so none of them is
	// in the tail. The extension's own printed echo of one is a different
	// thing: that is container output written while the invocation was in
	// flight, and it belongs to the invocation exactly as it does on AWS.
	for _, line := range strings.Split(tail, "\n") {
		if strings.HasPrefix(line, `{"time":"`) && strings.Contains(line, `"type":"platform.`) {
			t.Errorf("a platform record leaked into X-Amz-Log-Result: %q\n%s", line, tail)
		}
	}

	messages := logEventsFor(t, srv, "/aws/lambda/telemetry-init-fn", func(m []string) bool {
		return indexOfLine(m, "TELEMETRY platform.initReport") >= 0
	})

	for _, eventType := range []string{"platform.initStart", "platform.initRuntimeDone", "platform.initReport"} {
		if indexOfLine(messages, "TELEMETRY "+eventType+" ") < 0 {
			t.Errorf("the extension never received a %s record:\n%s", eventType, strings.Join(messages, "\n"))
		}
	}

	// The records the extension was handed are the AWS shapes, not strings of
	// them: it parsed each event's `record` as an object to print it.
	if record := telemetryRecord(t, messages, "platform.initStart"); record != nil {
		if record["initializationType"] != "on-demand" || record["phase"] != "init" {
			t.Errorf("the initStart record the extension received = %#v", record)
		}
		if record["functionName"] != "telemetry-init-fn" || record["functionVersion"] != "$LATEST" {
			t.Errorf("the initStart record the extension received = %#v", record)
		}
	}
	if record := telemetryRecord(t, messages, "platform.initReport"); record != nil {
		if record["status"] != "success" {
			t.Errorf("the initReport record the extension received = %#v", record)
		}
		metrics, _ := record["metrics"].(map[string]any)
		duration, _ := metrics["durationMs"].(float64)
		if duration <= 0 {
			t.Errorf("the initReport record carries durationMs %#v, want a real measurement", metrics["durationMs"])
		}
	}

	// This function logs in Text format, so none of it is in CloudWatch as a
	// platform record — only the extension's own echo of what it was sent.
	for _, msg := range messages {
		if strings.HasPrefix(msg, `{"time":"`) && strings.Contains(msg, `"type":"platform.init`) {
			t.Errorf("Text log format wrote an init-phase record to CloudWatch: %q", msg)
		}
	}
}

// telemetryRecord decodes the `record` object out of the extension's echo of
// one Telemetry API event.
func telemetryRecord(t *testing.T, messages []string, eventType string) map[string]any {
	t.Helper()
	prefix := "TELEMETRY " + eventType + " "
	for _, msg := range messages {
		at := strings.Index(msg, prefix)
		if at < 0 {
			continue
		}
		var record map[string]any
		if err := json.Unmarshal([]byte(msg[at+len(prefix):]), &record); err != nil {
			t.Errorf("the %s record the extension printed is not an object: %q", eventType, msg)
			return nil
		}
		return record
	}
	return nil
}
