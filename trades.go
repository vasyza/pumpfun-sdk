// Public trades API; implementation lives in internal/trades.
package pumpfun

import (
	sdkTrades "github.com/vasyza/pumpfun-sdk/internal/trades"
)

type Trade = sdkTrades.Trade
type TradeOptions = sdkTrades.TradeOptions
type TradePage = sdkTrades.TradePage

const SolanaMainnet = sdkTrades.SolanaMainnet
