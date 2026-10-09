package client

import (
	"context"

	"github.com/vasyza/pumpfun-sdk/internal/coins"
	"github.com/vasyza/pumpfun-sdk/internal/curve"
	"github.com/vasyza/pumpfun-sdk/internal/models"
	"github.com/vasyza/pumpfun-sdk/internal/solana"
	"github.com/vasyza/pumpfun-sdk/internal/stream"
	"github.com/vasyza/pumpfun-sdk/internal/trades"
	"github.com/vasyza/pumpfun-sdk/internal/transport"
)

type Options = transport.Options
type RetryPolicy = transport.RetryPolicy

const (
	DefaultAPIBaseURL = transport.DefaultAPIBaseURL
	DefaultRPCURL     = transport.DefaultRPCURL
	DefaultWSURL      = transport.DefaultWSURL
	DefaultTimeout    = transport.DefaultTimeout
	Version           = transport.Version
)

// Client joins the read services. Its settings are immutable.
type Client struct {
	coins  *coins.Service
	trades *trades.Service
	curve  *curve.Service
	rpc    *solana.Client
	stream *stream.Service
}

// NewClient checks the settings and creates a read client.
func NewClient(opts Options) (*Client, error) {
	t, err := transport.New(opts)
	if err != nil {
		return nil, err
	}
	rpc := solana.New(t)
	return &Client{coins: coins.New(t), trades: trades.New(t), curve: curve.New(rpc), rpc: rpc, stream: stream.New(t)}, nil
}

// GetCoin reads coin data by its Solana mint address.
func (c *Client) GetCoin(ctx context.Context, mint string) (*models.Coin, error) {
	return c.coins.GetCoin(ctx, mint)
}

// ListNewCoins lists coins by creation time, newest first.
func (c *Client) ListNewCoins(ctx context.Context, opts models.PageOptions) (*models.Page[models.Coin], error) {
	return c.coins.ListNewCoins(ctx, opts)
}

// ListTrendingCoins lists coins by market cap, highest first.
// This is a public ranking proxy. It is not a Pump.fun trend score.
func (c *Client) ListTrendingCoins(ctx context.Context, opts models.PageOptions) (*models.Page[models.Coin], error) {
	return c.coins.ListTrendingCoins(ctx, opts)
}

// ListGraduatedCoins lists complete curves by creation time, newest first.
// The frontend API does not expose a sort by graduation time.
func (c *Client) ListGraduatedCoins(ctx context.Context, opts models.PageOptions) (*models.Page[models.Coin], error) {
	return c.coins.ListGraduatedCoins(ctx, opts)
}

// Search finds coins by name, symbol, or mint through the search API.
// The API can include other assets that Pump.fun indexes.
func (c *Client) Search(ctx context.Context, term string, opts models.PageOptions) (*models.Page[models.Coin], error) {
	return c.coins.Search(ctx, term, opts)
}

// GetTrades reads one cursor page of indexed trades for a Solana coin.
func (c *Client) GetTrades(ctx context.Context, mint string, opts models.TradeOptions) (*models.TradePage, error) {
	return c.trades.GetTrades(ctx, mint, opts)
}

// GetUser reads a public profile by its Solana address.
func (c *Client) GetUser(ctx context.Context, address string) (*models.User, error) {
	return c.coins.GetUser(ctx, address)
}

// GetCreator reads the creator address for a coin and its public profile.
// A missing profile does not remove the creator address from the result.
func (c *Client) GetCreator(ctx context.Context, mint string) (*models.CreatorInfo, error) {
	return c.coins.GetCreator(ctx, mint)
}

// ListCreatedCoins reads coins created by a Solana address.
func (c *Client) ListCreatedCoins(ctx context.Context, address string, opts models.PageOptions) (*models.Page[models.Coin], error) {
	return c.coins.ListCreatedCoins(ctx, address, opts)
}

// GetBondingCurve reads the derived account through Solana RPC at confirmed state.
func (c *Client) GetBondingCurve(ctx context.Context, mint string) (*curve.BondingCurve, error) {
	return c.curve.GetBondingCurve(ctx, mint)
}

// GetGraduationProgress reads the curve and Global account in one RPC snapshot.
// Curve completion does not prove that a PumpSwap pool is already open.
func (c *Client) GetGraduationProgress(ctx context.Context, mint string) (*curve.GraduationProgress, error) {
	return c.curve.GetGraduationProgress(ctx, mint)
}

// GetHolders reads the 20 largest token accounts, the supply, and their owners.
// RPC can return fewer than 20 accounts. A closed account has no owner in the result.
func (c *Client) GetHolders(ctx context.Context, mint string) (*solana.HolderInfo, error) {
	return c.rpc.GetHolders(ctx, mint)
}

// Stream reads selected events until the context stops or the handler fails.
// It uses one connection per client. Reconnects have a bounded retry budget.
// Events can be lost during reconnects. PumpPortal meters trade subscriptions.
func (c *Client) Stream(ctx context.Context, opts stream.StreamOptions, handler stream.EventHandler) error {
	return c.stream.Stream(ctx, opts, handler)
}

// StreamNewCoins reads token creation events.
func (c *Client) StreamNewCoins(ctx context.Context, handler stream.EventHandler) error {
	return c.stream.StreamNewCoins(ctx, handler)
}

// StreamTrades reads trades for the selected mints. PumpPortal meters this data.
func (c *Client) StreamTrades(ctx context.Context, mints []string, handler stream.EventHandler) error {
	return c.stream.StreamTrades(ctx, mints, handler)
}

// StreamMigrations reads migration events.
func (c *Client) StreamMigrations(ctx context.Context, handler stream.EventHandler) error {
	return c.stream.StreamMigrations(ctx, handler)
}

// Observe reads events for at most one minute and returns at most 100 events.
// A time limit returns the events received so far. Parent cancellation is an error.
func (c *Client) Observe(ctx context.Context, streams stream.StreamOptions, opts stream.ObserveOptions) (*stream.Observation, error) {
	return c.stream.Observe(ctx, streams, opts)
}
