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

func TestCoinsPaginationShortPageHasMore(t *testing.T) {
	shortFixture, err := os.ReadFile("testdata/coins-short-page.json")
	if err != nil {
		t.Fatal(err)
	}
	tailFixture, err := os.ReadFile("testdata/coins-tail-page.json")
	if err != nil {
		t.Fatal(err)
	}
	emptyFixture, err := os.ReadFile("testdata/coins-empty-page.json")
	if err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name string
		call func(context.Context, *Service, PageOptions) (*Page[Coin], error)
	}{
		{"new", func(ctx context.Context, c *Service, p PageOptions) (*Page[Coin], error) {
			return c.ListNewCoins(ctx, p)
		}},
		{"trending", func(ctx context.Context, c *Service, p PageOptions) (*Page[Coin], error) {
			return c.ListTrendingCoins(ctx, p)
		}},
		{"graduated", func(ctx context.Context, c *Service, p PageOptions) (*Page[Coin], error) {
			return c.ListGraduatedCoins(ctx, p)
		}},
		{"search", func(ctx context.Context, c *Service, p PageOptions) (*Page[Coin], error) {
			return c.Search(ctx, "test", p)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				q := r.URL.Query()
				if q.Get("limit") != "100" {
					t.Errorf("expected limit=100, got %s", q.Get("limit"))
				}
				switch q.Get("offset") {
				case "0":
					_, _ = w.Write(shortFixture)
				case "70":
					_, _ = w.Write(tailFixture)
				case "100":
					_, _ = w.Write(emptyFixture)
				default:
					t.Errorf("unexpected offset: %s", q.Get("offset"))
				}
			}, nil)

			ctx := context.Background()

			// First page: requests limit=100, API returns 70 items (< limit).
			page1, err := tc.call(ctx, client, PageOptions{Limit: 100, Offset: 0})
			if err != nil {
				t.Fatalf("first page error: %v", err)
			}
			if len(page1.Items) != 70 {
				t.Fatalf("page1 items = %d, want 70", len(page1.Items))
			}
			if !page1.HasMore {
				t.Fatal("expected page1.HasMore == true for short page with 70 items")
			}
			if page1.NextOffset == nil || *page1.NextOffset != 70 {
				t.Fatalf("page1.NextOffset = %v, want 70", page1.NextOffset)
			}

			// Second page: requests limit=100 at offset=70, API returns 30 items.
			page2, err := tc.call(ctx, client, PageOptions{Limit: 100, Offset: *page1.NextOffset})
			if err != nil {
				t.Fatalf("second page error: %v", err)
			}
			if len(page2.Items) != 30 {
				t.Fatalf("page2 items = %d, want 30", len(page2.Items))
			}
			if !page2.HasMore {
				t.Fatal("expected page2.HasMore == true for short page with 30 items")
			}
			if page2.NextOffset == nil || *page2.NextOffset != 100 {
				t.Fatalf("page2.NextOffset = %v, want 100", page2.NextOffset)
			}

			// Third page: requests limit=100 at offset=100, API returns empty array.
			page3, err := tc.call(ctx, client, PageOptions{Limit: 100, Offset: *page2.NextOffset})
			if err != nil {
				t.Fatalf("third page error: %v", err)
			}
			if len(page3.Items) != 0 {
				t.Fatalf("page3 items = %d, want 0", len(page3.Items))
			}
			if page3.HasMore {
				t.Fatal("expected page3.HasMore == false for empty page")
			}
			if page3.NextOffset != nil {
				t.Fatalf("expected page3.NextOffset == nil, got %v", page3.NextOffset)
			}
		})
	}
}

func TestNewPage(t *testing.T) {
	p1 := newPage([]int{1, 2, 3}, PageOptions{Limit: 10, Offset: 5})
	if !p1.HasMore || p1.NextOffset == nil || *p1.NextOffset != 8 {
		t.Fatalf("p1 = %+v", p1)
	}

	p2 := newPage([]int{}, PageOptions{Limit: 10, Offset: 5})
	if p2.HasMore || p2.NextOffset != nil {
		t.Fatalf("p2 = %+v", p2)
	}

	p3 := newPage[int](nil, PageOptions{Limit: 10, Offset: 0})
	if p3.HasMore || p3.NextOffset != nil {
		t.Fatalf("p3 = %+v", p3)
	}
}

func TestCreatedCoinsPaginationEmptyPageRule(t *testing.T) {
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		switch q.Get("offset") {
		case "0":
			testutil.WriteJSON(t, w, map[string]any{
				"coins": []Coin{{Mint: testMint, Name: "Coin 1"}},
				"count": 1,
			})
		case "1":
			testutil.WriteJSON(t, w, map[string]any{
				"coins": []Coin{},
				"count": 1,
			})
		default:
			t.Errorf("unexpected offset: %s", q.Get("offset"))
		}
	}, nil)

	page1, err := client.ListCreatedCoins(context.Background(), testCreator, PageOptions{Limit: 100, Offset: 0})
	if err != nil {
		t.Fatal(err)
	}
	if len(page1.Items) != 1 || !page1.HasMore || page1.NextOffset == nil || *page1.NextOffset != 1 {
		t.Fatalf("expected HasMore=true and NextOffset=1 on non-empty page, got %+v", page1)
	}
	if page1.Total == nil || *page1.Total != 1 {
		t.Fatalf("expected Total=1, got %v", page1.Total)
	}

	page2, err := client.ListCreatedCoins(context.Background(), testCreator, PageOptions{Limit: 100, Offset: *page1.NextOffset})
	if err != nil {
		t.Fatal(err)
	}
	if len(page2.Items) != 0 || page2.HasMore || page2.NextOffset != nil {
		t.Fatalf("expected HasMore=false and NextOffset=nil on empty page, got %+v", page2)
	}
	if page2.Total == nil || *page2.Total != 1 {
		t.Fatalf("expected Total=1, got %v", page2.Total)
	}
}
