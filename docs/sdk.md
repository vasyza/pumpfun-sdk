# Go SDK

Import the root package:

```go
import pumpfun "github.com/vasyza/pumpfun-sdk"
```

The SDK contains the only code that calls Pump.fun and its data services.
It uses direct HTTP, WebSocket, and Solana RPC requests.
The SDK has no CLI, MCP, browser, or config file code.
Each operation accepts a context.
One client can be shared by concurrent readers.

## Package layout

The root package is the public SDK facade.
It contains type aliases, constant aliases, and direct function wrappers.
Each facade file uses its matching internal module.
SDK code and unit tests are in these modules:

| Module | Function |
| --- | --- |
| `internal/client` | Join read services behind the public client. |
| `internal/transport` | Send HTTP requests. Apply retries and rate limits. |
| `internal/coins` | Read coin lists, search results, and public profiles. |
| `internal/trades` | Read indexed trades with cursor pages. |
| `internal/solana` | Send RPC requests. Decode accounts and read holder data. |
| `internal/curve` | Read curve state and estimate graduation progress. |
| `internal/stream` | Read WebSocket events on one connection. |
| `internal/models` | Define data types and retain exact amounts. |
| `internal/errs` | Define typed errors and safe messages. |

CLI and MCP code use the public facade.
SDK modules do not import CLI, MCP, or file configuration code.
Architecture tests check these rules.

## Create a client

```go
client, err := pumpfun.NewClient(pumpfun.Options{
    APIBaseURL: pumpfun.DefaultAPIBaseURL,
    RPCURL:     pumpfun.DefaultRPCURL,
    WSURL:      pumpfun.DefaultWSURL,
    Timeout:    20 * time.Second,
    Logger:     zerolog.New(os.Stderr).With().Timestamp().Logger(),
})
if err != nil {
    return err
}
```

Zero option values select defaults.
An absent logger disables SDK logs.
Use a zerolog writer for standard error when logs are required.
The client copies the supplied HTTP client settings.
It does not change the caller's HTTP client.
The default redirect policy does not follow HTTP redirects.

## Read methods

| Method | Result |
| --- | --- |
| `GetCoin(ctx, mint)` | `*Coin` |
| `ListNewCoins(ctx, PageOptions)` | `*Page[Coin]` |
| `ListTrendingCoins(ctx, PageOptions)` | `*Page[Coin]` |
| `ListGraduatedCoins(ctx, PageOptions)` | `*Page[Coin]` |
| `Search(ctx, query, PageOptions)` | `*Page[Coin]` |
| `GetTrades(ctx, mint, TradeOptions)` | `*TradePage` |
| `GetBondingCurve(ctx, mint)` | `*BondingCurve` |
| `GetGraduationProgress(ctx, mint)` | `*GraduationProgress` |
| `GetHolders(ctx, mint)` | `*HolderInfo` |
| `GetCreator(ctx, mint)` | `*CreatorInfo` |
| `GetUser(ctx, address)` | `*User` |
| `ListCreatedCoins(ctx, address, PageOptions)` | `*Page[Coin]` |

Use `ValidateAddress` to check a Solana public address.
Use `DeriveBondingCurveAddress` to derive the Pump curve address.
Use `DecodeBondingCurve` to decode account bytes that you already have.
When you supply bytes directly, check their account owner first.

The source of each endpoint is in [endpoint sources](endpoints.md).

## Pagination

```go
page, err := client.ListNewCoins(ctx, pumpfun.PageOptions{Limit: 20})
if err != nil {
    return err
}
if page.NextOffset != nil {
    next, err := client.ListNewCoins(ctx, pumpfun.PageOptions{
        Limit: 20,
        Offset: *page.NextOffset,
    })
    if err != nil {
        return err
    }
    _ = next
}
```

`PageOptions` has `Offset`, `Limit`, and `IncludeNSFW` fields.
The default limit is 20.
The maximum limit is 100.
The maximum offset is 1000000.
`Page.OrderBy` states the source ordering.
A full page means that another page can exist.
It does not prove that another page has items.
List changes can move items between offset pages.

Trade pages use `TradeOptions{Limit, Cursor}`.
Copy `TradePage.NextCursor` into the next request.
The SDK preserves the cursor without changing it.

## Amounts and state

`RawAmount` stores unsigned integer text.
It accepts integer or string input from the service.
JSON output always uses a string.
Use `RawAmount.Uint64()` when a Go integer is required.
`TokenAmount.Decimals` gives the scale for trade amounts.
Trade prices retain decimal text from the indexed API.

Curve data contains virtual and real reserves for the token and quote asset.
The zero quote mint identifies native SOL.
The decoder supports the old core layout and the current appended fields.
It checks the discriminator and boolean fields.
RPC reads also check the account owner.

Progress reads the curve and Global account in one confirmed snapshot.
The estimate is:

```text
percent = 100 * (initial_real_token_reserves - real_token_reserves)
              / initial_real_token_reserves
```

The start reserve comes from current Global state.
It can differ from the coin's launch setting.
`Estimated` is therefore true for an incomplete curve.
`Percent` is absent when a safe estimate is not available.
The result includes a reason.
Mayhem curves do not receive this estimate.
A complete curve reports 100 percent.
Completion does not prove that pool migration is complete.

Holder data covers at most 20 largest token accounts.
Wallet owners can occur more than once.
Separate RPC calls read the accounts, supply, and owners.
The result reports their slots.
`SharePercent` is an approximate ratio to the reported supply.
Closed token accounts can have an absent owner.

## Event streams

```go
err := client.Stream(ctx, pumpfun.StreamOptions{
    NewCoins: true,
    Trades: []string{mint},
    Migrations: true,
}, func(ctx context.Context, event pumpfun.StreamEvent) error {
    return processEvent(ctx, event)
})
```

Use one `Stream` call to combine subscriptions.
`StreamNewCoins`, `StreamTrades`, and `StreamMigrations` are convenience methods.
Each client permits one active stream.
A second stream returns `ErrStreamActive`.
The handler runs in receive order.
An error from the handler stops the stream without a retry.
The handler must finish promptly and check its context during long work.

PumpPortal can charge for trade subscriptions.
The client can connect again after a temporary failure.
It does not provide a replay guarantee.
Use event signatures to remove duplicates when required.
`StreamEvent.SeenAt` is the local receive time.
`DecimalAmount` retains source display amounts as decimal text.

Use `Observe` for a bounded event sample:

```go
sample, err := client.Observe(ctx,
    pumpfun.StreamOptions{NewCoins: true},
    pumpfun.ObserveOptions{Duration: 10 * time.Second, MaxEvents: 20},
)
```

The maximum duration is one minute.
The maximum event count is 100.
A duration limit returns the events received so far.
Parent cancellation returns an error.
An empty sample is permitted.

## Errors and request limits

```go
var apiErr *pumpfun.Error
if errors.As(err, &apiErr) {
    fmt.Println(apiErr.Kind, apiErr.StatusCode, apiErr.RPCCode)
}
if errors.Is(err, pumpfun.ErrRateLimited) {
    // Use apiErr.RetryAfter to select a later request time.
}
if errors.Is(err, context.DeadlineExceeded) {
    // The read exceeded its time limit.
}
```

`Error` has a kind, operation, safe message, HTTP status, RPC code, and optional retry delay.
It wraps cancellation and timeout causes.
SDK errors do not expose service response bodies in their messages.
RPC application errors are returned as `KindRPC`.

The default timeout is 20 seconds.
It includes rate limit waits and HTTP retries.
Compound creator and holder reads share one time budget.
Stream connections use this timeout for connection, subscription, and ping steps.
The stream itself uses the caller's context.

The default request rate is two requests per second with a burst of one.
Set `RequestsPerSecond` and `Burst` to change it.
The limiter is shared by the client's HTTP, RPC, and WebSocket connection requests.

The default policy permits three extra attempts.
It retries transport failures and HTTP 408, 429, 500, 502, 503, and 504.
HTTP retry waits use exponential backoff and jitter.
The initial backoff is 200 milliseconds.
The maximum backoff is five seconds.
The client does not shorten a server `Retry-After` value.
If it exceeds the retry budget, the client returns the typed error.
To disable retries, use `Retry: &pumpfun.RetryPolicy{MaxRetries: 0}`.

The default response limit is 8 MiB.
It also limits a WebSocket message.
Use `MaxResponseBytes` to change it, up to 64 MiB.

## Tests

```sh
go test ./...
go test -race ./...
PUMPFUN_LIVE_MINT=MINT go test -tags live -run '^TestLiveRead$' ./internal/client
```

The normal tests use local mock servers.
The live test needs both the `live` build tag and a mint environment value.
It reads coin data and trade history.
It does not start a paid trade stream.
