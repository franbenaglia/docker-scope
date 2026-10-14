// Package config loads and validates DockerScope configuration.
//
// Precedence: defaults < JSON file (--config) < command-line flags.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"time"
)

// Config is the runtime configuration of the agent.
type Config struct {
	// Listen is the HTTP listen address. Only loopback addresses are accepted (ADR-004).
	Listen string `json:"listen"`
	// DockerHost is the Docker endpoint, e.g. unix:///var/run/docker.sock.
	// Empty means DOCKER_HOST from the environment or the SDK default.
	DockerHost string `json:"docker_host"`
	// DataDir holds the SQLite database.
	DataDir string `json:"data_dir"`
	// SampleInterval is the metrics sampling interval.
	SampleInterval Duration `json:"sample_interval"`
	// Retention is how long historical samples are kept.
	Retention Duration `json:"retention"`
	// Demo runs with simulated data and no Docker connection.
	Demo bool `json:"demo"`
}

// Default returns the default configuration.
func Default() Config {
	return Config{
		Listen:         "127.0.0.1:8080",
		DataDir:        "./data",
		SampleInterval: Duration(5 * time.Second),
		Retention:      Duration(7 * 24 * time.Hour),
	}
}

// LoadFile overlays the JSON file at path onto cfg. Unknown fields are rejected.
func LoadFile(path string, cfg *Config) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("abrir configuración: %w", err)
	}
	defer f.Close()
	dec := json.NewDecoder(f)
	dec.DisallowUnknownFields()
	if err := dec.Decode(cfg); err != nil {
		return fmt.Errorf("leer configuración %s: %w", path, err)
	}
	return nil
}

// Validate checks the configuration for errors.
func (c Config) Validate() error {
	var errs []error
	if err := validateLoopback(c.Listen); err != nil {
		errs = append(errs, err)
	}
	if d := c.SampleInterval.D(); d < time.Second || d > 5*time.Minute {
		errs = append(errs, fmt.Errorf("sample_interval debe estar entre 1s y 5m (es %s)", d))
	}
	if d := c.Retention.D(); d < time.Hour || d > 365*24*time.Hour {
		errs = append(errs, fmt.Errorf("retention debe estar entre 1h y 365d (es %s)", d))
	}
	if c.DataDir == "" {
		errs = append(errs, errors.New("data_dir no puede estar vacío"))
	}
	return errors.Join(errs...)
}

func validateLoopback(listen string) error {
	host, port, err := net.SplitHostPort(listen)
	if err != nil {
		return fmt.Errorf("listen %q inválido: %w", listen, err)
	}
	if port == "" {
		return fmt.Errorf("listen %q: falta el puerto", listen)
	}
	if host == "localhost" {
		return nil
	}
	addr, err := netip.ParseAddr(host)
	if err != nil || !addr.IsLoopback() {
		return fmt.Errorf("listen %q: sólo se permiten direcciones loopback (127.0.0.1, ::1, localhost); "+
			"para acceso remoto use un túnel SSH (ver README)", listen)
	}
	return nil
}

// Duration is a time.Duration that marshals to/from JSON strings like "5s".
type Duration time.Duration

// D returns the value as a time.Duration.
func (d Duration) D() time.Duration { return time.Duration(d) }

func (d Duration) MarshalJSON() ([]byte, error) {
	return json.Marshal(time.Duration(d).String())
}

func (d *Duration) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return fmt.Errorf("duración debe ser un texto como \"5s\": %w", err)
	}
	v, err := time.ParseDuration(s)
	if err != nil {
		return err
	}
	*d = Duration(v)
	return nil
}
