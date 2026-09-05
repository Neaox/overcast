package cloudformation

// provisioner_properties_test.go — the shared property-forwarding helpers
// (#540). These are the mechanism every handler that adopts them relies on, so
// the cases that would silently corrupt a request — a user-supplied map key
// lower-cased, an absent property forwarded as null, a tag list read as a tag
// map — are pinned here rather than left to each handler's own coverage.

import (
	"context"
	"reflect"
	"testing"
)

func TestForwardProperties_convertsNamesAndNestedKeys(t *testing.T) {
	props := map[string]any{
		"ScalingConfig": map[string]any{"MinSize": 1.0, "MaxSize": 5.0},
		"Taints":        []any{map[string]any{"Key": "dedicated", "Effect": "NO_SCHEDULE"}},
		"AmiType":       "AL2023_x86_64_STANDARD",
		"NotRequested":  "ignored",
	}
	body := map[string]any{}

	forwardProperties(props, body, "ScalingConfig", "Taints", "AmiType")

	want := map[string]any{
		"scalingConfig": map[string]any{"minSize": 1.0, "maxSize": 5.0},
		"taints":        []any{map[string]any{"key": "dedicated", "effect": "NO_SCHEDULE"}},
		"amiType":       "AL2023_x86_64_STANDARD",
	}
	if !reflect.DeepEqual(body, want) {
		t.Errorf("forwardProperties built %v, want %v", body, want)
	}
}

func TestForwardProperties_skipsAbsentAndNull(t *testing.T) {
	// An absent property must not become an explicit null: a service that
	// distinguishes "unset" from "set to nothing" would see the wrong one.
	body := map[string]any{}
	forwardProperties(map[string]any{"Version": nil}, body, "Version", "AmiType")
	if len(body) != 0 {
		t.Errorf("forwardProperties forwarded %v, want nothing for an absent and a null property", body)
	}
}

func TestForwardPropertiesAs_leavesValuesUntouched(t *testing.T) {
	// Labels are the user's own keys. Converting them the way a modelled
	// member name is converted would rewrite the data.
	props := map[string]any{"Labels": map[string]any{"Environment": "prod", "team": "platform"}}
	body := map[string]any{}

	forwardPropertiesAs(props, body, map[string]string{"Labels": "labels"})

	want := map[string]any{"labels": map[string]any{"Environment": "prod", "team": "platform"}}
	if !reflect.DeepEqual(body, want) {
		t.Errorf("forwardPropertiesAs built %v, want %v", body, want)
	}
}

func TestUnconsumedProperties(t *testing.T) {
	props := map[string]any{"Name": "x", "RoleArn": "y", "UpgradePolicy": map[string]any{}, "Force": true}

	got := unconsumedProperties(props, "Name", "RoleArn")

	want := []string{"Force", "UpgradePolicy"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("unconsumedProperties = %v, want %v (sorted)", got, want)
	}
	if left := unconsumedProperties(props, "Name", "RoleArn", "UpgradePolicy", "Force"); len(left) != 0 {
		t.Errorf("a fully-covering handler left %v, want nothing", left)
	}
	if left := unconsumedProperties(nil, "Name"); len(left) != 0 {
		t.Errorf("a resource with no properties left %v, want nothing", left)
	}
}

func TestNoteUnconsumedProperties_reachesTheResourcesReason(t *testing.T) {
	ctx, limitations := withLimitationCollector(context.Background())

	noteUnconsumedProperties(ctx, "AWS::EKS::Cluster",
		map[string]any{"Name": "x", "OutpostConfig": map[string]any{}, "Force": true}, "Name")

	const want = "Overcast: AWS::EKS::Cluster properties not applied: Force, OutpostConfig. They were accepted and ignored."
	if got := limitations.reason(); got != want {
		t.Errorf("collected reason = %q, want %q", got, want)
	}
}

func TestNoteUnconsumedProperties_silentWhenFullyCovered(t *testing.T) {
	ctx, limitations := withLimitationCollector(context.Background())

	noteUnconsumedProperties(ctx, "AWS::EKS::Cluster", map[string]any{"Name": "x"}, "Name", "RoleArn")

	if got := limitations.reason(); got != "" {
		t.Errorf("collected reason = %q, want none", got)
	}
}

func TestCFNTagMap_readsBothTagShapes(t *testing.T) {
	// The [{Key,Value}] list most resources use...
	list := cfnTagMap([]any{
		map[string]any{"Key": "env", "Value": "prod"},
		map[string]any{"Key": "", "Value": "dropped"},
		map[string]any{"Key": "count", "Value": 3.0},
	})
	wantList := map[string]string{"env": "prod", "count": "3"}
	if !reflect.DeepEqual(list, wantList) {
		t.Errorf("cfnTagMap(list) = %v, want %v", list, wantList)
	}

	// ...and the object AWS::EKS::* and AWS::MSK::Cluster use instead.
	object := cfnTagMap(map[string]any{"env": "prod", "Owner": "platform"})
	wantObject := map[string]string{"env": "prod", "Owner": "platform"}
	if !reflect.DeepEqual(object, wantObject) {
		t.Errorf("cfnTagMap(object) = %v, want %v", object, wantObject)
	}

	if got := cfnTagMap(nil); got != nil {
		t.Errorf("cfnTagMap(nil) = %v, want nil", got)
	}
	if got := cfnTagMap("not tags"); got != nil {
		t.Errorf("cfnTagMap(string) = %v, want nil", got)
	}
}
