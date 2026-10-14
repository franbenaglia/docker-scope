package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/franbenaglia/docker-scope/internal/alerts"
)

var _ alerts.Store = (*Store)(nil)

// IncidentRetention is how long resolved incidents are kept (ADR-013).
const IncidentRetention = 90 * 24 * time.Hour

const ruleCols = `id, name, kind, enabled, scope_container, scope_project, scope_service, threshold,
	for_ms, window_ms, cooldown_ms, recover_ms, hysteresis, created_at, updated_at`

func scanRule(sc interface{ Scan(...any) error }) (alerts.Rule, error) {
	var r alerts.Rule
	var kind string
	var forMs, windowMs, coolMs, recMs, created, updated int64
	err := sc.Scan(&r.ID, &r.Name, &kind, &r.Enabled, &r.Scope.Container, &r.Scope.Project, &r.Scope.Service,
		&r.Threshold, &forMs, &windowMs, &coolMs, &recMs, &r.Hysteresis, &created, &updated)
	r.Kind = alerts.Kind(kind)
	r.For, r.Window = time.Duration(forMs)*time.Millisecond, time.Duration(windowMs)*time.Millisecond
	r.Cooldown, r.RecoverFor = time.Duration(coolMs)*time.Millisecond, time.Duration(recMs)*time.Millisecond
	r.CreatedAt, r.UpdatedAt = fromMs(created), fromMs(updated)
	return r, err
}

func ruleArgs(r *alerts.Rule) []any {
	return []any{r.Name, string(r.Kind), r.Enabled, r.Scope.Container, r.Scope.Project, r.Scope.Service, r.Threshold,
		r.For.Milliseconds(), r.Window.Milliseconds(), r.Cooldown.Milliseconds(), r.RecoverFor.Milliseconds(),
		r.Hysteresis, ms(r.CreatedAt), ms(r.UpdatedAt)}
}

func (s *Store) ListRules(ctx context.Context) ([]alerts.Rule, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+ruleCols+` FROM rules ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []alerts.Rule
	for rows.Next() {
		r, err := scanRule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) InsertRule(ctx context.Context, r *alerts.Rule) error {
	res, err := s.db.ExecContext(ctx, `INSERT INTO rules (name, kind, enabled, scope_container, scope_project,
		scope_service, threshold, for_ms, window_ms, cooldown_ms, recover_ms, hysteresis, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, ruleArgs(r)...)
	if err != nil {
		return err
	}
	r.ID, err = res.LastInsertId()
	return err
}

func (s *Store) UpdateRule(ctx context.Context, r *alerts.Rule) error {
	res, err := s.db.ExecContext(ctx, `UPDATE rules SET name = ?, kind = ?, enabled = ?, scope_container = ?,
		scope_project = ?, scope_service = ?, threshold = ?, for_ms = ?, window_ms = ?, cooldown_ms = ?,
		recover_ms = ?, hysteresis = ?, created_at = ?, updated_at = ? WHERE id = ?`, append(ruleArgs(r), r.ID)...)
	return affected(res, err)
}

func (s *Store) DeleteRule(ctx context.Context, id int64) error {
	return affected(s.db.ExecContext(ctx, `DELETE FROM rules WHERE id = ?`, id))
}

func (s *Store) ListMaintenance(ctx context.Context, now time.Time) ([]alerts.Maintenance, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, container, until, reason, created_at FROM maintenance
		WHERE until > ? ORDER BY until`, ms(now))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []alerts.Maintenance
	for rows.Next() {
		var m alerts.Maintenance
		var until, created int64
		if err := rows.Scan(&m.ID, &m.Container, &until, &m.Reason, &created); err != nil {
			return nil, err
		}
		m.Until, m.CreatedAt = fromMs(until), fromMs(created)
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *Store) InsertMaintenance(ctx context.Context, m *alerts.Maintenance) error {
	res, err := s.db.ExecContext(ctx, `INSERT INTO maintenance (container, until, reason, created_at) VALUES (?, ?, ?, ?)`,
		m.Container, ms(m.Until), m.Reason, ms(m.CreatedAt))
	if err != nil {
		return err
	}
	m.ID, err = res.LastInsertId()
	return err
}

func (s *Store) DeleteMaintenance(ctx context.Context, id int64) error {
	return affected(s.db.ExecContext(ctx, `DELETE FROM maintenance WHERE id = ?`, id))
}

const incidentCols = `id, rule_id, rule_name, kind, container_id, container_name, state, started_at, opened_at,
	resolved_at, acked_at, suppressed, summary, cause, resolution, peak`

func scanIncident(sc interface{ Scan(...any) error }) (alerts.Incident, error) {
	var i alerts.Incident
	var kind, state string
	var started, opened int64
	var resolved, acked sql.NullInt64
	var peak sql.NullFloat64
	err := sc.Scan(&i.ID, &i.RuleID, &i.RuleName, &kind, &i.ContainerID, &i.ContainerName, &state, &started, &opened,
		&resolved, &acked, &i.Suppressed, &i.Summary, &i.Cause, &i.Resolution, &peak)
	i.Kind, i.State = alerts.Kind(kind), alerts.State(state)
	i.StartedAt, i.OpenedAt = fromMs(started), fromMs(opened)
	if resolved.Valid {
		t := fromMs(resolved.Int64)
		i.ResolvedAt = &t
	}
	if acked.Valid {
		t := fromMs(acked.Int64)
		i.AckedAt = &t
	}
	if peak.Valid {
		i.Peak = &peak.Float64
	}
	return i, err
}

func (s *Store) OpenIncidents(ctx context.Context) ([]alerts.Incident, error) {
	out, _, err := s.ListIncidents(ctx, alerts.IncidentQuery{State: alerts.Open, Limit: -1})
	return out, err
}

func (s *Store) InsertIncident(ctx context.Context, i *alerts.Incident) error {
	res, err := s.db.ExecContext(ctx, `INSERT INTO incidents (rule_id, rule_name, kind, container_id, container_name,
		state, started_at, opened_at, resolved_at, acked_at, suppressed, summary, cause, resolution, peak)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		i.RuleID, i.RuleName, string(i.Kind), i.ContainerID, i.ContainerName, string(i.State), ms(i.StartedAt),
		ms(i.OpenedAt), msPtr(i.ResolvedAt), msPtr(i.AckedAt), i.Suppressed, i.Summary, i.Cause, i.Resolution, i.Peak)
	if err != nil {
		return err
	}
	i.ID, err = res.LastInsertId()
	return err
}

// UpdateIncident persists the mutable fields: state, resolution, ack and peak.
func (s *Store) UpdateIncident(ctx context.Context, i *alerts.Incident) error {
	return affected(s.db.ExecContext(ctx, `UPDATE incidents SET state = ?, resolved_at = ?, acked_at = ?,
		resolution = ?, peak = ? WHERE id = ?`,
		string(i.State), msPtr(i.ResolvedAt), msPtr(i.AckedAt), i.Resolution, i.Peak, i.ID))
}

func (s *Store) GetIncident(ctx context.Context, id int64) (alerts.Incident, error) {
	i, err := scanIncident(s.db.QueryRowContext(ctx, `SELECT `+incidentCols+` FROM incidents WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return alerts.Incident{}, alerts.ErrNotFound
	}
	return i, err
}

// ListIncidents returns incidents newest first and the total matching count.
// A negative Limit means no limit.
func (s *Store) ListIncidents(ctx context.Context, q alerts.IncidentQuery) ([]alerts.Incident, int, error) {
	where, args := `WHERE 1 = 1`, []any{}
	if q.State != "" {
		where += ` AND state = ?`
		args = append(args, string(q.State))
	}
	if q.ContainerID != "" {
		where += ` AND container_id = ?`
		args = append(args, q.ContainerID)
	}
	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM incidents `+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	limit := q.Limit
	if limit == 0 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+incidentCols+` FROM incidents `+where+
		` ORDER BY opened_at DESC, id DESC LIMIT ? OFFSET ?`, append(args, limit, q.Offset)...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []alerts.Incident{}
	for rows.Next() {
		i, err := scanIncident(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, i)
	}
	return out, total, rows.Err()
}

func msPtr(t *time.Time) any {
	if t == nil {
		return nil
	}
	return ms(*t)
}

func affected(res sql.Result, err error) error {
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return alerts.ErrNotFound
	}
	return nil
}
