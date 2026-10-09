package solana

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mr-tron/base58"
	"github.com/vasyza/pumpfun-sdk/internal/errs"
	"github.com/vasyza/pumpfun-sdk/internal/testutil"
	"github.com/vasyza/pumpfun-sdk/internal/transport"
)

const testMint = testutil.Mint
const testCreator = testutil.Creator

type Options = transport.Options

func testClient(t *testing.T, handler http.HandlerFunc, change func(*Options)) *Client {
	t.Helper()
	return New(testutil.Transport(t, handler, change))
}

func TestHoldersResolveToken2022Owner(t *testing.T) {
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Method string `json:"method"`
			ID     uint64 `json:"id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		switch request.Method {
		case "getTokenLargestAccounts":
			testutil.RPCReply(t, w, request.ID, []any{map[string]any{"address": testCreator, "amount": "9007199254740993", "decimals": 6}})
		case "getTokenSupply":
			testutil.RPCReply(t, w, request.ID, map[string]any{"amount": "18014398509481986"})
		case "getMultipleAccounts":
			data := make([]byte, 165)
			mint, _ := base58.Decode(testMint)
			owner, _ := base58.Decode(testCreator)
			copy(data, mint)
			copy(data[32:64], owner)
			testutil.RPCReply(t, w, request.ID, []any{testutil.EncodedAccount(data, Token2022Program)})
		default:
			t.Error(request.Method)
		}
	}, nil)
	result, err := client.GetHolders(context.Background(), testMint)
	if err != nil || len(result.Accounts) != 1 || result.Accounts[0].Owner != testCreator || result.Accounts[0].SharePercent != 50 {
		t.Fatalf("result = %+v, err = %v", result, err)
	}
}

func TestRPCErrorAndWrongID(t *testing.T) {
	for _, badID := range []bool{false, true} {
		client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
			var request struct {
				ID uint64 `json:"id"`
			}
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Error(err)
				return
			}
			id := request.ID
			if badID {
				id++
			}
			testutil.WriteJSON(t, w, map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": -32005, "message": "secret"}})
		}, nil)
		_, err := client.GetHolders(context.Background(), testMint)
		if badID && !errors.Is(err, errs.ErrDecode) || !badID && !errors.Is(err, errs.ErrRPC) {
			t.Fatalf("wrong ID = %v, err = %v", badID, err)
		}
	}
}

func TestHoldersRetryRateLimits(t *testing.T) {
	for _, tc := range []struct {
		name       string
		status     int
		code       int
		message    string
		retryAfter string
	}{
		{name: "HTTP", status: http.StatusTooManyRequests},
		{name: "HTTP Retry-After", status: http.StatusTooManyRequests, retryAfter: "1"},
		{name: "RPC code", code: 429, message: "secret"},
		{name: "RPC message", code: -32005, message: "Too many requests: secret"},
		{name: "RPC Retry-After", code: -32005, message: "Rate limit exceeded: secret", retryAfter: "1"},
		{name: "shared budget", status: http.StatusTooManyRequests, code: 429},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var attempts atomic.Int32
			client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				var request struct {
					ID     uint64 `json:"id"`
					Method string `json:"method"`
				}
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
					return
				}
				if request.Method == "getTokenSupply" {
					testutil.RPCReply(t, w, request.ID, map[string]any{"amount": "0"})
					return
				}
				attempt := attempts.Add(1)
				if attempt <= 2 {
					w.Header().Set("Retry-After", tc.retryAfter)
					if tc.status != 0 && (tc.code == 0 || attempt == 1) {
						w.WriteHeader(tc.status)
					} else {
						testutil.WriteJSON(t, w, map[string]any{"jsonrpc": "2.0", "id": request.ID, "error": map[string]any{"code": tc.code, "message": tc.message}})
					}
					return
				}
				testutil.RPCReply(t, w, request.ID, []any{})
			}, func(o *Options) {
				o.Timeout = 5 * time.Second
				o.Retry = &transport.RetryPolicy{MaxRetries: 2, InitialBackoff: time.Millisecond, MaxBackoff: 2 * time.Second}
			})
			start := time.Now()
			result, err := client.GetHolders(t.Context(), testMint)
			if err != nil || result == nil || attempts.Load() != 3 {
				t.Fatalf("result = %+v, attempts = %d, error = %v", result, attempts.Load(), err)
			}
			if tc.retryAfter != "" && time.Since(start) < 2*time.Second {
				t.Fatal("Retry-After was shortened")
			}
		})
	}
}

func TestRPCFinalRateLimitHasPrivateRPCGuidance(t *testing.T) {
	for _, tc := range []struct {
		name       string
		status     int
		code       int
		retryAfter string
		wantCalls  int32
	}{
		{name: "HTTP exhausted", status: 429, wantCalls: 3},
		{name: "RPC exhausted", code: -32005, wantCalls: 3},
		{name: "HTTP long wait", status: 429, retryAfter: "120", wantCalls: 1},
		{name: "RPC long wait", code: 429, retryAfter: "120", wantCalls: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var attempts atomic.Int32
			client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				attempts.Add(1)
				w.Header().Set("Retry-After", tc.retryAfter)
				if tc.status != 0 {
					w.WriteHeader(tc.status)
					return
				}
				var request struct {
					ID uint64 `json:"id"`
				}
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
					return
				}
				testutil.WriteJSON(t, w, map[string]any{"jsonrpc": "2.0", "id": request.ID, "error": map[string]any{"code": tc.code, "message": "Rate limit exceeded: secret"}})
			}, func(o *Options) { o.Retry.MaxRetries = 2 })
			_, err := client.GetHolders(t.Context(), testMint)
			var typed *errs.Error
			if !errors.Is(err, errs.ErrRateLimited) || !errors.As(err, &typed) || typed.Message != rpcRateLimitMessage || typed.RPCCode != tc.code || attempts.Load() != tc.wantCalls || strings.Contains(err.Error(), "secret") {
				t.Fatalf("attempts = %d, error = %+v", attempts.Load(), err)
			}
			if tc.retryAfter != "" && typed.RetryAfter != 2*time.Minute {
				t.Fatalf("Retry-After = %v", typed.RetryAfter)
			}
		})
	}
}

func TestRPCRateLimitBackoffCanBeCanceled(t *testing.T) {
	var attempts atomic.Int32
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		var request struct {
			ID uint64 `json:"id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		w.Header().Set("Retry-After", "1")
		testutil.WriteJSON(t, w, map[string]any{"jsonrpc": "2.0", "id": request.ID, "error": map[string]any{"code": 429}})
	}, func(o *Options) {
		o.Retry = &transport.RetryPolicy{MaxRetries: 2, InitialBackoff: time.Millisecond, MaxBackoff: 2 * time.Second}
	})
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	if _, err := client.GetHolders(ctx, testMint); !errors.Is(err, context.DeadlineExceeded) || attempts.Load() != 1 {
		t.Fatalf("attempts = %d, error = %v", attempts.Load(), err)
	}
}
