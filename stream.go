// Public stream API; implementation lives in internal/stream.
package pumpfun

import (
	sdkStream "github.com/vasyza/pumpfun-sdk/internal/stream"
)

type DecimalAmount = sdkStream.DecimalAmount
type StreamEvent = sdkStream.StreamEvent
type StreamOptions = sdkStream.StreamOptions
type EventHandler = sdkStream.EventHandler
type ObserveOptions = sdkStream.ObserveOptions
type Observation = sdkStream.Observation
