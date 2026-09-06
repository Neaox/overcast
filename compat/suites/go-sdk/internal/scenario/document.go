package scenario

import (
	"encoding/base64"
	"fmt"
	"math"
	"reflect"
	"time"
	"unicode"
)

// The bridge between the AWS SDK's typed values and the IR's documents.
//
// Every rule the IR states about a response — a path resolves or it does not,
// an absent list reads like an empty one, `equals` compares in the JSON type
// system — is stated over JSON. The three interpreters get that for free: they
// hold the parsed response. A typed SDK hands us a struct of pointers, enums
// and slices instead, so one conversion stands between the two, and its
// choices are the ones that make this suite agree with the others.
//
// Two of those choices are load-bearing:
//
//   - **nil is absence, not null.** The Go SDK deserializes an omitted member
//     and a JSON null to the same nil pointer, and compat/model/README.md
//     § Paths settles which of the two that is: "`undefined` in an SDK's object
//     model is absence, not a value". So a nil field is left out of the
//     document rather than written as null. Encoding the struct with
//     encoding/json instead would write `"NextToken": null` for every unset
//     member, which resolves — and would fail `missing` on an absent token and
//     `isList` on an omitted page, both of which are correct AWS answers.
//   - **every number becomes a float64.** That is what encoding/json produces
//     on the interpreters' side, so an `equals` on an int32 member compares
//     the same way here as it does there.

// toDocument converts an SDK value to the IR's document form: map[string]any,
// []any, string, float64, bool, or nil.
//
// present is false when the value is absent — a nil pointer, slice, map or
// interface — and the caller then omits the member entirely.
func toDocument(v any) (doc any, present bool) {
	if v == nil {
		return nil, false
	}
	return fromValue(reflect.ValueOf(v))
}

// resultMetadataField is the one member of every SDK output struct that is not
// part of the modeled response: middleware.Metadata, whose contents are the
// SDK's own bookkeeping. It carries no exported fields, so it would render as
// an empty object shadowing nothing — but a scenario path can only ever mean
// modeled members, so it is dropped rather than surfaced.
const resultMetadataField = "ResultMetadata"

func fromValue(rv reflect.Value) (any, bool) {
	// The kinds not named here are handled by the second switch below, which
	// ends in a catch-all: this one only peels off absence and indirection.
	//exhaustive:ignore
	switch rv.Kind() {
	case reflect.Invalid:
		return nil, false
	case reflect.Pointer, reflect.Interface:
		if rv.IsNil() {
			return nil, false
		}
		return fromValue(rv.Elem())
	}

	// A timestamp is never compared by the IR (compat/model/README.md
	// § Assertions), but it can sit on a response a path walks past, so it is
	// rendered rather than dropped.
	if t, ok := rv.Interface().(time.Time); ok {
		return t.UTC().Format(time.RFC3339Nano), true
	}

	// Every kind an SDK response can hold is named; anything else falls through
	// to the catch-all after the switch rather than being enumerated here.
	//exhaustive:ignore
	switch rv.Kind() {
	case reflect.String:
		return rv.String(), true
	case reflect.Bool:
		return rv.Bool(), true
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return float64(rv.Int()), true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return float64(rv.Uint()), true
	case reflect.Float32, reflect.Float64:
		return rv.Float(), true
	case reflect.Slice, reflect.Array:
		if rv.Kind() == reflect.Slice && rv.IsNil() {
			return nil, false
		}
		// A blob is []byte, which JSON carries base64-encoded. Blobs are
		// refused by the generator, so nothing asserts on one; rendering it
		// the way the wire does is still better than a list of 8-bit numbers
		// in a failure message.
		if rv.Type().Elem().Kind() == reflect.Uint8 && rv.Type().Elem().PkgPath() == "" {
			return base64.StdEncoding.EncodeToString(rv.Bytes()), true
		}
		out := make([]any, 0, rv.Len())
		for i := 0; i < rv.Len(); i++ {
			item, ok := fromValue(rv.Index(i))
			if !ok {
				// A nil element of a list is a null the service sent, not an
				// absent member: dropping it would renumber every index after
				// it, which a path can address.
				item = nil
			}
			out = append(out, item)
		}
		return out, true
	case reflect.Map:
		if rv.IsNil() {
			return nil, false
		}
		out := make(map[string]any, rv.Len())
		for _, key := range rv.MapKeys() {
			name, ok := mapKeyString(key)
			if !ok {
				continue
			}
			if value, ok := fromValue(rv.MapIndex(key)); ok {
				out[name] = value
			}
		}
		return out, true
	case reflect.Struct:
		out := make(map[string]any)
		rt := rv.Type()
		for i := 0; i < rt.NumField(); i++ {
			field := rt.Field(i)
			if !field.IsExported() || field.Name == resultMetadataField {
				continue
			}
			if value, ok := fromValue(rv.Field(i)); ok {
				out[field.Name] = value
			}
		}
		return out, true
	}
	return fmt.Sprint(rv.Interface()), true
}

// exportedName is the Go field smithy-go generates for a modeled member: the
// member name with its first letter capitalized. Almost every AWS member is
// already PascalCase, but not all — SQS models
// ListDeadLetterSourceQueues' page as `queueUrls` and CreateQueue's tags as
// `tags`, and the Go SDK spells both with a capital.
//
// This is the one place the Go object model and the IR's member names differ,
// and it is bridged here rather than by rewriting paths at emit time: a
// failure message has to quote the path the scenario file writes, or field 4
// stops matching the other backends'.
//
// smithy-go's own rule is capitalization plus reserved-word handling; only the
// capitalization is reproduced, because a member that needed the second half
// would not resolve and would fail the check that names it rather than passing
// wrongly.
func exportedName(member string) string {
	if member == "" {
		return member
	}
	r := []rune(member)
	if !unicode.IsLower(r[0]) {
		return member
	}
	r[0] = unicode.ToUpper(r[0])
	return string(r)
}

// mapKeyString renders a map key. SDK map keys are strings or named string
// types (an enum-keyed attribute map); anything else has no place in a
// document and is dropped rather than stringified into a member name a path
// could accidentally address.
func mapKeyString(key reflect.Value) (string, bool) {
	if key.Kind() == reflect.String {
		return key.String(), true
	}
	return "", false
}

// ---------------------------------------------------------------------------
// The other direction: a document value into a typed SDK input field
// ---------------------------------------------------------------------------

// assign writes an evaluated value into one field of an SDK input struct.
// dst is a pointer to the field (&in.QueueUrl), which is what lets one helper
// serve both spellings smithy-go uses for a scalar member: *string and string,
// *int32 and int32. Deriving pointer-ness from the model instead is not
// possible — the pinned shape snapshot and the vendored SDK are generated from
// different revisions of the same AWS model, and for SQS they already
// disagree: ReceiveMessage's MaxNumberOfMessages targets NullableInteger in
// models/aws/shapes/sqs.json and is a plain int32 in
// aws-sdk-go-v2/service/sqs. A generator that emitted aws.Int32 there would
// not compile.
func assign(dst any, v any) error {
	ptr := reflect.ValueOf(dst)
	if ptr.Kind() != reflect.Pointer || ptr.IsNil() {
		return fmt.Errorf("internal: assignment target is %T, want a pointer to an input field", dst)
	}
	return assignTo(ptr.Elem(), v)
}

func assignTo(dst reflect.Value, v any) error {
	if v == nil {
		// Nothing to set: a member the scenario does not supply is left at the
		// SDK's zero value, which is how it is left out of the request.
		return nil
	}
	if dst.Kind() == reflect.Pointer {
		holder := reflect.New(dst.Type().Elem())
		if err := assignTo(holder.Elem(), v); err != nil {
			return err
		}
		dst.Set(holder)
		return nil
	}
	if dst.Kind() == reflect.Interface {
		dst.Set(reflect.ValueOf(v))
		return nil
	}

	// Pointer and interface targets are handled above; every other kind an SDK
	// input field can have is named, and the rest reach the refusal after the
	// switch rather than being enumerated here.
	//exhaustive:ignore
	switch dst.Kind() {
	case reflect.String:
		s, ok := v.(string)
		if !ok {
			return typeErr(dst, v, "a string")
		}
		dst.SetString(s)
		return nil
	case reflect.Bool:
		b, ok := v.(bool)
		if !ok {
			return typeErr(dst, v, "a boolean")
		}
		dst.SetBool(b)
		return nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		n, ok := numberOf(v)
		if !ok {
			return typeErr(dst, v, "a number")
		}
		if n != math.Trunc(n) {
			return typeErr(dst, v, "a whole number")
		}
		if dst.OverflowInt(int64(n)) {
			return typeErr(dst, v, "a number in range for "+dst.Type().String())
		}
		dst.SetInt(int64(n))
		return nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		n, ok := numberOf(v)
		if !ok || n < 0 {
			return typeErr(dst, v, "a non-negative number")
		}
		if n != math.Trunc(n) || dst.OverflowUint(uint64(n)) {
			return typeErr(dst, v, "a whole number in range for "+dst.Type().String())
		}
		dst.SetUint(uint64(n))
		return nil
	case reflect.Float32, reflect.Float64:
		n, ok := numberOf(v)
		if !ok {
			return typeErr(dst, v, "a number")
		}
		dst.SetFloat(n)
		return nil
	case reflect.Slice:
		items, ok := v.([]any)
		if !ok {
			return typeErr(dst, v, "a list")
		}
		out := reflect.MakeSlice(dst.Type(), len(items), len(items))
		for i, item := range items {
			if err := assignTo(out.Index(i), item); err != nil {
				return err
			}
		}
		dst.Set(out)
		return nil
	case reflect.Map:
		entries, ok := v.(map[string]any)
		if !ok {
			return typeErr(dst, v, "an object")
		}
		if dst.Type().Key().Kind() != reflect.String {
			return typeErr(dst, v, "a map with string keys")
		}
		out := reflect.MakeMapWithSize(dst.Type(), len(entries))
		for _, key := range sortedKeys(entries) {
			value := reflect.New(dst.Type().Elem()).Elem()
			if err := assignTo(value, entries[key]); err != nil {
				return err
			}
			out.SetMapIndex(reflect.ValueOf(key).Convert(dst.Type().Key()), value)
		}
		dst.Set(out)
		return nil
	case reflect.Struct:
		members, ok := v.(map[string]any)
		if !ok {
			return typeErr(dst, v, "an object")
		}
		for _, name := range sortedKeys(members) {
			field := dst.FieldByName(name)
			if !field.IsValid() {
				field = dst.FieldByName(exportedName(name))
			}
			if !field.IsValid() || !field.CanSet() {
				return fmt.Errorf("%s has no settable member %q", dst.Type(), name)
			}
			if err := assignTo(field, members[name]); err != nil {
				return err
			}
		}
		return nil
	}
	return typeErr(dst, v, "a value this backend can build")
}

func typeErr(dst reflect.Value, v any, want string) error {
	return fmt.Errorf("%s wants %s, got %s", dst.Type(), want, render(v))
}

// numberOf accepts every spelling of a number a value expression can produce:
// a Go literal the emitter wrote, and a float64 read back out of a document.
func numberOf(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int32:
		return float64(n), true
	case int64:
		return float64(n), true
	}
	// A string is deliberately not accepted: "30" is not 30 anywhere else in
	// the IR, and coercing it here would let a wrong literal through silently.
	return 0, false
}
