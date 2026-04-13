package runtime

import (
	"testing"
	"time"
)

func TestStatus_DefaultsToNotReady(t *testing.T) {
	startedAt := time.Date(2026, 4, 7, 8, 0, 0, 0, time.UTC)

	status := NewStatus(startedAt)

	if status.Ready() {
		t.Fatal("expected new status to be not ready")
	}

	snapshot := status.Snapshot()
	if snapshot.Ready {
		t.Fatal("expected snapshot to report not ready")
	}
	if !snapshot.StartedAt.Equal(startedAt) {
		t.Fatalf("expected startedAt %s, got %s", startedAt, snapshot.StartedAt)
	}
}

func TestStatus_SetReadyUpdatesSnapshot(t *testing.T) {
	status := NewStatus(time.Date(2026, 4, 7, 8, 0, 0, 0, time.UTC))

	status.SetReady(true)

	if !status.Ready() {
		t.Fatal("expected status to report ready")
	}
	if !status.Snapshot().Ready {
		t.Fatal("expected snapshot to report ready")
	}
}
