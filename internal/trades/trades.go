package trades

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"

	"github.com/vasyza/pumpfun-sdk/internal/errs"
	"github.com/vasyza/pumpfun-sdk/internal/models"
	"github.com/vasyza/pumpfun-sdk/internal/solana"
	"github.com/vasyza/pumpfun-sdk/internal/transport"
)

const SolanaMainnet = "solana:5eykt4UsFv8P8NJdTREpY1vzqKqZKvdp"

// Service reads indexed trade pages.
type Service struct{ transport *transport.Transport }

// New creates a trade service with the supplied transport.
func New(t *transport.Transport) *Service { return &Service{transport: t} }

type Trade = models.Trade
type TradeOptions = models.TradeOptions
type TradePage = models.TradePage

// GetTrades reads one cursor page of indexed trades for a Solana coin.
func (c *Service) GetTrades(ctx context.Context, mint string, opts TradeOptions) (*TradePage, error) {
	if err := solana.ValidateAddress(mint); err != nil {
		return nil, err
	}
	if opts.Limit == 0 {
		opts.Limit = 20
	}
	if opts.Limit < 1 || opts.Limit > 100 || len(opts.Cursor) > 4096 {
		return nil, errs.Invalid("get_trades", "Use a limit from 1 to 100 and a cursor with at most 4096 bytes.")
	}
	q := url.Values{"limit": {strconv.Itoa(opts.Limit)}}
	if opts.Cursor != "" {
		q.Set("cursor", opts.Cursor)
	}
	var wire struct {
		Trades []Trade `json:"trades"`
		Cursor string  `json:"cursor"`
		Source string  `json:"source"`
	}
	endpoint := c.transport.APIURL("/trades/"+SolanaMainnet+"/"+mint, q)
	if err := c.transport.Do(ctx, "get_trades", http.MethodGet, endpoint, nil, &wire); err != nil {
		return nil, err
	}
	if wire.Trades == nil || len(wire.Trades) > opts.Limit {
		return nil, errs.Decode("get_trades", errors.New("the trade array is missing or too large"))
	}
	return &TradePage{Items: wire.Trades, Limit: opts.Limit, NextCursor: wire.Cursor, HasMore: wire.Cursor != "", Source: wire.Source}, nil
}
