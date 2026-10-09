// Package config loads shared CLI and MCP settings.
package config

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gofrs/flock"
	"github.com/rs/zerolog"
	pumpfun "github.com/vasyza/pumpfun-sdk"
	"go.yaml.in/yaml/v3"
)

// Keys lists the supported settings in a fixed order.
var Keys = []string{"api_base_url", "rpc_url", "ws_url", "timeout", "log_level", "output"}

// Config contains validated settings.
type Config struct {
	APIBaseURL string
	RPCURL     string
	WSURL      string
	Timeout    time.Duration
	LogLevel   zerolog.Level
	Output     string
}

// Defaults returns a new map of default values.
func Defaults() map[string]string {
	return map[string]string{
		"api_base_url": pumpfun.DefaultAPIBaseURL, "rpc_url": pumpfun.DefaultRPCURL, "ws_url": pumpfun.DefaultWSURL,
		"timeout": pumpfun.DefaultTimeout.String(), "log_level": "warn", "output": "table",
	}
}

// Path uses an explicit path, PUMPFUN_CONFIG_FILE, or the OS config directory.
// XDG_CONFIG_HOME has priority over the OS directory on all systems.
func Path(explicit string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	if path := os.Getenv("PUMPFUN_CONFIG_FILE"); path != "" {
		return path, nil
	}
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir != "" && !filepath.IsAbs(dir) {
		return "", errors.New("XDG_CONFIG_HOME must be an absolute path")
	}
	if dir == "" {
		var err error
		dir, err = os.UserConfigDir()
		if err != nil {
			return "", errors.New("the OS config directory is not available")
		}
	}
	return filepath.Join(dir, "pumpfun", "config.yaml"), nil
}

// Store reads and writes a YAML file. File writes are atomic and use a lock.
type Store struct {
	Path string
}

// Read returns file values only. An absent file is an empty configuration.
func (s Store) Read() (map[string]string, error) {
	f, err := os.Open(s.Path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, errors.New("the config file could not be opened")
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, 64*1024+1))
	if err != nil || len(data) > 64*1024 {
		return nil, errors.New("the config file could not be read or exceeds 64 KiB")
	}
	decoder := yaml.NewDecoder(strings.NewReader(string(data)))
	values := map[string]string{}
	if err := decoder.Decode(&values); err != nil && !errors.Is(err, io.EOF) {
		return nil, errors.New("the config file must contain one YAML map of setting names and values")
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, errors.New("the config file must contain one YAML document")
	}
	for key := range values {
		if !knownKey(key) {
			return nil, fmt.Errorf("the config key %q is not supported", key)
		}
	}
	if values == nil {
		values = map[string]string{}
	}
	return values, nil
}

// Resolve applies flag > environment > file > default precedence.
// It permits invalid values so that config get and list can help fix them.
func (s Store) Resolve(flags map[string]string) (map[string]string, error) {
	file, err := s.Read()
	if err != nil {
		return nil, err
	}
	values := Defaults()
	for _, key := range Keys {
		if value, ok := file[key]; ok {
			values[key] = value
		}
		if value, ok := os.LookupEnv("PUMPFUN_" + strings.ToUpper(key)); ok {
			values[key] = value
		}
		if value, ok := flags[key]; ok {
			values[key] = value
		}
	}
	return values, nil
}

// Load resolves and validates all settings.
func (s Store) Load(flags map[string]string) (Config, error) {
	values, err := s.Resolve(flags)
	if err != nil {
		return Config{}, err
	}
	for _, key := range Keys {
		if err := Validate(key, values[key]); err != nil {
			return Config{}, err
		}
	}
	timeout, _ := time.ParseDuration(values["timeout"])
	level, _ := zerolog.ParseLevel(values["log_level"])
	return Config{APIBaseURL: values["api_base_url"], RPCURL: values["rpc_url"], WSURL: values["ws_url"], Timeout: timeout, LogLevel: level, Output: values["output"]}, nil
}

// Validate checks one setting. Error messages do not include its value.
func Validate(key, value string) error {
	switch key {
	case "api_base_url", "rpc_url", "ws_url":
		u, err := url.Parse(value)
		if err != nil || len(value) > 4096 || u.Hostname() == "" || u.User != nil || u.Fragment != "" {
			return fmt.Errorf("%s must be a URL with a host and no user data or fragment", key)
		}
		if key == "ws_url" {
			if u.Scheme != "ws" && u.Scheme != "wss" {
				return errors.New("ws_url must use ws or wss")
			}
		} else if u.Scheme != "http" && u.Scheme != "https" {
			return fmt.Errorf("%s must use http or https", key)
		}
	case "timeout":
		d, err := time.ParseDuration(value)
		if err != nil || d <= 0 || d > 5*time.Minute {
			return errors.New("timeout must be greater than zero and at most 5m")
		}
	case "log_level":
		switch value {
		case "trace", "debug", "info", "warn", "error", "disabled":
		default:
			return errors.New("log_level must be trace, debug, info, warn, error, or disabled")
		}
	case "output":
		if value != "table" && value != "json" && value != "yaml" {
			return errors.New("output must be table, json, or yaml")
		}
	default:
		return fmt.Errorf("the config key %q is not supported", key)
	}
	return nil
}

func knownKey(key string) bool {
	_, ok := Defaults()[key]
	return ok
}

// Set validates a value and writes it to the file.
func (s Store) Set(ctx context.Context, key, value string) error {
	if err := Validate(key, value); err != nil {
		return err
	}
	return s.update(ctx, key, &value)
}

// Unset removes a file value. It does not change flags or environment values.
func (s Store) Unset(ctx context.Context, key string) error {
	if !knownKey(key) {
		return fmt.Errorf("the config key %q is not supported", key)
	}
	return s.update(ctx, key, nil)
}

func (s Store) update(ctx context.Context, key string, value *string) error {
	dir := filepath.Dir(s.Path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return errors.New("the config directory could not be created")
	}
	lock := flock.New(s.Path + ".lock")
	defer lock.Close()
	lockCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	locked, err := lock.TryLockContext(lockCtx, 25*time.Millisecond)
	if err != nil || !locked {
		return errors.New("the config file lock could not be acquired within 5 seconds")
	}
	values, err := s.Read()
	if err != nil {
		return err
	}
	if value == nil {
		delete(values, key)
	} else {
		values[key] = *value
	}
	data, err := yaml.Marshal(values)
	if err != nil {
		return errors.New("the config data could not be encoded")
	}
	temp, err := os.CreateTemp(dir, ".config-*")
	if err != nil {
		return errors.New("a temporary config file could not be created")
	}
	defer os.Remove(temp.Name())
	defer temp.Close()
	if _, err := temp.Write(data); err != nil {
		return errors.New("the config data could not be written")
	}
	if err := temp.Sync(); err != nil {
		return errors.New("the config data could not be saved to disk")
	}
	if err := temp.Close(); err != nil {
		return errors.New("the temporary config file could not be closed")
	}
	if err := os.Rename(temp.Name(), s.Path); err != nil {
		return errors.New("the config file could not be replaced")
	}
	return nil
}
