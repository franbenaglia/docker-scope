package alerts

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/moby/moby/api/types/container"

	"github.com/franbenaglia/docker-scope/internal/collector"
	"github.com/franbenaglia/docker-scope/internal/inventory"
)

// describe builds the incident summary and probable cause. Causes are worded
// as possibilities: the agent sees exit codes and events, not intent.
func (e *Engine) describe(r Rule, c *inventory.Container, val *float64, latest map[string]collector.Sample) (string, string) {
	switch r.Kind {
	case NotRunning:
		s := fmt.Sprintf("%s dejó de ejecutarse (estado %s", c.Name, c.State)
		if c.ExitCode != nil {
			s += fmt.Sprintf(", código de salida %d", *c.ExitCode)
		}
		return s + ")", e.exitCause(c)
	case Unhealthy:
		s := fmt.Sprintf("%s está unhealthy desde hace al menos %s", c.Name, dur(r.For))
		if c.HealthFailingStreak > 0 {
			s += fmt.Sprintf(" (%d chequeos fallidos seguidos)", c.HealthFailingStreak)
		}
		return s, "el healthcheck definido en la imagen o en Compose está fallando"
	case Restarts:
		n := 0.0
		if val != nil {
			n = *val
		}
		s := fmt.Sprintf("%s se reinició %.0f veces en %s (umbral %.0f)", c.Name, n, dur(r.Window), r.Threshold)
		cause := "reinicios observados por el agente (política de reinicio o manuales)"
		if c.ExitCode != nil {
			cause += fmt.Sprintf("; último código de salida conocido %d: %s", *c.ExitCode, exitMeaning(*c.ExitCode, c.OOMKilled))
		}
		return s, cause
	case CPU:
		return fmt.Sprintf("%s usa %s de CPU (100%% = 1 núcleo), por encima de %s durante al menos %s",
			c.Name, pct(val), pct(&r.Threshold), dur(r.For)), ""
	case Memory:
		limit := "límite"
		if s, ok := latest[c.ID]; ok && s.MemLimit != nil {
			if s.MemLimitExplicit {
				limit = fmt.Sprintf("su límite de %s", bytes(*s.MemLimit))
			} else {
				limit = fmt.Sprintf("la memoria del host (%s; el contenedor no tiene límite)", bytes(*s.MemLimit))
			}
		}
		return fmt.Sprintf("%s usa %s de %s, por encima de %s durante al menos %s",
			c.Name, pct(val), limit, pct(&r.Threshold), dur(r.For)), ""
	}
	return c.Name, ""
}

func (e *Engine) exitCause(c *inventory.Container) string {
	var parts []string
	if c.ExitCode != nil {
		parts = append(parts, exitMeaning(*c.ExitCode, c.OOMKilled))
	}
	if c.Error != "" {
		parts = append(parts, "error informado por Docker: "+c.Error)
	}
	if ev, ok := e.events[c.ID]; ok {
		parts = append(parts, fmt.Sprintf("último evento de Docker: %s (%s)", ev.action, ev.at.Format(time.RFC3339)))
	}
	if c.State == container.StateRestarting {
		parts = append(parts, "Docker lo está reiniciando según su política")
	}
	if len(parts) == 0 {
		return "sin información de causa"
	}
	return strings.Join(parts, "; ")
}

func (e *Engine) removedCause(id string) string {
	if ev, ok := e.events[id]; ok {
		return fmt.Sprintf("el contenedor fue eliminado (último evento de Docker: %s); posible docker rm, recreación o docker compose down", ev.action)
	}
	return "el contenedor fue eliminado; posible docker rm, recreación o docker compose down"
}

// exitMeaning interprets common exit codes. 128+N means signal N.
func exitMeaning(code int, oom bool) string {
	switch {
	case oom:
		return "Docker indica OOMKilled: posiblemente el kernel lo terminó por falta de memoria"
	case code == 0:
		return "salida normal (código 0): posiblemente terminó su tarea o se detuvo de forma ordenada"
	case code == 137:
		return "código 137 (SIGKILL): posible docker kill, docker stop que agotó el tiempo, o el kernel por memoria"
	case code == 143:
		return "código 143 (SIGTERM): posible docker stop o apagado ordenado"
	case code == 130:
		return "código 130 (SIGINT): posible interrupción"
	case code > 128 && code < 160:
		return fmt.Sprintf("código %d: posiblemente terminado por la señal %d", code, code-128)
	case code == 125 || code == 126 || code == 127:
		return fmt.Sprintf("código %d: posible error al iniciar el comando (Docker o binario no encontrado/no ejecutable)", code)
	default:
		return fmt.Sprintf("código %d: posible error de la aplicación", code)
	}
}

func pct(v *float64) string {
	if v == nil {
		return "n/d"
	}
	if math.Abs(*v) >= 10 {
		return fmt.Sprintf("%.0f%%", *v)
	}
	return fmt.Sprintf("%.1f%%", *v)
}

// dur formats durations compactly: 30s, 5m, 1h30m.
func dur(d time.Duration) string {
	d = d.Round(time.Second)
	if d == 0 {
		return "0s"
	}
	var b strings.Builder
	if h := d / time.Hour; h > 0 {
		fmt.Fprintf(&b, "%dh", h)
	}
	if m := d % time.Hour / time.Minute; m > 0 {
		fmt.Fprintf(&b, "%dm", m)
	}
	if s := d % time.Minute / time.Second; s > 0 {
		fmt.Fprintf(&b, "%ds", s)
	}
	return b.String()
}

func bytes(n uint64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := uint64(unit), 0
	for m := n / unit; m >= unit && exp < 4; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTP"[exp])
}
