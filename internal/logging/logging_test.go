package logging

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/rs/zerolog"
)

func TestMCPLoggerUsesStructuredZerologOutput(t *testing.T) {
	var buffer bytes.Buffer
	logger := Slog(New(&buffer, zerolog.InfoLevel)).With("server", "pumpfun").WithGroup("request")
	logger.Info("Read complete.", slog.Int("count", 2))
	var record map[string]any
	if err := json.Unmarshal(buffer.Bytes(), &record); err != nil {
		t.Fatal(err)
	}
	if record["server"] != "pumpfun" || record["request.count"] != float64(2) || record["level"] != "info" {
		t.Fatalf("record = %v", record)
	}
}
