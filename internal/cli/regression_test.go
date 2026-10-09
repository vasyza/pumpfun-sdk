package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
)

func TestMixedSearchOutputGolden(t *testing.T) {
	isolatedEnv(t)
	fixture, err := os.ReadFile("../coins/testdata/search-mixed.json")
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/coins/search-v2" || r.URL.Query().Get("offset") != "4" {
			t.Errorf("request = %s", r.URL)
		}
		_, _ = w.Write(fixture)
	}))
	defer server.Close()
	for _, format := range []string{"json", "yaml", "table"} {
		t.Run(format, func(t *testing.T) {
			output := run(t, "search", "pepe", "--api-base-url", server.URL, "--limit", "4", "--offset", "4", "--output", format)
			golden(t, "search_"+format, output)
		})
	}
}

func TestSDKErrorGuidanceFromCLI(t *testing.T) {
	isolatedEnv(t)
	var rpcCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rpcCalls.Add(1)
		var request struct {
			ID     uint64 `json:"id"`
			Method string `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		if request.Method == "getTokenLargestAccounts" {
			w.Header().Set("Retry-After", "120")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		if err := json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": map[string]any{"context": map[string]any{"slot": 1}, "value": nil}}); err != nil {
			t.Error(err)
		}
	}))
	defer server.Close()
	for _, tc := range []struct {
		name    string
		args    []string
		message string
	}{
		{name: "holder rate limit", args: []string{"holders", mint, "--rpc-url", server.URL}, message: "Set rpc_url to a private RPC."},
		{name: "absent curve", args: []string{"curve", mint, "--rpc-url", server.URL}, message: "No bonding curve exists for this mint."},
		{name: "missing stream key", args: []string{"stream", "trades", mint, "--duration", "1s"}, message: "Set ws_url to a PumpPortal URL with api-key."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := Execute(t.Context(), append(tc.args, "--output", "json"), &stdout, &stderr)
			var record map[string]any
			if err := json.Unmarshal(stderr.Bytes(), &record); err != nil {
				t.Fatal(err)
			}
			message, _ := record["error"].(string)
			if code != 1 || stdout.Len() != 0 || !strings.Contains(message, tc.message) || strings.Contains(message, "response is not valid") {
				t.Fatalf("exit = %d, stdout = %s, stderr = %s", code, &stdout, &stderr)
			}
		})
	}
	if rpcCalls.Load() != 2 {
		t.Fatalf("RPC calls = %d", rpcCalls.Load())
	}
}

func TestVersionFlag(t *testing.T) {
	isolatedEnv(t)
	var stdout, stderr bytes.Buffer
	code := Execute(context.Background(), []string{"--version"}, &stdout, &stderr)
	if code != 0 || stdout.String() != "1.0.0\n" || stderr.Len() != 0 {
		t.Fatalf("exit = %d, stdout = %q, stderr = %q", code, stdout.String(), stderr.String())
	}
}

func TestRejectLimitZeroAndOutOfRange(t *testing.T) {
	isolatedEnv(t)
	const testMint = "A13oRB9FFaiUjfi6LdCg6p9ka1u8SfGkUFs4SKvPpump"
	tests := []struct {
		name    string
		args    []string
		message string
	}{
		{name: "coins new limit 0", args: []string{"coins", "new", "--limit", "0"}, message: "Use a limit from 1 to 100"},
		{name: "coins new limit -1", args: []string{"coins", "new", "--limit", "-1"}, message: "Use a limit from 1 to 100"},
		{name: "coins new limit 101", args: []string{"coins", "new", "--limit", "101"}, message: "Use a limit from 1 to 100"},
		{name: "coins new offset -1", args: []string{"coins", "new", "--offset", "-1"}, message: "Use a limit from 1 to 100 and an offset from 0 to 1000000."},
		{name: "coins new offset 1000001", args: []string{"coins", "new", "--offset", "1000001"}, message: "Use a limit from 1 to 100 and an offset from 0 to 1000000."},
		{name: "coins trending limit 0", args: []string{"coins", "trending", "--limit", "0"}, message: "Use a limit from 1 to 100"},
		{name: "coins graduated limit 0", args: []string{"coins", "graduated", "--limit", "0"}, message: "Use a limit from 1 to 100"},
		{name: "coins created limit 0", args: []string{"coins", "created", testMint, "--limit", "0"}, message: "Use a limit from 1 to 100"},
		{name: "search limit 0", args: []string{"search", "pepe", "--limit", "0"}, message: "Use a limit from 1 to 100"},
		{name: "trades limit 0", args: []string{"trades", testMint, "--limit", "0"}, message: "Use a limit from 1 to 100 and a cursor with at most 4096 bytes."},
		{name: "trades limit -1", args: []string{"trades", testMint, "--limit", "-1"}, message: "Use a limit from 1 to 100 and a cursor with at most 4096 bytes."},
		{name: "trades limit 101", args: []string{"trades", testMint, "--limit", "101"}, message: "Use a limit from 1 to 100 and a cursor with at most 4096 bytes."},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := Execute(context.Background(), tc.args, &stdout, &stderr)
			if code != 1 {
				t.Fatalf("expected exit code 1, got %d", code)
			}
			if stdout.Len() != 0 {
				t.Fatalf("expected empty stdout, got %q", stdout.String())
			}
			var record map[string]any
			if err := json.Unmarshal(stderr.Bytes(), &record); err != nil {
				t.Fatalf("stderr not valid JSON: %s", stderr.String())
			}
			msg, _ := record["error"].(string)
			if !strings.Contains(msg, tc.message) {
				t.Fatalf("expected error message to contain %q, got %q", tc.message, msg)
			}
		})
	}
}
