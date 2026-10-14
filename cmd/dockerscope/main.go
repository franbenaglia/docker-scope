// Command dockerscope is a local Docker monitoring agent with an embedded web panel.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/fbenaglia/dockerscope/internal/api"
	"github.com/fbenaglia/dockerscope/internal/config"
	"github.com/fbenaglia/dockerscope/internal/demo"
	"github.com/fbenaglia/dockerscope/internal/docker"
	"github.com/fbenaglia/dockerscope/web"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "dockerscope:", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := loadConfig(os.Args[1:])
	if err != nil {
		return err
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var src docker.API
	mode := "docker"
	if cfg.Demo {
		mode = "demo"
		src = demo.Source{}
		log.Warn("MODO DEMO: datos simulados, sin conexión a Docker")
	} else {
		cli, err := docker.New(cfg.DockerHost)
		if err != nil {
			return err
		}
		defer cli.Close()
		src = cli
	}

	srv := &http.Server{
		Addr: cfg.Listen,
		Handler: (&api.Server{
			Docker: src, Mode: mode, Web: web.Dist(), Log: log, Now: time.Now,
		}).Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
		// No WriteTimeout: SSE streams are long-lived (stage 5 handles per-stream deadlines).
	}
	ln, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		return fmt.Errorf("escuchar en %s: %w", cfg.Listen, err)
	}
	log.Info("panel disponible", "url", "http://"+ln.Addr().String(), "modo", mode)

	errCh := make(chan error, 1)
	go func() { errCh <- srv.Serve(ln) }()

	select {
	case err := <-errCh:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	case <-ctx.Done():
		log.Info("apagando")
		shCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(shCtx); err != nil {
			return fmt.Errorf("apagado: %w", err)
		}
	}
	return nil
}

func loadConfig(args []string) (config.Config, error) {
	cfg := config.Default()
	fsFlags := flag.NewFlagSet("dockerscope", flag.ContinueOnError)
	cfgPath := fsFlags.String("config", "", "archivo de configuración JSON")
	listen := fsFlags.String("listen", "", "dirección de escucha (sólo loopback), por defecto 127.0.0.1:8080")
	dockerHost := fsFlags.String("docker-host", "", "endpoint Docker, p.ej. unix:///var/run/docker.sock")
	dataDir := fsFlags.String("data-dir", "", "directorio de datos (SQLite)")
	interval := fsFlags.Duration("interval", 0, "intervalo de muestreo de métricas (p.ej. 5s)")
	demoMode := fsFlags.Bool("demo", false, "modo demo con datos simulados (sin Docker)")
	if err := fsFlags.Parse(args); err != nil {
		return cfg, err
	}
	if *cfgPath != "" {
		if err := config.LoadFile(*cfgPath, &cfg); err != nil {
			return cfg, err
		}
	}
	// Flags explicitly set override the file.
	fsFlags.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "listen":
			cfg.Listen = *listen
		case "docker-host":
			cfg.DockerHost = *dockerHost
		case "data-dir":
			cfg.DataDir = *dataDir
		case "interval":
			cfg.SampleInterval = config.Duration(*interval)
		case "demo":
			cfg.Demo = *demoMode
		}
	})
	if err := cfg.Validate(); err != nil {
		return cfg, fmt.Errorf("configuración inválida:\n%w", err)
	}
	return cfg, nil
}
