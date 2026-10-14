package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestValidateListen(t *testing.T) {
	cases := []struct {
		listen string
		ok     bool
	}{
		{"127.0.0.1:8080", true},
		{"[::1]:8080", true},
		{"localhost:8080", true},
		{"127.0.0.2:9000", true},
		{"0.0.0.0:8080", false},
		{"192.168.1.10:8080", false},
		{":8080", false},
		{"[::]:8080", false},
		{"example.com:8080", false},
		{"127.0.0.1", false},
	}
	for _, tc := range cases {
		c := Default()
		c.Listen = tc.listen
		err := c.Validate()
		if (err == nil) != tc.ok {
			t.Errorf("listen %q: ok=%v, err=%v", tc.listen, tc.ok, err)
		}
	}
}

func TestValidateRanges(t *testing.T) {
	c := Default()
	if err := c.Validate(); err != nil {
		t.Fatalf("default config inválida: %v", err)
	}
	c.SampleInterval = Duration(100 * time.Millisecond)
	if c.Validate() == nil {
		t.Error("intervalo de 100ms debería ser inválido")
	}
	c = Default()
	c.Retention = Duration(time.Minute)
	if c.Validate() == nil {
		t.Error("retención de 1m debería ser inválida")
	}
}

func TestLoadFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "c.json")
	if err := os.WriteFile(p, []byte(`{"listen":"127.0.0.1:9999","sample_interval":"10s"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	c := Default()
	if err := LoadFile(p, &c); err != nil {
		t.Fatal(err)
	}
	if c.Listen != "127.0.0.1:9999" || c.SampleInterval.D() != 10*time.Second {
		t.Errorf("valores no aplicados: %+v", c)
	}
	if c.Retention.D() != 7*24*time.Hour {
		t.Errorf("default de retención perdido: %s", c.Retention.D())
	}

	if err := os.WriteFile(p, []byte(`{"lisen":"x"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	c = Default()
	if LoadFile(p, &c) == nil {
		t.Error("campo desconocido debería fallar")
	}
}
