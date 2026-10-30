package protocol

import (
	"bytes"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestResourceTagsValidationAndMatching(t *testing.T) {
	for _, tags := range [][]string{nil, {}, {"kostka", "production", "front-end_1.2"}, {strings.Repeat("a", 64)}} {
		if err := ValidateTags(tags); err != nil {
			t.Fatal(tags, err)
		}
	}
	for _, tags := range [][]string{{""}, {"Production"}, {"český"}, {"-bad"}, {".bad"}, {"_bad"}, {"x y"}, {"x,y"}, {"x/y"}, {"x\ny"}, {"$(whoami)"}, {"a", "a"}, {strings.Repeat("a", 65)}} {
		if ValidateTags(tags) == nil {
			t.Fatalf("invalid tags accepted: %q", tags)
		}
	}
	tags := make([]string, MaxResourceTags+1)
	for i := range tags {
		tags[i] = fmt.Sprintf("t%d", i)
	}
	if ValidateTags(tags) == nil {
		t.Fatal("too many tags accepted")
	}
	if !MatchesTags([]string{"a", "b"}, []string{"b", "a"}) || MatchesTags([]string{"a"}, []string{"a", "b"}) || MatchesTags(nil, []string{"a"}) || !MatchesTags(nil, nil) {
		t.Fatal("AND/untagged semantics")
	}
}

func TestResourceTagsWireCompatibility(t *testing.T) {
	// Frozen v1 descriptor: the deployed decoder ignores unknown CBOR map fields.
	type legacyResource struct {
		ID      string   `cbor:"id"`
		Type    string   `cbor:"type"`
		Actions []string `cbor:"actions"`
	}
	type legacyInventory struct {
		Revision  uint64           `cbor:"revision"`
		Resources []legacyResource `cbor:"resources"`
	}
	old := legacyInventory{Revision: 1, Resources: []legacyResource{{ID: "app", Type: "container", Actions: []string{"status"}}}}
	oldBytes, err := EncodePayload(old)
	if err != nil {
		t.Fatal(err)
	}
	var current InventorySnapshot
	if err := DecodePayload(oldBytes, &current); err != nil || current.Validate() != nil || len(current.Resources[0].Tags) != 0 {
		t.Fatal("old inventory rejected", err)
	}
	untagged, _ := EncodePayload(current)
	if !bytes.Equal(oldBytes, untagged) {
		t.Fatal("untagged inventory must preserve original wire bytes")
	}
	current.Resources[0].Tags = []string{"kostka", "production"}
	data, err := EncodePayload(current)
	if err != nil {
		t.Fatal(err)
	}
	var decodedOld legacyInventory
	if err := DecodePayload(data, &decodedOld); err != nil || !reflect.DeepEqual(old, decodedOld) {
		t.Fatal("legacy descriptor cannot ignore tags", err)
	}
	var decoded InventorySnapshot
	if err := DecodePayload(data, &decoded); err != nil || decoded.Validate() != nil || !reflect.DeepEqual(current, decoded) {
		t.Fatal("tagged round trip failed", err)
	}
	current.Resources[0].Tags = []string{"bad tag"}
	if current.Validate() == nil {
		t.Fatal("wire inventory bypasses tag validation")
	}
}
