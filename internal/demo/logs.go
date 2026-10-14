package demo

import (
	"context"
	"fmt"
	"io"
	"math/rand/v2"
	"time"

	"github.com/franbenaglia/docker-scope/internal/docker"
	dt "github.com/franbenaglia/docker-scope/internal/docker/dockertest"
)

var demoMessages = []struct {
	stream byte
	text   string
}{
	{1, "GET /api/orders 200 12ms"},
	{1, "GET /health 200 1ms"},
	{1, "POST /api/orders 201 48ms"},
	{2, "WARN conexión lenta con la base de datos (850ms)"},
	{1, "worker: tarea procesada id=%d"},
	{2, "ERROR timeout consultando el servicio de pagos"},
	{1, "\x1b[32mINFO\x1b[0m caché renovada"}, // colors are stripped by the agent
}

// logs serves synthetic multiplexed logs with Docker timestamps. Following
// streams emit a line every ~1.5s until ctx is cancelled.
func logs(running func(id string) bool) func(ctx context.Context, id string, opts docker.LogsOptions) (io.ReadCloser, error) {
	return func(ctx context.Context, id string, opts docker.LogsOptions) (io.ReadCloser, error) {
		pr, pw := io.Pipe()
		go func() {
			defer pw.Close()
			rng := rand.New(rand.NewPCG(uint64(len(id)), uint64(time.Now().UnixNano())))
			line := func(t time.Time, n int) []byte {
				m := demoMessages[rng.IntN(len(demoMessages))]
				text := m.text
				if m.stream == 1 && text == demoMessages[4].text {
					text = fmt.Sprintf(text, n)
				}
				return dt.Mux(m.stream, t.UTC().Format(time.RFC3339Nano)+" "+text+"\n")
			}
			now := time.Now()
			for i := range opts.Tail {
				if _, err := pw.Write(line(now.Add(-time.Duration(opts.Tail-i)*3*time.Second), i)); err != nil {
					return
				}
			}
			if !opts.Follow || !running(id) {
				return
			}
			t := time.NewTicker(1500 * time.Millisecond)
			defer t.Stop()
			for n := opts.Tail; ; n++ {
				select {
				case <-ctx.Done():
					pw.CloseWithError(ctx.Err())
					return
				case now := <-t.C:
					if _, err := pw.Write(line(now, n)); err != nil {
						return
					}
				}
			}
		}()
		return pr, nil
	}
}
