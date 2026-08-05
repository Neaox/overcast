package codec

import (
	"net/url"
	"testing"
)

func TestDecodeQueryForm_PointerSlicePreservesPresence(t *testing.T) {
	type member struct {
		Key string `json:"Key"`
	}
	type input struct {
		Tags *[]member `json:"Tags"`
	}

	var omitted input
	if aerr := decodeQueryForm(url.Values{}, &omitted); aerr != nil {
		t.Fatal(aerr)
	}
	if omitted.Tags != nil {
		t.Fatalf("omitted Tags = %#v, want nil", omitted.Tags)
	}

	var empty input
	if aerr := decodeQueryForm(url.Values{"Tags": {""}}, &empty); aerr != nil {
		t.Fatal(aerr)
	}
	if empty.Tags == nil || len(*empty.Tags) != 0 {
		t.Fatalf("explicit empty Tags = %#v, want non-nil empty", empty.Tags)
	}

	var populated input
	if aerr := decodeQueryForm(url.Values{"Tags.member.1.Key": {"stage"}}, &populated); aerr != nil {
		t.Fatal(aerr)
	}
	if populated.Tags == nil || len(*populated.Tags) != 1 || (*populated.Tags)[0].Key != "stage" {
		t.Fatalf("populated Tags = %#v", populated.Tags)
	}
}

func TestDecodeQueryForm_EmptyScalarPointersRemainNil(t *testing.T) {
	type input struct {
		Name    *string `json:"Name"`
		Count   *int    `json:"Count"`
		Enabled *bool   `json:"Enabled"`
	}
	var got input
	if aerr := decodeQueryForm(url.Values{"Name": {""}, "Count": {""}, "Enabled": {""}}, &got); aerr != nil {
		t.Fatal(aerr)
	}
	if got.Name != nil || got.Count != nil || got.Enabled != nil {
		t.Fatalf("empty scalar pointers = Name:%v Count:%v Enabled:%v, want all nil", got.Name, got.Count, got.Enabled)
	}
}
