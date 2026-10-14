// Package alerts evaluates rules over the inventory and metrics and manages
// incidents: pending → open → resolved, with minimum duration, cooldown,
// hysteresis, deduplication and maintenance windows (ADR-013).
package alerts

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/franbenaglia/docker-scope/internal/inventory"
)

// Kind is the condition a rule watches.
type Kind string

const (
	NotRunning Kind = "not_running" // a watched container stops running
	Unhealthy  Kind = "unhealthy"   // healthcheck unhealthy for at least For
	Restarts   Kind = "restarts"    // ≥ Threshold observed restarts within Window
	CPU        Kind = "cpu"         // CPU > Threshold (% of one core) for at least For
	Memory     Kind = "memory"      // memory > Threshold (% of the effective limit) for at least For
)

// Kinds lists every rule kind.
var Kinds = []Kind{NotRunning, Unhealthy, Restarts, CPU, Memory}

// Scope selects containers. Empty fields match anything. Container is a
// container name: rules follow a container across recreations (new IDs).
type Scope struct {
	Container string `json:"container"`
	Project   string `json:"project"`
	Service   string `json:"service"`
}

// Empty reports whether the scope matches every container.
func (s Scope) Empty() bool { return s.Container == "" && s.Project == "" && s.Service == "" }

// Matches reports whether c is in scope.
func (s Scope) Matches(c *inventory.Container) bool {
	return (s.Container == "" || s.Container == c.Name) &&
		(s.Project == "" || s.Project == c.ComposeProject) &&
		(s.Service == "" || s.Service == c.ComposeService)
}

// Rule is an alert rule.
type Rule struct {
	ID      int64
	Name    string
	Kind    Kind
	Enabled bool
	Scope   Scope
	// Threshold: cpu → % of one core (100 = one core); memory → % of the
	// effective limit (host memory if none is configured); restarts → count.
	Threshold float64
	For       time.Duration // condition must hold this long before opening
	Window    time.Duration // restarts: counting window
	Cooldown  time.Duration // after resolving, minimum time before reopening
	// RecoverFor: the condition must stay clear this long before resolving.
	RecoverFor time.Duration
	// Hysteresis (cpu/memory, in threshold units): an open incident resolves
	// only below Threshold − Hysteresis.
	Hysteresis float64
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// Limits for rule validation.
const (
	maxNameLen  = 100
	maxDuration = 24 * time.Hour
)

// ErrValidation reports an invalid rule or request.
var ErrValidation = errors.New("validación")

// Validate checks a rule.
func (r Rule) Validate() error {
	var errs []string
	name := strings.TrimSpace(r.Name)
	if name == "" || len(name) > maxNameLen {
		errs = append(errs, fmt.Sprintf("name es obligatorio (máx. %d caracteres)", maxNameLen))
	}
	switch r.Kind {
	case NotRunning:
		if r.Scope.Empty() {
			errs = append(errs, "not_running requiere un alcance (container o project): evita alertar por cada contenedor que termina")
		}
	case Unhealthy:
	case Restarts:
		if r.Threshold < 1 || r.Threshold > 1000 || r.Threshold != float64(int(r.Threshold)) {
			errs = append(errs, "threshold de restarts debe ser un entero entre 1 y 1000")
		}
		if r.Window < time.Minute || r.Window > maxDuration {
			errs = append(errs, "window de restarts debe estar entre 1m y 24h")
		}
	case CPU:
		if r.Threshold <= 0 || r.Threshold > 100*1024 {
			errs = append(errs, "threshold de cpu es % de un núcleo (100 = 1 núcleo) y debe ser > 0")
		}
	case Memory:
		if r.Threshold <= 0 || r.Threshold > 100 {
			errs = append(errs, "threshold de memory es % del límite y debe estar en (0, 100]")
		}
	default:
		errs = append(errs, "kind debe ser uno de: not_running, unhealthy, restarts, cpu, memory")
	}
	for _, d := range []struct {
		name string
		v    time.Duration
	}{{"for", r.For}, {"cooldown", r.Cooldown}, {"recover_for", r.RecoverFor}} {
		if d.v < 0 || d.v > maxDuration {
			errs = append(errs, d.name+" debe estar entre 0 y 24h")
		}
	}
	if r.Hysteresis < 0 || (r.Kind == CPU || r.Kind == Memory) && r.Hysteresis >= r.Threshold && r.Threshold > 0 {
		errs = append(errs, "hysteresis debe ser ≥ 0 y menor que threshold")
	}
	for _, s := range []string{r.Scope.Container, r.Scope.Project, r.Scope.Service} {
		if len(s) > 255 {
			errs = append(errs, "los campos de scope admiten hasta 255 caracteres")
			break
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("%w: %s", ErrValidation, strings.Join(errs, "; "))
	}
	return nil
}

// State of a persisted incident.
type State string

const (
	Open     State = "open"
	Resolved State = "resolved"
)

// Incident is an alert occurrence for one rule and one container.
type Incident struct {
	ID            int64
	RuleID        int64
	RuleName      string
	Kind          Kind
	ContainerID   string
	ContainerName string
	State         State
	StartedAt     time.Time // condition first observed (pending start)
	OpenedAt      time.Time
	ResolvedAt    *time.Time
	AckedAt       *time.Time // acknowledging does not resolve
	Suppressed    bool       // opened during maintenance: no notification
	Summary       string
	Cause         string // probable cause, worded as a possibility
	Resolution    string
	Peak          *float64 // worst value observed (cpu %, memory %, restarts)
}

// Maintenance silences notifications until Until. Container "" is global.
type Maintenance struct {
	ID        int64
	Container string // container name
	Until     time.Time
	Reason    string
	CreatedAt time.Time
}

// IncidentQuery filters the incident history.
type IncidentQuery struct {
	State       State  // "" = any
	ContainerID string // "" = any
	Limit       int
	Offset      int
}

// Store persists rules, maintenance windows and incidents.
type Store interface {
	ListRules(ctx context.Context) ([]Rule, error)
	InsertRule(ctx context.Context, r *Rule) error
	UpdateRule(ctx context.Context, r *Rule) error
	DeleteRule(ctx context.Context, id int64) error

	ListMaintenance(ctx context.Context, now time.Time) ([]Maintenance, error) // active only
	InsertMaintenance(ctx context.Context, m *Maintenance) error
	DeleteMaintenance(ctx context.Context, id int64) error

	OpenIncidents(ctx context.Context) ([]Incident, error)
	InsertIncident(ctx context.Context, inc *Incident) error
	UpdateIncident(ctx context.Context, inc *Incident) error
	GetIncident(ctx context.Context, id int64) (Incident, error)
	ListIncidents(ctx context.Context, q IncidentQuery) ([]Incident, int, error)
}

// ErrNotFound reports a missing rule, maintenance window or incident.
var ErrNotFound = errors.New("no encontrado")
