// Package testutil provides local test servers for SDK modules.
package testutil

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/vasyza/pumpfun-sdk/internal/transport"
)

const Mint = "DZQPU9RmUyCSyUMmy611562SJToJknBzQSg2pGqapump"
const Creator = "HKSXVMLXFe6vjNp9sxkiUDNtuSrWzjN4yDwFaRYn9FHj"

// Transport creates a local HTTP and WebSocket test transport.
func Transport(t *testing.T, handler http.HandlerFunc, change func(*transport.Options)) *transport.Transport {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	opts := transport.Options{
		APIBaseURL: server.URL, RPCURL: server.URL, WSURL: "ws" + strings.TrimPrefix(server.URL, "http"),
		RequestsPerSecond: 100_000, Burst: 100,
		Retry:  &transport.RetryPolicy{MaxRetries: 0, InitialBackoff: time.Millisecond, MaxBackoff: time.Second},
		Logger: zerolog.Nop(),
	}
	if change != nil {
		change(&opts)
	}
	client, err := transport.New(opts)
	if err != nil {
		t.Fatal(err)
	}
	return client
}

// WriteJSON sends a mock JSON result.
func WriteJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Error(err)
	}
}

// EncodedAccount creates an RPC account in base64 form.
func EncodedAccount(data []byte, owner string) map[string]any {
	return map[string]any{"owner": owner, "executable": false, "data": []string{base64.StdEncoding.EncodeToString(data), "base64"}}
}

// RPCReply sends an RPC result at a fixed slot.
func RPCReply(t *testing.T, w http.ResponseWriter, id uint64, value any) {
	t.Helper()
	WriteJSON(t, w, map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{"context": map[string]any{"slot": 123}, "value": value}})
}
