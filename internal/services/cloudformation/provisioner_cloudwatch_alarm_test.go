package cloudformation

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"
)

// alarmTagRouter records what the alarm handler dispatched, so a test can say
// which calls an update made rather than only what state it left behind.
type alarmTagRouter struct {
	actions []string
	forms   []url.Values
	failAt  map[string]int
}

func (r *alarmTagRouter) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	if target := req.Header.Get("X-Amz-Target"); target != "" {
		r.actions = append(r.actions, strings.TrimPrefix(target, "GraniteServiceVersion20100801."))
		r.forms = append(r.forms, nil)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
		return
	}

	_ = req.ParseForm()
	action := req.FormValue("Action")
	r.actions = append(r.actions, action)
	r.forms = append(r.forms, req.PostForm)
	if status := r.failAt[action]; status != 0 {
		http.Error(w, "injected failure", status)
		return
	}
	w.Header().Set("Content-Type", "text/xml")
	_, _ = w.Write([]byte(`<` + action + `Response/>`))
}

// form returns the parameters of the first call to action.
func (r *alarmTagRouter) form(action string) url.Values {
	for i, got := range r.actions {
		if got == action {
			return r.forms[i]
		}
	}
	return nil
}

func alarmTagProps(tags ...[2]string) map[string]any {
	props := map[string]any{
		"AlarmName":          "orders-errors",
		"MetricName":         "Errors",
		"Namespace":          "AWS/Lambda",
		"EvaluationPeriods":  float64(1),
		"Threshold":          float64(1),
		"ComparisonOperator": "GreaterThanOrEqualToThreshold",
	}
	if tags != nil {
		list := make([]any, len(tags))
		for i, tag := range tags {
			list[i] = map[string]any{"Key": tag[0], "Value": tag[1]}
		}
		props["Tags"] = list
	}
	return props
}

func updateAlarm(t *testing.T, router http.Handler, props, oldProps map[string]any) error {
	t.Helper()
	_, _, err := (&cloudwatchAlarmHandler{}).Update(context.Background(), router, nil,
		"orders-errors", props, oldProps,
		&resolveContext{Region: "us-east-1", AccountID: "000000000000"})
	return err
}

func TestCloudWatchAlarmUpdate_tagChangesReachTagResource(t *testing.T) {
	// Given: an update that edits one tag value, adds a key and drops another
	router := &alarmTagRouter{}
	err := updateAlarm(t, router,
		alarmTagProps([2]string{"env", "prod"}, [2]string{"team", "payments"}),
		alarmTagProps([2]string{"env", "dev"}, [2]string{"owner", "platform"}))
	if err != nil {
		t.Fatalf("Update: %v", err)
	}

	// Then: the tags go through TagResource/UntagResource, which is how the
	// change reaches an alarm at all — PutMetricAlarm ignores Tags on an update
	want := []string{"PutMetricAlarm", "TagResource", "UntagResource"}
	if !reflect.DeepEqual(router.actions, want) {
		t.Fatalf("calls = %v, want %v", router.actions, want)
	}

	// And: the added and edited keys are sent, the untouched one is not
	tagged := router.form("TagResource")
	gotTags := map[string]string{}
	for i := 1; ; i++ {
		key := tagged.Get(fmt.Sprintf("Tags.member.%d.Key", i))
		if key == "" {
			break
		}
		gotTags[key] = tagged.Get(fmt.Sprintf("Tags.member.%d.Value", i))
	}
	if wantTags := map[string]string{"env": "prod", "team": "payments"}; !reflect.DeepEqual(gotTags, wantTags) {
		t.Errorf("TagResource tags = %v, want %v", gotTags, wantTags)
	}
	if got := tagged.Get("ResourceARN"); got != "arn:aws:cloudwatch:us-east-1:000000000000:alarm:orders-errors" {
		t.Errorf("TagResource ResourceARN = %q", got)
	}

	// And: only the dropped key is untagged
	untagged := router.form("UntagResource")
	if got := untagged.Get("TagKeys.member.1"); got != "owner" {
		t.Errorf("UntagResource TagKeys.member.1 = %q, want owner", got)
	}
	if got := untagged.Get("TagKeys.member.2"); got != "" {
		t.Errorf("UntagResource sent a second key %q; only owner was removed", got)
	}
}

func TestCloudWatchAlarmUpdate_unchangedTagsIssueNoTagCalls(t *testing.T) {
	// Given: an update that leaves the tags exactly as they were
	router := &alarmTagRouter{}
	tags := [][2]string{{"env", "prod"}, {"owner", "platform"}}
	props := alarmTagProps(tags...)
	props["Threshold"] = float64(5)
	if err := updateAlarm(t, router, props, alarmTagProps(tags...)); err != nil {
		t.Fatalf("Update: %v", err)
	}

	// Then: nothing is tagged or untagged. The diff is what decides, so an
	// unrelated property change does not rewrite tags that did not move.
	if want := []string{"PutMetricAlarm"}; !reflect.DeepEqual(router.actions, want) {
		t.Errorf("calls = %v, want %v", router.actions, want)
	}
}

func TestCloudWatchAlarmUpdate_tagsDroppedFromTemplateAreRemoved(t *testing.T) {
	// Given: an update whose template no longer carries Tags at all
	router := &alarmTagRouter{}
	if err := updateAlarm(t, router, alarmTagProps(), alarmTagProps([2]string{"env", "dev"})); err != nil {
		t.Fatalf("Update: %v", err)
	}

	// Then: the tag is removed rather than left behind from the old template
	if want := []string{"PutMetricAlarm", "UntagResource"}; !reflect.DeepEqual(router.actions, want) {
		t.Fatalf("calls = %v, want %v", router.actions, want)
	}
	if got := router.form("UntagResource").Get("TagKeys.member.1"); got != "env" {
		t.Errorf("UntagResource TagKeys.member.1 = %q, want env", got)
	}
}

func TestCloudWatchAlarmUpdate_tagFailureIsDirtyNotAReplacement(t *testing.T) {
	// Given: TagResource fails after PutMetricAlarm has already applied
	router := &alarmTagRouter{failAt: map[string]int{"TagResource": http.StatusInternalServerError}}
	err := updateAlarm(t, router,
		alarmTagProps([2]string{"env", "prod"}),
		alarmTagProps([2]string{"env", "dev"}))

	// Then: the update fails terminally and is marked dirty. Falling through to
	// replacement — the default for a bare error — would answer a half-applied
	// update by building a second alarm, and rollback would report a restore
	// that did not happen.
	if err == nil {
		t.Fatal("Update returned nil error")
	}
	if errors.Is(err, errReplacementRequired) {
		t.Fatal("tag failure asked for replacement")
	}
	var terminal updateFailure
	if !errors.As(err, &terminal) {
		t.Fatalf("error type = %T, want terminal updateFailure", err)
	}
	if !isDirtyUpdateFailure(err) {
		t.Error("tag failure after PutMetricAlarm was not marked dirty")
	}
}
