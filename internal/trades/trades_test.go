package trades

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/vasyza/pumpfun-sdk/internal/errs"
	"github.com/vasyza/pumpfun-sdk/internal/models"
	"github.com/vasyza/pumpfun-sdk/internal/testutil"
	"github.com/vasyza/pumpfun-sdk/internal/transport"
)

const testMint = testutil.Mint

type Options = transport.Options

func testClient(t *testing.T, handler http.HandlerFunc, change func(*Options)) *Service {
	t.Helper()
	return New(testutil.Transport(t, handler, change))
}

func TestTradesUseChainAndCursor(t *testing.T) {
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/trades/"+SolanaMainnet+"/"+testMint || r.URL.Query().Get("cursor") != "A+B/=" {
			t.Errorf("URL = %s", r.URL)
		}
		testutil.WriteJSON(t, w, map[string]any{"trades": []Trade{{TxID: "sig", BaseAmount: models.TokenAmount{Raw: "9007199254740993", Decimals: 6}}}, "cursor": "next", "source": "indexed"})
	}, nil)
	page, err := client.GetTrades(context.Background(), testMint, TradeOptions{Limit: 2, Cursor: "A+B/="})
	if err != nil || page.NextCursor != "next" || !page.HasMore || page.Items[0].BaseAmount.Raw != "9007199254740993" {
		t.Fatalf("page = %+v, err = %v", page, err)
	}
}

func TestInvalidTradeInputDoesNotSendRequests(t *testing.T) {
	var calls atomic.Int32
	client := testClient(t, func(http.ResponseWriter, *http.Request) { calls.Add(1) }, nil)
	for _, opts := range []TradeOptions{{Limit: -1}, {Limit: 101}, {Cursor: strings.Repeat("x", 4097)}} {
		if _, err := client.GetTrades(t.Context(), testMint, opts); !errors.Is(err, errs.ErrInvalidArgument) {
			t.Fatal(err)
		}
	}
	if calls.Load() != 0 {
		t.Fatal("invalid input sent an HTTP request")
	}
}
