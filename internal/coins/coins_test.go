package coins

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/vasyza/pumpfun-sdk/internal/errs"
	"github.com/vasyza/pumpfun-sdk/internal/testutil"
	"github.com/vasyza/pumpfun-sdk/internal/transport"
)

const testMint = testutil.Mint
const testCreator = testutil.Creator

type Options = transport.Options

func testClient(t *testing.T, handler http.HandlerFunc, change func(*Options)) *Service {
	t.Helper()
	return New(testutil.Transport(t, handler, change))
}

func TestCoinReadsAndPagination(t *testing.T) {
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Accept") != "application/json" || !strings.HasPrefix(r.Header.Get("User-Agent"), "pumpfun-sdk/") {
			t.Error("request headers are missing")
		}
		switch r.URL.Path {
		case "/coins-v2/" + testMint:
			testutil.WriteJSON(t, w, map[string]any{"mint": testMint, "name": "Test Coin", "total_supply": uint64(18_446_744_073_709_551_615)})
		case "/coins", "/coins/search-v2":
			q := r.URL.Query()
			if q.Get("limit") != "2" || q.Get("offset") != "4" || q.Get("includeNsfw") != "false" || q.Get("order") != "DESC" {
				t.Errorf("query = %v", q)
			}
			if r.URL.Path == "/coins/search-v2" && q.Get("searchTerm") != "A & B" {
				t.Error("search text was not encoded")
			}
			complete := q.Get("complete") == "true"
			testutil.WriteJSON(t, w, []Coin{{Mint: testMint, Complete: complete}, {Mint: testCreator, Complete: complete}})
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

func TestSearchSkipsOtherChainsAndKeepsSourceOffsets(t *testing.T) {
	fixture, err := os.ReadFile("testdata/search-mixed.json")
	if err != nil {
		t.Fatal(err)
	}
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/coins/search-v2" || r.URL.Query().Get("searchTerm") != "pepe" {
			t.Errorf("request = %s", r.URL)
		}
		switch r.URL.Query().Get("offset") {
		case "4":
			_, _ = w.Write(fixture)
		case "8":
			_, _ = io.WriteString(w, `[{"mint":"0x1"},{"mint":"0x2"},{"mint":"0x3"},{"mint":"0x4"}]`)
		case "12":
			_, _ = io.WriteString(w, `[]`)
		default:
			t.Errorf("offset = %s", r.URL.Query().Get("offset"))
		}
	}, nil)
	page, err := client.Search(context.Background(), "pepe", PageOptions{Limit: 4, Offset: 4})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 2 || page.Items[0].Mint != testMint || page.Items[1].TotalSupply != "18446744073709551615" || page.SkippedNonSolana != 2 || !page.HasMore || page.NextOffset == nil || *page.NextOffset != 8 {
		t.Fatalf("mixed page = %+v", page)
	}
	page, err = client.Search(context.Background(), "pepe", PageOptions{Limit: 4, Offset: *page.NextOffset})
	if err != nil || page == nil || page.Items == nil || len(page.Items) != 0 || page.SkippedNonSolana != 4 || !page.HasMore || page.NextOffset == nil || *page.NextOffset != 12 {
		t.Fatalf("other chain page = %+v, error = %v", page, err)
	}
	page, err = client.Search(context.Background(), "pepe", PageOptions{Limit: 4, Offset: *page.NextOffset})
	if err != nil || page == nil || page.Items == nil || page.HasMore || page.NextOffset != nil || page.SkippedNonSolana != 0 {
		t.Fatalf("last page = %+v, error = %v", page, err)
	}
}

func TestSearchStillRejectsInvalidSolanaData(t *testing.T) {
	for _, body := range []string{`null`, `{}`, `[{}]`, `[{"mint":"bad","chain_id":"solana"}]`, `[{"mint":"` + testMint + `","total_supply":"1.5"}]`} {
		t.Run(body, func(t *testing.T) {
			client := testClient(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, body) }, nil)
			if _, err := client.Search(context.Background(), "pepe", PageOptions{}); !errors.Is(err, errs.ErrDecode) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestCreatorAndCreatedCoins(t *testing.T) {
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/coins-v2/" + testMint:
			testutil.WriteJSON(t, w, Coin{Mint: testMint, Creator: testCreator})
		case "/users/" + testCreator:
			w.WriteHeader(http.StatusNotFound)
		case "/coins-v2/user-created-coins/" + testCreator:
			testutil.WriteJSON(t, w, map[string]any{"coins": []Coin{{Mint: testMint}}, "count": 2})
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

func TestMalformedAndOversizeResponses(t *testing.T) {
	for _, body := range []string{"null", "{}", "[]", "<html>error</html>", `{"mint":"wrong"}`, `{"mint":"` + testMint + `"} {}`} {
		t.Run(body, func(t *testing.T) {
			client := testClient(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, body) }, nil)
			_, err := client.GetCoin(context.Background(), testMint)
			if !errors.Is(err, errs.ErrDecode) {
				t.Fatalf("error = %v", err)
			}
		})
	}
	client := testClient(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, strings.Repeat("x", 33)) }, func(o *Options) { o.MaxResponseBytes = 32 })
	if _, err := client.GetCoin(context.Background(), testMint); !errors.Is(err, errs.ErrDecode) {
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
	}
	for _, check := range checks {
		if err := check(); !errors.Is(err, errs.ErrInvalidArgument) {
			t.Errorf("error = %v", err)
		}
	}
	if calls.Load() != 0 {
		t.Fatal("invalid input sent an HTTP request")
	}
}
