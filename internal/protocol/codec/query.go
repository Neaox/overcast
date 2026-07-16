package codec

import (
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"strconv"
	"strings"

	"github.com/Neaox/overcast/internal/protocol"
)

// queryXML implements Codec for the AWS Query protocol (form-encoded
// requests, XML responses). Used by SNS, IAM, STS, EC2, CloudFormation,
// RDS, SES, CloudWatch metrics, ELBv2, AutoScaling, Route53, ElastiCache.
type queryXML struct{}

// QueryXML is the singleton AWS Query codec.
var QueryXML Codec = queryXML{}

func (queryXML) Name() string { return NameAWSQuery }

func (q queryXML) Decode(r *http.Request, into any) *protocol.AWSError {
	if r.Body == nil {
		return nil
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		return &protocol.AWSError{
			Code: "SerializationException", Message: "Failed to read request body: " + err.Error(), HTTPStatus: http.StatusBadRequest,
		}
	}
	values, err := url.ParseQuery(string(body))
	if err != nil {
		return &protocol.AWSError{
			Code: "SerializationException", Message: "Failed to parse form body: " + err.Error(), HTTPStatus: http.StatusBadRequest,
		}
	}
	if aerr := decodeQueryForm(values, into); aerr != nil {
		return aerr
	}
	return nil
}

func (queryXML) WriteResponse(w http.ResponseWriter, r *http.Request, status int, v any) {
	if v == nil {
		protocol.WriteQueryXMLError(w, r, &protocol.AWSError{
			Code: "InternalError", Message: "query codec: WriteResponse called with nil value", HTTPStatus: http.StatusInternalServerError,
		})
		return
	}
	protocol.WriteQueryXML(w, r, status, v)
}

func (queryXML) WriteError(w http.ResponseWriter, r *http.Request, aerr *protocol.AWSError) {
	protocol.WriteQueryXMLError(w, r, aerr)
}

// decodeQueryForm decodes AWS Query protocol form values into a struct.
// Supports:
//   - Simple fields: Name=value maps to struct field with json:"Name"
//   - Lists: Foo.member.N=value or Foo.member.N.Bar=value
//   - Structs: Foo.Bar=value (nested dot notation)
//   - Maps (entry pattern): Attributes.entry.N.key=K&Attributes.entry.N.value=V
func decodeQueryForm(values url.Values, into any) *protocol.AWSError {
	if into == nil {
		return nil
	}
	rv := reflect.ValueOf(into)
	if rv.Kind() != reflect.Ptr || rv.IsNil() {
		return &protocol.AWSError{
			Code: "InternalError", Message: "decodeQueryForm: into must be a non-nil pointer", HTTPStatus: http.StatusInternalServerError,
		}
	}
	return decodeStruct(values, rv.Elem(), "")
}

func decodeStruct(values url.Values, rv reflect.Value, prefix string) *protocol.AWSError {
	if rv.Kind() == reflect.Ptr {
		if rv.IsNil() {
			rv.Set(reflect.New(rv.Type().Elem()))
		}
		rv = rv.Elem()
	}
	if rv.Kind() != reflect.Struct {
		return nil
	}
	rt := rv.Type()

	// Build a field index: json tag → field index
	fieldByJSON := map[string]int{}
	for i := range rt.NumField() {
		tag := rt.Field(i).Tag.Get("json")
		if tag == "" || tag == "-" {
			continue
		}
		name, _, _ := strings.Cut(tag, ",")
		fieldByJSON[name] = i
	}

	// Collect list members: {baseKey: map[int]url.Values}
	type listMember struct {
		idx    int
		values url.Values
	}
	listMembers := map[string][]listMember{}
	// Collect map entries: {baseKey: map[int]url.Values}
	mapEntries := map[string][]listMember{}

	for key, vals := range values {
		if len(vals) == 0 {
			continue
		}
		if prefix != "" {
			if !strings.HasPrefix(key, prefix+".") {
				continue
			}
			key = key[len(prefix)+1:]
		}
		parts := strings.Split(key, ".")
		if len(parts) == 0 {
			continue
		}

		// Check for list: Foo.member.N or Foo.member.N.Bar
		if len(parts) >= 2 && strings.HasPrefix(parts[1], "member") && len(parts) > 2 {
			base := parts[0]
			idx, err := strconv.Atoi(parts[2])
			if err != nil {
				continue
			}
			// 1-indexed → 0-indexed
			idx--
			remainingKey := strings.Join(parts[3:], ".")
			listMembers[base] = append(listMembers[base], listMember{idx: idx, values: url.Values{remainingKey: vals}})
			continue
		}

		// Check for flattened EC2 Query lists: Foo.N or Foo.N.Bar.
		if len(parts) >= 2 {
			idx, err := strconv.Atoi(parts[1])
			if err == nil {
				base := parts[0]
				idx--
				remainingKey := strings.Join(parts[2:], ".")
				listMembers[base] = append(listMembers[base], listMember{idx: idx, values: url.Values{remainingKey: vals}})
				continue
			}
		}

		// Check for map entry: Attributes.entry.N.key=K and Attributes.entry.N.value=V
		if len(parts) >= 3 && strings.HasPrefix(parts[1], "entry") {
			base := parts[0]
			idx, err := strconv.Atoi(parts[2])
			if err != nil {
				continue
			}
			idx-- // 1-indexed → 0-indexed
			remainingKey := strings.Join(parts[3:], ".")
			mapEntries[base] = append(mapEntries[base], listMember{idx: idx, values: url.Values{remainingKey: vals}})
			continue
		}

		// Simple field or dot-separated struct field
		fieldIdx, ok := fieldByJSON[parts[0]]
		if !ok {
			continue
		}
		field := rv.Field(fieldIdx)
		if len(parts) == 1 {
			// Simple field
			if err := setFieldValue(field, vals[0]); err != nil {
				continue
			}
		} else {
			// Nested struct: Foo.Bar=value
			if err := decodeStruct(values, field, parts[0]); err != nil {
				continue
			}
		}
	}

	// Process list members
	for base, members := range listMembers {
		fieldIdx, ok := fieldByJSON[base]
		if !ok {
			continue
		}
		field := rv.Field(fieldIdx)
		if field.Kind() != reflect.Slice {
			continue
		}
		// Find max index
		maxIdx := -1
		for _, m := range members {
			if m.idx > maxIdx {
				maxIdx = m.idx
			}
		}
		if maxIdx < 0 {
			continue
		}
		slice := reflect.MakeSlice(field.Type(), maxIdx+1, maxIdx+1)
		for _, m := range members {
			if m.idx < 0 || m.idx > maxIdx {
				continue
			}
			elem := slice.Index(m.idx)
			elemType := field.Type().Elem()
			if elemType.Kind() == reflect.Ptr {
				elem.Set(reflect.New(elemType.Elem()))
			}
			if err := decodeItem(elem, m.values); err != nil {
				continue
			}
		}
		field.Set(slice)
	}

	// Process map entries
	for base, entries := range mapEntries {
		fieldIdx, ok := fieldByJSON[base]
		if !ok {
			continue
		}
		field := rv.Field(fieldIdx)
		if field.Kind() != reflect.Map {
			continue
		}
		mapType := field.Type()
		if mapType.Key().Kind() != reflect.String {
			continue
		}
		newMap := reflect.MakeMap(mapType)
		for _, e := range entries {
			k := e.values.Get("key")
			v := e.values.Get("value")
			if k == "" {
				continue
			}
			newMap.SetMapIndex(reflect.ValueOf(k), reflect.ValueOf(v))
		}
		field.Set(newMap)
	}

	return nil
}

func decodeItem(rv reflect.Value, values url.Values) error {
	if rv.Kind() == reflect.Ptr {
		if rv.IsNil() {
			rv.Set(reflect.New(rv.Type().Elem()))
		}
		rv = rv.Elem()
	}
	if rv.Kind() == reflect.Struct {
		rt := rv.Type()
		for i := range rt.NumField() {
			tag := rt.Field(i).Tag.Get("json")
			if tag == "" || tag == "-" {
				continue
			}
			name, _, _ := strings.Cut(tag, ",")
			if values.Has(name) {
				if err := setFieldValue(rv.Field(i), values.Get(name)); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if rv.Kind() == reflect.String && len(values) > 0 {
		// Single value for non-struct member in list — use first key's first value
		for _, v := range values {
			rv.SetString(v[0])
			break
		}
		return nil
	}
	return nil
}

func setFieldValue(rv reflect.Value, s string) error {
	if !rv.CanSet() {
		return fmt.Errorf("cannot set field")
	}
	switch rv.Kind() {
	case reflect.String:
		rv.SetString(s)
	case reflect.Int, reflect.Int64:
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return err
		}
		rv.SetInt(n)
	case reflect.Bool:
		b, err := strconv.ParseBool(s)
		if err != nil {
			return err
		}
		rv.SetBool(b)
	case reflect.Float64:
		f, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return err
		}
		rv.SetFloat(f)
	case reflect.Ptr:
		if s == "" {
			return nil
		}
		if rv.IsNil() {
			rv.Set(reflect.New(rv.Type().Elem()))
		}
		return setFieldValue(rv.Elem(), s)
	case reflect.Invalid, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Uint, reflect.Uint8,
		reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr, reflect.Float32,
		reflect.Complex64, reflect.Complex128, reflect.Array, reflect.Chan, reflect.Func,
		reflect.Interface, reflect.Map, reflect.Slice, reflect.Struct, reflect.UnsafePointer:
		return fmt.Errorf("unsupported field type: %v", rv.Kind())
	default:
		return fmt.Errorf("unsupported field type: %v", rv.Kind())
	}
	return nil
}

var _ = xml.Marshal // keep xml import for potential future use
