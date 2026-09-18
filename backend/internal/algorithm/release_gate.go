package algorithm

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// Release gate statuses derived from a frozen simulation result. The gate is
// a pure function of the stored evidence so the API, the audit trail, and the
// rollover page always agree on the verdict.
const (
	GateNotSimulated       = "not_simulated"
	GateClear              = "clear"
	GateAcceptanceRequired = "acceptance_required"
	GateBlocked            = "blocked"
)

// CriticalityLevelCritical marks services whose broken paths block release.
const CriticalityLevelCritical = "critical"

// GateBreak is one blocked service at one evaluated timepoint.
type GateBreak struct {
	ServiceID   uint      `json:"service_id"`
	ServiceCode string    `json:"service_code"`
	At          time.Time `json:"at"`
	Reason      string    `json:"reason"`
}

// ReleaseGate is the release-readiness verdict for a simulated scenario.
type ReleaseGate struct {
	Status            string      `json:"status"`
	CriticalBreaks    []GateBreak `json:"critical_breaks"`
	NonCriticalBreaks []GateBreak `json:"non_critical_breaks"`
	Summary           string      `json:"summary"`
}

// EvaluateReleaseGate classifies simulated affected services into a release
// verdict: any critical break blocks marking ready, non-critical breaks
// require a recorded risk acceptance, and a clean result passes directly.
func EvaluateReleaseGate(affected []AffectedService, simulated bool) ReleaseGate {
	gate := ReleaseGate{Status: GateNotSimulated, CriticalBreaks: []GateBreak{}, NonCriticalBreaks: []GateBreak{}}
	if !simulated {
		gate.Summary = "Release gate is pending: the frozen input has not been simulated yet."
		return gate
	}
	for _, item := range affected {
		entry := GateBreak{ServiceID: item.ServiceID, ServiceCode: item.ServiceCode, At: item.At.UTC(), Reason: item.Reason}
		if item.Criticality == CriticalityLevelCritical {
			gate.CriticalBreaks = append(gate.CriticalBreaks, entry)
		} else {
			gate.NonCriticalBreaks = append(gate.NonCriticalBreaks, entry)
		}
	}
	orderBreaks(gate.CriticalBreaks)
	orderBreaks(gate.NonCriticalBreaks)
	switch {
	case len(gate.CriticalBreaks) > 0:
		gate.Status = GateBlocked
		gate.Summary = fmt.Sprintf("Release gate blocked: %d critical service-timepoint breaks must be resolved before the scenario can be marked ready.", len(gate.CriticalBreaks))
	case len(gate.NonCriticalBreaks) > 0:
		gate.Status = GateAcceptanceRequired
		gate.Summary = fmt.Sprintf("Release gate requires a recorded risk acceptance for %d non-critical service-timepoint breaks.", len(gate.NonCriticalBreaks))
	default:
		gate.Status = GateClear
		gate.Summary = "Release gate clear: no broken trust paths at any evaluated timepoint."
	}
	return gate
}

// BlockedDetail lists the blocking services and moments for error reporting.
func (gate ReleaseGate) BlockedDetail() string {
	parts := make([]string, 0, len(gate.CriticalBreaks))
	for _, item := range gate.CriticalBreaks {
		parts = append(parts, item.ServiceCode+" at "+item.At.Format(time.RFC3339))
	}
	return strings.Join(parts, "; ")
}

func orderBreaks(items []GateBreak) {
	sort.Slice(items, func(i, j int) bool {
		if !items[i].At.Equal(items[j].At) {
			return items[i].At.Before(items[j].At)
		}
		return items[i].ServiceID < items[j].ServiceID
	})
}
