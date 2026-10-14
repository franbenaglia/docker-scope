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
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/franbenaglia/docker-scope/internal/api"
	"github.com/franbenaglia/docker-scope/internal/collector"
	"github.com/franbenaglia/docker-scope/internal/config"
	"github.com/franbenaglia/docker-scope/internal/demo"
	"github.com/franbenaglia/docker-scope/internal/docker"
	"github.com/franbenaglia/docker-scope/internal/inventory"
	"github.com/franbenaglia/docker-scope/internal/sse"
	"github.com/franbenaglia/docker-scope/internal/store"
	"github.com/franbenaglia/docker-scope/web"
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
		src = demo.New(time.Now())
		log.Warn("MODO DEMO: datos simulados, sin conexión a Docker")
	} else {
		cli, err := docker.New(cfg.DockerHost)
		if err != nil {
			return err
		}
		defer cli.Close()
		src = cli
	}

	// Demo data lives in its own database file: never mixed with real history.
	dbName := "dockerscope.db"
	if cfg.Demo {
		dbName = "dockerscope-demo.db"
	}
	st, err := store.Open(ctx, cfg.DataDir, dbName, log.With("comp", "store"))
	if err != nil {
		return err
	}
	defer st.Close() // runs after the goroutines below have stopped
	log.Info("base de datos", "ruta", st.Path())
	retention := store.Retention{Raw: min(24*time.Hour, cfg.Retention.D()), Rollups: cfg.Retention.D()}
	writer := store.NewWriter(st, store.WriterOptions{Retention: retention})

	inv := inventory.New(src, inventory.Options{Log: log.With("comp", "inventory")})
	inv.OnChange(func(ch inventory.Change) { writer.EnqueueContainers(containerMeta(ch)) })
	col := collector.New(src, inv, collector.Options{
		Interval: cfg.SampleInterval.D(), Log: log.With("comp", "collector"),
	})
	col.OnRound(writer.EnqueueSamples)


	ln, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		return fmt.Errorf("escuchar en %s: %w", cfg.Listen, err)
	}
	listenHost, listenPort, _ := net.SplitHostPort(ln.Addr().String())

	hub := sse.NewHub(sse.Options{})
	apiSrv := &api.Server{
		Inventory: inv, Metrics: col, History: st, Retention: retention, Hub: hub,
		HeartbeatSeconds: 15, Mode: mode, Web: web.Dist(), Log: log, Now: time.Now,
	}
	apiSrv.Attach(inv, col)

	// Start the background components only after every listener is registered.
	var wg sync.WaitGroup
	wg.Go(func() { _ = writer.Run(ctx) })
	wg.Go(func() { _ = inv.Run(ctx) })
	wg.Go(func() { _ = col.Run(ctx) })
	defer func() { stop(); wg.Wait() }() // cancel first: Run returns on ctx.Done
	sec := api.Security{ListenPort: listenPort, ExtraHosts: []string{listenHost}, AllowedOrigins: cfg.AllowedOrigins}
	srv := &http.Server{
		Handler:           sec.Middleware(apiSrv.Handler()),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
		// No WriteTimeout: SSE streams are long-lived; the hub sets a deadline per write.
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
		hub.Close() // SSE handlers return, so Shutdown does not wait for them
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
	allowOrigin := fsFlags.String("allow-origin", "", "orígenes extra (loopback) permitidos para mutaciones, separados por coma; p. ej. el servidor de Vite en desarrollo")
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
		case "allow-origin":
			cfg.AllowedOrigins = nil
			for o := range strings.SplitSeq(*allowOrigin, ",") {
				if o = strings.TrimSpace(o); o != "" {
					cfg.AllowedOrigins = append(cfg.AllowedOrigins, o)
				}
			}
		}
	})
	if err := cfg.Validate(); err != nil {
		return cfg, fmt.Errorf("configuración inválida:\n%w", err)
	}
	return cfg, nil
}

func containerMeta(ch inventory.Change) store.ContainerMeta {
	if ch.Kind == inventory.Removed {
		return store.ContainerMeta{ID: ch.ID, SeenAt: ch.At, Removed: true}
	}
	c := ch.Cur
	return store.ContainerMeta{
		ID: c.ID, Name: c.Name, Image: c.Image, ComposeProject: c.ComposeProject,
		ComposeService: c.ComposeService, SeenAt: ch.At,
	}
}
