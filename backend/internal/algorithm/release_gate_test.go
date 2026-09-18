package algorithm

import (
	"testing"
	"time"
)

func gateResult(criticality string, count int, at time.Time) Result {
	affected := make([]AffectedService, 0, count)
	paths := make([]BrokenPath, 0, count)
	for i := 0; i < count; i++ {
		affected = append(affected, AffectedService{
			ServiceID:   uint(100 + i),
			ServiceCode: "svc-" + string(rune('a'+i)),
			Criticality: criticality,
			At:          at,
			Reason:      "trust set does not include the replacement anchor",
		})
		paths = append(paths, BrokenPath{At: at, ServiceCodes: []string{"svc-" + string(rune('a'+i))}, Reason: "broken"})
	}
	return Result{AffectedServices: affected, BrokenPaths: paths}
}

func TestReleaseGatePassesWithoutBrokenPaths(t *testing.T) {
	gate := EvaluateReleaseGate(Result{AffectedServices: []AffectedService{}, BrokenPaths: []BrokenPath{}})
	if gate.Decision != GatePass {
		t.Fatalf("got %s want %s", gate.Decision, GatePass)
	}
}

func TestReleaseGateBlocksAnyCriticalFailure(t *testing.T) {
	at := time.Date(2026, 9, 2, 10, 0, 0, 0, time.UTC)
	gate := EvaluateReleaseGate(gateResult(CriticalityCritical, 2, at))
	if gate.Decision != GateBlockedCritical {
		t.Fatalf("got %s want %s", gate.Decision, GateBlockedCritical)
	}
	if gate.CriticalFailures != 2 || len(gate.BlockedServices) != 2 {
		t.Fatalf("expected 2 critical blocked services, got %+v", gate)
	}
	entry := gate.BlockedServices[0]
	if len(entry.Times) != 1 || entry.Times[0] != "2026-09-02T10:00:00Z" {
		t.Fatalf("blocked service must report the failure timepoint: %+v", entry)
	}
	if entry.ServiceCode == "" {
		t.Fatal("blocked service must carry its code for the blocking message")
	}
}

func TestReleaseGateRequiresAcceptanceForNonCriticalOnly(t *testing.T) {
	at := time.Date(2026, 9, 2, 10, 0, 0, 0, time.UTC)
	gate := EvaluateReleaseGate(gateResult("high", 1, at))
	if gate.Decision != GateRiskAcceptanceRequired {
		t.Fatalf("got %s want %s", gate.Decision, GateRiskAcceptanceRequired)
	}
	if gate.NonCriticalFailures != 1 || gate.CriticalFailures != 0 {
		t.Fatalf("expected one non-critical failure, got %+v", gate)
	}
	if len(gate.BlockedServices) != 0 {
		t.Fatalf("non-critical failures must not be listed as hard blockers: %+v", gate.BlockedServices)
	}
}

func TestReleaseGateCriticalDominatesMixedFailures(t *testing.T) {
	at := time.Date(2026, 9, 3, 8, 30, 0, 0, time.UTC)
	result := gateResult("low", 2, at)
	result.AffectedServices = append(result.AffectedServices, AffectedService{ServiceID: 200, ServiceCode: "core-payments", Criticality: CriticalityCritical, At: at, Reason: "anchor unavailable"})
	result.BrokenPaths = append(result.BrokenPaths, BrokenPath{At: at, ServiceCodes: []string{"core-payments"}, Reason: "anchor unavailable"})
	gate := EvaluateReleaseGate(result)
	if gate.Decision != GateBlockedCritical {
		t.Fatalf("one critical failure must block even when non-critical failures also exist, got %s", gate.Decision)
	}
}

func TestReleaseGateAggregatesTimepointsPerService(t *testing.T) {
	first := time.Date(2026, 9, 1, 23, 59, 0, 0, time.UTC)
	second := time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC)
	result := Result{AffectedServices: []AffectedService{
		{ServiceID: 1, ServiceCode: "core", Criticality: CriticalityCritical, At: first, Reason: "before overlap"},
		{ServiceID: 1, ServiceCode: "core", Criticality: CriticalityCritical, At: second, Reason: "after overlap"},
	}, BrokenPaths: []BrokenPath{{At: first}, {At: second}}}
	gate := EvaluateReleaseGate(result)
	if gate.CriticalFailures != 1 || len(gate.BlockedServices) != 1 {
		t.Fatalf("same service must aggregate into one blocker, got %+v", gate)
	}
	if len(gate.BlockedServices[0].Times) != 2 {
		t.Fatalf("blocker must report every broken timepoint: %+v", gate.BlockedServices[0])
	}
}
