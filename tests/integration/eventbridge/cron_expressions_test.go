package eventbridge_test

// cron_expressions_test.go — PutRule accepts every cron expression AWS accepts.
//
// A CDK stack reaches PutRule through AWS::Events::Rule, and a rule it refuses
// fails the deploy. Two failures came out of one such stack:
//
//   - `cron(*/5 * * * *)` — five fields, a Unix cron. AWS wants six and refuses
//     this too, so the emulator should refuse it, but the message has to say
//     what was wrong and what six fields look like.
//   - Everything with a name or a day specifier in it. `cron(15 10 ? * MON-FRI *)`
//     is valid AWS and was stored and then never fired, because the matcher
//     read the field character by character and "MON-FRI" matched no number.
//     A rule that never fires is worse than a rule that is refused.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/overcast-sh/overcast/tests/helpers"
)

func TestPutRule_acceptsEveryAWSCronForm(t *testing.T) {
	srv := helpers.NewTestServer(t, helpers.WithRegion("ap-southeast-2"), helpers.WithAccountID("000000000000"))

	for i, expr := range []string{
		"cron(*/5 * * * ? *)", // every five minutes
		"cron(0/5 * * * ? *)", // the same, written the other way
		"cron(0 3 * * ? *)",   // daily
		"cron(0 5 1 * ? *)",   // monthly
		"cron(15 10 ? * MON-FRI *)",
		"cron(0 18 ? * MON *)",
		"cron(0 8 1 JAN ? *)",
		"cron(0 0-6/2 * * ? *)", // a range with a step
		"cron(0 8 L * ? *)",     // last day of the month
		"cron(0 8 LW * ? *)",    // last weekday of the month
		"cron(0 8 15W * ? *)",   // nearest weekday to the 15th
		"cron(0 12 ? * 6#3 *)",  // third Friday
		"cron(0 12 ? * FRI#3 *)",
		"cron(0 12 ? * 6L *)", // last Friday
		"rate(5 minutes)",
	} {
		resp := ebCall(t, srv, "PutRule", map[string]any{
			"Name":               fmt.Sprintf("cron-form-%d", i),
			"ScheduleExpression": expr,
			"State":              "ENABLED",
		})
		body := decodeRuleResponse(t, resp)
		if resp.StatusCode != http.StatusOK {
			t.Errorf("PutRule %q = %d: %v", expr, resp.StatusCode, body["message"])
		}
	}
}

func TestPutRule_refusesAFiveFieldCronAndSaysWhat(t *testing.T) {
	srv := helpers.NewTestServer(t, helpers.WithRegion("ap-southeast-2"), helpers.WithAccountID("000000000000"))

	// When: the Unix five-field form, which AWS also refuses.
	resp := ebCall(t, srv, "PutRule", map[string]any{
		"Name":               "five-field",
		"ScheduleExpression": "cron(*/5 * * * *)",
		"State":              "ENABLED",
	})
	body := decodeRuleResponse(t, resp)

	// Then: it is refused, and the message names the expression, the count and
	// the six-field form — everything needed to fix the template without
	// reading the emulator's source.
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("PutRule = %d, want 400", resp.StatusCode)
	}
	message, _ := body["message"].(string)
	for _, want := range []string{"cron(*/5 * * * *)", "5 fields", "cron(*/5 * * * ? *)"} {
		if !strings.Contains(message, want) {
			t.Errorf("message = %q, want it to mention %q", message, want)
		}
	}
}

func decodeRuleResponse(t *testing.T, resp *http.Response) map[string]any {
	t.Helper()
	defer resp.Body.Close()
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode PutRule response: %v", err)
	}
	return body
}
