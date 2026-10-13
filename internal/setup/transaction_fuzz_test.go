package setup

import (
	"bytes"
	"encoding/json"
	"testing"
)

func FuzzTransactionMetadata(f *testing.F) {
	store := newVirtualStore()
	root, _ := store.rootIdentity()
	valid := diskTransaction{Version: 1, Phase: "prepared", Root: root, Owner: store.Owner(), Files: []transactionFile{{Name: "config.json", Action: "create", After: stamp(store.snapshot([]byte(`{}`), 0o600))}}}
	encoded, _ := json.Marshal(valid)
	f.Add(encoded)
	f.Add([]byte(`{"version":1,"version":1}`))
	f.Add([]byte(`{"phase":"secret-corrupt"}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > maximumManifestBytes+1 {
			t.Skip()
		}
		store := newVirtualStore()
		before := store.areas[""]["a.json"]
		record, err := parseTransaction(store, store.snapshot(data, 0o600), store.Owner())
		if err == nil {
			canonical, marshalErr := json.Marshal(record)
			if marshalErr != nil || !bytes.Equal(bytes.TrimSpace(data), canonical) {
				t.Fatal("non-canonical metadata accepted")
			}
		}
		if len(store.calls) != 0 || !bytes.Equal(store.areas[""]["a.json"].Data, before.Data) {
			t.Fatal("metadata parsing mutated filesystem")
		}
	})
}
