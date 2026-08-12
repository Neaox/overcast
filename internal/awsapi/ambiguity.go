package awsapi

import "sort"

// Ambiguity resolution: what the registry does when several modeled services
// declare one wire key.
//
// awsmodelgen refuses to guess. When two services bind the same X-Amz-Target or
// the same (Version, Action) pair, it blanks the service on the index entry and
// records the members in a collision array beside it. That is the right answer
// for a generator reading AWS's models alone, because the models genuinely do
// not say which service owns the key.
//
// Overcast is not reading the models alone. It knows two things the generator
// does not, and both are already declared:
//
//   - Which modeled identities are the same service. serviceAliases maps a
//     model identity to Overcast's key, and a "collision" whose members all map
//     to one key was never a collision — nothing has to be chosen. Every one of
//     the 51 target collisions between "cloudwatch-events" and "eventbridge" is
//     this: one AWS service the models carry under two identities.
//   - Which service Overcast serves a Query API version for. queryVersionOwners
//     (versions.go) is the same declaration each service's OwnsVersion is built
//     from, so it is already how the *router* resolves this. All 71 query
//     collisions are DocumentDB, Neptune and RDS sharing 2014-10-31, and RDS is
//     the only one Overcast implements.
//
// Resolving here rather than in the generator keeps both declarations in the
// package that owns them — serviceAliases beside the indexes it translates, and
// the version constants where the service packages already import them from.
//
// What this does not do is widen a claim. A collision that survives both steps
// stays ambiguous, which is the honest answer for the four Timestream target
// collisions: two real services, neither implemented, nothing to prefer.
//
// The cost is paid only by a claim that is already ambiguous — a binary search
// over a sorted array of 55 or 71 entries, then at most three alias lookups.
// An unambiguous claim, which is every claim on the hot path, does none of it.

// resolveCollision returns the Overcast service key that owns an ambiguous
// index entry, or "" when the collision is real and unresolved.
//
// declaredOwner is the caller's own declaration of who serves this wire, and is
// honoured only when it is one of the modeled services that actually collided.
// Requiring that is what keeps the declaration a tie-break rather than an
// override: a version owner cannot claim an action its service never declared.
func resolveCollision(collisions []operationCollision, key, declaredOwner string) string {
	services := collidingServices(collisions, key)
	if len(services) == 0 {
		return ""
	}
	if single := singleServiceKey(services); single != "" {
		return single
	}
	if declaredOwner == "" {
		return ""
	}
	for _, service := range services {
		if overcastService(service) == declaredOwner {
			return declaredOwner
		}
	}
	return ""
}

// collidingServices returns the modeled services recorded against key. The
// arrays are emitted in their index's sorted order, so the lookup is a binary
// search rather than a scan.
func collidingServices(collisions []operationCollision, key string) []string {
	i := sort.Search(len(collisions), func(i int) bool {
		return collisions[i].Key >= key
	})
	if i == len(collisions) || collisions[i].Key != key {
		return nil
	}
	return collisions[i].Services
}

// singleServiceKey returns the one Overcast key every modeled service in a
// collision maps to, or "" when they do not agree.
func singleServiceKey(services []string) string {
	var single string
	for _, service := range services {
		key := overcastService(service)
		if key == "" {
			return ""
		}
		if single == "" {
			single = key
			continue
		}
		if key != single {
			return ""
		}
	}
	return single
}
