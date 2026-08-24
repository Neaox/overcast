package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"sort"
	"strings"
)

type modelInventory struct {
	Revision   string               `json:"revision"`
	Operations []inventoryOperation `json:"operations"`
	Collisions []inventoryCollision `json:"collisions"`
	Coverage   fallbackCoverage     `json:"fallbackCoverage"`
}

type inventoryOperation struct {
	Service      string   `json:"service"`
	ServiceShape string   `json:"serviceShape"`
	SDKID        string   `json:"sdkId"`
	APIVersion   string   `json:"apiVersion"`
	Name         string   `json:"name"`
	Protocol     string   `json:"protocol"`
	Protocols    []string `json:"protocols"`
	TargetPrefix string   `json:"targetPrefix,omitempty"`
	SigningName  string   `json:"signingName,omitempty"`
	HTTPMethod   string   `json:"httpMethod,omitempty"`
	URI          string   `json:"uri,omitempty"`
}

type inventoryCollision struct {
	Kind     string   `json:"kind"`
	Key      string   `json:"key"`
	Services []string `json:"services"`
}

type fallbackCoverage struct {
	NonS3Operations     int      `json:"nonS3Operations"`
	ClaimableOperations int      `json:"claimableOperations"`
	AmbiguousOperations int      `json:"ambiguousOperations"`
	TargetBindings      int      `json:"targetBindings"`
	QueryBindings       int      `json:"queryBindings"`
	RESTBindings        int      `json:"restBindings"`
	RPCBindings         int      `json:"rpcBindings"`
	Uncovered           []string `json:"uncovered,omitempty"`
}

func buildModelInventory(revision string, operations []operation) modelInventory {
	inventory := modelInventory{
		Revision:   revision,
		Operations: make([]inventoryOperation, 0, len(operations)),
	}
	collisionGroups := make(map[string]map[string]struct{})
	for _, op := range operations {
		protocols := slices.Clone(op.Protocols)
		sort.Strings(protocols)
		inventory.Operations = append(inventory.Operations, inventoryOperation{
			Service:      op.Service,
			ServiceShape: op.ServiceShape,
			SDKID:        op.SDKID,
			APIVersion:   op.APIVersion,
			Name:         op.Name,
			Protocol:     op.Protocol,
			Protocols:    protocols,
			TargetPrefix: op.TargetPrefix,
			SigningName:  op.SigningName,
			HTTPMethod:   op.HTTPMethod,
			URI:          op.URI,
		})
		if op.Service == "s3" {
			continue
		}
		if op.TargetPrefix != "" && (op.Protocol == "AWSJSON10" || op.Protocol == "AWSJSON11") {
			addCollisionService(collisionGroups, "target", op.TargetPrefix+op.Name, op.Service)
		}
		if op.Protocol == "AWSQuery" || op.Protocol == "EC2Query" {
			addCollisionService(collisionGroups, "query", op.APIVersion+"\x00"+op.Name, op.Service)
		}
		if (op.Protocol == "RESTJSON" || op.Protocol == "RESTXML") && op.HTTPMethod != "" && op.URI != "" {
			addCollisionService(collisionGroups, "rest", normalizedRESTBinding(op), op.Service)
		}
		for _, protocol := range protocols {
			if protocol != "RPCV2CBOR" && protocol != "RPCV2JSON" {
				continue
			}
			addCollisionService(collisionGroups, "rpc", protocol+"\x00"+op.ServiceShape+"\x00"+op.Name, op.Service)
		}
	}
	for _, op := range operations {
		addFallbackCoverage(&inventory.Coverage, collisionGroups, op)
	}
	sort.Slice(inventory.Operations, func(i, j int) bool {
		return inventoryOperationKey(inventory.Operations[i]) < inventoryOperationKey(inventory.Operations[j])
	})
	sort.Strings(inventory.Coverage.Uncovered)
	inventory.Collisions = collisionInventory(collisionGroups)
	return inventory
}

func addFallbackCoverage(coverage *fallbackCoverage, collisionGroups map[string]map[string]struct{}, op operation) {
	if op.Service == "s3" {
		return
	}
	coverage.NonS3Operations++
	claimable, ambiguous := false, false
	if op.TargetPrefix != "" && (op.Protocol == "AWSJSON10" || op.Protocol == "AWSJSON11") {
		coverage.TargetBindings++
		claimable = true
	}
	if op.Protocol == "AWSQuery" || op.Protocol == "EC2Query" {
		coverage.QueryBindings++
		claimable = true
	}
	if (op.Protocol == "RESTJSON" || op.Protocol == "RESTXML") && op.HTTPMethod != "" && op.URI != "" {
		coverage.RESTBindings++
		services := collisionGroups["rest\x00"+normalizedRESTBinding(op)]
		if len(services) > 1 {
			ambiguous = true
		} else {
			claimable = true
		}
	}
	for _, protocol := range op.Protocols {
		if protocol == "RPCV2CBOR" || protocol == "RPCV2JSON" {
			coverage.RPCBindings++
			claimable = true
		}
	}
	switch {
	case claimable:
		coverage.ClaimableOperations++
	case ambiguous:
		coverage.AmbiguousOperations++
	default:
		coverage.Uncovered = append(coverage.Uncovered, operationLabel(op.Service, op.Name))
	}
}

func addCollisionService(groups map[string]map[string]struct{}, kind, key, service string) {
	groupKey := kind + "\x00" + key
	if groups[groupKey] == nil {
		groups[groupKey] = make(map[string]struct{})
	}
	groups[groupKey][service] = struct{}{}
}

func collisionInventory(groups map[string]map[string]struct{}) []inventoryCollision {
	var collisions []inventoryCollision
	for groupKey, serviceSet := range groups {
		if len(serviceSet) < 2 {
			continue
		}
		kind, key, _ := strings.Cut(groupKey, "\x00")
		services := make([]string, 0, len(serviceSet))
		for service := range serviceSet {
			services = append(services, service)
		}
		sort.Strings(services)
		collisions = append(collisions, inventoryCollision{Kind: kind, Key: key, Services: services})
	}
	sort.Slice(collisions, func(i, j int) bool {
		return collisionKey(collisions[i]) < collisionKey(collisions[j])
	})
	return collisions
}

func writeModelInventory(path string, inventory modelInventory) error {
	contents, err := json.MarshalIndent(inventory, "", "  ")
	if err != nil {
		return fmt.Errorf("encode model inventory: %w", err)
	}
	contents = append(contents, '\n')
	if err := os.WriteFile(path, contents, 0o644); err != nil {
		return fmt.Errorf("write model inventory %s: %w", path, err)
	}
	return nil
}

func readModelInventory(path string) (modelInventory, error) {
	contents, err := os.ReadFile(path)
	if err != nil {
		return modelInventory{}, fmt.Errorf("read model inventory %s: %w", path, err)
	}
	var inventory modelInventory
	if err := json.Unmarshal(contents, &inventory); err != nil {
		return modelInventory{}, fmt.Errorf("decode model inventory %s: %w", path, err)
	}
	return inventory, nil
}

func writeInventorySummary(path string, baseline, current modelInventory) error {
	if err := os.WriteFile(path, []byte(summarizeInventoryChanges(baseline, current)), 0o644); err != nil {
		return fmt.Errorf("write model summary %s: %w", path, err)
	}
	return nil
}

// inventoryDiff is one comparison of two model inventories, computed once and
// read by both consumers: the Markdown summary that becomes the refresh PR's
// body, and the changelog fragment that records what a caller can observe.
// They must never disagree about what changed, which is why neither recomputes
// it.
type inventoryDiff struct {
	Baseline, Current modelInventory

	AddedServices, RemovedServices     []string
	AddedOperations, RemovedOperations []string
	TraitChanges, BindingChanges       []string
	AddedCollisions, RemovedCollisions []string
	NewGaps, ResolvedGaps              []string
}

func diffInventories(baseline, current modelInventory) inventoryDiff {
	baselineOperations, currentOperations := keyedOperations(baseline.Operations), keyedOperations(current.Operations)
	baselineServices, currentServices := serviceSet(baseline.Operations), serviceSet(current.Operations)
	baselineCollisions, currentCollisions := keyedCollisions(baseline.Collisions), keyedCollisions(current.Collisions)
	traitChanges, bindingChanges := inventoryOperationChanges(baseline.Operations, current.Operations)
	return inventoryDiff{
		Baseline:          baseline,
		Current:           current,
		AddedServices:     setDifference(currentServices, baselineServices),
		RemovedServices:   setDifference(baselineServices, currentServices),
		AddedOperations:   setDifference(currentOperations, baselineOperations),
		RemovedOperations: setDifference(baselineOperations, currentOperations),
		TraitChanges:      traitChanges,
		BindingChanges:    bindingChanges,
		AddedCollisions:   setDifference(currentCollisions, baselineCollisions),
		RemovedCollisions: setDifference(baselineCollisions, currentCollisions),
		NewGaps:           setDifference(sliceSet(current.Coverage.Uncovered), sliceSet(baseline.Coverage.Uncovered)),
		ResolvedGaps:      setDifference(sliceSet(baseline.Coverage.Uncovered), sliceSet(current.Coverage.Uncovered)),
	}
}

// empty reports whether the two inventories describe the same corpus. The
// coverage comparison is what catches a refresh that moved an operation
// between the claimable and ambiguous tiers without adding or removing one.
func (d inventoryDiff) empty() bool {
	return len(d.AddedServices)+len(d.RemovedServices)+
		len(d.AddedOperations)+len(d.RemovedOperations)+
		len(d.TraitChanges)+len(d.BindingChanges)+
		len(d.AddedCollisions)+len(d.RemovedCollisions) == 0 &&
		coverageEqual(d.Baseline.Coverage, d.Current.Coverage)
}

func summarizeInventoryChanges(baseline, current modelInventory) string {
	d := diffInventories(baseline, current)

	var out bytes.Buffer
	out.WriteString("## AWS API model refresh\n\n")
	fmt.Fprintf(&out, "`%s` -> `%s`\n\n", baseline.Revision, current.Revision)
	if d.empty() {
		out.WriteString("No model changes detected.\n")
		return out.String()
	}
	fmt.Fprintf(&out, "- Services: **+%d / -%d**\n", len(d.AddedServices), len(d.RemovedServices))
	fmt.Fprintf(&out, "- Operations: **+%d / -%d**\n", len(d.AddedOperations), len(d.RemovedOperations))
	fmt.Fprintf(&out, "- Protocol trait changes: **%d**\n", len(d.TraitChanges))
	fmt.Fprintf(&out, "- HTTP/target binding changes: **%d**\n", len(d.BindingChanges))
	fmt.Fprintf(&out, "- Collision changes: **+%d / -%d**\n", len(d.AddedCollisions), len(d.RemovedCollisions))
	fmt.Fprintf(&out, "- Fallback coverage: **%d claimable + %d ambiguous / %d total -> %d claimable + %d ambiguous / %d total non-S3 operations**\n",
		baseline.Coverage.ClaimableOperations, baseline.Coverage.AmbiguousOperations, baseline.Coverage.NonS3Operations,
		current.Coverage.ClaimableOperations, current.Coverage.AmbiguousOperations, current.Coverage.NonS3Operations)
	appendSummaryList(&out, "Added services", d.AddedServices)
	appendSummaryList(&out, "Removed services", d.RemovedServices)
	appendSummaryList(&out, "Added operations", d.AddedOperations)
	appendSummaryList(&out, "Removed operations", d.RemovedOperations)
	appendSummaryList(&out, "Protocol trait changes", d.TraitChanges)
	appendSummaryList(&out, "HTTP/target binding changes", d.BindingChanges)
	appendSummaryList(&out, "Added collisions", d.AddedCollisions)
	appendSummaryList(&out, "Removed collisions", d.RemovedCollisions)
	appendSummaryList(&out, "New fallback coverage gaps", d.NewGaps)
	appendSummaryList(&out, "Resolved fallback coverage gaps", d.ResolvedGaps)
	return out.String()
}

func keyedOperations(operations []inventoryOperation) map[string]string {
	result := make(map[string]string, len(operations))
	for _, op := range operations {
		result[inventoryOperationKey(op)] = operationLabel(op.Service, op.Name)
	}
	return result
}

func inventoryOperationKey(op inventoryOperation) string {
	return op.Service + "\x00" + op.APIVersion + "\x00" + op.Name
}

func serviceSet(operations []inventoryOperation) map[string]string {
	result := make(map[string]string)
	for _, op := range operations {
		result[op.Service] = op.Service
	}
	return result
}

func inventoryOperationChanges(baseline, current []inventoryOperation) (traitChanges, bindingChanges []string) {
	baselineByKey := make(map[string]inventoryOperation, len(baseline))
	for _, op := range baseline {
		baselineByKey[inventoryOperationKey(op)] = op
	}
	for _, op := range current {
		old, ok := baselineByKey[inventoryOperationKey(op)]
		if !ok {
			continue
		}
		label := operationLabel(op.Service, op.Name)
		if old.Protocol != op.Protocol || !slices.Equal(old.Protocols, op.Protocols) {
			traitChanges = append(traitChanges, label)
		}
		if old.ServiceShape != op.ServiceShape || old.TargetPrefix != op.TargetPrefix || old.SigningName != op.SigningName || old.HTTPMethod != op.HTTPMethod || old.URI != op.URI {
			bindingChanges = append(bindingChanges, label)
		}
	}
	sort.Strings(traitChanges)
	sort.Strings(bindingChanges)
	return traitChanges, bindingChanges
}

func keyedCollisions(collisions []inventoryCollision) map[string]string {
	result := make(map[string]string, len(collisions))
	for _, collision := range collisions {
		result[collisionKey(collision)] = collision.Kind + " " + strings.ReplaceAll(collision.Key, "\x00", " / ") + " (" + strings.Join(collision.Services, ", ") + ")"
	}
	return result
}

func collisionKey(collision inventoryCollision) string {
	return collision.Kind + "\x00" + collision.Key + "\x00" + strings.Join(collision.Services, "\x00")
}

func setDifference(left, right map[string]string) []string {
	result := make([]string, 0)
	for key, value := range left {
		if _, ok := right[key]; !ok {
			result = append(result, value)
		}
	}
	sort.Strings(result)
	return result
}

func sliceSet(values []string) map[string]string {
	result := make(map[string]string, len(values))
	for _, value := range values {
		result[value] = value
	}
	return result
}

func coverageEqual(left, right fallbackCoverage) bool {
	return left.NonS3Operations == right.NonS3Operations &&
		left.ClaimableOperations == right.ClaimableOperations &&
		left.AmbiguousOperations == right.AmbiguousOperations &&
		left.TargetBindings == right.TargetBindings &&
		left.QueryBindings == right.QueryBindings &&
		left.RESTBindings == right.RESTBindings &&
		left.RPCBindings == right.RPCBindings &&
		slices.Equal(left.Uncovered, right.Uncovered)
}

func appendSummaryList(out *bytes.Buffer, title string, values []string) {
	if len(values) == 0 {
		return
	}
	fmt.Fprintf(out, "\n### %s\n\n", title)
	const detailLimit = 50
	for _, value := range values[:min(len(values), detailLimit)] {
		fmt.Fprintf(out, "- `%s`\n", value)
	}
	if len(values) > detailLimit {
		fmt.Fprintf(out, "- ...and %d more\n", len(values)-detailLimit)
	}
}

func operationLabel(service, operation string) string {
	return service + "/" + operation
}
