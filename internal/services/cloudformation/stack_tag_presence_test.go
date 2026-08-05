package cloudformation

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/state"
	"go.uber.org/zap"
)

func TestTagsParameterPresent_QueryOmittedVsExplicitEmpty(t *testing.T) {
	tests := []struct {
		name string
		form url.Values
		want bool
	}{
		{name: "omitted", form: url.Values{}, want: false},
		{name: "explicit empty", form: url.Values{"Tags": {""}}, want: true},
		{name: "member", form: url.Values{"Tags.member.1.Key": {"stage"}, "Tags.member.1.Value": {"test"}}, want: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("POST", "/", nil)
			req.Form = tc.form
			if got := tagsParameterPresent(req); got != tc.want {
				t.Fatalf("tagsParameterPresent = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestUpdateStack_QueryTagPresenceControlsClearing(t *testing.T) {
	for _, tc := range []struct {
		name string
		form url.Values
		want int
	}{
		{name: "omitted preserves", form: url.Values{"StackName": {"stack"}}, want: 1},
		{name: "explicit empty clears", form: url.Values{"StackName": {"stack"}, "Tags": {""}}, want: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newStackTagTestHandler(t)
			req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(tc.form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			h.UpdateStack(httptest.NewRecorder(), req)
			stack, _ := h.store.getStack(context.Background(), "stack")
			if len(stack.Tags) != tc.want {
				t.Fatalf("stack tags = %#v, want count %d", stack.Tags, tc.want)
			}
		})
	}
}

func TestUpdateStack_TypedTagPresenceControlsClearing(t *testing.T) {
	for _, tc := range []struct {
		name string
		tags *[]cfnTagMember
		want int
	}{
		{name: "omitted preserves", tags: nil, want: 1},
		{name: "explicit empty clears", tags: &[]cfnTagMember{}, want: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newStackTagTestHandler(t)
			if _, aerr := h.updateStackTyped(context.Background(), &updateStackReq{StackName: "stack", Tags: tc.tags}); aerr != nil {
				t.Fatalf("UpdateStack: %v", aerr)
			}
			stack, _ := h.store.getStack(context.Background(), "stack")
			if len(stack.Tags) != tc.want {
				t.Fatalf("stack tags = %#v, want count %d", stack.Tags, tc.want)
			}
		})
	}
}

func TestExecuteChangeSet_PreservesStoredTagPresence(t *testing.T) {
	for _, tc := range []struct {
		name    string
		tagsSet bool
		tags    []Tag
		want    []Tag
	}{
		{name: "omitted preserves", want: []Tag{{Key: "stage", Value: "old"}}},
		{name: "explicit empty clears", tagsSet: true, want: nil},
		{name: "legacy populated applies", tags: []Tag{{Key: "stage", Value: "legacy"}}, want: []Tag{{Key: "stage", Value: "legacy"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newStackTagTestHandler(t)
			cs := &ChangeSet{
				ChangeSetName: "change", ChangeSetID: "change-id", StackName: "stack", StackID: "stack-id",
				TemplateBody: `{"Resources":{}}`, ChangeSetType: "UPDATE", Tags: tc.tags, TagsSet: tc.tagsSet,
				Status: ChangeSetStatusCreateComplete, ExecutionStatus: ExecStatusAvailable, CreatedAt: time.Unix(0, 0),
			}
			if err := h.store.putChangeSet(context.Background(), cs); err != nil {
				t.Fatal(err)
			}
			if _, aerr := h.executeChangeSetTyped(context.Background(), &executeChangeSetReq{StackName: "stack", ChangeSetName: "change"}); aerr != nil {
				t.Fatalf("ExecuteChangeSet: %v", aerr)
			}
			stack, _ := h.store.getStack(context.Background(), "stack")
			if !reflect.DeepEqual(stack.Tags, tc.want) {
				t.Fatalf("stack tags = %#v, want %#v", stack.Tags, tc.want)
			}
		})
	}
}

func TestExecuteChangeSet_QueryAppliesLegacyStoredTags(t *testing.T) {
	h := newStackTagTestHandler(t)
	cs := &ChangeSet{
		ChangeSetName: "change", ChangeSetID: "change-id", StackName: "stack", StackID: "stack-id",
		TemplateBody: `{"Resources":{}}`, ChangeSetType: "UPDATE",
		Tags:   []Tag{{Key: "stage", Value: "legacy"}},
		Status: ChangeSetStatusCreateComplete, ExecutionStatus: ExecStatusAvailable, CreatedAt: time.Unix(0, 0),
	}
	if err := h.store.putChangeSet(context.Background(), cs); err != nil {
		t.Fatal(err)
	}
	form := url.Values{"StackName": {"stack"}, "ChangeSetName": {"change"}}
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	h.ExecuteChangeSet(httptest.NewRecorder(), req)
	stack, _ := h.store.getStack(context.Background(), "stack")
	want := []Tag{{Key: "stage", Value: "legacy"}}
	if !reflect.DeepEqual(stack.Tags, want) {
		t.Fatalf("stack tags = %#v, want %#v", stack.Tags, want)
	}
}

func newStackTagTestHandler(t *testing.T) *Handler {
	t.Helper()
	svc := New(&config.Config{Region: "us-east-1", AccountID: "000000000000"}, state.NewMemoryStore(), zap.NewNop(), clock.NewMock())
	stack := &Stack{
		StackName: "stack", StackID: "stack-id", Region: "us-east-1", TemplateBody: `{"Resources":{}}`,
		Status: StatusCreateComplete, CreatedAt: time.Unix(0, 0), Tags: []Tag{{Key: "stage", Value: "old"}},
	}
	if err := svc.handler.store.putStack(context.Background(), stack); err != nil {
		t.Fatal(err)
	}
	return svc.handler
}

func TestUpdateStackReqTagsPresence_TypedOmittedVsExplicitEmpty(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		want bool
	}{
		{name: "omitted", body: `{}`, want: false},
		{name: "explicit empty", body: `{"Tags":[]}`, want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var req updateStackReq
			if err := json.Unmarshal([]byte(tc.body), &req); err != nil {
				t.Fatal(err)
			}
			if got := req.Tags != nil; got != tc.want {
				t.Fatalf("Tags presence = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestApplyStackTags_OmittedPreservesAndExplicitEmptyClears(t *testing.T) {
	stack := &Stack{Tags: []Tag{{Key: "stage", Value: "old"}}}
	applyStackTags(stack, nil, false)
	if !reflect.DeepEqual(stack.Tags, []Tag{{Key: "stage", Value: "old"}}) {
		t.Fatalf("omitted tags = %#v, want preserved", stack.Tags)
	}
	applyStackTags(stack, nil, true)
	if len(stack.Tags) != 0 {
		t.Fatalf("explicit empty tags = %#v, want cleared", stack.Tags)
	}
}

func TestChangeSetTagsPresenceSurvivesStorageShape(t *testing.T) {
	for _, tagsSet := range []bool{false, true} {
		original := ChangeSet{ChangeSetName: "change", TagsSet: tagsSet}
		data, err := json.Marshal(original)
		if err != nil {
			t.Fatal(err)
		}
		var restored ChangeSet
		if err := json.Unmarshal(data, &restored); err != nil {
			t.Fatal(err)
		}
		stack := &Stack{Tags: []Tag{{Key: "stage", Value: "old"}}}
		applyStackTags(stack, restored.Tags, restored.TagsSet)
		if tagsSet && len(stack.Tags) != 0 {
			t.Fatalf("explicit-empty change set tags = %#v, want cleared", stack.Tags)
		}
		if !tagsSet && len(stack.Tags) != 1 {
			t.Fatalf("omitted change set tags = %#v, want preserved", stack.Tags)
		}
	}
}
