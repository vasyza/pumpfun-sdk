package config

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"
)

func cleanEnv(t *testing.T) {
	t.Helper()
	for _, key := range Keys {
		t.Setenv("PUMPFUN_"+upper(key), "")
		if err := os.Unsetenv("PUMPFUN_" + upper(key)); err != nil {
			t.Fatal(err)
		}
	}
}

func upper(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'a' && c <= 'z' {
			b[i] = c - ('a' - 'A')
		}
	}
	return string(b)
}

func TestPrecedenceAndConfigOperations(t *testing.T) {
	cleanEnv(t)
	store := Store{Path: filepath.Join(t.TempDir(), "pumpfun", "config.yaml")}
	ctx := context.Background()
	if err := store.Set(ctx, "timeout", "12s"); err != nil {
		t.Fatal(err)
	}
	if err := store.Set(ctx, "output", "yaml"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PUMPFUN_TIMEOUT", "13s")
	cfg, err := store.Load(map[string]string{"timeout": "14s"})
	if err != nil || cfg.Timeout != 14*time.Second || cfg.Output != "yaml" {
		t.Fatalf("config = %+v, err = %v", cfg, err)
	}
	cfg, err = store.Load(nil)
	if err != nil || cfg.Timeout != 13*time.Second {
		t.Fatalf("config = %+v, err = %v", cfg, err)
	}
	if err := os.Unsetenv("PUMPFUN_TIMEOUT"); err != nil {
		t.Fatal(err)
	}
	cfg, err = store.Load(nil)
	if err != nil || cfg.Timeout != 12*time.Second {
		t.Fatalf("config = %+v, err = %v", cfg, err)
	}
	if err := store.Unset(ctx, "timeout"); err != nil {
		t.Fatal(err)
	}
	cfg, err = store.Load(nil)
	if err != nil || cfg.Timeout != 20*time.Second {
		t.Fatalf("config = %+v, err = %v", cfg, err)
	}
	stat, err := os.Stat(store.Path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && stat.Mode().Perm() != 0o600 {
		t.Fatalf("config mode = %v", stat.Mode())
	}
}

func TestXDGPath(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("PUMPFUN_CONFIG_FILE", "")
	path, err := Path("")
	if err != nil || path != filepath.Join(dir, "pumpfun", "config.yaml") {
		t.Fatalf("path = %s, err = %v", path, err)
	}
	t.Setenv("PUMPFUN_CONFIG_FILE", filepath.Join(dir, "env.yaml"))
	path, err = Path(filepath.Join(dir, "flag.yaml"))
	if err != nil || path != filepath.Join(dir, "flag.yaml") {
		t.Fatalf("path = %s, err = %v", path, err)
	}
}

func TestConcurrentConfigWrites(t *testing.T) {
	store := Store{Path: filepath.Join(t.TempDir(), "config.yaml")}
	var wg sync.WaitGroup
	for key, value := range Defaults() {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := store.Set(context.Background(), key, value); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	values, err := store.Read()
	if err != nil || len(values) != len(Keys) {
		t.Fatalf("values = %v, err = %v", values, err)
	}
}

func TestRejectInvalidConfig(t *testing.T) {
	store := Store{Path: filepath.Join(t.TempDir(), "config.yaml")}
	for _, body := range []string{"output: json\noutput: yaml\n", "output: [json]\n", "private_key: secret\n", "output: json\n---\noutput: yaml\n"} {
		if err := os.WriteFile(store.Path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := store.Read(); err == nil {
			t.Errorf("accepted %q", body)
		}
	}
	for key, value := range map[string]string{"timeout": "-1s", "output": "xml", "log_level": "panic", "ws_url": "https://example.com", "rpc_url": "http://user:secret@example.com"} {
		if err := Validate(key, value); err == nil {
			t.Errorf("accepted invalid %s", key)
		}
	}
}
