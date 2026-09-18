package algorithm

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// Release gate decisions are derived solely from the frozen simulation result.
// The frontend never infers these values; it renders the backend decision.
const (
	GatePass                   = "pass"
	GateRiskAcceptanceRequired = "risk_acceptance_required"
	GateBlockedCritical        = "blocked_critical"
)

// CriticalityCritical marks a dependent service as business critical.
const CriticalityCritical = "critical"

type GateBlockedService struct {
	ServiceID   uint     `json:"service_id"`
	ServiceCode string   `json:"service_code"`
	Criticality string   `json:"criticality"`
	Times       []string `json:"times"`
	Reasons     []string `json:"reasons"`
}

type ReleaseGate struct {
	Decision             string               `json:"decision"`
	Summary              string               `json:"summary"`
	BrokenPathCount      int                  `json:"broken_path_count"`
	AffectedServiceCount int                  `json:"affected_service_count"`
	CriticalFailures     int                  `json:"critical_failures"`
	NonCriticalFailures  int                  `json:"non_critical_failures"`
	BlockedServices      []GateBlockedService `json:"blocked_services"`
}

// EvaluateReleaseGate applies the rotation release risk gate to a frozen
// simulation result. A single failure of a critical service at any timepoint
// blocks the "ready" transition; only non-critical breaks may proceed once a
// risk-acceptance note is supplied; a result without broken paths passes.
func EvaluateReleaseGate(result Result) ReleaseGate {
	gate := ReleaseGate{
		Decision:             GatePass,
		BrokenPathCount:      len(result.BrokenPaths),
		AffectedServiceCount: len(result.AffectedServices),
		BlockedServices:      []GateBlockedService{},
	}
	if len(result.AffectedServices) == 0 {
		gate.Summary = "冻结推演在所有关键时间点均未发现断裂路径，可直接标记待执行。"
		return gate
	}

	grouped := map[uint]*GateBlockedService{}
	order := []uint{}
	nonCriticalServices := map[uint]bool{}
	for _, affected := range result.AffectedServices {
		isCritical := affected.Criticality == CriticalityCritical
		if isCritical {
			entry, exists := grouped[affected.ServiceID]
			if !exists {
				entry = &GateBlockedService{ServiceID: affected.ServiceID, ServiceCode: affected.ServiceCode, Criticality: CriticalityCritical, Times: []string{}, Reasons: []string{}}
				grouped[affected.ServiceID] = entry
				order = append(order, affected.ServiceID)
			}
			entry.Times = appendUniqueTime(entry.Times, affected.At)
			if affected.Reason != "" {
				entry.Reasons = appendUnique(entry.Reasons, affected.Reason)
			}
		} else {
			nonCriticalServices[affected.ServiceID] = true
		}
	}

	for _, id := range order {
		entry := grouped[id]
		sort.Strings(entry.Times)
		gate.BlockedServices = append(gate.BlockedServices, *entry)
	}
	gate.CriticalFailures = len(gate.BlockedServices)
	gate.NonCriticalFailures = len(nonCriticalServices)

	if gate.CriticalFailures > 0 {
		gate.Decision = GateBlockedCritical
		gate.Summary = fmt.Sprintf("冻结推演发现 %d 个关键服务在交叠窗口前后的关键时间点存在断裂路径，禁止标记待执行；请先修复信任路径或调整轮换窗口。", gate.CriticalFailures)
		return gate
	}
	gate.Decision = GateRiskAcceptanceRequired
	gate.Summary = fmt.Sprintf("未发现关键服务断裂，但有 %d 个非关键服务存在断裂路径；填写风险接受说明后才可标记待执行，说明将随审计保留。", gate.NonCriticalFailures)
	return gate
}

// BlockedServiceCodes renders the blocked service codes for audit messages.
func (gate ReleaseGate) BlockedServiceCodes() string {
	codes := make([]string, 0, len(gate.BlockedServices))
	for _, entry := range gate.BlockedServices {
		codes = append(codes, entry.ServiceCode)
	}
	return strings.Join(codes, ", ")
}

func appendUniqueTime(values []string, at time.Time) []string {
	formatted := at.UTC().Format("2006-01-02T15:04:05Z")
	return appendUnique(values, formatted)
}

func appendUnique(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}
