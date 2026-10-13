package setup

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"
)

func FuzzReadOnlyPlan(f *testing.F) {
	f.Add("config.json", "candidate", false, false)
	f.Add("api.token", "private-secret", true, true)
	f.Add("../unsafe", "secret", true, false)
	f.Fuzz(func(t *testing.T, name, value string, present, replace bool) {
		if len(name) > 256 || len(value) > 4096 {
			t.Skip()
		}
		data, err := json.Marshal(map[string]string{"private": value})
		if err != nil {
			t.Fatal(err)
		}
		files := map[string]Snapshot{}
		if present {
			files[name] = existing(`{"previous":"private-value"}`, 0o600)
		}
		planner, source := testPlanner(t, files)
		before := string(source.files[name].Data)
		intent := jsonIntent(name, data)
		intent.AllowReplace = replace
		plan, err := planner.Build(context.Background(), []Intent{intent})
		if string(source.files[name].Data) != before {
			t.Fatal("preview mutated source")
		}
		if err != nil {
			return
		}
		encoded, err := json.Marshal(plan)
		if err != nil {
			t.Fatal(err)
		}
		expected, err := json.Marshal(plan.Preview())
		if err != nil || !bytes.Equal(encoded, expected) {
			t.Fatal("plan emitted extra private fields")
		}
		var public Preview
		if json.Unmarshal(encoded, &public) != nil || !reflect.DeepEqual(public, plan.Preview()) || !public.ReadOnly {
			t.Fatal("serialized private plan instead of preview")
		}
		if fmt.Sprintf("%+v", plan) != string(encoded) || fmt.Sprintf("%#v", plan) != string(encoded) || fmt.Sprintf("%+v", *plan) != string(encoded) || fmt.Sprintf("%#v", *plan) != string(encoded) {
			t.Fatal("formatted private plan instead of preview")
		}
		if err := planner.Recheck(context.Background(), plan); err != nil {
			t.Fatal(err)
		}
	})
}
