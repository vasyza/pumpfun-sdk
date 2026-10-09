package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	protocol "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rs/zerolog"
	pumpfun "github.com/vasyza/pumpfun-sdk"
	"github.com/vasyza/pumpfun-sdk/internal/logging"
)

const mint = "DZQPU9RmUyCSyUMmy611562SJToJknBzQSg2pGqapump"

func testServer(t *testing.T, handler http.HandlerFunc) *protocol.Server {
	t.Helper()
	upstream := httptest.NewServer(handler)
	t.Cleanup(upstream.Close)
	client, err := pumpfun.NewClient(pumpfun.Options{APIBaseURL: upstream.URL, RPCURL: upstream.URL, RequestsPerSecond: 100_000, Burst: 100, Retry: &pumpfun.RetryPolicy{MaxRetries: 0}})
	if err != nil {
		t.Fatal(err)
	}
	return New(client, zerolog.Nop())
}

func meta() map[string]any {
	return map[string]any{
		protocol.MetaKeyProtocolVersion: ProtocolVersion, protocol.MetaKeyClientCapabilities: map[string]any{},
		protocol.MetaKeyClientInfo: map[string]any{"name": "test", "version": "1.0.0"},
	}
}

func wireCall(t *testing.T, handler http.Handler, method string, params map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	if params == nil {
		params = map[string]any{}
	}
	if _, ok := params["_meta"]; !ok {
		params["_meta"] = meta()
	}
	data, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "http://127.0.0.1:8080/mcp", bytes.NewReader(data))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")
	request.Header.Set("MCP-Method", method)
	if name, ok := params["name"].(string); ok {
		request.Header.Set("MCP-Name", name)
	}
	request.Header.Set("MCP-Protocol-Version", params["_meta"].(map[string]any)[protocol.MetaKeyProtocolVersion].(string))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}

func TestModernDiscoveryAndToolSchemas(t *testing.T) {
	var upstreamCalls atomic.Int32
	server := testServer(t, func(http.ResponseWriter, *http.Request) { upstreamCalls.Add(1) })
	handler := HTTPHandler(server, zerolog.Nop())
	discovery := wireCall(t, handler, "server/discover", nil)
	if discovery.Code != http.StatusOK || !bytes.Contains(discovery.Body.Bytes(), []byte(`"resultType":"complete"`)) || !bytes.Contains(discovery.Body.Bytes(), []byte(ProtocolVersion)) {
		t.Fatalf("discovery = %d %s", discovery.Code, discovery.Body)
	}
	list := wireCall(t, handler, "tools/list", nil)
	var wire struct {
		Result struct {
			ResultType string           `json:"resultType"`
			Tools      []*protocol.Tool `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(list.Body.Bytes(), &wire); err != nil {
		t.Fatal(err)
	}
	if list.Code != http.StatusOK || wire.Result.ResultType != "complete" || len(wire.Result.Tools) != 15 {
		t.Fatalf("tool list = %d %s", list.Code, list.Body)
	}
	lastName := ""
	for _, tool := range wire.Result.Tools {
		if tool.Name <= lastName || tool.Description == "" || tool.InputSchema == nil || tool.OutputSchema == nil || tool.Annotations == nil || !tool.Annotations.ReadOnlyHint {
			t.Fatalf("tool = %+v", tool)
		}
		lastName = tool.Name
	}
	if upstreamCalls.Load() != 0 || list.Header().Get("Mcp-Session-Id") != "" {
		t.Fatal("discovery called an upstream service or created a session")
	}
}

func TestStructuredSuccessAndErrors(t *testing.T) {
	var upstreamCalls atomic.Int32
	server := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		upstreamCalls.Add(1)
		if r.URL.Path == "/coins-v2/"+mint {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"mint":"` + mint + `","name":"Test","total_supply":9007199254740993}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	})
	handler := HTTPHandler(server, zerolog.Nop())
	response := wireCall(t, handler, "tools/call", map[string]any{"name": "get_coin", "arguments": map[string]any{"mint": mint}})
	var wire struct {
		Result struct {
			IsError           bool `json:"isError"`
			StructuredContent struct {
				Data  *pumpfun.Coin `json:"data"`
				Error *ToolError    `json:"error"`
			} `json:"structuredContent"`
		} `json:"result"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &wire); err != nil {
		t.Fatal(err)
	}
	if response.Code != 200 || wire.Result.IsError || wire.Result.StructuredContent.Data == nil || wire.Result.StructuredContent.Data.TotalSupply != "9007199254740993" {
		t.Fatalf("result = %s", response.Body)
	}
	missing := "HKSXVMLXFe6vjNp9sxkiUDNtuSrWzjN4yDwFaRYn9FHj"
	response = wireCall(t, handler, "tools/call", map[string]any{"name": "get_coin", "arguments": map[string]any{"mint": missing}})
	wire.Result.StructuredContent.Data = nil
	if err := json.Unmarshal(response.Body.Bytes(), &wire); err != nil {
		t.Fatal(err)
	}
	if !wire.Result.IsError || wire.Result.StructuredContent.Error == nil || wire.Result.StructuredContent.Error.Kind != "not_found" || wire.Result.StructuredContent.Data != nil {
		t.Fatalf("error result = %s", response.Body)
	}
	for _, arguments := range []map[string]any{{"mint": "invalid"}, {"mint": mint, "private_key": "unused"}} {
		response = wireCall(t, handler, "tools/call", map[string]any{"name": "get_coin", "arguments": arguments})
		if !bytes.Contains(response.Body.Bytes(), []byte(`"isError":true`)) {
			t.Fatalf("invalid input result = %s", response.Body)
		}
	}
	if upstreamCalls.Load() != 2 {
		t.Fatalf("upstream calls = %d", upstreamCalls.Load())
	}
}

func TestObserveTradesReturnsMissingKeyError(t *testing.T) {
	var upstreamCalls atomic.Int32
	server := testServer(t, func(http.ResponseWriter, *http.Request) { upstreamCalls.Add(1) })
	response := wireCall(t, HTTPHandler(server, zerolog.Nop()), "tools/call", map[string]any{
		"name": "observe_trades", "arguments": map[string]any{"mints": []string{mint}, "seconds": 1},
	})
	var reply struct {
		Result struct {
			IsError bool `json:"isError"`
			Content struct {
				Error *ToolError `json:"error"`
			} `json:"structuredContent"`
		} `json:"result"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &reply); err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusOK || !reply.Result.IsError || reply.Result.Content.Error == nil || reply.Result.Content.Error.Kind != "unauthorized" || !strings.Contains(reply.Result.Content.Error.Message, "api-key") || upstreamCalls.Load() != 0 {
		t.Fatalf("upstream calls = %d, result = %s", upstreamCalls.Load(), response.Body)
	}
}

func TestHTTPRejectsHostOriginAndProtocol(t *testing.T) {
	server := testServer(t, func(http.ResponseWriter, *http.Request) {})
	handler := HTTPHandler(server, zerolog.Nop())
	for _, tc := range []struct{ host, origin string }{{"attacker.example", ""}, {"127.0.0.1:8080", "https://attacker.example"}, {"127.0.0.1:8080", "null"}} {
		request := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "http://127.0.0.1:8080/mcp", nil)
		request.Host = tc.host
		request.Header.Set("Origin", tc.origin)
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusForbidden {
			t.Fatalf("host = %s, origin = %s, status = %d", tc.host, tc.origin, recorder.Code)
		}
	}
	badMeta := meta()
	badMeta[protocol.MetaKeyProtocolVersion] = "2027-01-01"
	response := wireCall(t, handler, "tools/list", map[string]any{"_meta": badMeta})
	if response.Code != http.StatusBadRequest || !bytes.Contains(response.Body.Bytes(), []byte(`-32022`)) {
		t.Fatalf("protocol result = %d %s", response.Code, response.Body)
	}
	if err := ServeHTTP(t.Context(), server, "0.0.0.0:8080", zerolog.Nop()); err == nil {
		t.Fatal("the server accepted a remote listen address")
	}
}

func TestOfficialClientOverHTTP(t *testing.T) {
	server := testServer(t, func(http.ResponseWriter, *http.Request) {})
	httpServer := httptest.NewServer(HTTPHandler(server, zerolog.Nop()))
	defer httpServer.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	client := protocol.NewClient(&protocol.Implementation{Name: "test", Version: "1.0.0"}, &protocol.ClientOptions{Logger: logging.Slog(zerolog.Nop())})
	session, err := client.Connect(ctx, &protocol.StreamableClientTransport{Endpoint: httpServer.URL + "/mcp"}, &protocol.ClientSessionOptions{ProtocolVersion: ProtocolVersion})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := session.Close(); err != nil {
			t.Error(err)
		}
	}()
	if session.InitializeResult().ProtocolVersion != ProtocolVersion {
		t.Fatalf("protocol = %s", session.InitializeResult().ProtocolVersion)
	}
	list, err := session.ListTools(ctx, nil)
	if err != nil || len(list.Tools) != 15 {
		t.Fatalf("tools = %+v, err = %v", list, err)
	}
}

func TestHTTPRequestCancellationStopsUpstream(t *testing.T) {
	started := make(chan struct{})
	stopped := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
		close(stopped)
	}))
	defer upstream.Close()
	client, err := pumpfun.NewClient(pumpfun.Options{
		APIBaseURL: upstream.URL, Timeout: 2 * time.Second,
		Retry: &pumpfun.RetryPolicy{MaxRetries: 0},
	})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(HTTPHandler(New(client, zerolog.Nop()), zerolog.Nop()))
	defer server.Close()
	body, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"_meta": meta(), "name": "get_coin", "arguments": map[string]any{"mint": mint}},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, server.URL+"/mcp", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")
	request.Header.Set("Mcp-Protocol-Version", ProtocolVersion)
	request.Header.Set("Mcp-Method", "tools/call")
	request.Header.Set("Mcp-Name", "get_coin")
	done := make(chan error, 1)
	go func() {
		response, err := server.Client().Do(request)
		if response != nil {
			_ = response.Body.Close()
		}
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("The MCP tool did not start its upstream request.")
	}
	cancel()
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("The canceled HTTP request did not stop the upstream read.")
	}
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("request error = %v", err)
	}
}
