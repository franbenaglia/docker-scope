package store

import (
	"errors"
	"testing"
	"time"

	"github.com/franbenaglia/docker-scope/internal/alerts"
)

func TestSeededRulesAndRuleCRUD(t *testing.T) {
	s := open(t, t.TempDir())
	rules, err := s.ListRules(ctx)
	if err != nil || len(rules) != 4 {
		t.Fatalf("reglas sembradas = %d, %v", len(rules), err)
	}
	for _, r := range rules {
		if err := r.Validate(); err != nil {
			t.Errorf("regla sembrada inválida %q: %v", r.Name, err)
		}
		if r.CreatedAt.Year() < 2020 {
			t.Errorf("created_at sembrado = %s", r.CreatedAt)
		}
	}
	r := alerts.Rule{Name: "api caída", Kind: alerts.NotRunning, Enabled: true, Scope: alerts.Scope{Container: "api"},
		For: 10 * time.Second, Cooldown: time.Minute, CreatedAt: t0, UpdatedAt: t0}
	if err := s.InsertRule(ctx, &r); err != nil || r.ID == 0 {
		t.Fatalf("insert: %v", err)
	}
	r.Threshold, r.Enabled = 0, false
	if err := s.UpdateRule(ctx, &r); err != nil {
		t.Fatal(err)
	}
	rules, _ = s.ListRules(ctx)
	got := rules[len(rules)-1]
	if got.Scope.Container != "api" || got.For != 10*time.Second || got.Enabled || !got.CreatedAt.Equal(t0) {
		t.Errorf("round-trip: %+v", got)
	}
	if err := s.DeleteRule(ctx, r.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteRule(ctx, r.ID); !errors.Is(err, alerts.ErrNotFound) {
		t.Errorf("borrar dos veces: %v", err)
	}
}

func TestIncidentsPersistence(t *testing.T) {
	s := open(t, t.TempDir())
	peak := 91.5
	inc := alerts.Incident{RuleID: 1, RuleName: "u", Kind: alerts.Unhealthy, ContainerID: "c1", ContainerName: "web",
		State: alerts.Open, StartedAt: t0, OpenedAt: t0.Add(30 * time.Second), Summary: "s", Cause: "c", Peak: &peak, Suppressed: true}
	if err := s.InsertIncident(ctx, &inc); err != nil || inc.ID == 0 {
		t.Fatal(err)
	}
	dup := inc
	if err := s.InsertIncident(ctx, &dup); err == nil {
		t.Error("el índice único impide dos incidentes abiertos para la misma regla y contenedor")
	}
	open, err := s.OpenIncidents(ctx)
	if err != nil || len(open) != 1 || !open[0].Suppressed || *open[0].Peak != 91.5 {
		t.Fatalf("abiertos = %+v, %v", open, err)
	}
	now := t0.Add(time.Minute)
	inc.State, inc.ResolvedAt, inc.AckedAt, inc.Resolution = alerts.Resolved, &now, &now, "recuperado"
	if err := s.UpdateIncident(ctx, &inc); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetIncident(ctx, inc.ID)
	if err != nil || got.State != alerts.Resolved || !got.ResolvedAt.Equal(now) || got.Resolution != "recuperado" {
		t.Errorf("get = %+v, %v", got, err)
	}
	if _, err := s.GetIncident(ctx, 999); !errors.Is(err, alerts.ErrNotFound) {
		t.Error("inexistente")
	}
	// After resolving, a new open incident for the same pair is allowed.
	again := inc
	again.ID, again.State, again.ResolvedAt = 0, alerts.Open, nil
	again.OpenedAt = t0.Add(2 * time.Hour)
	if err := s.InsertIncident(ctx, &again); err != nil {
		t.Fatal(err)
	}
	list, total, err := s.ListIncidents(ctx, alerts.IncidentQuery{Limit: 1})
	if err != nil || total != 2 || len(list) != 1 || list[0].ID != again.ID {
		t.Errorf("lista (más reciente primero): total=%d %+v", total, list)
	}
	list, total, _ = s.ListIncidents(ctx, alerts.IncidentQuery{State: alerts.Resolved, ContainerID: "c1"})
	if total != 1 || list[0].ID != inc.ID {
		t.Errorf("filtro: %d", total)
	}
}

func TestMaintenanceAndIncidentRetention(t *testing.T) {
	s := open(t, t.TempDir())
	m := alerts.Maintenance{Container: "web", Until: t0.Add(time.Hour), Reason: "deploy", CreatedAt: t0}
	if err := s.InsertMaintenance(ctx, &m); err != nil {
		t.Fatal(err)
	}
	if act, _ := s.ListMaintenance(ctx, t0); len(act) != 1 || act[0].Reason != "deploy" {
		t.Errorf("activos = %+v", act)
	}
	if act, _ := s.ListMaintenance(ctx, t0.Add(2*time.Hour)); len(act) != 0 {
		t.Error("vencido no se lista")
	}
	old := t0.Add(-100 * 24 * time.Hour)
	inc := alerts.Incident{RuleID: 1, RuleName: "u", Kind: alerts.Unhealthy, ContainerID: "c1", ContainerName: "web",
		State: alerts.Resolved, StartedAt: old, OpenedAt: old, ResolvedAt: &old, Summary: "s"}
	if err := s.InsertIncident(ctx, &inc); err != nil {
		t.Fatal(err)
	}
	res, err := s.Prune(ctx, t0.Add(3*24*time.Hour), Retention{Raw: time.Hour, Rollups: 7 * 24 * time.Hour})
	if err != nil || res.Incidents != 1 {
		t.Errorf("prune incidentes = %+v, %v", res, err)
	}
	if act, _ := s.ListMaintenance(ctx, t0); len(act) != 0 {
		t.Error("mantenimiento vencido hace más de 24h se borra")
	}
}
