package service

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"pki-certificate-rollover-impact/backend/internal/algorithm"
	"pki-certificate-rollover-impact/backend/internal/constants"
	"pki-certificate-rollover-impact/backend/internal/dto"
	"pki-certificate-rollover-impact/backend/internal/model"
	"pki-certificate-rollover-impact/backend/internal/repository"
	"pki-certificate-rollover-impact/backend/internal/util"
)

func newGateService(t *testing.T) (*RolloverScenarioService, model.RolloverScenario) {
	t.Helper()
	db := newScenarioTestDB(t)
	at := time.Date(2032, 4, 2, 7, 0, 0, 0, time.UTC)
	result := algorithm.Result{AffectedServices: []algorithm.AffectedService{{ServiceID: 51, ServiceCode: "CORE-API", Criticality: "critical", At: at, Reason: "trust set does not include the new anchor"}}, BrokenPaths: []algorithm.BrokenPath{{At: at, ServiceCodes: []string{"CORE-API"}, Reason: "trust set does not include the new anchor"}}}
	gate := algorithm.EvaluateReleaseGate(result)
	scenario := minimalScenario(t, db, "gate-critical", "gate-hash-critical", "gate-key-critical", "simulated", 7, 0)
	scenario.AffectedServicesJSON, _ = encode(result.AffectedServices)
	scenario.BrokenPathsJSON, _ = encode(result.BrokenPaths)
	scenario.ReleaseGateJSON, _ = encode(gate)
	scenario = persistScenario(t, db, scenario)
	svc := NewRolloverScenarioService(repository.NewRolloverScenarioRepository(db), nil, nil, nil, repository.NewAuditRepository(db), repository.NewTransactionManager(db))
	return svc, scenario
}

func TestCriticalBreakBlocksReadyEvenWithNote(t *testing.T) {
	svc, scenario := newGateService(t)
	_, err := svc.Transition(context.Background(), scenario.ID, dto.RolloverScenarioTransitionRequest{ToState: "ready", RiskAcceptance: "we accept this risk"}, operatorActor(), "request-gate-block")
	var apiErr *util.APIError
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusConflict || apiErr.Code != util.CodeRiskGateBlocked {
		t.Fatalf("got %#v, want 409 %s", err, util.CodeRiskGateBlocked)
	}
	if !strings.Contains(apiErr.Message, "CORE-API") {
		t.Fatalf("block message must identify the service: %s", apiErr.Message)
	}
}

func TestNonCriticalBreakRequiresRiskAcceptance(t *testing.T) {
	db := newScenarioTestDB(t)
	at := time.Date(2032, 4, 2, 7, 0, 0, 0, time.UTC)
	result := algorithm.Result{AffectedServices: []algorithm.AffectedService{{ServiceID: 61, ServiceCode: "BATCH-WORKER", Criticality: "low", At: at, Reason: "anchor unavailable"}}, BrokenPaths: []algorithm.BrokenPath{{At: at, ServiceCodes: []string{"BATCH-WORKER"}}}}
	gate := algorithm.EvaluateReleaseGate(result)
	scenario := minimalScenario(t, db, "gate-noncritical", "gate-hash-low", "gate-key-low", "simulated", 7, 0)
	scenario.AffectedServicesJSON, _ = encode(result.AffectedServices)
	scenario.BrokenPathsJSON, _ = encode(result.BrokenPaths)
	scenario.ReleaseGateJSON, _ = encode(gate)
	scenario = persistScenario(t, db, scenario)
	svc := NewRolloverScenarioService(repository.NewRolloverScenarioRepository(db), nil, nil, nil, repository.NewAuditRepository(db), repository.NewTransactionManager(db))

	_, err := svc.Transition(context.Background(), scenario.ID, dto.RolloverScenarioTransitionRequest{ToState: "ready"}, operatorActor(), "request-gate-missing-note")
	var apiErr *util.APIError
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusUnprocessableEntity || apiErr.Code != util.CodeRiskAcceptance {
		t.Fatalf("got %#v, want 422 %s", err, util.CodeRiskAcceptance)
	}

	response, err := svc.Transition(context.Background(), scenario.ID, dto.RolloverScenarioTransitionRequest{ToState: "ready", RiskAcceptance: "  非关键批处理在窗口内可重试，值班团队已确认接受该断裂风险。  "}, operatorActor(), "request-gate-accepted")
	if err != nil {
		t.Fatalf("risk acceptance should allow ready transition: %v", err)
	}
	if response.ScenarioState != "ready" {
		t.Fatalf("got state %s want ready", response.ScenarioState)
	}
	if response.RiskAcceptance == "" {
		t.Fatal("risk acceptance note must be returned after release")
	}

	var stored model.RolloverScenario
	if err := db.First(&stored, scenario.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.ScenarioState != "ready" || stored.RiskAcceptance == "" {
		t.Fatalf("risk acceptance must persist with the scenario: state=%s note=%q", stored.ScenarioState, stored.RiskAcceptance)
	}
	var auditEntry model.AuditLog
	if err := db.Where("entity_type = ? AND entity_id = ? AND action = ?", "rollover_scenario", scenario.ID, "transition").First(&auditEntry).Error; err != nil {
		t.Fatalf("risk acceptance must be retained in the audit trail: %v", err)
	}
}

func TestCleanSimulationPassesGateWithoutNote(t *testing.T) {
	db := newScenarioTestDB(t)
	gate := algorithm.EvaluateReleaseGate(algorithm.Result{AffectedServices: []algorithm.AffectedService{}})
	scenario := minimalScenario(t, db, "gate-pass", "gate-hash-pass", "gate-key-pass", "simulated", 7, 0)
	scenario.AffectedServicesJSON = "[]"
	scenario.BrokenPathsJSON = "[]"
	scenario.ReleaseGateJSON, _ = encode(gate)
	scenario = persistScenario(t, db, scenario)
	svc := NewRolloverScenarioService(repository.NewRolloverScenarioRepository(db), nil, nil, nil, repository.NewAuditRepository(db), repository.NewTransactionManager(db))
	response, err := svc.Transition(context.Background(), scenario.ID, dto.RolloverScenarioTransitionRequest{ToState: "ready"}, operatorActor(), "request-gate-pass")
	if err != nil {
		t.Fatalf("a result without broken paths must pass directly: %v", err)
	}
	if response.ScenarioState != "ready" || response.ReleaseGate.Decision != algorithm.GatePass {
		t.Fatalf("unexpected ready response: state=%s gate=%s", response.ScenarioState, response.ReleaseGate.Decision)
	}
}

func TestGateDoesNotAffectOtherTransitions(t *testing.T) {
	svc, scenario := newGateService(t)
	// simulated -> draft must remain legal even with a critical break.
	_, err := svc.Transition(context.Background(), scenario.ID, dto.RolloverScenarioTransitionRequest{ToState: "draft"}, operatorActor(), "request-gate-back-to-draft")
	if err != nil {
		t.Fatalf("simulated -> draft must stay allowed: %v", err)
	}
}

func operatorActor() util.Actor {
	return util.Actor{UserID: 7, Username: "operator", Role: string(constants.RolePKIOperator)}
}
