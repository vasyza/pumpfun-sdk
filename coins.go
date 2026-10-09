// Public coins API; implementation lives in internal/coins.
package pumpfun

import (
	sdkCoins "github.com/vasyza/pumpfun-sdk/internal/coins"
)

type Coin = sdkCoins.Coin
type PageOptions = sdkCoins.PageOptions
type Page[T any] = sdkCoins.Page[T]
type User = sdkCoins.User
type CreatorInfo = sdkCoins.CreatorInfo
