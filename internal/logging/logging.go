// Package logging sends structured logs to a zerolog writer.
package logging

import (
	"context"
	"io"
	"log/slog"
	"strings"

	"github.com/rs/zerolog"
)

// New creates a logger that can write from more than one goroutine.
func New(w io.Writer, level zerolog.Level) zerolog.Logger {
	return zerolog.New(zerolog.SyncWriter(w)).With().Timestamp().Logger().Level(level)
}

// Slog adapts the MCP SDK logger interface. Zerolog writes all log records.
func Slog(logger zerolog.Logger) *slog.Logger { return slog.New(handler{logger: logger}) }

type field struct {
	key   string
	value any
}

type handler struct {
	logger zerolog.Logger
	fields []field
	groups []string
}

func level(l slog.Level) zerolog.Level {
	switch {
	case l >= slog.LevelError:
		return zerolog.ErrorLevel
	case l >= slog.LevelWarn:
		return zerolog.WarnLevel
	case l >= slog.LevelInfo:
		return zerolog.InfoLevel
	default:
		return zerolog.DebugLevel
	}
}

func (h handler) Enabled(_ context.Context, l slog.Level) bool {
	return h.logger.GetLevel() != zerolog.Disabled && level(l) >= h.logger.GetLevel()
}

func (h handler) Handle(_ context.Context, record slog.Record) error {
	fields := make(map[string]any, len(h.fields)+record.NumAttrs())
	for _, f := range h.fields {
		fields[f.key] = f.value
	}
	record.Attrs(func(attr slog.Attr) bool {
		for _, f := range flatten(h.groups, attr) {
			fields[f.key] = f.value
		}
		return true
	})
	h.logger.WithLevel(level(record.Level)).Fields(fields).Msg(record.Message)
	return nil
}

func flatten(groups []string, attr slog.Attr) []field {
	attr.Value = attr.Value.Resolve()
	if attr.Equal(slog.Attr{}) {
		return nil
	}
	if attr.Value.Kind() == slog.KindGroup {
		if attr.Key != "" {
			groups = append(append([]string{}, groups...), attr.Key)
		}
		var fields []field
		for _, sub := range attr.Value.Group() {
			fields = append(fields, flatten(groups, sub)...)
		}
		return fields
	}
	key := strings.Join(append(append([]string{}, groups...), attr.Key), ".")
	value := attr.Value.Any()
	if err, ok := value.(error); ok {
		value = err.Error()
	}
	return []field{{key, value}}
}

func (h handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	h.fields = append([]field{}, h.fields...)
	for _, attr := range attrs {
		h.fields = append(h.fields, flatten(h.groups, attr)...)
	}
	return h
}

func (h handler) WithGroup(name string) slog.Handler {
	if name != "" {
		h.groups = append(append([]string{}, h.groups...), name)
	}
	return h
}

// Writer adapts the HTTP server error writer to structured zerolog records.
type Writer struct{ Logger zerolog.Logger }

func (w Writer) Write(p []byte) (int, error) {
	w.Logger.Error().Msg(strings.TrimSpace(string(p)))
	return len(p), nil
}
