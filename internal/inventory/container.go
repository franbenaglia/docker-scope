package inventory

import (
	"strings"
	"time"

	"github.com/moby/moby/api/types/container"
)

// Compose label keys.
const (
	LabelComposeProject = "com.docker.compose.project"
	LabelComposeService = "com.docker.compose.service"
)

// Health values. HealthNone means the container has no HEALTHCHECK, which is
// not the same as healthy.
const (
	HealthNone      = "none"
	HealthStarting  = "starting"
	HealthHealthy   = "healthy"
	HealthUnhealthy = "unhealthy"
)

// Container is the agent's view of one container. Identity is the full ID:
// a container recreated with the same name is a different Container.
type Container struct {
	ID      string
	Name    string
	Image   string // as requested by the operator (Config.Image)
	ImageID string
	State   container.ContainerState
	// Health is the healthcheck status (HealthNone without HEALTHCHECK). For
	// containers that are not running it is the last status Docker kept, which
	// is not current: use HealthCurrent.
	Health              string
	HealthFailingStreak int

	CreatedAt  time.Time
	StartedAt  time.Time // zero if never started
	FinishedAt time.Time // zero if never finished
	// ExitCode is set only for exited/dead containers that have run.
	ExitCode  *int
	OOMKilled bool
	Error     string

	RestartPolicy   string
	RestartMaxRetry int
	// RestartCount is reported by Docker: restarts by the restart policy since
	// the last manual start; it does not count manual restarts.
	RestartCount int
	// ObservedRestarts counts the times the agent saw StartedAt move forward for a
	// container that had already run (any cause: policy, manual, daemon restart).
	// Restarts while the agent was disconnected count as one at most.
	ObservedRestarts    int
	LastObservedRestart time.Time

	ComposeProject string
	ComposeService string
	Labels         map[string]string

	TTY       bool
	LogDriver string
}

// ShortID returns the 12-character ID used by the Docker CLI.
func (c *Container) ShortID() string {
	if len(c.ID) > 12 {
		return c.ID[:12]
	}
	return c.ID
}

// Running reports whether the container is running (paused/restarting are not).
func (c *Container) Running() bool { return c.State == container.StateRunning }

// HealthCurrent returns the health status if it is meaningful now: the
// container is running and has a healthcheck. Otherwise "".
func (c *Container) HealthCurrent() string {
	if !c.Running() || c.Health == HealthNone {
		return ""
	}
	return c.Health
}

// Uptime returns how long the container has been running at now, or 0.
func (c *Container) Uptime(now time.Time) time.Duration {
	if !c.Running() || c.StartedAt.IsZero() {
		return 0
	}
	return now.Sub(c.StartedAt)
}

func (c *Container) clone() *Container {
	cp := *c
	if c.ExitCode != nil {
		v := *c.ExitCode
		cp.ExitCode = &v
	}
	// Labels are never mutated after construction; sharing the map is safe.
	return &cp
}

// fromInspect converts an inspect response. Fields the agent tracks itself
// (observed restarts) are left zero.
func fromInspect(r container.InspectResponse) *Container {
	c := &Container{
		ID:        r.ID,
		Name:      strings.TrimPrefix(r.Name, "/"),
		ImageID:   r.Image,
		CreatedAt: parseTime(r.Created),
		Health:    HealthNone,
	}
	c.RestartCount = r.RestartCount
	if s := r.State; s != nil {
		c.State = s.Status
		c.StartedAt = parseTime(s.StartedAt)
		c.FinishedAt = parseTime(s.FinishedAt)
		c.OOMKilled = s.OOMKilled
		c.Error = s.Error
		if s.Health != nil && s.Health.Status != "" && s.Health.Status != container.NoHealthcheck {
			c.Health = string(s.Health.Status)
			c.HealthFailingStreak = s.Health.FailingStreak
		}
		if (s.Status == container.StateExited || s.Status == container.StateDead) && !c.StartedAt.IsZero() {
			code := s.ExitCode
			c.ExitCode = &code
		}
	}
	if cfg := r.Config; cfg != nil {
		c.Image = cfg.Image
		c.TTY = cfg.Tty
		c.Labels = cfg.Labels
		c.ComposeProject = cfg.Labels[LabelComposeProject]
		c.ComposeService = cfg.Labels[LabelComposeService]
	}
	if hc := r.HostConfig; hc != nil {
		c.RestartPolicy = string(hc.RestartPolicy.Name)
		if c.RestartPolicy == "" {
			c.RestartPolicy = string(container.RestartPolicyDisabled)
		}
		c.RestartMaxRetry = hc.RestartPolicy.MaximumRetryCount
		c.LogDriver = hc.LogConfig.Type
	}
	return c
}

// parseTime parses Docker RFC 3339 timestamps; Docker's "0001-01-01T00:00:00Z"
// and unparsable values map to the zero time.
func parseTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil || t.Year() <= 1 {
		return time.Time{}
	}
	return t.UTC()
}
