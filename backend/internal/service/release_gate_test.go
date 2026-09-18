package service

import (
	"context"
	"errors"
	"net/http"
	"pki-certificate-rollover-impact/backend/internal/algorithm"
	"pki-certificate-rollover-impact/backend/internal/constants"
	"pki-certificate-rollover-impact/backend/internal/dto"
	"pki-certificate-rollover-impact/backend/internal/model"
	"pki-certificate-rollover-impact/backend/internal/repository"
	"pki-certificate-rollover-impact/backend/internal/util"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"
)

var gateActor = util.Actor{UserID: 7, Username: "operator", Role: string(constants.RolePKIOperator)}

func scenarioWithAffected(t *testing.T, db *gorm.DB, name, hash string, affected []algorithm.AffectedService) model.RolloverScenario {
	t.Helper()
	affectedJSON, err := encode(affected)
	if err != nil {
		t.Fatal(err)
	}
	scenario := minimalScenario(t, db, name, hash, "", "simulated", 7, 0)
	scenario.AffectedServicesJSON = affectedJSON
	return persistScenario(t, db, scenario)
}

func newGateService(db *gorm.DB) *RolloverScenarioService {
	return NewRolloverScenarioService(repository.NewRolloverScenarioRepository(db), nil, nil, nil, repository.NewAuditRepository(db), repository.NewTransactionManager(db))
}

func TestReadyTransitionBlockedByCriticalBreak(t *testing.T) {
	db := newScenarioTestDB(t)
	at := time.Date(2032, 5, 1, 10, 0, 0, 0, time.UTC)
	scenario := scenarioWithAffected(t, db, "blocked", "hash-blocked", []algorithm.AffectedService{
		{ServiceID: 11, ServiceCode: "PAYMENTS-API", Criticality: "critical", At: at, Reason: "trust set does not include anchor 2"},
		{ServiceID: 12, ServiceCode: "ORDER-WORKER", Criticality: "high", At: at, Reason: "upstream dependency PAYMENTS-API is unreachable"},
	})
	service := newGateService(db)
	_, err := service.Transition(context.Background(), scenario.ID, dto.RolloverScenarioTransitionRequest{ToState: string(constants.ScenarioReady)}, gateActor, "request-gate-blocked")
	var apiErr *util.APIError
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusConflict || apiErr.Code != util.CodeRiskGate {
		t.Fatalf("got %#v, want 409 %s", err, util.CodeRiskGate)
	}
	if !strings.Contains(apiErr.Message, "PAYMENTS-API") || !strings.Contains(apiErr.Message, at.Format(time.RFC3339)) {
		t.Fatalf("error must name blocked services and moments: %s", apiErr.Message)
	}
	stored, getErr := service.Get(context.Background(), scenario.ID)
	if getErr != nil {
		t.Fatal(getErr)
	}
	if stored.ScenarioState != "simulated" || stored.ReleaseGate.Status != algorithm.GateBlocked {
		t.Fatalf("scenario must stay simulated with a blocked gate: state=%s gate=%s", stored.ScenarioState, stored.ReleaseGate.Status)
	}
}

func TestReadyTransitionRequiresRiskAcceptance(t *testing.T) {
	db := newScenarioTestDB(t)
	at := time.Date(2032, 5, 2, 10, 0, 0, 0, time.UTC)
	scenario := scenarioWithAffected(t, db, "acceptance", "hash-acceptance", []algorithm.AffectedService{
		{ServiceID: 12, ServiceCode: "ORDER-WORKER", Criticality: "high", At: at, Reason: "chain is outside its validity window"},
	})
	audits := repository.NewAuditRepository(db)
	service := NewRolloverScenarioService(repository.NewRolloverScenarioRepository(db), nil, nil, nil, audits, repository.NewTransactionManager(db))
	_, err := service.Transition(context.Background(), scenario.ID, dto.RolloverScenarioTransitionRequest{ToState: string(constants.ScenarioReady)}, gateActor, "request-missing-acceptance")
	var apiErr *util.APIError
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusBadRequest || apiErr.Code != util.CodeValidation {
		t.Fatalf("got %#v, want 400 %s", err, util.CodeValidation)
	}
	note := "非关键断裂已通知业务方，风险由支付平台团队承担。"
	updated, err := service.Transition(context.Background(), scenario.ID, dto.RolloverScenarioTransitionRequest{ToState: string(constants.ScenarioReady), RiskAcceptance: "  " + note + "  "}, gateActor, "request-acceptance")
	if err != nil {
		t.Fatal(err)
	}
	if updated.ScenarioState != "ready" {
		t.Fatalf("got state %s want ready", updated.ScenarioState)
	}
	if updated.RiskAcceptanceNote != note || updated.RiskAcceptedBy == nil || *updated.RiskAcceptedBy != gateActor.UserID || updated.RiskAcceptedByName != gateActor.Username || updated.RiskAcceptedAt == nil {
		t.Fatalf("acceptance metadata incomplete: %+v", updated)
	}
	entries, total, err := audits.List(context.Background(), repository.AuditQuery{EntityType: "rollover_scenario", Action: "transition", Page: 1, PageSize: 10})
	if err != nil || total == 0 {
		t.Fatalf("expected transition audit entries: %v %d", err, total)
	}
	retained := false
	for _, entry := range entries {
		if strings.Contains(entry.PathSummary, note) {
			retained = true
		}
	}
	if !retained {
		t.Fatal("risk acceptance note must be retained with the audit trail")
	}
}

func TestReadyTransitionPassesWithoutBreaks(t *testing.T) {
	db := newScenarioTestDB(t)
	scenario := scenarioWithAffected(t, db, "clear", "hash-clear", []algorithm.AffectedService{})
	service := newGateService(db)
	updated, err := service.Transition(context.Background(), scenario.ID, dto.RolloverScenarioTransitionRequest{ToState: string(constants.ScenarioReady)}, gateActor, "request-clear")
	if err != nil {
		t.Fatal(err)
	}
	if updated.ScenarioState != "ready" || updated.ReleaseGate.Status != algorithm.GateClear {
		t.Fatalf("clean evidence should pass directly: state=%s gate=%s", updated.ScenarioState, updated.ReleaseGate.Status)
	}
	if updated.RiskAcceptanceNote != "" {
		t.Fatal("no acceptance note should be recorded for a clear gate")
	}
}

func TestResimulationClearsRecordedRiskAcceptance(t *testing.T) {
	db := newScenarioTestDB(t)
	now := time.Date(2032, 8, 1, 12, 0, 0, 0, time.UTC)
	snapshot := algorithm.NewSnapshot(
		algorithm.ScenarioConfig{Name: "regate", OldAnchorID: 1, NewAnchorID: 2, OverlapStart: now.Add(time.Hour), OverlapEnd: now.Add(2 * time.Hour), CandidateChainIDs: []uint{1}, SimulationTime: now.Add(90 * time.Minute)},
		[]algorithm.AnchorSnapshot{{ID: 1, Code: "OLD", State: "valid", NotBefore: now.Add(-time.Hour), NotAfter: now.Add(24 * time.Hour)}, {ID: 2, Code: "NEW", State: "valid", NotBefore: now.Add(-time.Hour), NotAfter: now.Add(24 * time.Hour)}},
		[]algorithm.ChainSnapshot{{ID: 1, Code: "CHAIN", AnchorID: 1, LeafSubject: "CN=leaf", ValidFrom: now.Add(-time.Hour), ValidTo: now.Add(24 * time.Hour), State: "validated", ValidationValid: true}},
		[]algorithm.ServiceSnapshot{{ID: 1, Code: "WORKER", ChainID: 1, TrustAnchorIDs: []uint{1}, Criticality: "high", State: "active"}},
	)
	raw, err := snapshot.Canonical()
	if err != nil {
		t.Fatal(err)
	}
	hash, err := snapshot.Hash()
	if err != nil {
		t.Fatal(err)
	}
	scenario := minimalScenario(t, db, "regate", hash, "", "draft", 7, 0)
	scenario.InputSnapshot = raw
	scenario = persistScenario(t, db, scenario)
	service := newGateService(db)
	if _, _, err := service.Simulate(context.Background(), scenario.ID, "regate-key-1", gateActor, "request-sim-1"); err != nil {
		t.Fatal(err)
	}
	updated, err := service.Transition(context.Background(), scenario.ID, dto.RolloverScenarioTransitionRequest{ToState: string(constants.ScenarioReady), RiskAcceptance: "non-critical break accepted by platform"}, gateActor, "request-ready")
	if err != nil {
		t.Fatal(err)
	}
	if updated.RiskAcceptanceNote == "" {
		t.Fatal("acceptance should be recorded before re-simulation")
	}
	if _, err := service.Transition(context.Background(), scenario.ID, dto.RolloverScenarioTransitionRequest{ToState: string(constants.ScenarioDraft)}, gateActor, "request-back-to-draft"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.Simulate(context.Background(), scenario.ID, "regate-key-2", gateActor, "request-sim-2"); err != nil {
		t.Fatal(err)
	}
	var stored model.RolloverScenario
	if err := db.First(&stored, scenario.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.RiskAcceptanceNote != "" || stored.RiskAcceptedBy != nil || stored.RiskAcceptedAt != nil {
		t.Fatalf("re-simulation must clear the recorded acceptance: %+v", stored)
	}
}
