package lambda

import (
	"context"
	"testing"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/serviceutil"
	"github.com/Neaox/overcast/internal/state"
)

func TestMalformedPolicyStateIsIsolatedAndRepairable(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemoryStore()
	ls := newLambdaStore(store, "us-east-1", clock.New())
	fn := &Function{Name: "policy-corrupt-fn", ARN: "arn:aws:lambda:us-east-1:000000000000:function:policy-corrupt-fn"}
	if aerr := ls.putFunction(ctx, fn); aerr != nil {
		t.Fatalf("put function: %v", aerr)
	}
	key := serviceutil.RegionKey("us-east-1", policyStoreKey(fn.Name, ""))
	if err := store.Set(ctx, nsFunctionPolicies, key, "{not-json"); err != nil {
		t.Fatalf("seed corrupt policy: %v", err)
	}
	if got, aerr := ls.getFunction(ctx, fn.Name); aerr != nil || got == nil {
		t.Fatalf("function read affected by corrupt policy: got=%v err=%v", got, aerr)
	}
	if _, found, aerr := ls.getFunctionPolicy(ctx, fn.Name, ""); aerr != nil || found {
		t.Fatalf("corrupt policy should be isolated as absent: found=%v err=%v", found, aerr)
	}
	if aerr := ls.mutateFunctionPolicy(ctx, fn.Name, "", func(policy *functionPolicy, _ bool) (bool, *protocol.AWSError) {
		policy.RevisionID = "repaired"
		return false, nil
	}); aerr != nil {
		t.Fatalf("repair policy: %v", aerr)
	}
	if policy, found, aerr := ls.getFunctionPolicy(ctx, fn.Name, ""); aerr != nil || !found || policy.RevisionID != "repaired" {
		t.Fatalf("repaired policy = %+v, found=%v err=%v", policy, found, aerr)
	}
}
