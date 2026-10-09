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
| `pumpfun search QUERY` | A page of search results. |

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
A full page can have an empty next page.

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
Search can include other assets that Pump.fun indexes.

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
