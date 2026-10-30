package agent

import (
	"reflect"
	"strings"
	"testing"
)

func TestResourceTagsYAMLAndInventory(t *testing.T) {
	text := strings.Replace(validConfigYAML, "    actions:", "    tags: [kostka, production]\n    actions:", 1)
	config, err := Parse([]byte(text))
	if err != nil {
		t.Fatal(err)
	}
	sync, err := config.synchronization()
	if err != nil || !reflect.DeepEqual(sync.Inventory.Resources[0].Tags, []string{"kostka", "production"}) {
		t.Fatal("tags lost before inventory", err)
	}
	config.Resources[0].Tags[0] = "changed"
	if sync.Inventory.Resources[0].Tags[0] != "kostka" {
		t.Fatal("inventory aliases mutable configuration")
	}
	for _, tags := range []string{"[kostka, kostka]", "[Production]", "[\"bad tag\"]", "wrong-type"} {
		bad := strings.Replace(text, "[kostka, production]", tags, 1)
		if _, err := Parse([]byte(bad)); err == nil {
			t.Fatal("invalid YAML tags accepted", tags)
		}
	}
	if _, err := Parse([]byte(validConfigYAML)); err != nil {
		t.Fatal("old configuration rejected", err)
	}
}
