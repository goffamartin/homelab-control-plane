package adapter

import (
	"hlcp/internal/protocol"
	"reflect"
	"testing"
)

func TestContainerLogArgumentsAreFixedAndBounded(t *testing.T) {
	q := protocol.DefaultLogQuery()
	q.Tail = 20
	q.Follow = true
	q.Since = "2026-10-16T00:00:00Z"
	q.Until = "2026-10-16T01:00:00Z"
	q.Contains = "--details;touch"
	q.Stream = "stderr"
	for _, runtime := range []string{"docker", "podman"} {
		args, ok := containerLogArguments(runtime, "app", q)
		if !ok || !reflect.DeepEqual(args, []string{"logs", "--timestamps", "--tail", "20", "--since", q.Since, "--until", q.Until, "--follow", "--", "app"}) {
			t.Fatal(args, ok)
		}
	}
	for _, runtime := range []string{"sh", "/tmp/podman"} {
		if _, ok := containerLogArguments(runtime, "app", q); ok {
			t.Fatal("untrusted runtime accepted")
		}
	}
	for _, target := range []string{"", "--all", "app other", "app\n--details"} {
		if _, ok := containerLogArguments("docker", target, q); ok {
			t.Fatal("untrusted target accepted")
		}
	}
}
