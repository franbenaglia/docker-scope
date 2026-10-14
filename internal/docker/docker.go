// Package docker wraps the Docker Engine SDK behind a small interface so the
// rest of the agent can be tested with fakes. Read-only: no exec, stop,
// restart or remove (SPEC "Seguridad").
package docker

import (
	"context"
	"fmt"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
)

// API is the subset of the Docker Engine API used by DockerScope.
// It grows stage by stage (see docs/PLAN.md); keep it minimal.
type API interface {
	Ping(ctx context.Context) (PingInfo, error)
	ListContainers(ctx context.Context) ([]container.Summary, error)
}

// PingInfo is the result of a successful ping.
type PingInfo struct {
	APIVersion string
	OSType     string
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

func (c *Client) Ping(ctx context.Context) (PingInfo, error) {
	res, err := c.cli.Ping(ctx, client.PingOptions{NegotiateAPIVersion: true})
	if err != nil {
		return PingInfo{}, err
	}
	return PingInfo{APIVersion: res.APIVersion, OSType: res.OSType}, nil
}

func (c *Client) ListContainers(ctx context.Context) ([]container.Summary, error) {
	res, err := c.cli.ContainerList(ctx, client.ContainerListOptions{All: true})
	if err != nil {
		return nil, err
	}
	return res.Items, nil
}
