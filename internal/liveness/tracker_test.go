package liveness

import (
	"testing"
	"time"
)

func TestTrackerTransitionsAndRecovers(t *testing.T) {
	registeredAt := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	tracker, err := NewTracker(Policy{HeartbeatInterval: 5 * time.Second, DegradedAfter: 1, OfflineAfter: 3}, registeredAt)
	if err != nil {
		t.Fatalf("NewTracker() error = %v", err)
	}

	assertState(t, tracker.Evaluate(registeredAt.Add(4*time.Second)), NodeOnline, false)
	assertState(t, tracker.Evaluate(registeredAt.Add(5*time.Second)), NodeDegraded, true)
	assertState(t, tracker.Evaluate(registeredAt.Add(10*time.Second)), NodeDegraded, false)
	assertState(t, tracker.Evaluate(registeredAt.Add(15*time.Second)), NodeOffline, true)
	assertState(t, tracker.RecordActivity(registeredAt.Add(16*time.Second)), NodeOnline, true)
}

func TestPolicyRejectsAmbiguousThresholds(t *testing.T) {
	for _, policy := range []Policy{
		{},
		{HeartbeatInterval: time.Second, DegradedAfter: 1, OfflineAfter: 1},
	} {
		if err := policy.Validate(); err == nil {
			t.Fatalf("Validate() error = nil for %#v", policy)
		}
	}
}

func assertState(t *testing.T, snapshot Snapshot, want NodeState, changed bool) {
	t.Helper()
	if snapshot.State != want || snapshot.Changed != changed {
		t.Fatalf("snapshot = %#v, want state %q and changed %t", snapshot, want, changed)
	}
}
