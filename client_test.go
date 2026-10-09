package pumpfun

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rs/zerolog"
)

const testMint = "DZQPU9RmUyCSyUMmy611562SJToJknBzQSg2pGqapump"
const testCreator = "HKSXVMLXFe6vjNp9sxkiUDNtuSrWzjN4yDwFaRYn9FHj"

func testClient(t *testing.T, handler http.HandlerFunc, change func(*Options)) *Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	opts := Options{
		APIBaseURL: server.URL, RPCURL: server.URL, WSURL: "ws" + strings.TrimPrefix(server.URL, "http"),
		RequestsPerSecond: 100_000, Burst: 100,
		Retry:  &RetryPolicy{MaxRetries: 0, InitialBackoff: time.Millisecond, MaxBackoff: time.Second},
		Logger: zerolog.Nop(),
	}
	if change != nil {
		change(&opts)
	}
	client, err := NewClient(opts)
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func writeJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Error(err)
	}
}

func TestCoinReadsAndPagination(t *testing.T) {
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Accept") != "application/json" || !strings.HasPrefix(r.Header.Get("User-Agent"), "pumpfun-sdk/") {
			t.Error("request headers are missing")
		}
		switch r.URL.Path {
		case "/coins-v2/" + testMint:
			writeJSON(t, w, map[string]any{"mint": testMint, "name": "Test Coin", "total_supply": uint64(18_446_744_073_709_551_615)})
		case "/coins", "/coins/search-v2":
			q := r.URL.Query()
			if q.Get("limit") != "2" || q.Get("offset") != "4" || q.Get("includeNsfw") != "false" || q.Get("order") != "DESC" {
				t.Errorf("query = %v", q)
			}
			if r.URL.Path == "/coins/search-v2" && q.Get("searchTerm") != "A & B" {
				t.Error("search text was not encoded")
			}
			complete := q.Get("complete") == "true"
			writeJSON(t, w, []Coin{{Mint: testMint, Complete: complete}, {Mint: testCreator, Complete: complete}})
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}, nil)
	coin, err := client.GetCoin(context.Background(), testMint)
	if err != nil || coin.TotalSupply != "18446744073709551615" {
		t.Fatalf("coin = %+v, err = %v", coin, err)
	}
	for _, read := range []struct {
		name string
		fn   func(context.Context, PageOptions) (*Page[Coin], error)
		sort string
	}{
		{"new", client.ListNewCoins, "created_timestamp DESC"},
		{"trending", client.ListTrendingCoins, "market_cap DESC"},
		{"graduated", client.ListGraduatedCoins, "created_timestamp DESC"},
		{"search", func(ctx context.Context, p PageOptions) (*Page[Coin], error) { return client.Search(ctx, " A & B ", p) }, "market_cap DESC"},
	} {
		t.Run(read.name, func(t *testing.T) {
			page, err := read.fn(context.Background(), PageOptions{Offset: 4, Limit: 2})
			if err != nil || page == nil {
				t.Fatal(err)
			}
			if !page.HasMore || page.NextOffset == nil || *page.NextOffset != 6 || page.OrderBy != read.sort {
				t.Fatalf("page = %+v", page)
			}
		})
	}
}

func TestTradesUseChainAndCursor(t *testing.T) {
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/trades/"+SolanaMainnet+"/"+testMint || r.URL.Query().Get("cursor") != "A+B/=" {
			t.Errorf("URL = %s", r.URL)
		}
		writeJSON(t, w, map[string]any{"trades": []Trade{{TxID: "sig", BaseAmount: TokenAmount{Raw: "9007199254740993", Decimals: 6}}}, "cursor": "next", "source": "indexed"})
	}, nil)
	page, err := client.GetTrades(context.Background(), testMint, TradeOptions{Limit: 2, Cursor: "A+B/="})
	if err != nil || page.NextCursor != "next" || !page.HasMore || page.Items[0].BaseAmount.Raw != "9007199254740993" {
		t.Fatalf("page = %+v, err = %v", page, err)
	}
}

func TestCreatorAndCreatedCoins(t *testing.T) {
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/coins-v2/" + testMint:
			writeJSON(t, w, Coin{Mint: testMint, Creator: testCreator})
		case "/users/" + testCreator:
			w.WriteHeader(http.StatusNotFound)
		case "/coins-v2/user-created-coins/" + testCreator:
			writeJSON(t, w, map[string]any{"coins": []Coin{{Mint: testMint}}, "count": 2})
		default:
			t.Errorf("path = %s", r.URL.Path)
		}
	}, nil)
	creator, err := client.GetCreator(context.Background(), testMint)
	if err != nil || creator.Address != testCreator || creator.Profile != nil {
		t.Fatalf("creator = %+v, err = %v", creator, err)
	}
	page, err := client.ListCreatedCoins(context.Background(), testCreator, PageOptions{})
	if err != nil || !page.HasMore || page.Total == nil || *page.Total != 2 || *page.NextOffset != 1 {
		t.Fatalf("page = %+v, err = %v", page, err)
	}
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
				writeJSON(t, w, Coin{Mint: testMint})
			}, func(o *Options) { o.Retry.MaxRetries = 2 })
			_, err := client.GetCoin(context.Background(), testMint)
			if retryStatus(status) {
				if err != nil || calls.Load() != 2 {
					t.Fatalf("calls = %d, err = %v", calls.Load(), err)
				}
			} else {
				var typed *Error
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
	_, err := client.GetCoin(context.Background(), testMint)
	var typed *Error
	if !errors.Is(err, ErrRateLimited) || !errors.As(err, &typed) || typed.RetryAfter != 2*time.Minute || calls.Load() != 1 {
		t.Fatalf("calls = %d, err = %v", calls.Load(), err)
	}
	now := time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC)
	if got := parseRetryAfter(now.Add(time.Minute).Format(http.TimeFormat), now); got != time.Minute {
		t.Fatalf("Retry-After date = %v", got)
	}
}

func TestCancellationAndTimeout(t *testing.T) {
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}, func(o *Options) { o.Timeout = 30 * time.Millisecond })
	_, err := client.GetCoin(context.Background(), testMint)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("timeout error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = client.GetCoin(ctx, testMint)
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
		writeJSON(t, w, Coin{Mint: testMint})
	}, func(o *Options) { o.RequestsPerSecond, o.Burst = 25, 1 })
	var wg sync.WaitGroup
	for range 3 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := client.GetCoin(context.Background(), testMint); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if len(times) != 3 || times[2].Sub(times[0]) < 65*time.Millisecond {
		t.Fatalf("request times = %v", times)
	}
}

func TestMalformedAndOversizeResponses(t *testing.T) {
	for _, body := range []string{"null", "{}", "[]", "<html>error</html>", `{"mint":"wrong"}`, `{"mint":"` + testMint + `"} {}`} {
		t.Run(body, func(t *testing.T) {
			client := testClient(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, body) }, nil)
			_, err := client.GetCoin(context.Background(), testMint)
			if !errors.Is(err, ErrDecode) {
				t.Fatalf("error = %v", err)
			}
		})
	}
	client := testClient(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, strings.Repeat("x", 33)) }, func(o *Options) { o.MaxResponseBytes = 32 })
	if _, err := client.GetCoin(context.Background(), testMint); !errors.Is(err, ErrDecode) {
		t.Fatalf("oversize error = %v", err)
	}
}

func TestInvalidInputDoesNotSendRequests(t *testing.T) {
	var calls atomic.Int32
	client := testClient(t, func(http.ResponseWriter, *http.Request) { calls.Add(1) }, nil)
	checks := []func() error{
		func() error { _, err := client.GetCoin(context.Background(), "../coins"); return err },
		func() error { _, err := client.ListNewCoins(context.Background(), PageOptions{Limit: 101}); return err },
		func() error { _, err := client.ListNewCoins(context.Background(), PageOptions{Offset: -1}); return err },
		func() error { _, err := client.Search(context.Background(), " ", PageOptions{}); return err },
		func() error {
			_, err := client.GetTrades(context.Background(), testMint, TradeOptions{Limit: -1})
			return err
		},
	}
	for _, check := range checks {
		if err := check(); !errors.Is(err, ErrInvalidArgument) {
			t.Errorf("error = %v", err)
		}
	}
	if calls.Load() != 0 {
		t.Fatal("invalid input sent an HTTP request")
	}
}

func TestRawAmountRetainsPrecision(t *testing.T) {
	for _, input := range []string{`9007199254740993`, `"9007199254740993"`} {
		var amount RawAmount
		if err := json.Unmarshal([]byte(input), &amount); err != nil {
			t.Fatal(err)
		}
		data, err := json.Marshal(amount)
		if err != nil || string(data) != `"9007199254740993"` {
			t.Fatalf("amount = %s, err = %v", data, err)
		}
	}
	for _, input := range []string{`-1`, `1.5`, `"no"`} {
		var amount RawAmount
		if err := json.Unmarshal([]byte(input), &amount); err == nil {
			t.Errorf("accepted %s", input)
		}
	}
}
