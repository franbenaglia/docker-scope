package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/franbenaglia/docker-scope/internal/alerts"
)

// Alerts is the alert engine as seen by the API.
type Alerts interface {
	Rules() []alerts.Rule
	CreateRule(ctx context.Context, r alerts.Rule) (alerts.Rule, error)
	UpdateRule(ctx context.Context, r alerts.Rule) (alerts.Rule, error)
	DeleteRule(ctx context.Context, id int64) error
	Ack(ctx context.Context, id int64) (alerts.Incident, error)
	AddMaintenance(ctx context.Context, container string, until time.Time, reason string) (alerts.Maintenance, error)
	EndMaintenance(ctx context.Context, id int64) error
	Incidents(ctx context.Context, q alerts.IncidentQuery) ([]alerts.Incident, int, error)
	Pending() []alerts.Pending
	Status() alerts.Status
}

// --- DTOs ---

// ScopeDTO selects containers; empty fields match anything. container is a name.
type ScopeDTO struct {
	Container string `json:"container"`
	Project   string `json:"project"`
	Service   string `json:"service"`
}

// RuleInput is the body of POST /api/rules and PUT /api/rules/{id}.
type RuleInput struct {
	Name    string   `json:"name"`
	Kind    string   `json:"kind"`
	Enabled bool     `json:"enabled"`
	Scope   ScopeDTO `json:"scope"`
	// Threshold: cpu → % of one core (100 = 1 core); memory → % of the
	// effective limit; restarts → count.
	Threshold       float64 `json:"threshold"`
	ForSeconds      float64 `json:"for_seconds"`
	WindowSeconds   float64 `json:"window_seconds"`
	CooldownSeconds float64 `json:"cooldown_seconds"`
	RecoverSeconds  float64 `json:"recover_seconds"`
	Hysteresis      float64 `json:"hysteresis"`
}

// RuleDTO is a rule.
type RuleDTO struct {
	ID int64 `json:"id"`
	RuleInput
	ThresholdUnit string    `json:"threshold_unit"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// IncidentDTO is an incident.
type IncidentDTO struct {
	ID            int64      `json:"id"`
	RuleID        int64      `json:"rule_id"`
	RuleName      string     `json:"rule_name"`
	Kind          string     `json:"kind"`
	ContainerID   string     `json:"container_id"`
	ContainerName string     `json:"container_name"`
	State         string     `json:"state"` // open | resolved
	StartedAt     time.Time  `json:"started_at"`
	OpenedAt      time.Time  `json:"opened_at"`
	ResolvedAt    *time.Time `json:"resolved_at"`
	AckedAt       *time.Time `json:"acked_at"` // acknowledging does not resolve
	Suppressed    bool       `json:"suppressed"`
	Summary       string     `json:"summary"`
	Cause         string     `json:"cause"` // probable cause
	Resolution    string     `json:"resolution"`
	Peak          *float64   `json:"peak"`
}

// PendingDTO is a condition that holds without an incident yet.
type PendingDTO struct {
	RuleID        int64     `json:"rule_id"`
	RuleName      string    `json:"rule_name"`
	Kind          string    `json:"kind"`
	ContainerID   string    `json:"container_id"`
	ContainerName string    `json:"container_name"`
	Since         time.Time `json:"since"`
	OpensAt       time.Time `json:"opens_at"`
	InCooldown    bool      `json:"in_cooldown"`
	Value         *float64  `json:"value"`
}

// MaintenanceInput is the body of POST /api/maintenance: either
// duration_seconds or until (RFC 3339). container "" = global.
type MaintenanceInput struct {
	Container       string     `json:"container"`
	DurationSeconds float64    `json:"duration_seconds"`
	Until           *time.Time `json:"until"`
	Reason          string     `json:"reason"`
}

// MaintenanceDTO is an active maintenance window.
type MaintenanceDTO struct {
	ID        int64     `json:"id"`
	Container string    `json:"container"` // "" = global
	Global    bool      `json:"global"`
	Until     time.Time `json:"until"`
	Reason    string    `json:"reason"`
	CreatedAt time.Time `json:"created_at"`
}

// AlertsSummaryDTO is part of SummaryDTO.
type AlertsSummaryDTO struct {
	Open        int              `json:"open"`
	Unacked     int              `json:"unacked"`
	Pending     int              `json:"pending"`
	Maintenance []MaintenanceDTO `json:"maintenance"`
}

// IncidentEventDTO is the data of the SSE "incident" event.
type IncidentEventDTO struct {
	Type     string      `json:"type"` // opened | resolved | acked
	Incident IncidentDTO `json:"incident"`
}

func secs(f float64) time.Duration { return time.Duration(f * float64(time.Second)) }

func (in RuleInput) rule(id int64) alerts.Rule {
	return alerts.Rule{
		ID: id, Name: in.Name, Kind: alerts.Kind(in.Kind), Enabled: in.Enabled,
		Scope:     alerts.Scope{Container: in.Scope.Container, Project: in.Scope.Project, Service: in.Scope.Service},
		Threshold: in.Threshold, For: secs(in.ForSeconds), Window: secs(in.WindowSeconds),
		Cooldown: secs(in.CooldownSeconds), RecoverFor: secs(in.RecoverSeconds), Hysteresis: in.Hysteresis,
	}
}

func ruleDTO(r alerts.Rule) RuleDTO {
	unit := ""
	switch r.Kind {
	case alerts.CPU:
		unit = "% de un núcleo (100 = 1 núcleo)"
	case alerts.Memory:
		unit = "% del límite de memoria (o de la memoria del host si no hay límite)"
	case alerts.Restarts:
		unit = "reinicios observados en la ventana"
	}
	return RuleDTO{
		ID: r.ID, ThresholdUnit: unit, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
		RuleInput: RuleInput{
			Name: r.Name, Kind: string(r.Kind), Enabled: r.Enabled,
			Scope:     ScopeDTO{Container: r.Scope.Container, Project: r.Scope.Project, Service: r.Scope.Service},
			Threshold: r.Threshold, ForSeconds: r.For.Seconds(), WindowSeconds: r.Window.Seconds(),
			CooldownSeconds: r.Cooldown.Seconds(), RecoverSeconds: r.RecoverFor.Seconds(), Hysteresis: r.Hysteresis,
		},
	}
}

func incidentDTO(i alerts.Incident) IncidentDTO {
	return IncidentDTO{
		ID: i.ID, RuleID: i.RuleID, RuleName: i.RuleName, Kind: string(i.Kind), ContainerID: i.ContainerID,
		ContainerName: i.ContainerName, State: string(i.State), StartedAt: i.StartedAt, OpenedAt: i.OpenedAt,
		ResolvedAt: i.ResolvedAt, AckedAt: i.AckedAt, Suppressed: i.Suppressed, Summary: i.Summary,
		Cause: i.Cause, Resolution: i.Resolution, Peak: i.Peak,
	}
}

func maintenanceDTO(m alerts.Maintenance) MaintenanceDTO {
	return MaintenanceDTO{ID: m.ID, Container: m.Container, Global: m.Container == "", Until: m.Until,
		Reason: m.Reason, CreatedAt: m.CreatedAt}
}

func (s *Server) alertsSummary() *AlertsSummaryDTO {
	if s.Alerts == nil {
		return nil
	}
	st := s.Alerts.Status()
	out := &AlertsSummaryDTO{Open: st.Open, Unacked: st.Unacked, Pending: st.Pending, Maintenance: []MaintenanceDTO{}}
	for _, m := range st.Maintenance {
		out.Maintenance = append(out.Maintenance, maintenanceDTO(m))
	}
	return out
}

// --- handlers ---

func (s *Server) routeAlerts(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/rules", s.handleListRules)
	mux.HandleFunc("POST /api/rules", s.handleCreateRule)
	mux.HandleFunc("PUT /api/rules/{id}", s.handleUpdateRule)
	mux.HandleFunc("DELETE /api/rules/{id}", s.handleDeleteRule)
	mux.HandleFunc("GET /api/incidents", s.handleListIncidents)
	mux.HandleFunc("GET /api/incidents/pending", s.handlePending)
	mux.HandleFunc("POST /api/incidents/{id}/ack", s.handleAck)
	mux.HandleFunc("GET /api/maintenance", s.handleListMaintenance)
	mux.HandleFunc("POST /api/maintenance", s.handleAddMaintenance)
	mux.HandleFunc("DELETE /api/maintenance/{id}", s.handleEndMaintenance)
}

func (s *Server) handleListRules(w http.ResponseWriter, r *http.Request) {
	items := []RuleDTO{}
	for _, rule := range s.Alerts.Rules() {
		items = append(items, ruleDTO(rule))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) handleCreateRule(w http.ResponseWriter, r *http.Request) {
	var in RuleInput
	if !decodeBody(w, r, &in) {
		return
	}
	rule, err := s.Alerts.CreateRule(r.Context(), in.rule(0))
	if err != nil {
		s.alertsError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, ruleDTO(rule))
}

func (s *Server) handleUpdateRule(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var in RuleInput
	if !decodeBody(w, r, &in) {
		return
	}
	rule, err := s.Alerts.UpdateRule(r.Context(), in.rule(id))
	if err != nil {
		s.alertsError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, ruleDTO(rule))
}

func (s *Server) handleDeleteRule(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := s.Alerts.DeleteRule(r.Context(), id); err != nil {
		s.alertsError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleListIncidents(w http.ResponseWriter, r *http.Request) {
	v := r.URL.Query()
	q := alerts.IncidentQuery{Limit: 50}
	switch st := v.Get("state"); st {
	case "", "all":
	case "open", "resolved":
		q.State = alerts.State(st)
	default:
		writeError(w, http.StatusBadRequest, "invalid_parameter", "state debe ser open, resolved o all")
		return
	}
	if c := v.Get("container"); c != "" {
		if !idRe.MatchString(c) {
			writeError(w, http.StatusBadRequest, "invalid_parameter", "container debe ser un ID de contenedor")
			return
		}
		q.ContainerID = c
	}
	if l := v.Get("limit"); l != "" {
		n, err := strconv.Atoi(l)
		if err != nil || n < 1 || n > 200 {
			writeError(w, http.StatusBadRequest, "invalid_parameter", "limit debe estar entre 1 y 200")
			return
		}
		q.Limit = n
	}
	if o := v.Get("offset"); o != "" {
		n, err := strconv.Atoi(o)
		if err != nil || n < 0 {
			writeError(w, http.StatusBadRequest, "invalid_parameter", "offset debe ser un entero ≥ 0")
			return
		}
		q.Offset = n
	}
	list, total, err := s.Alerts.Incidents(r.Context(), q)
	if err != nil {
		s.internalError(w, err)
		return
	}
	items := make([]IncidentDTO, 0, len(list))
	for _, i := range list {
		items = append(items, incidentDTO(i))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "total": total, "limit": q.Limit, "offset": q.Offset})
}

func (s *Server) handlePending(w http.ResponseWriter, r *http.Request) {
	items := []PendingDTO{}
	for _, p := range s.Alerts.Pending() {
		items = append(items, PendingDTO{RuleID: p.RuleID, RuleName: p.RuleName, Kind: string(p.Kind),
			ContainerID: p.ContainerID, ContainerName: p.ContainerName, Since: p.Since, OpensAt: p.OpensAt,
			InCooldown: p.InCooldown, Value: p.Value})
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) handleAck(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	inc, err := s.Alerts.Ack(r.Context(), id)
	if err != nil {
		s.alertsError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, incidentDTO(inc))
}

func (s *Server) handleListMaintenance(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"items": s.alertsSummary().Maintenance})
}

func (s *Server) handleAddMaintenance(w http.ResponseWriter, r *http.Request) {
	var in MaintenanceInput
	if !decodeBody(w, r, &in) {
		return
	}
	var until time.Time
	switch {
	case in.Until != nil && in.DurationSeconds != 0:
		writeError(w, http.StatusBadRequest, "validation_error", "use duration_seconds o until, no ambos")
		return
	case in.Until != nil:
		until = *in.Until
	case in.DurationSeconds > 0:
		until = s.Now().Add(secs(in.DurationSeconds))
	default:
		writeError(w, http.StatusBadRequest, "validation_error", "falta duration_seconds (> 0) o until")
		return
	}
	m, err := s.Alerts.AddMaintenance(r.Context(), in.Container, until, in.Reason)
	if err != nil {
		s.alertsError(w, err)
		return
	}
	s.publish(EvSummary, s.summary())
	writeJSON(w, http.StatusCreated, maintenanceDTO(m))
}

func (s *Server) handleEndMaintenance(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := s.Alerts.EndMaintenance(r.Context(), id); err != nil {
		s.alertsError(w, err)
		return
	}
	s.publish(EvSummary, s.summary())
	w.WriteHeader(http.StatusNoContent)
}

// --- helpers ---

const maxBody = 64 << 10

// decodeBody reads a JSON body strictly (unknown fields rejected, 64 KiB max).
func decodeBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			writeError(w, http.StatusRequestEntityTooLarge, "body_too_large", "el cuerpo supera 64 KiB")
		} else {
			writeError(w, http.StatusBadRequest, "invalid_json", "JSON inválido: "+err.Error())
		}
		return false
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "invalid_json", "el cuerpo debe contener un único objeto JSON")
		return false
	}
	return true
}

func pathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid_parameter", "id inválido")
		return 0, false
	}
	return id, true
}

func (s *Server) alertsError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, alerts.ErrValidation):
		writeError(w, http.StatusBadRequest, "validation_error", err.Error())
	case errors.Is(err, alerts.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "no encontrado")
	default:
		s.internalError(w, err)
	}
}
