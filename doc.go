// Package pumpfun reads Pump.fun data through HTTP, WebSocket, and Solana RPC.
//
// All operations accept a context. The client can be used from more than one
// goroutine. Each client permits one active WebSocket stream. Use Stream to
// subscribe to more than one event type on that connection.
//
// The package does not send transactions or accept private keys.
package pumpfun
