package algorithm

import (
	"strings"
	"testing"
	"time"
)

func gateAffected() []AffectedService {
	base := time.Date(2026, 10, 1, 8, 0, 0, 0, time.UTC)
	return []AffectedService{
		{ServiceID: 102, ServiceCode: "worker", Criticality: "high", At: base.Add(2 * time.Hour), Reason: "upstream dependency api is unreachable"},
		{ServiceID: 101, ServiceCode: "api", Criticality: "critical", At: base.Add(time.Hour), Reason: "trust set does not include anchor 2"},
		{ServiceID: 101, ServiceCode: "api", Criticality: "critical", At: base.Add(3 * time.Hour), Reason: "trust set does not include anchor 2"},
	}
}

func TestReleaseGateBlocksOnCriticalBreaks(t *testing.T) {
	gate := EvaluateReleaseGate(gateAffected(), true)
	if gate.Status != GateBlocked {
		t.Fatalf("got %s want %s", gate.Status, GateBlocked)
	}
	if len(gate.CriticalBreaks) != 2 || len(gate.NonCriticalBreaks) != 1 {
		t.Fatalf("unexpected break split: %+v", gate)
	}
	if !gate.CriticalBreaks[0].At.Before(gate.CriticalBreaks[1].At) {
		t.Fatal("critical breaks must be ordered by timepoint")
	}
	detail := gate.BlockedDetail()
	if !strings.Contains(detail, "api at 2026-10-01T09:00:00Z") || !strings.Contains(detail, "api at 2026-10-01T11:00:00Z") {
		t.Fatalf("blocked detail must name services and moments: %s", detail)
	}
	if !strings.Contains(gate.Summary, "2 critical") {
		t.Fatalf("summary should count critical breaks: %s", gate.Summary)
	}
}

func TestReleaseGateRequiresAcceptanceForNonCriticalBreaks(t *testing.T) {
	affected := gateAffected()
	gate := EvaluateReleaseGate(affected[1:], true)
	if gate.Status != GateBlocked {
		t.Fatalf("expected remaining critical breaks to block: %s", gate.Status)
	}
	gate = EvaluateReleaseGate(affected[:1], true)
	if gate.Status != GateAcceptanceRequired {
		t.Fatalf("got %s want %s", gate.Status, GateAcceptanceRequired)
	}
	if len(gate.CriticalBreaks) != 0 || len(gate.NonCriticalBreaks) != 1 {
		t.Fatalf("unexpected break split: %+v", gate)
	}
	if gate.NonCriticalBreaks[0].ServiceCode != "worker" {
		t.Fatalf("unexpected non-critical break: %+v", gate.NonCriticalBreaks[0])
	}
}

func TestReleaseGateClearAndPending(t *testing.T) {
	gate := EvaluateReleaseGate(nil, true)
	if gate.Status != GateClear {
		t.Fatalf("got %s want %s", gate.Status, GateClear)
	}
	gate = EvaluateReleaseGate(gateAffected(), false)
	if gate.Status != GateNotSimulated {
		t.Fatalf("got %s want %s", gate.Status, GateNotSimulated)
	}
	if len(gate.CriticalBreaks) != 0 || len(gate.NonCriticalBreaks) != 0 {
		t.Fatal("pending gate must not classify breaks")
	}
}

func TestReleaseGateIsDeterministic(t *testing.T) {
	first := EvaluateReleaseGate(gateAffected(), true)
	second := EvaluateReleaseGate(gateAffected(), true)
	if first.Status != second.Status || first.Summary != second.Summary || first.BlockedDetail() != second.BlockedDetail() {
		t.Fatal("gate evaluation must be deterministic")
	}
}
