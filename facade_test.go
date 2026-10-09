package pumpfun_test

import (
	"context"
	"errors"
	pumpfun "github.com/vasyza/pumpfun-sdk"
	"net/http"
	"net/http/httptest"
	"testing"
)

// readAPI fixes the public method set without a dependency on internal types.
type readAPI interface {
	GetCoin(context.Context, string) (*pumpfun.Coin, error)
	ListNewCoins(context.Context, pumpfun.PageOptions) (*pumpfun.Page[pumpfun.Coin], error)
	ListTrendingCoins(context.Context, pumpfun.PageOptions) (*pumpfun.Page[pumpfun.Coin], error)
	ListGraduatedCoins(context.Context, pumpfun.PageOptions) (*pumpfun.Page[pumpfun.Coin], error)
	Search(context.Context, string, pumpfun.PageOptions) (*pumpfun.Page[pumpfun.Coin], error)
	GetTrades(context.Context, string, pumpfun.TradeOptions) (*pumpfun.TradePage, error)
	GetUser(context.Context, string) (*pumpfun.User, error)
	GetCreator(context.Context, string) (*pumpfun.CreatorInfo, error)
	ListCreatedCoins(context.Context, string, pumpfun.PageOptions) (*pumpfun.Page[pumpfun.Coin], error)
	GetBondingCurve(context.Context, string) (*pumpfun.BondingCurve, error)
	GetGraduationProgress(context.Context, string) (*pumpfun.GraduationProgress, error)
	GetHolders(context.Context, string) (*pumpfun.HolderInfo, error)
	Stream(context.Context, pumpfun.StreamOptions, pumpfun.EventHandler) error
	StreamNewCoins(context.Context, pumpfun.EventHandler) error
	StreamTrades(context.Context, []string, pumpfun.EventHandler) error
	StreamMigrations(context.Context, pumpfun.EventHandler) error
	Observe(context.Context, pumpfun.StreamOptions, pumpfun.ObserveOptions) (*pumpfun.Observation, error)
}

var _ readAPI = (*pumpfun.Client)(nil)

func TestFacadeConstructorAndDelegation(t *testing.T) {
	const mint = "DZQPU9RmUyCSyUMmy611562SJToJknBzQSg2pGqapump"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/coins-v2/"+mint {
			t.Error(r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"mint":"` + mint + `","name":"Facade"}`))
	}))
	defer upstream.Close()
	sdk, err := pumpfun.NewClient(pumpfun.Options{APIBaseURL: upstream.URL})
	if err != nil {
		t.Fatal(err)
	}
	coin, err := sdk.GetCoin(t.Context(), mint)
	if err != nil || coin.Name != "Facade" {
		t.Fatalf("coin = %+v, error = %v", coin, err)
	}
	if err := pumpfun.ValidateAddress("bad"); !errors.Is(err, pumpfun.ErrInvalidArgument) {
		t.Fatal(err)
	}
	address, err := pumpfun.DeriveBondingCurveAddress(mint)
	if err != nil || address == "" {
		t.Fatal(err)
	}
	if _, err := pumpfun.DecodeBondingCurve(nil); !errors.Is(err, pumpfun.ErrDecode) {
		t.Fatal(err)
	}
}

func TestFacadeCoinsPaginationShortPage(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/coins" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		q := r.URL.Query()
		if q.Get("limit") != "100" {
			t.Errorf("expected limit=100, got %s", q.Get("limit"))
		}
		switch q.Get("offset") {
		case "0":
			_, _ = w.Write([]byte(`[{"mint":"DZQPU9RmUyCSyUMmy611562SJToJknBzQSg2pGqapump","name":"Coin 1"}]`))
		case "1":
			_, _ = w.Write([]byte(`[]`))
		default:
			t.Errorf("unexpected offset: %s", q.Get("offset"))
		}
	}))
	defer upstream.Close()

	sdk, err := pumpfun.NewClient(pumpfun.Options{APIBaseURL: upstream.URL})
	if err != nil {
		t.Fatal(err)
	}

	page1, err := sdk.ListNewCoins(context.Background(), pumpfun.PageOptions{Limit: 100, Offset: 0})
	if err != nil {
		t.Fatal(err)
	}
	if !page1.HasMore || page1.NextOffset == nil || *page1.NextOffset != 1 {
		t.Fatalf("expected HasMore=true and NextOffset=1, got %+v", page1)
	}

	page2, err := sdk.ListNewCoins(context.Background(), pumpfun.PageOptions{Limit: 100, Offset: *page1.NextOffset})
	if err != nil {
		t.Fatal(err)
	}
	if page2.HasMore || page2.NextOffset != nil {
		t.Fatalf("expected HasMore=false and NextOffset=nil, got %+v", page2)
	}
}

func TestFacadeCreatedCoinsPaginationEmptyPage(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/coins-v2/user-created-coins/2wjsP4wEovqK8WGK8c4y9UXbkPJqMc7g6BhoRJQHjvJw" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		q := r.URL.Query()
		if q.Get("limit") != "100" {
			t.Errorf("expected limit=100, got %s", q.Get("limit"))
		}
		switch q.Get("offset") {
		case "0":
			_, _ = w.Write([]byte(`{"coins":[{"mint":"DZQPU9RmUyCSyUMmy611562SJToJknBzQSg2pGqapump","name":"Coin 1"}],"count":1}`))
		case "1":
			_, _ = w.Write([]byte(`{"coins":[],"count":1}`))
		default:
			t.Errorf("unexpected offset: %s", q.Get("offset"))
		}
	}))
	defer upstream.Close()

	sdk, err := pumpfun.NewClient(pumpfun.Options{APIBaseURL: upstream.URL})
	if err != nil {
		t.Fatal(err)
	}

	page1, err := sdk.ListCreatedCoins(context.Background(), "2wjsP4wEovqK8WGK8c4y9UXbkPJqMc7g6BhoRJQHjvJw", pumpfun.PageOptions{Limit: 100, Offset: 0})
	if err != nil {
		t.Fatal(err)
	}
	if !page1.HasMore || page1.NextOffset == nil || *page1.NextOffset != 1 {
		t.Fatalf("expected HasMore=true and NextOffset=1, got %+v", page1)
	}

	page2, err := sdk.ListCreatedCoins(context.Background(), "2wjsP4wEovqK8WGK8c4y9UXbkPJqMc7g6BhoRJQHjvJw", pumpfun.PageOptions{Limit: 100, Offset: *page1.NextOffset})
	if err != nil {
		t.Fatal(err)
	}
	if page2.HasMore || page2.NextOffset != nil {
		t.Fatalf("expected HasMore=false and NextOffset=nil, got %+v", page2)
	}
}
