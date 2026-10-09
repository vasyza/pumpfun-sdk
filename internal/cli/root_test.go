package cli

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"flag"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rs/zerolog"
	"github.com/spf13/cobra"
	pumpfun "github.com/vasyza/pumpfun-sdk"
	"github.com/vasyza/pumpfun-sdk/internal/config"
	"github.com/vasyza/pumpfun-sdk/internal/logging"
)

var updateGolden = flag.Bool("update", false, "Update golden output files.")

const mint = "DZQPU9RmUyCSyUMmy611562SJToJknBzQSg2pGqapump"
const creator = "HKSXVMLXFe6vjNp9sxkiUDNtuSrWzjN4yDwFaRYn9FHj"

type loopbackTransport struct{ base http.RoundTripper }

func (t loopbackTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	ip := net.ParseIP(r.URL.Hostname())
	if ip == nil || !ip.IsLoopback() {
		return nil, errors.New("default tests only permit loopback HTTP requests")
	}
	return t.base.RoundTrip(r)
}

func TestMain(m *testing.M) {
	http.DefaultTransport = loopbackTransport{base: http.DefaultTransport}
	os.Exit(m.Run())
}

func isolatedEnv(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("PUMPFUN_CONFIG_FILE", "")
	for _, key := range config.Keys {
		name := "PUMPFUN_" + strings.ToUpper(key)
		t.Setenv(name, "")
		if err := os.Unsetenv(name); err != nil {
			t.Fatal(err)
		}
	}
}

func golden(t *testing.T, name, output string) {
	t.Helper()
	path := filepath.Join("testdata", name+".golden")
	if *updateGolden {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(output), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if output != string(want) {
		t.Errorf("output differs from %s\nwant:\n%s\ngot:\n%s", path, want, output)
	}
}

func run(t *testing.T, args ...string) string {
	t.Helper()
	var out, errOut bytes.Buffer
	if code := Execute(t.Context(), args, &out, &errOut); code != 0 {
		t.Fatalf("args = %v, exit = %d, stderr = %s", args, code, &errOut)
	}
	if errOut.Len() != 0 {
		t.Errorf("unexpected stderr: %s", &errOut)
	}
	return out.String()
}

func TestCoinOutputGolden(t *testing.T) {
	isolatedEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"mint":"` + mint + `","name":"Test Coin","symbol":"TEST","complete":false,"created_timestamp":1000,"market_cap":12.5,"usd_market_cap":1250,"total_supply":9007199254740993}]`))
	}))
	defer server.Close()
	for _, format := range []string{"table", "json", "yaml"} {
		t.Run(format, func(t *testing.T) {
			output := run(t, "--api-base-url", server.URL, "coins", "new", "--output", format, "--limit", "1")
			golden(t, "coins_"+format, output)
		})
	}
}

func TestConfigCommandsAndPrecedence(t *testing.T) {
	isolatedEnv(t)
	run(t, "config", "set", "timeout", "12s")
	if got := run(t, "config", "get", "timeout"); got != "12s\n" {
		t.Fatal(got)
	}
	t.Setenv("PUMPFUN_TIMEOUT", "13s")
	if got := run(t, "--timeout", "14s", "config", "get", "timeout"); got != "14s\n" {
		t.Fatal(got)
	}
	if got := run(t, "config", "get", "timeout"); got != "13s\n" {
		t.Fatal(got)
	}
	run(t, "config", "unset", "timeout")
	if err := os.Unsetenv("PUMPFUN_TIMEOUT"); err != nil {
		t.Fatal(err)
	}
	if got := run(t, "config", "get", "timeout"); got != "20s\n" {
		t.Fatal(got)
	}
	golden(t, "config_json", run(t, "config", "list", "--output", "json"))
}

func TestHelpGoldenAndDescriptions(t *testing.T) {
	isolatedEnv(t)
	golden(t, "help", run(t, "--help"))
	var out, errOut bytes.Buffer
	root := NewRoot(&out, &errOut)
	root.SetArgs([]string{"--help"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	var check func(*cobra.Command)
	check = func(cmd *cobra.Command) {
		if cmd.Short == "" || cmd.Long == "" || cmd.Example == "" {
			t.Errorf("help text is missing for %s", cmd.CommandPath())
		}
		args := strings.Fields(strings.TrimPrefix(cmd.CommandPath(), "pumpfun"))
		args = append(args, "--help")
		help := run(t, args...)
		if strings.HasPrefix(cmd.Flags().Lookup("help").Usage, "help for ") || strings.Contains(help, "Simply type") {
			t.Errorf("default help text remains for %s", cmd.CommandPath())
		}
		for _, child := range cmd.Commands() {
			check(child)
		}
	}
	check(root)
}

func TestReadCommandsHaveJSONOutput(t *testing.T) {
	isolatedEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		var result any
		switch {
		case r.Method == http.MethodPost:
			var request struct {
				ID     uint64 `json:"id"`
				Method string `json:"method"`
			}
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Error(err)
				return
			}
			data := make([]byte, 166)
			copy(data, []byte{23, 183, 248, 55, 96, 216, 172, 96})
			account := map[string]any{"owner": pumpfun.PumpProgramID, "data": []string{base64.StdEncoding.EncodeToString(data), "base64"}}
			var value any
			switch request.Method {
			case "getAccountInfo":
				value = account
			case "getMultipleAccounts":
				global := make([]byte, 113)
				copy(global, []byte{167, 232, 232, 177, 200, 108, 114, 127})
				binary.LittleEndian.PutUint64(global[89:97], 100)
				value = []any{account, map[string]any{"owner": pumpfun.PumpProgramID, "data": []string{base64.StdEncoding.EncodeToString(global), "base64"}}}
			case "getTokenLargestAccounts":
				value = []any{}
			case "getTokenSupply":
				value = map[string]any{"amount": "100"}
			}
			result = map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": map[string]any{"context": map[string]any{"slot": 1}, "value": value}}
		case r.URL.Path == "/coins-v2/"+mint:
			result = pumpfun.Coin{Mint: mint, Name: "Test", Creator: creator}
		case strings.HasPrefix(r.URL.Path, "/users/"):
			result = pumpfun.User{Address: creator, Username: "test"}
		case strings.HasPrefix(r.URL.Path, "/trades/"):
			result = map[string]any{"trades": []pumpfun.Trade{}, "source": "indexed"}
		case strings.HasPrefix(r.URL.Path, "/coins-v2/user-created-coins/"):
			result = map[string]any{"coins": []pumpfun.Coin{{Mint: mint}}, "count": 1}
		default:
			result = []pumpfun.Coin{{Mint: mint, Complete: r.URL.Query().Get("complete") == "true"}}
		}
		if err := json.NewEncoder(w).Encode(result); err != nil {
			t.Error(err)
		}
	}))
	defer server.Close()
	for _, args := range [][]string{{"coin", "get", mint}, {"coins", "new"}, {"coins", "trending"}, {"coins", "graduated"}, {"coins", "created", creator}, {"trades", mint}, {"curve", mint}, {"progress", mint}, {"holders", mint}, {"creator", mint}, {"user", creator}, {"search", "test"}} {
		output := run(t, append([]string{"--api-base-url", server.URL, "--rpc-url", server.URL, "--output", "json"}, args...)...)
		if !json.Valid([]byte(output)) {
			t.Errorf("args = %v, output = %s", args, output)
		}
	}
}

func TestCLIErrorIsStructuredAndTableIsSafe(t *testing.T) {
	isolatedEnv(t)
	var out, errOut bytes.Buffer
	if code := Execute(t.Context(), []string{"coin", "get", "invalid"}, &out, &errOut); code != 1 || out.Len() != 0 || !json.Valid(errOut.Bytes()) {
		t.Fatalf("exit = %d, stdout = %s, stderr = %s", code, &out, &errOut)
	}
	out.Reset()
	if err := render(&out, "table", &pumpfun.Coin{Mint: mint, Name: "Test\nName\x1b[31m\u202e"}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "\x1b") || strings.Contains(out.String(), "\u202e") || strings.Contains(out.String(), "Test\nName") {
		t.Fatal("the table contains terminal controls")
	}
}

func TestCLIStreamJSON(t *testing.T) {
	isolatedEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer func() { _ = conn.CloseNow() }()
		_, _, _ = conn.Read(r.Context())
		_ = conn.Write(r.Context(), websocket.MessageText, []byte(`{"txType":"create","mint":"`+mint+`"}`))
		_, _, _ = conn.Read(r.Context())
	}))
	defer server.Close()
	output := run(t, "--ws-url", "ws"+strings.TrimPrefix(server.URL, "http"), "stream", "new", "--count", "1", "--output", "json")
	var event pumpfun.StreamEvent
	if err := json.Unmarshal([]byte(output), &event); err != nil || event.Mint != mint {
		t.Fatalf("output = %s, err = %v", output, err)
	}
}

func TestStdioProcessHelper(t *testing.T) {
	if os.Getenv("PUMPFUN_MCP_TEST_PROCESS") != "1" {
		return
	}
	code := Execute(context.Background(), []string{"--config", os.Getenv("PUMPFUN_MCP_TEST_CONFIG"), "mcp", "serve"}, os.Stdout, os.Stderr)
	os.Exit(code)
}

func TestMCPStdioListsTools(t *testing.T) {
	isolatedEnv(t)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestStdioProcessHelper$")
	command.Env = append(os.Environ(), "PUMPFUN_MCP_TEST_PROCESS=1", "PUMPFUN_MCP_TEST_CONFIG="+filepath.Join(t.TempDir(), "config.yaml"))
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1.0.0"}, &mcp.ClientOptions{Logger: logging.Slog(zerolog.Nop())})
	session, err := client.Connect(ctx, &mcp.CommandTransport{Command: command}, &mcp.ClientSessionOptions{ProtocolVersion: "2026-07-28"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := session.Close(); err != nil {
			t.Error(err)
		}
	}()
	if session.InitializeResult().ProtocolVersion != "2026-07-28" {
		t.Fatalf("protocol = %s", session.InitializeResult().ProtocolVersion)
	}
	list, err := session.ListTools(ctx, nil)
	if err != nil || len(list.Tools) != 15 {
		t.Fatalf("tools = %+v, err = %v", list, err)
	}
}
