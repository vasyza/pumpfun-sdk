package transport_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vasyza/pumpfun-sdk/internal/errs"
	"github.com/vasyza/pumpfun-sdk/internal/models"
	"github.com/vasyza/pumpfun-sdk/internal/testutil"
	"github.com/vasyza/pumpfun-sdk/internal/transport"
)

type Options = transport.Options
type Transport = transport.Transport

const testMint = testutil.Mint

func testClient(t *testing.T, handler http.HandlerFunc, change func(*Options)) *Transport {
	t.Helper()
	return testutil.Transport(t, handler, change)
}

func readCoin(client *Transport, ctx context.Context, _ string) (*models.Coin, error) {
	var coin models.Coin
	err := client.Do(ctx, "test_read", http.MethodGet, client.APIURL("/test", nil), nil, &coin)
	return &coin, err
}

func TestRetryAndStatusErrors(t *testing.T) {
	for _, status := range []int{400, 401, 403, 404, 408, 429, 500, 502, 503, 504} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var calls atomic.Int32
			client := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
				if calls.Add(1) == 1 {
					w.WriteHeader(status)
					_, _ = io.WriteString(w, "secret data")
					return
				}
				testutil.WriteJSON(t, w, models.Coin{Mint: testMint})
			}, func(o *Options) { o.Retry.MaxRetries = 2 })
			_, err := readCoin(client, context.Background(), testMint)
			if transport.RetryStatus(status) {
				if err != nil || calls.Load() != 2 {
					t.Fatalf("calls = %d, err = %v", calls.Load(), err)
				}
			} else {
				var typed *errs.Error
				if !errors.As(err, &typed) || typed.StatusCode != status || calls.Load() != 1 || strings.Contains(err.Error(), "secret") {
					t.Fatalf("calls = %d, err = %v", calls.Load(), err)
				}
			}
		})
	}
}

func TestRetryAfterIsNotShortened(t *testing.T) {
	var calls atomic.Int32
	client := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.Header().Set("Retry-After", "120")
		w.WriteHeader(http.StatusTooManyRequests)
	}, func(o *Options) { o.Retry.MaxRetries = 3 })
	_, err := readCoin(client, context.Background(), testMint)
	var typed *errs.Error
	if !errors.Is(err, errs.ErrRateLimited) || !errors.As(err, &typed) || typed.RetryAfter != 2*time.Minute || calls.Load() != 1 {
		t.Fatalf("calls = %d, err = %v", calls.Load(), err)
	}
	now := time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC)
	if got := transport.ParseRetryAfter(now.Add(time.Minute).Format(http.TimeFormat), now); got != time.Minute {
		t.Fatalf("Retry-After date = %v", got)
	}
}

func TestDecodedRateErrorRetainsSharedErrorFields(t *testing.T) {
	shared := &errs.Error{Kind: errs.KindRateLimited, Operation: "rpc", RPCCode: 429, Message: "The request rate is too high."}
	client := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "120")
		_, _ = io.WriteString(w, `{}`)
	}, nil)
	err := client.DoDecode(t.Context(), "rpc", http.MethodPost, client.RPCURL(), []byte(`{}`), func([]byte) error { return shared })
	var typed *errs.Error
	if !errors.Is(err, errs.ErrRateLimited) || !errors.As(err, &typed) || typed.RPCCode != 429 || typed.StatusCode != 200 || typed.RetryAfter != 2*time.Minute {
		t.Fatalf("error = %+v", err)
	}
	if shared.StatusCode != 0 || shared.RetryAfter != 0 {
		t.Fatalf("decoder error changed: %+v", shared)
	}
}

func TestCancellationAndTimeout(t *testing.T) {
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}, func(o *Options) { o.Timeout = 30 * time.Millisecond })
	_, err := readCoin(client, context.Background(), testMint)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("timeout error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = readCoin(client, ctx, testMint)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel error = %v", err)
	}
}

func TestRateLimitAcrossGoroutines(t *testing.T) {
	var mu sync.Mutex
	var times []time.Time
	client := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		times = append(times, time.Now())
		mu.Unlock()
		testutil.WriteJSON(t, w, models.Coin{Mint: testMint})
	}, func(o *Options) { o.RequestsPerSecond, o.Burst = 25, 1 })
	var wg sync.WaitGroup
	for range 3 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := readCoin(client, context.Background(), testMint); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if len(times) != 3 || times[2].Sub(times[0]) < 65*time.Millisecond {
		t.Fatalf("request times = %v", times)
	}
}

func TestInterruptedResponseRetries(t *testing.T) {
	var calls atomic.Int32
	client := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 1 {
			hijacker, ok := w.(http.Hijacker)
			if !ok {
				t.Error("The mock server cannot close a response early.")
				return
			}
			conn, buffer, err := hijacker.Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			defer func() { _ = conn.Close() }()
			_, _ = fmt.Fprint(buffer, "HTTP/1.1 200 OK\r\nContent-Length: 1000\r\n\r\n{\"mint\":")
			_ = buffer.Flush()
			return
		}
		testutil.WriteJSON(t, w, models.Coin{Mint: testMint})
	}, func(o *Options) { o.Retry.MaxRetries = 1 })
	coin, err := readCoin(client, t.Context(), testMint)
	if err != nil || coin.Mint != testMint || calls.Load() != 2 {
		t.Fatalf("coin = %+v, calls = %d, error = %v", coin, calls.Load(), err)
	}
}
