package models

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/vasyza/pumpfun-sdk/internal/errs"
)

// RawAmount stores a non-negative integer as text. JSON output retains all digits.
type RawAmount string

// Uint64 converts a raw amount to a Go integer.
func (a RawAmount) Uint64() (uint64, error) {
	v, err := strconv.ParseUint(string(a), 10, 64)
	if err != nil {
		return 0, errs.Invalid("amount", "The amount must be an unsigned 64-bit integer.")
	}
	return v, nil
}

// UnmarshalJSON accepts an integer or a string from an API response.
func (a *RawAmount) UnmarshalJSON(data []byte) error {
	if bytes.Equal(data, []byte("null")) {
		*a = ""
		return nil
	}
	s := string(data)
	if len(data) > 0 && data[0] == '"' {
		if err := json.Unmarshal(data, &s); err != nil {
			return err
		}
	}
	if s == "" {
		*a = ""
		return nil
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return fmt.Errorf("the amount must contain digits only")
		}
	}
	*a = RawAmount(s)
	return nil
}

// Coin contains data from the frontend API. Timestamps are Unix milliseconds.
// Market caps are estimates from that API. Reserve amounts use raw token units.
type Coin struct {
	Mint                   string    `json:"mint" yaml:"mint"`
	Name                   string    `json:"name" yaml:"name"`
	Symbol                 string    `json:"symbol" yaml:"symbol"`
	Description            string    `json:"description,omitempty" yaml:"description,omitempty"`
	ImageURI               string    `json:"image_uri,omitempty" yaml:"image_uri,omitempty"`
	MetadataURI            string    `json:"metadata_uri,omitempty" yaml:"metadata_uri,omitempty"`
	Creator                string    `json:"creator,omitempty" yaml:"creator,omitempty"`
	BondingCurve           string    `json:"bonding_curve,omitempty" yaml:"bonding_curve,omitempty"`
	AssociatedBondingCurve string    `json:"associated_bonding_curve,omitempty" yaml:"associated_bonding_curve,omitempty"`
	CreatedTimestamp       int64     `json:"created_timestamp" yaml:"created_timestamp"`
	LastTradeTimestamp     int64     `json:"last_trade_timestamp,omitempty" yaml:"last_trade_timestamp,omitempty"`
	Complete               bool      `json:"complete" yaml:"complete"`
	MarketCap              float64   `json:"market_cap" yaml:"market_cap"`
	USDMarketCap           float64   `json:"usd_market_cap" yaml:"usd_market_cap"`
	VirtualTokenReserves   RawAmount `json:"virtual_token_reserves,omitempty" yaml:"virtual_token_reserves,omitempty"`
	VirtualQuoteReserves   RawAmount `json:"virtual_quote_reserves,omitempty" yaml:"virtual_quote_reserves,omitempty"`
	RealTokenReserves      RawAmount `json:"real_token_reserves,omitempty" yaml:"real_token_reserves,omitempty"`
	RealQuoteReserves      RawAmount `json:"real_quote_reserves,omitempty" yaml:"real_quote_reserves,omitempty"`
	VirtualSOLReserves     RawAmount `json:"virtual_sol_reserves,omitempty" yaml:"virtual_sol_reserves,omitempty"`
	RealSOLReserves        RawAmount `json:"real_sol_reserves,omitempty" yaml:"real_sol_reserves,omitempty"`
	TotalSupply            RawAmount `json:"total_supply,omitempty" yaml:"total_supply,omitempty"`
	TotalSupplyExact       string    `json:"total_supply_str,omitempty" yaml:"total_supply_str,omitempty"`
	QuoteMint              string    `json:"quote_mint,omitempty" yaml:"quote_mint,omitempty"`
	BaseDecimals           uint8     `json:"base_decimals" yaml:"base_decimals"`
	QuoteDecimals          uint8     `json:"quote_decimals" yaml:"quote_decimals"`
	TokenProgram           string    `json:"token_program,omitempty" yaml:"token_program,omitempty"`
	Program                string    `json:"program,omitempty" yaml:"program,omitempty"`
	ChainID                string    `json:"chain_id,omitempty" yaml:"chain_id,omitempty"`
	PumpSwapPool           string    `json:"pump_swap_pool,omitempty" yaml:"pump_swap_pool,omitempty"`
	RaydiumPool            string    `json:"raydium_pool,omitempty" yaml:"raydium_pool,omitempty"`
	Website                string    `json:"website,omitempty" yaml:"website,omitempty"`
	Twitter                string    `json:"twitter,omitempty" yaml:"twitter,omitempty"`
	Telegram               string    `json:"telegram,omitempty" yaml:"telegram,omitempty"`
	NSFW                   bool      `json:"nsfw" yaml:"nsfw"`
}

// PageOptions selects an offset page. A zero limit selects 20 items.
type PageOptions struct {
	Offset      int
	Limit       int
	IncludeNSFW bool
}

// Page contains one offset page. A full page can have an empty next page.
// Search offsets count all source entries, including other chains that it skips.
type Page[T any] struct {
	Items            []T    `json:"items" yaml:"items"`
	Offset           int    `json:"offset" yaml:"offset"`
	Limit            int    `json:"limit" yaml:"limit"`
	HasMore          bool   `json:"has_more" yaml:"has_more"`
	NextOffset       *int   `json:"next_offset,omitempty" yaml:"next_offset,omitempty"`
	Total            *int   `json:"total,omitempty" yaml:"total,omitempty"`
	OrderBy          string `json:"order_by,omitempty" yaml:"order_by,omitempty"`
	SkippedNonSolana int    `json:"skipped_non_solana,omitempty" yaml:"skipped_non_solana,omitempty"`
}

// TokenAmount contains an exact raw amount and its decimal scale.
type TokenAmount struct {
	Raw      RawAmount `json:"raw" yaml:"raw"`
	Decimals uint8     `json:"decimals" yaml:"decimals"`
}

// AddressInfo contains a trader address.
type AddressInfo struct {
	Address string `json:"address" yaml:"address"`
}

// PoolInfo identifies the trade pool and chain.
type PoolInfo struct {
	ChainID string `json:"chainId" yaml:"chain_id"`
	Address string `json:"address" yaml:"address"`
}

// QuoteInfo identifies the quote asset.
type QuoteInfo struct {
	ID string `json:"id" yaml:"id"`
}

// Trade contains one indexed trade. Prices retain the source decimal text.
type Trade struct {
	OrdinalKey  string      `json:"ordinalKey" yaml:"ordinal_key"`
	BlockID     string      `json:"blockId" yaml:"block_id"`
	BlockTimeMS int64       `json:"blockTimeMs" yaml:"block_time_ms"`
	TxID        string      `json:"txId" yaml:"tx_id"`
	Side        string      `json:"side" yaml:"side"`
	Kind        string      `json:"kind" yaml:"kind"`
	Venue       string      `json:"venue" yaml:"venue"`
	Pool        PoolInfo    `json:"pool" yaml:"pool"`
	Trader      AddressInfo `json:"trader" yaml:"trader"`
	BaseAmount  TokenAmount `json:"baseAmount" yaml:"base_amount"`
	QuoteAmount TokenAmount `json:"quoteAmount" yaml:"quote_amount"`
	Quote       QuoteInfo   `json:"quote" yaml:"quote"`
	PriceUSD    string      `json:"priceUsd" yaml:"price_usd"`
	PriceQuote  string      `json:"priceQuote" yaml:"price_quote"`
	ValueUSD    string      `json:"valueUsd" yaml:"value_usd"`
	ValueNative string      `json:"valueNative" yaml:"value_native"`
}

// TradeOptions selects a cursor page. Copy NextCursor from the previous result.
type TradeOptions struct {
	Limit  int
	Cursor string
}

// TradePage contains a cursor page from the indexed trade API.
type TradePage struct {
	Items      []Trade `json:"items" yaml:"items"`
	Limit      int     `json:"limit" yaml:"limit"`
	NextCursor string  `json:"next_cursor,omitempty" yaml:"next_cursor,omitempty"`
	HasMore    bool    `json:"has_more" yaml:"has_more"`
	Source     string  `json:"source" yaml:"source"`
}

// User contains the public profile fields that the frontend API exposes.
type User struct {
	Address      string `json:"address" yaml:"address"`
	Username     string `json:"username,omitempty" yaml:"username,omitempty"`
	ProfileImage string `json:"profile_image,omitempty" yaml:"profile_image,omitempty"`
	Bio          string `json:"bio,omitempty" yaml:"bio,omitempty"`
	Followers    int    `json:"followers" yaml:"followers"`
	Following    int    `json:"following" yaml:"following"`
}

// CreatorInfo contains the coin creator address and an optional public profile.
type CreatorInfo struct {
	Mint    string `json:"mint" yaml:"mint"`
	Address string `json:"address" yaml:"address"`
	Profile *User  `json:"profile,omitempty" yaml:"profile,omitempty"`
}
