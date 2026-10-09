# CLI commands

Use `pumpfun` after installation.
Use `./bin/pumpfun` after `make build`.
Replace `MINT`, `ADDRESS`, and `QUERY` with your values.

## Read commands

| Command | Result |
| --- | --- |
| `pumpfun coin get MINT` | Coin metadata and source market data. |
| `pumpfun coins new` | Coins by creation time, newest first. |
| `pumpfun coins trending` | Coins by market cap, highest first. |
| `pumpfun coins graduated` | Complete curves by creation time, newest first. |
| `pumpfun coins created ADDRESS` | Coins from the selected creator. |
| `pumpfun trades MINT` | A cursor page of indexed trades. |
| `pumpfun curve MINT` | Confirmed on-chain curve state. |
| `pumpfun progress MINT` | Reserve depletion and completion state. |
| `pumpfun holders MINT` | At most 20 largest token accounts and their owners. |
| `pumpfun creator MINT` | Creator address and an optional public profile. |
| `pumpfun user ADDRESS` | A public profile. |
| `pumpfun search QUERY` | A page of Solana search results. |

Every read command accepts `--output json` and `--output yaml`.
The default output is a table.
JSON amounts retain all raw integer digits as strings.
Table output removes terminal control characters from source text.

```sh
pumpfun coin get MINT --output json
pumpfun --output yaml coins new --limit 10
pumpfun search 'test coin' --output json
```

## Pagination

Coin lists and search accept these flags:

| Flag | Default | Limit |
| --- | --- | --- |
| `--limit` | `20` | 1 to 100 items. |
| `--offset` | `0` | 0 to 1000000 items. |
| `--include-nsfw` | `false` | Include content marked NSFW. |

Copy `next_offset` from JSON output into the next command.
An absent next offset means that the page is complete.
A non-empty page can have an empty next page.
Search skips results from other chains.
Its JSON and YAML output reports `skipped_non_solana` when entries are skipped.
Search offsets count all source entries, including skipped entries.
A search page can have no coins and still have `next_offset`.
Use that offset to continue the search.

```sh
pumpfun coins new --limit 10 --offset 0 --output json
pumpfun coins new --limit 10 --offset 10 --output json
```

Trade reads accept `--limit` from 1 to 100.
They use `--cursor` instead of an offset.
Copy `next_cursor` from the previous result.

```sh
pumpfun trades MINT --limit 10 --output json
pumpfun trades MINT --limit 10 --cursor 'CURSOR' --output json
```

Lists can change between reads.
Offset pages are not a fixed snapshot.
The graduated list uses creation time because the API has no sort by graduation time.
The trending list uses a market cap ranking.

## RPC errors

Holder reads use `getTokenLargestAccounts` on Solana RPC.
The shared public RPC can return HTTP 429 or a JSON-RPC rate limit error.
The SDK retries these errors with backoff and respects `Retry-After`.
If the retries fail, the error tells you to set `rpc_url` to a private RPC.
The client also returns that error if the requested wait exceeds its retry policy.

```sh
pumpfun config set rpc_url 'https://YOUR_RPC_HOST'
pumpfun holders MINT --output json
```

Replace `YOUR_RPC_HOST` with your private mainnet RPC host.
You can also use `--rpc-url URL` or `PUMPFUN_RPC_URL`.
See [RPC rate limits](config.md#rpc-rate-limits) for setting priority and API keys.

If the mint has no bonding curve, `curve` returns a `not_found` error:
`No bonding curve exists for this mint.`
The `progress` command uses the same check.

## Event streams

```sh
pumpfun stream new --count 5 --output json
pumpfun stream trades MINT --duration 30s --output json
pumpfun stream migrations --duration 30s --output json
```

Supply one or more mints to `stream trades`.
The client puts them on one connection.
The maximum is 100 mints.
PumpPortal requires an API key and can charge for trade events.
Supply the key in `ws_url` with the `api-key` query name.
Without this key, `stream trades` returns an error before it connects.
The client stops if the socket sends a subscription error.
Check the key and linked wallet balance if that error occurs.
See [service API keys](config.md#service-api-keys).
See the [endpoint sources](endpoints.md#pumpportal).

`--count` stops after the selected number of events.
`--duration` stops after the selected time.
A zero count or duration removes that limit.
Press Ctrl+C to stop an unlimited stream.

Stream JSON output has one compact JSON value per line.
Stream YAML output has one document per event.
`seen_at` is the local receive time.
It is not the chain time.
Stream amounts use source display units.
The client cannot replay missed events.

## Common flags

| Flag | Config key |
| --- | --- |
| `--api-base-url` | `api_base_url` |
| `--rpc-url` | `rpc_url` |
| `--ws-url` | `ws_url` |
| `--timeout` | `timeout` |
| `--log-level` | `log_level` |
| `--output`, `-o` | `output` |
| `--config` | The file path. |

Common flags can appear before or after a read command.
Flags have priority over environment and file values.
Use a duration such as `20s` or `1m` with `--timeout`.
For streams, this timeout limits the connection and subscription steps.
Use `--duration` to limit the full stream read.

## Configuration and MCP

```sh
pumpfun config get timeout
pumpfun config set output json
pumpfun config list
pumpfun config unset output
pumpfun mcp serve
pumpfun mcp serve --transport http --listen 127.0.0.1:8080
```

Config writes do not print data on success.
See [configuration](config.md) for file paths and environment variables.
See [MCP](mcp.md) for tool data and protocol requests.

## Help and errors

```sh
pumpfun --help
pumpfun coin get --help
pumpfun help coins new
pumpfun --version
```

Command help uses short sentences and direct instructions.
Errors are structured JSON logs on standard error.
Successful read data goes to standard output.
The exit code is 0 for success, 1 for failure, and 130 for cancellation by Ctrl+C.
An event duration limit ends with exit code 0.
