package coins

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/vasyza/pumpfun-sdk/internal/errs"
	"github.com/vasyza/pumpfun-sdk/internal/models"
	"github.com/vasyza/pumpfun-sdk/internal/solana"
	"github.com/vasyza/pumpfun-sdk/internal/transport"
)

// Service reads coin lists and public profiles.
type Service struct{ transport *transport.Transport }

// New creates a coin service with the supplied transport.
func New(t *transport.Transport) *Service { return &Service{transport: t} }

type Coin = models.Coin
type PageOptions = models.PageOptions
type Page[T any] = models.Page[T]
type User = models.User
type CreatorInfo = models.CreatorInfo

// GetCoin reads coin data by its Solana mint address.
func (c *Service) GetCoin(ctx context.Context, mint string) (*Coin, error) {
	if err := solana.ValidateAddress(mint); err != nil {
		return nil, err
	}
	var coin Coin
	if err := c.transport.Do(ctx, "get_coin", http.MethodGet, c.transport.APIURL("/coins-v2/"+mint, nil), nil, &coin); err != nil {
		return nil, err
	}
	if coin.Mint != mint {
		return nil, errs.Decode("get_coin", errors.New("the response mint does not match the request"))
	}
	return &coin, nil
}

// ListNewCoins lists coins by creation time, newest first.
func (c *Service) ListNewCoins(ctx context.Context, opts PageOptions) (*Page[Coin], error) {
	return c.listCoins(ctx, "list_new_coins", "/coins", "created_timestamp", opts, nil)
}

// ListTrendingCoins lists coins by market cap, highest first.
// This is a public ranking proxy. It is not a Pump.fun trend score.
func (c *Service) ListTrendingCoins(ctx context.Context, opts PageOptions) (*Page[Coin], error) {
	return c.listCoins(ctx, "list_trending_coins", "/coins", "market_cap", opts, nil)
}

// ListGraduatedCoins lists complete curves by creation time, newest first.
// The frontend API does not expose a sort by graduation time.
func (c *Service) ListGraduatedCoins(ctx context.Context, opts PageOptions) (*Page[Coin], error) {
	page, err := c.listCoins(ctx, "list_graduated_coins", "/coins", "created_timestamp", opts, url.Values{"complete": {"true"}})
	if err != nil {
		return nil, err
	}
	for _, coin := range page.Items {
		if !coin.Complete {
			return nil, errs.Decode("list_graduated_coins", errors.New("the server did not apply the complete filter"))
		}
	}
	return page, nil
}

// Search finds coins by name, symbol, or mint through the search API.
// The API can include other assets that Pump.fun indexes.
func (c *Service) Search(ctx context.Context, term string, opts PageOptions) (*Page[Coin], error) {
	term = strings.TrimSpace(term)
	if len(term) == 0 || len(term) > 200 {
		return nil, errs.Invalid("search", "The search text must contain 1 to 200 bytes.")
	}
	return c.listCoins(ctx, "search", "/coins/search-v2", "market_cap", opts, url.Values{"searchTerm": {term}})
}

func (c *Service) listCoins(ctx context.Context, op, path, sort string, opts PageOptions, extra url.Values) (*Page[Coin], error) {
	opts, err := normalizePage(opts)
	if err != nil {
		return nil, err
	}
	q := pageQuery(opts)
	q.Set("sort", sort)
	q.Set("order", "DESC")
	for k, v := range extra {
		q[k] = v
	}
	var coins []Coin
	if err := c.transport.Do(ctx, op, http.MethodGet, c.transport.APIURL(path, q), nil, &coins); err != nil {
		return nil, err
	}
	if coins == nil || len(coins) > opts.Limit {
		return nil, errs.Decode(op, errors.New("the response must be a bounded array"))
	}
	for _, coin := range coins {
		if err := solana.ValidateAddress(coin.Mint); err != nil {
			return nil, errs.Decode(op, err)
		}
	}
	page := newPage(coins, opts)
	page.OrderBy = sort + " DESC"
	return page, nil
}

func normalizePage(opts PageOptions) (PageOptions, error) {
	if opts.Limit == 0 {
		opts.Limit = 20
	}
	if opts.Limit < 1 || opts.Limit > 100 || opts.Offset < 0 || opts.Offset > 1_000_000 {
		return opts, errs.Invalid("pagination", "Use a limit from 1 to 100 and an offset from 0 to 1000000.")
	}
	return opts, nil
}

func pageQuery(opts PageOptions) url.Values {
	return url.Values{
		"limit": {strconv.Itoa(opts.Limit)}, "offset": {strconv.Itoa(opts.Offset)},
		"includeNsfw": {strconv.FormatBool(opts.IncludeNSFW)},
	}
}

func newPage[T any](items []T, opts PageOptions) *Page[T] {
	if items == nil {
		items = []T{}
	}
	p := &Page[T]{Items: items, Limit: opts.Limit, Offset: opts.Offset, HasMore: len(items) == opts.Limit}
	if p.HasMore {
		next := opts.Offset + len(items)
		p.NextOffset = &next
	}
	return p
}

// GetUser reads a public profile by its Solana address.
func (c *Service) GetUser(ctx context.Context, address string) (*User, error) {
	if err := solana.ValidateAddress(address); err != nil {
		return nil, err
	}
	var user User
	if err := c.transport.Do(ctx, "get_user", http.MethodGet, c.transport.APIURL("/users/"+address, nil), nil, &user); err != nil {
		return nil, err
	}
	if user.Address != address {
		return nil, errs.Decode("get_user", errors.New("the profile address does not match the request"))
	}
	return &user, nil
}

// GetCreator reads the creator address for a coin and its public profile.
// A missing profile does not remove the creator address from the result.
func (c *Service) GetCreator(ctx context.Context, mint string) (*CreatorInfo, error) {
	ctx, cancel := context.WithTimeout(ctx, c.transport.Timeout())
	defer cancel()
	coin, err := c.GetCoin(ctx, mint)
	if err != nil {
		return nil, err
	}
	if coin.Creator == "" {
		return nil, &errs.Error{Kind: errs.KindNotFound, Operation: "get_creator", Message: "The API does not expose a creator address."}
	}
	profile, err := c.GetUser(ctx, coin.Creator)
	if err != nil && !errors.Is(err, errs.ErrNotFound) {
		return nil, err
	}
	return &CreatorInfo{Mint: mint, Address: coin.Creator, Profile: profile}, nil
}

// ListCreatedCoins reads coins created by a Solana address.
func (c *Service) ListCreatedCoins(ctx context.Context, address string, opts PageOptions) (*Page[Coin], error) {
	if err := solana.ValidateAddress(address); err != nil {
		return nil, err
	}
	opts, err := normalizePage(opts)
	if err != nil {
		return nil, err
	}
	var wire struct {
		Coins []Coin `json:"coins"`
		Count int    `json:"count"`
	}
	if err := c.transport.Do(ctx, "list_created_coins", http.MethodGet, c.transport.APIURL("/coins-v2/user-created-coins/"+address, pageQuery(opts)), nil, &wire); err != nil {
		return nil, err
	}
	if wire.Coins == nil || len(wire.Coins) > opts.Limit || wire.Count < 0 {
		return nil, errs.Decode("list_created_coins", errors.New("the coin page is not valid"))
	}
	page := newPage(wire.Coins, opts)
	page.Total = &wire.Count
	page.HasMore = opts.Offset+len(wire.Coins) < wire.Count && len(wire.Coins) > 0
	page.NextOffset = nil
	if page.HasMore {
		next := opts.Offset + len(wire.Coins)
		page.NextOffset = &next
	}
	return page, nil
}
