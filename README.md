# Pump.fun SDK for Go

Read Pump.fun data with a Go SDK, a command line interface, or an MCP server.
The module path is `github.com/vasyza/pumpfun-sdk`.
Use Go 1.27.1.
An older Go installation can download it with `GOTOOLCHAIN=auto`.

## Install

You need Go 1.27.1 or later.
An older Go installation can download Go 1.27.1 with `GOTOOLCHAIN=auto`.

### Install the CLI

Run this command:

```sh
GOTOOLCHAIN=auto go install github.com/vasyza/pumpfun-sdk/cmd/pumpfun@latest
```

Go puts the `pumpfun` command in `$(go env GOPATH)/bin`.
Add this directory to your `PATH`.
Make sure that the command works:

```sh
pumpfun --help
```

### Add the SDK to a Go project

Run this command in your project directory:

```sh
go get github.com/vasyza/pumpfun-sdk@latest
```

Import the package:

```go
import pumpfun "github.com/vasyza/pumpfun-sdk"
```

### Build from source

```sh
git clone https://github.com/vasyza/pumpfun-sdk.git
cd pumpfun-sdk
GOTOOLCHAIN=auto make build
./bin/pumpfun --help
```

### Set a private Solana RPC

The default Solana RPC is a public service with low rate limits.
Some commands, for example `holders`, need a private RPC.
Set your RPC URL one time:

```sh
pumpfun config set rpc_url "https://YOUR-RPC-PROVIDER/?api-key=YOUR_KEY"
```

You can also use the `PUMPFUN_RPC_URL` environment variable or the `--rpc-url` flag.
Do not put API keys in files that you commit.

### Add the MCP server to an MCP client

Use this command as a stdio server:

```json
{
  "mcpServers": {
    "pumpfun": {
      "command": "pumpfun",
      "args": ["mcp", "serve"]
    }
  }
}
```

## Build and check

Run these commands from the project directory:

```sh
GOTOOLCHAIN=auto make build
make check
./bin/pumpfun --help
```

`make check` checks formatting, builds the project, runs tests, and runs `go vet` and `golangci-lint`.
The tests use local HTTP and WebSocket servers.
Default tests do not call live APIs.
The CLI tests also block external HTTP requests.

Use `make install` to install the command in your Go binary directory.

## Read data

Replace `MINT` with a Solana mint address.
Replace `ADDRESS` with a Solana wallet address.

```sh
./bin/pumpfun coin get MINT --output json
./bin/pumpfun coins new --limit 10 --output json
./bin/pumpfun coins trending --output json
./bin/pumpfun coins graduated --output json
./bin/pumpfun trades MINT --output json
./bin/pumpfun curve MINT --output json
./bin/pumpfun progress MINT --output json
./bin/pumpfun holders MINT --output json
./bin/pumpfun creator MINT --output json
./bin/pumpfun search 'test coin' --output json
./bin/pumpfun stream new --count 5 --output json
```

The SDK supports coin data, coin lists, search, trade pages, creator profiles, curve state, and holder data.
It also reads new coin, trade, and migration events.
All operations are read operations.
The project does not accept private keys or send transactions.

The root package contains the public SDK facade.
All SDK code is in internal modules.
The CLI and MCP server use the facade.
See the [SDK package layout](docs/sdk.md#package-layout).

Raw amounts use strings in JSON output.
This keeps all integer digits.
Market caps are source estimates.
PumpPortal stream amounts use source display units.

## Set configuration

```sh
./bin/pumpfun config set timeout 30s
./bin/pumpfun config get timeout
./bin/pumpfun config list --output json
./bin/pumpfun config unset timeout
```

Settings use this priority: flag, environment, file, then default.
The default file is in the OS config directory under `pumpfun/config.yaml`.
On Linux, it is normally `~/.config/pumpfun/config.yaml`.
`XDG_CONFIG_HOME` selects another config directory.
Use `--config` to select a file.
Use `PUMPFUN_` environment variables for settings.

## Start MCP

```sh
./bin/pumpfun mcp serve
./bin/pumpfun mcp serve --transport http --listen 127.0.0.1:8080
```

The server supports MCP specification 2026-07-28.
It has 15 tools with input schemas, output schemas, and `readOnlyHint` annotations.
The HTTP endpoint is `/mcp`.
The HTTP server binds to loopback addresses only.
Stdio standard output contains protocol messages only.
All logs use zerolog and go to standard error.

## Use the SDK

```go
package main

import (
    "context"
    "fmt"
    "os"
    "time"

    "github.com/rs/zerolog"
    pumpfun "github.com/vasyza/pumpfun-sdk"
)

func main() {
    logger := zerolog.New(os.Stderr).With().Timestamp().Logger()
    client, err := pumpfun.NewClient(pumpfun.Options{Logger: logger})
    if err != nil {
        logger.Error().Err(err).Msg("The client could not start.")
        os.Exit(1)
    }
    ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
    defer cancel()
    page, err := client.ListNewCoins(ctx, pumpfun.PageOptions{Limit: 10})
    if err != nil {
        logger.Error().Err(err).Msg("The read failed.")
        os.Exit(1)
    }
    for _, coin := range page.Items {
        fmt.Println(coin.Mint, coin.Symbol)
    }
}
```

## Data limits

The trending list ranks coins by market cap.
It is a proxy for trends.
The graduated list shows complete curves with the newest creation time first.
The public API has no sort by graduation time.
Use migration events to observe new migrations.

Holder data covers at most 20 token accounts.
It is not a full holder list.
The shared public RPC can limit these reads.
The SDK retries HTTP and RPC rate limit errors.
Set `rpc_url` to a private mainnet RPC if the limit continues.
See [RPC configuration](docs/config.md#rpc-rate-limits).
Progress is an estimate from current Global reserves.
A complete curve can await pool migration.
An absent bonding curve returns a typed `not_found` error.

Search returns Solana coins and skips other chains.
Its page offsets include skipped entries.
Use `next_offset` to continue, including after an empty search page.

PumpPortal can charge for trade events.
Trade subscriptions require an API key and a funded linked wallet.
Without the `api-key` URL value, trade streams return an error before they connect.
The MCP `observe_trades` tool uses the same check.
The SDK uses one active WebSocket connection per client.
It cannot replay events lost during a connection failure.

## Read more

- [CLI commands](docs/cli.md)
- [SDK types and methods](docs/sdk.md)
- [MCP tools and transports](docs/mcp.md)
- [Configuration](docs/config.md)
- [Endpoint sources](docs/endpoints.md)
- [Project decisions](DECISIONS.md)
