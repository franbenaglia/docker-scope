package alerts

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// CreateRule validates and stores a new rule.
func (e *Engine) CreateRule(ctx context.Context, r Rule) (Rule, error) {
	r.Name = strings.TrimSpace(r.Name)
	if err := r.Validate(); err != nil {
		return Rule{}, err
	}
	now := e.opts.Now().UTC()
	r.ID, r.CreatedAt, r.UpdatedAt = 0, now, now
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.store.InsertRule(ctx, &r); err != nil {
		return Rule{}, err
	}
	e.rules[r.ID] = r
	return r, nil
}

// UpdateRule replaces a rule. Open incidents of the rule are resolved
// ("regla modificada") and its pending conditions restart, so incidents
// always reflect the rule that opened them.
func (e *Engine) UpdateRule(ctx context.Context, r Rule) (Rule, error) {
	r.Name = strings.TrimSpace(r.Name)
	if err := r.Validate(); err != nil {
		return Rule{}, err
	}
	var events []Event
	defer func() {
		for _, ev := range events {
			e.emit(ev)
		}
	}()
	e.mu.Lock()
	defer e.mu.Unlock()
	old, ok := e.rules[r.ID]
	if !ok {
		return Rule{}, ErrNotFound
	}
	r.CreatedAt, r.UpdatedAt = old.CreatedAt, e.opts.Now().UTC()
	if err := e.store.UpdateRule(ctx, &r); err != nil {
		return Rule{}, err
	}
	e.rules[r.ID] = r
	events = e.dropRuleTracks(ctx, r.ID, "regla modificada")
	return r, nil
}

// DeleteRule removes a rule, resolving its open incidents ("regla eliminada").
// Incident history is kept.
func (e *Engine) DeleteRule(ctx context.Context, id int64) error {
	var events []Event
	defer func() {
		for _, ev := range events {
			e.emit(ev)
		}
	}()
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, ok := e.rules[id]; !ok {
		return ErrNotFound
	}
	if err := e.store.DeleteRule(ctx, id); err != nil {
		return err
	}
	delete(e.rules, id)
	events = e.dropRuleTracks(ctx, id, "regla eliminada")
	return nil
}

func (e *Engine) dropRuleTracks(ctx context.Context, id int64, resolution string) []Event {
	var events []Event
	now := e.opts.Now()
	for k, t := range e.tracks {
		if k.rule != id {
			continue
		}
		e.resolveTrack(ctx, k, t, now, resolution, func(ev Event) { events = append(events, ev) })
		delete(e.tracks, k) // no cooldown carried over to the new definition
	}
	return events
}

// Ack acknowledges an incident. It does not resolve it.
func (e *Engine) Ack(ctx context.Context, id int64) (Incident, error) {
	e.mu.Lock()
	inc, err := e.store.GetIncident(ctx, id)
	if err != nil {
		e.mu.Unlock()
		return Incident{}, err
	}
	if inc.AckedAt != nil {
		e.mu.Unlock()
		return inc, nil // idempotent
	}
	now := e.opts.Now().UTC()
	inc.AckedAt = &now
	if err := e.store.UpdateIncident(ctx, &inc); err != nil {
		e.mu.Unlock()
		return Incident{}, err
	}
	if t, ok := e.tracks[key{inc.RuleID, inc.ContainerID}]; ok && t.incident != nil && t.incident.ID == id {
		t.incident.AckedAt = &now
	}
	e.mu.Unlock()
	e.emit(Event{Type: EvAcked, Incident: inc})
	return inc, nil
}

// Maintenance limits.
const maxMaintenance = 30 * 24 * time.Hour

// AddMaintenance starts a maintenance window: global if container is "".
// Collection and evaluation continue; incidents opened during it are marked
// suppressed and do not notify.
func (e *Engine) AddMaintenance(ctx context.Context, container string, until time.Time, reason string) (Maintenance, error) {
	now := e.opts.Now().UTC()
	reason = strings.TrimSpace(reason)
	switch {
	case !until.After(now):
		return Maintenance{}, fmt.Errorf("%w: until debe ser posterior a ahora", ErrValidation)
	case until.Sub(now) > maxMaintenance:
		return Maintenance{}, fmt.Errorf("%w: el mantenimiento puede durar hasta 30 días", ErrValidation)
	case len(container) > 255 || len(reason) > 500:
		return Maintenance{}, fmt.Errorf("%w: container (máx. 255) o reason (máx. 500) demasiado largos", ErrValidation)
	}
	m := Maintenance{Container: container, Until: until.UTC(), Reason: reason, CreatedAt: now}
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.store.InsertMaintenance(ctx, &m); err != nil {
		return Maintenance{}, err
	}
	e.maint = append(e.maint, m)
	return m, nil
}

// EndMaintenance removes a maintenance window.
func (e *Engine) EndMaintenance(ctx context.Context, id int64) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.store.DeleteMaintenance(ctx, id); err != nil {
		return err
	}
	for i, m := range e.maint {
		if m.ID == id {
			e.maint = append(e.maint[:i], e.maint[i+1:]...)
			break
		}
	}
	return nil
}

// Incidents lists the incident history.
func (e *Engine) Incidents(ctx context.Context, q IncidentQuery) ([]Incident, int, error) {
	return e.store.ListIncidents(ctx, q)
}
