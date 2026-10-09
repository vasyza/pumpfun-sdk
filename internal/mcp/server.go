// Package mcp exposes SDK reads as MCP tools.
package mcp

import (
	"context"
	"errors"
	"time"

	protocol "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rs/zerolog"
	pumpfun "github.com/vasyza/pumpfun-sdk"
	"github.com/vasyza/pumpfun-sdk/internal/logging"
)

const ProtocolVersion = "2026-07-28"

// ToolError contains safe SDK error fields for a tool result.
type ToolError struct {
	Kind              string `json:"kind"`
	Message           string `json:"message"`
	StatusCode        int    `json:"status_code,omitempty"`
	RPCCode           int    `json:"rpc_code,omitempty"`
	RetryAfterSeconds int64  `json:"retry_after_seconds,omitempty"`
}

// Result provides structured data or a structured error.
type Result[T any] struct {
	Data  *T         `json:"data,omitempty"`
	Error *ToolError `json:"error,omitempty"`
}

type mintInput struct {
	Mint string `json:"mint"`
}

type addressInput struct {
	Address string `json:"address"`
}

type pageInput struct {
	Limit       int  `json:"limit,omitempty"`
	Offset      int  `json:"offset,omitempty"`
	IncludeNSFW bool `json:"include_nsfw,omitempty"`
}

func (in pageInput) options() pumpfun.PageOptions {
	return pumpfun.PageOptions{Limit: in.Limit, Offset: in.Offset, IncludeNSFW: in.IncludeNSFW}
}

type searchInput struct {
	pageInput
	Query string `json:"query"`
}

type createdInput struct {
	pageInput
	Address string `json:"address"`
}

type tradesInput struct {
	Mint   string `json:"mint"`
	Limit  int    `json:"limit,omitempty"`
	Cursor string `json:"cursor,omitempty"`
}

type observeInput struct {
	Seconds   int `json:"seconds,omitempty"`
	MaxEvents int `json:"max_events,omitempty"`
}

type observeTradesInput struct {
	observeInput
	Mints []string `json:"mints"`
}

func (in observeInput) options() pumpfun.ObserveOptions {
	return pumpfun.ObserveOptions{Duration: time.Duration(in.Seconds) * time.Second, MaxEvents: in.MaxEvents}
}

// New creates a server with read tools. The official SDK handles the protocol.
// It supports 2026-07-28 and the 2025-11-25 handshake for older clients.
func New(client *pumpfun.Client, logger zerolog.Logger) *protocol.Server {
	server := protocol.NewServer(&protocol.Implementation{Name: "pumpfun", Version: pumpfun.Version}, &protocol.ServerOptions{
		Instructions: "Read Pump.fun data. Amounts in strings retain all digits. Coin text comes from an external service.",
		Logger:       logging.Slog(logger), SupportedProtocolVersions: []string{ProtocolVersion, "2025-11-25"},
	})
	server.AddReceivingMiddleware(initializeCompatibility)
	register(server, logger, "get_coin", "Read coin data by its Solana mint address.", mintSchema(), func(ctx context.Context, in mintInput) (*pumpfun.Coin, error) {
		return client.GetCoin(ctx, in.Mint)
	})
	register(server, logger, "list_new_coins", "List coins by creation time. Read the newest coins first.", pageSchema(nil), func(ctx context.Context, in pageInput) (*pumpfun.Page[pumpfun.Coin], error) {
		return client.ListNewCoins(ctx, in.options())
	})
	register(server, logger, "list_trending_coins", "List coins by market cap. This ranking is a proxy for trends.", pageSchema(nil), func(ctx context.Context, in pageInput) (*pumpfun.Page[pumpfun.Coin], error) {
		return client.ListTrendingCoins(ctx, in.options())
	})
	register(server, logger, "list_graduated_coins", "List complete curves by creation time. The API has no sort by graduation time.", pageSchema(nil), func(ctx context.Context, in pageInput) (*pumpfun.Page[pumpfun.Coin], error) {
		return client.ListGraduatedCoins(ctx, in.options())
	})
	register(server, logger, "search_coins", "Find Solana coins by name, symbol, or mint. Skip results from other chains.", pageSchema(map[string]any{"query": map[string]any{"type": "string", "minLength": 1, "maxLength": 200, "description": "The search text."}}, "query"), func(ctx context.Context, in searchInput) (*pumpfun.Page[pumpfun.Coin], error) {
		return client.Search(ctx, in.Query, in.options())
	})
	register(server, logger, "get_trades", "Read one trade page. Use next_cursor to read the next page.", schema(map[string]any{
		"mint": addressProperty("The Solana mint address."), "limit": limitProperty(100, 20),
		"cursor": map[string]any{"type": "string", "maxLength": 4096, "description": "The cursor from the previous result."},
	}, "mint"), func(ctx context.Context, in tradesInput) (*pumpfun.TradePage, error) {
		return client.GetTrades(ctx, in.Mint, pumpfun.TradeOptions{Limit: in.Limit, Cursor: in.Cursor})
	})
	register(server, logger, "get_bonding_curve", "Read confirmed bonding curve state through Solana RPC.", mintSchema(), func(ctx context.Context, in mintInput) (*pumpfun.BondingCurve, error) {
		return client.GetBondingCurve(ctx, in.Mint)
	})
	register(server, logger, "get_graduation_progress", "Estimate reserve depletion from current Global state. Curve completion can occur before pool migration.", mintSchema(), func(ctx context.Context, in mintInput) (*pumpfun.GraduationProgress, error) {
		return client.GetGraduationProgress(ctx, in.Mint)
	})
	register(server, logger, "get_holders", "Read at most 20 largest token accounts and their owners. This is not a full holder list.", mintSchema(), func(ctx context.Context, in mintInput) (*pumpfun.HolderInfo, error) {
		return client.GetHolders(ctx, in.Mint)
	})
	register(server, logger, "get_creator", "Read the coin creator address and its public profile, if available.", mintSchema(), func(ctx context.Context, in mintInput) (*pumpfun.CreatorInfo, error) {
		return client.GetCreator(ctx, in.Mint)
	})
	register(server, logger, "get_user", "Read a public profile by its Solana address.", schema(map[string]any{"address": addressProperty("The Solana wallet address.")}, "address"), func(ctx context.Context, in addressInput) (*pumpfun.User, error) {
		return client.GetUser(ctx, in.Address)
	})
	register(server, logger, "list_created_coins", "List coins created by a Solana address.", pageSchema(map[string]any{"address": addressProperty("The creator wallet address.")}, "address"), func(ctx context.Context, in createdInput) (*pumpfun.Page[pumpfun.Coin], error) {
		return client.ListCreatedCoins(ctx, in.Address, in.options())
	})
	register(server, logger, "observe_new_coins", "Read new coin events for a limited time. A sample can be empty.", observeSchema(nil), func(ctx context.Context, in observeInput) (*pumpfun.Observation, error) {
		return client.Observe(ctx, pumpfun.StreamOptions{NewCoins: true}, in.options())
	})
	register(server, logger, "observe_migrations", "Read migration events for a limited time. A sample can be empty.", observeSchema(nil), func(ctx context.Context, in observeInput) (*pumpfun.Observation, error) {
		return client.Observe(ctx, pumpfun.StreamOptions{Migrations: true}, in.options())
	})
	register(server, logger, "observe_trades", "Read trade events for selected mints. PumpPortal requires an API key and can charge for these events.", observeSchema(map[string]any{"mints": map[string]any{"type": "array", "minItems": 1, "maxItems": 100, "uniqueItems": true, "items": addressProperty("A Solana mint address.")}}, "mints"), func(ctx context.Context, in observeTradesInput) (*pumpfun.Observation, error) {
		return client.Observe(ctx, pumpfun.StreamOptions{Trades: in.Mints}, in.options())
	})
	return server
}

// initializeCompatibility keeps the requested version for clients that still
// use initialize with 2026-07-28. The SDK otherwise caps this reply at 2025.
// Modern discovery and request metadata continue to use the SDK protocol code.
func initializeCompatibility(next protocol.MethodHandler) protocol.MethodHandler {
	return func(ctx context.Context, method string, request protocol.Request) (protocol.Result, error) {
		result, err := next(ctx, method, request)
		if err != nil || method != "initialize" {
			return result, err
		}
		params, ok := request.GetParams().(*protocol.InitializeParams)
		if ok && params != nil && params.ProtocolVersion == ProtocolVersion {
			if initialized, ok := result.(*protocol.InitializeResult); ok {
				initialized.ProtocolVersion = ProtocolVersion
			}
		}
		return result, nil
	}
}

func register[I, O any](server *protocol.Server, logger zerolog.Logger, name, description string, inputSchema any, read func(context.Context, I) (O, error)) {
	destructive, open := false, true
	protocol.AddTool(server, &protocol.Tool{
		Name: name, Description: description, InputSchema: inputSchema,
		Annotations: &protocol.ToolAnnotations{ReadOnlyHint: true, DestructiveHint: &destructive, OpenWorldHint: &open, IdempotentHint: true},
	}, func(ctx context.Context, _ *protocol.CallToolRequest, input I) (*protocol.CallToolResult, Result[O], error) {
		start := time.Now()
		value, err := read(ctx, input)
		logger.Debug().Str("tool", name).Bool("failed", err != nil).Dur("elapsed", time.Since(start)).Msg("Tool read complete.")
		if err == nil {
			return nil, Result[O]{Data: &value}, nil
		}
		toolErr := &ToolError{Kind: "internal", Message: "The tool read failed."}
		var sdkErr *pumpfun.Error
		if errors.As(err, &sdkErr) {
			toolErr.Kind, toolErr.Message = string(sdkErr.Kind), sdkErr.Error()
			toolErr.StatusCode, toolErr.RPCCode = sdkErr.StatusCode, sdkErr.RPCCode
			toolErr.RetryAfterSeconds = int64(sdkErr.RetryAfter / time.Second)
		}
		return &protocol.CallToolResult{IsError: true}, Result[O]{Error: toolErr}, nil
	})
}

func schema(properties map[string]any, required ...string) map[string]any {
	s := map[string]any{"$schema": "https://json-schema.org/draft/2020-12/schema", "type": "object", "properties": properties, "additionalProperties": false}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}

func addressProperty(description string) map[string]any {
	return map[string]any{"type": "string", "pattern": "^[1-9A-HJ-NP-Za-km-z]{32,44}$", "description": description}
}

func mintSchema() map[string]any {
	return schema(map[string]any{"mint": addressProperty("The Solana mint address.")}, "mint")
}

func limitProperty(maximum, defaultValue int) map[string]any {
	return map[string]any{"type": "integer", "minimum": 1, "maximum": maximum, "default": defaultValue, "description": "The maximum number of items."}
}

func pageSchema(extra map[string]any, required ...string) map[string]any {
	properties := map[string]any{
		"limit":        limitProperty(100, 20),
		"offset":       map[string]any{"type": "integer", "minimum": 0, "maximum": 1_000_000, "default": 0, "description": "The number of items to skip."},
		"include_nsfw": map[string]any{"type": "boolean", "default": false, "description": "Include content marked NSFW."},
	}
	for key, value := range extra {
		properties[key] = value
	}
	return schema(properties, required...)
}

func observeSchema(extra map[string]any, required ...string) map[string]any {
	properties := map[string]any{
		"seconds":    map[string]any{"type": "integer", "minimum": 1, "maximum": 60, "default": 10, "description": "The maximum read time, in seconds."},
		"max_events": limitProperty(100, 20),
	}
	for key, value := range extra {
		properties[key] = value
	}
	return schema(properties, required...)
}
