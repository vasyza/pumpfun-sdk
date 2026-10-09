package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	protocol "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rs/zerolog"
	pumpfun "github.com/vasyza/pumpfun-sdk"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func initializeMessage(version string) map[string]any {
	return map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "initialize",
		"params": map[string]any{
			"protocolVersion": version, "capabilities": map[string]any{},
			"clientInfo": map[string]any{"name": "test", "version": "1.0.0"},
		},
	}
}

func assertInitialize(t *testing.T, data []byte, version string) {
	t.Helper()
	var reply struct {
		Result *protocol.InitializeResult `json:"result"`
		Error  json.RawMessage            `json:"error"`
	}
	if err := json.Unmarshal(data, &reply); err != nil {
		t.Fatal(err)
	}
	if reply.Result == nil || reply.Result.ProtocolVersion != version || reply.Result.Capabilities.Tools == nil || reply.Result.ServerInfo.Name != "pumpfun" || reply.Result.ServerInfo.Version != "1.0.0" || len(reply.Error) != 0 {
		t.Fatalf("initialize reply = %s", data)
	}
}

func TestInitializeVersionOverHTTP(t *testing.T) {
	for _, version := range []string{ProtocolVersion, "2025-11-25"} {
		for _, header := range []string{"none", "version", "version and method"} {
			for _, metadata := range []string{"absent", "empty", "progress"} {
				t.Run(version+"/"+header+"/"+metadata, func(t *testing.T) {
					server := testServer(t, func(http.ResponseWriter, *http.Request) { t.Error("initialize called an upstream service") })
					message := initializeMessage(version)
					params := message["params"].(map[string]any)
					switch metadata {
					case "empty":
						params["_meta"] = map[string]any{}
					case "progress":
						params["_meta"] = map[string]any{"progressToken": "test"}
					}
					body, err := json.Marshal(message)
					if err != nil {
						t.Fatal(err)
					}
					request := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "http://127.0.0.1:8080/mcp", bytes.NewReader(body))
					request.Header.Set("Content-Type", "application/json")
					request.Header.Set("Accept", "application/json, text/event-stream")
					if header != "none" {
						request.Header.Set("Mcp-Protocol-Version", version)
					}
					if header == "version and method" {
						request.Header.Set("Mcp-Method", "initialize")
					}
					response := httptest.NewRecorder()
					HTTPHandler(server, zerolog.Nop()).ServeHTTP(response, request)
					if response.Code != http.StatusOK {
						t.Fatalf("status = %d, body = %s", response.Code, response.Body)
					}
					assertInitialize(t, response.Body.Bytes(), version)
				})
			}
		}
	}
}

func TestInitializeHTTPAdapterKeepsModernRequestChecks(t *testing.T) {
	server := testServer(t, func(http.ResponseWriter, *http.Request) { t.Error("invalid input called an upstream service") })
	handler := HTTPHandler(server, zerolog.Nop())
	for _, body := range []string{
		`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`,
		strings.Repeat(" ", maxHTTPBodyBytes+1),
	} {
		request := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "http://127.0.0.1:8080/mcp", strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Accept", "application/json, text/event-stream")
		request.Header.Set("Mcp-Protocol-Version", ProtocolVersion)
		request.Header.Set("Mcp-Method", "initialize")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, body = %s", response.Code, response.Body)
		}
	}
}

func TestInitializeVersionOverStdio(t *testing.T) {
	for _, version := range []string{ProtocolVersion, "2025-11-25"} {
		t.Run(version, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestInitializeStdioProcessHelper$")
			command.Env = append(os.Environ(), "PUMPFUN_INITIALIZE_TEST_PROCESS=1")
			var stderr bytes.Buffer
			command.Stderr = &stderr
			input, err := command.StdinPipe()
			if err != nil {
				t.Fatal(err)
			}
			output, err := command.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			if err := command.Start(); err != nil {
				t.Fatal(err)
			}
			defer func() {
				_ = input.Close()
				if err := command.Wait(); err != nil {
					t.Errorf("stdio exit = %v, stderr = %s", err, &stderr)
				}
			}()
			encoder, decoder := json.NewEncoder(input), json.NewDecoder(output)
			if err := encoder.Encode(initializeMessage(version)); err != nil {
				t.Fatal(err)
			}
			var reply json.RawMessage
			if err := decoder.Decode(&reply); err != nil {
				t.Fatal(err)
			}
			assertInitialize(t, reply, version)
			if err := encoder.Encode(map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"}); err != nil {
				t.Fatal(err)
			}
			params := map[string]any{}
			if version == ProtocolVersion {
				params["_meta"] = meta()
			}
			if err := encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/list", "params": params}); err != nil {
				t.Fatal(err)
			}
			var list struct {
				Result struct {
					Tools      []protocol.Tool `json:"tools"`
					ResultType string          `json:"resultType"`
				} `json:"result"`
			}
			if err := decoder.Decode(&list); err != nil {
				t.Fatal(err)
			}
			if len(list.Result.Tools) != 15 || version == ProtocolVersion && list.Result.ResultType != "complete" {
				t.Fatalf("tools/list = %+v", list)
			}
			if err := encoder.Encode(initializeMessage(version)); err != nil {
				t.Fatal(err)
			}
			var duplicate struct {
				Error json.RawMessage `json:"error"`
			}
			if err := decoder.Decode(&duplicate); err != nil || len(duplicate.Error) == 0 {
				t.Fatalf("duplicate initialize = %s, error = %v", duplicate.Error, err)
			}
		})
	}
}

func TestInitializeStdioProcessHelper(t *testing.T) {
	if os.Getenv("PUMPFUN_INITIALIZE_TEST_PROCESS") != "1" {
		t.Skip("This test runs in a child process.")
	}
	client, err := pumpfun.NewClient(pumpfun.Options{Logger: zerolog.Nop()})
	if err != nil {
		os.Exit(1)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := ServeStdio(ctx, New(client, zerolog.Nop())); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(0)
}

func TestServeStdioCanceledContext(t *testing.T) {
	client, err := pumpfun.NewClient(pumpfun.Options{Logger: zerolog.Nop()})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err = ServeStdio(ctx, New(client, zerolog.Nop()))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}
