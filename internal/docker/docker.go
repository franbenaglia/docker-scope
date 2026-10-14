// Package docker wraps the Docker Engine SDK behind a small interface so the
// rest of the agent can be tested with fakes. Read-only: no exec, stop,
// restart or remove (SPEC "Seguridad").
package docker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/events"
	"github.com/moby/moby/client"
)

// API is the subset of the Docker Engine API used by DockerScope.
// It grows stage by stage (see docs/PLAN.md); keep it minimal.
type API interface {
	// Ping checks the daemon and (re)negotiates the API version.
	Ping(ctx context.Context) (PingInfo, error)
	// Info returns host facts used as denominators and for diagnostics.
	Info(ctx context.Context) (HostInfo, error)
	// ListContainers lists all containers, including stopped ones.
	ListContainers(ctx context.Context) ([]container.Summary, error)
	// InspectContainer returns ErrNotFound (wrapped) if the container does not exist.
	InspectContainer(ctx context.Context, id string) (container.InspectResponse, error)
	// Events streams container events from since (zero: only new events) until
	// ctx is cancelled. The error channel receives one value when the stream ends.
	Events(ctx context.Context, since time.Time) (<-chan events.Message, <-chan error)
	// ContainerStats returns one stats sample without waiting for a previous
	// one (PreCPUStats is empty): the collector computes deltas between its own
	// samples. Returns ErrNotFound (wrapped) if the container does not exist.
	ContainerStats(ctx context.Context, id string) (container.StatsResponse, error)
	// ContainerLogs returns the raw log stream (multiplexed unless the
	// container has a TTY) with Docker timestamps. The caller must close it;
	// cancelling ctx also closes it. Returns ErrNotFound or ErrLogsUnsupported
	// (both wrapped) when applicable.
	ContainerLogs(ctx context.Context, id string, opts LogsOptions) (io.ReadCloser, error)
}

// LogsOptions selects log lines.
type LogsOptions struct {
	Tail   int  // last N lines (0 = none, only new lines when following)
	Follow bool // keep the stream open for new lines
}

// ErrLogsUnsupported reports a logging driver that does not allow reading.
var ErrLogsUnsupported = errors.New("el logging driver no permite leer logs")

// ErrNotFound reports a container that does not exist (anymore).
var ErrNotFound = errors.New("contenedor inexistente")

// PingInfo is the result of a successful ping.
type PingInfo struct {
	APIVersion string
	OSType     string
}

// HostInfo describes the Docker host.
type HostInfo struct {
	NCPU          int
	MemTotal      int64 // bytes
	CgroupVersion string
	ServerVersion string
	OS            string
}

// Client is the real implementation of API backed by the moby SDK.
type Client struct {
	cli *client.Client
}

// New creates a client for host. An empty host uses DOCKER_HOST or the SDK
// default (unix:///var/run/docker.sock). API version negotiation is on by default.
func New(host string) (*Client, error) {
	opts := []client.Opt{client.FromEnv}
	if host != "" {
		opts = append(opts, client.WithHost(host))
	}
	cli, err := client.New(opts...)
	if err != nil {
		return nil, fmt.Errorf("crear cliente Docker: %w", err)
	}
	return &Client{cli: cli}, nil
}

// Close releases the underlying transport.
func (c *Client) Close() error { return c.cli.Close() }

// Ping forces API version renegotiation on every call: the daemon may have been
// upgraded while disconnected, and a failed first ping leaves the SDK on the
// minimum version.
func (c *Client) Ping(ctx context.Context) (PingInfo, error) {
	res, err := c.cli.Ping(ctx, client.PingOptions{NegotiateAPIVersion: true, ForceNegotiate: true})
	if err != nil {
		return PingInfo{}, err
	}
	return PingInfo{APIVersion: res.APIVersion, OSType: res.OSType}, nil
}

func (c *Client) Info(ctx context.Context) (HostInfo, error) {
	res, err := c.cli.Info(ctx, client.InfoOptions{})
	if err != nil {
		return HostInfo{}, err
	}
	i := res.Info
	return HostInfo{
		NCPU: i.NCPU, MemTotal: i.MemTotal, CgroupVersion: i.CgroupVersion,
		ServerVersion: i.ServerVersion, OS: i.OperatingSystem,
	}, nil
}

func (c *Client) ListContainers(ctx context.Context) ([]container.Summary, error) {
	res, err := c.cli.ContainerList(ctx, client.ContainerListOptions{All: true})
	if err != nil {
		return nil, err
	}
	return res.Items, nil
}

func (c *Client) InspectContainer(ctx context.Context, id string) (container.InspectResponse, error) {
	res, err := c.cli.ContainerInspect(ctx, id, client.ContainerInspectOptions{})
	if cerrdefs.IsNotFound(err) {
		return container.InspectResponse{}, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	if err != nil {
		return container.InspectResponse{}, err
	}
	return res.Container, nil
}

func (c *Client) Events(ctx context.Context, since time.Time) (<-chan events.Message, <-chan error) {
	opts := client.EventsListOptions{
		Filters: client.Filters{}.Add("type", string(events.ContainerEventType)),
	}
	if !since.IsZero() {
		opts.Since = FormatEventTime(since)
	}
	res := c.cli.Events(ctx, opts)
	return res.Messages, res.Err
}

func (c *Client) ContainerStats(ctx context.Context, id string) (container.StatsResponse, error) {
	res, err := c.cli.ContainerStats(ctx, id, client.ContainerStatsOptions{Stream: false})
	if cerrdefs.IsNotFound(err) {
		return container.StatsResponse{}, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	if err != nil {
		return container.StatsResponse{}, err
	}
	defer res.Body.Close()
	var st container.StatsResponse
	// One sample is a few KB; cap the read in case of a misbehaving endpoint.
	if err := json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&st); err != nil {
		return container.StatsResponse{}, fmt.Errorf("decodificar stats de %.12s: %w", id, err)
	}
	return st, nil
}

func (c *Client) ContainerLogs(ctx context.Context, id string, opts LogsOptions) (io.ReadCloser, error) {
	rc, err := c.cli.ContainerLogs(ctx, id, client.ContainerLogsOptions{
		ShowStdout: true, ShowStderr: true, Timestamps: true, Follow: opts.Follow,
		Tail: strconv.Itoa(max(opts.Tail, 0)),
	})
	switch {
	case cerrdefs.IsNotFound(err):
		return nil, fmt.Errorf("%w: %s", ErrNotFound, id)
	case cerrdefs.IsNotImplemented(err):
		return nil, fmt.Errorf("%w: %v", ErrLogsUnsupported, err)
	case err != nil:
		return nil, err
	}
	return rc, nil
}

// FormatEventTime formats t as the "seconds.nanoseconds" form accepted by the
// events API "since"/"until" parameters.
func FormatEventTime(t time.Time) string {
	return fmt.Sprintf("%d.%09d", t.Unix(), t.Nanosecond())
}
