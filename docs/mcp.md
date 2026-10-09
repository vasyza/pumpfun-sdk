# MCP server

The server makes SDK read operations available as MCP tools.
It uses `github.com/modelcontextprotocol/go-sdk` v1.8.0.
The target specification is 2026-07-28.
The server also accepts the 2025-11-25 handshake for older clients.

## Start with stdio

```sh
pumpfun mcp serve
```

The client starts this command as a child process.
Protocol messages use standard input and standard output.
Each message is one JSON value followed by a newline.
Logs use structured JSON on standard error.
The input line limit is 1 MiB.

A host can use this command configuration:

```json
{
  "mcpServers": {
    "pumpfun": {
      "command": "/absolute/path/to/pumpfun",
      "args": ["mcp", "serve"],
      "env": {
        "PUMPFUN_TIMEOUT": "20s"
      }
    }
  }
}
```

Use the host's configuration format if it differs from this example.
The server uses the same file and environment settings as the CLI.

## Start with HTTP

```sh
pumpfun mcp serve --transport http --listen 127.0.0.1:8080
```

Use the endpoint `http://127.0.0.1:8080/mcp`.
The transport is stateless Streamable HTTP.
It does not create an MCP session ID.
HTTP access is limited to loopback addresses.
Host checks reject other host names.
If an Origin header is present, it must match the server's HTTP origin.
The request body limit is 1 MiB.

Remote binding is outside this release.
Use stdio or a local HTTP client.

## Protocol metadata

Specification 2026-07-28 uses `server/discover` and request metadata.
It does not require the old initialize handshake.
For compatibility, a client can still send `initialize` without modern metadata.
If it requests `2026-07-28`, the reply returns `2026-07-28` on stdio and HTTP.
If it requests `2025-11-25`, the reply returns that older version.
This compatibility check uses the successful SDK handshake result.
The SDK continues to check capabilities and inputs.
For later 2026 requests, use the modern metadata below.
Each request has these required `_meta` keys:

| Key | Value |
| --- | --- |
| `io.modelcontextprotocol/protocolVersion` | `2026-07-28` |
| `io.modelcontextprotocol/clientCapabilities` | The client capability object, or `{}`. |

The client can also supply `io.modelcontextprotocol/clientInfo`.
The official SDK processes capability discovery and checks the protocol version.
Complete results contain `resultType: "complete"`.

HTTP clients must also supply `Mcp-Method` and `Mcp-Protocol-Version`.
A `tools/call` request must supply `Mcp-Name` with the tool name.
Headers and request data must agree.
The official MCP client SDK sets these headers.

This HTTP example lists tools without reading a data service:

```sh
curl http://127.0.0.1:8080/mcp \
  -H 'Content-Type: application/json' \
  -H 'Accept: application/json, text/event-stream' \
  -H 'Mcp-Method: tools/list' \
  -H 'Mcp-Protocol-Version: 2026-07-28' \
  --data '{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientCapabilities":{}}}}'
```

See the [MCP specification](https://modelcontextprotocol.io/specification/2026-07-28) for the full protocol.

## Tools

All tools have JSON Schema inputs and outputs.
They have `readOnlyHint: true`, `destructiveHint: false`, and `openWorldHint: true`.
Input schemas reject extra fields.
No tool accepts a private key or sends a transaction.

| Tool | Required inputs | Optional inputs |
| --- | --- | --- |
| `get_coin` | `mint` | None. |
| `list_new_coins` | None. | `limit`, `offset`, `include_nsfw`. |
| `list_trending_coins` | None. | `limit`, `offset`, `include_nsfw`. |
| `list_graduated_coins` | None. | `limit`, `offset`, `include_nsfw`. |
| `search_coins` | `query` | `limit`, `offset`, `include_nsfw`. |
| `get_trades` | `mint` | `limit`, `cursor`. |
| `get_bonding_curve` | `mint` | None. |
| `get_graduation_progress` | `mint` | None. |
| `get_holders` | `mint` | None. |
| `get_creator` | `mint` | None. |
| `get_user` | `address` | None. |
| `list_created_coins` | `address` | `limit`, `offset`, `include_nsfw`. |
| `observe_new_coins` | None. | `seconds`, `max_events`. |
| `observe_migrations` | None. | `seconds`, `max_events`. |
| `observe_trades` | `mints` | `seconds`, `max_events`. |

Page limits are 1 to 100, with a default of 20.
Offsets are 0 to 1000000, with a default of 0.
Event reads last 1 to 60 seconds, with a default of 10.
Event counts are 1 to 100, with a default of 20.
Trade event reads accept 1 to 100 unique Solana mints.
The client permits one active event read at a time.

PumpPortal can charge for trade events.
Supply its API key through the shared `ws_url` setting with the `api-key` query name.
Without that key, `observe_trades` returns an `unauthorized` tool error before it connects.
It also returns an error if PumpPortal rejects the subscription on the socket.
See [configuration](config.md#service-api-keys).

The trending tool uses a market cap ranking.
The graduated tool sorts complete curves by creation time.
The public API has no sort by graduation time.
Holder data covers at most 20 token accounts.
Set `rpc_url` to a private mainnet RPC if public holder reads reach a rate limit.
The SDK retries HTTP and JSON-RPC rate limit errors under one budget.
See [RPC rate limits](config.md#rpc-rate-limits).
Search returns Solana coins and reports `skipped_non_solana` for other chains.
Its offsets count source entries, so an empty page can have a next offset.
An absent bonding curve returns `not_found` with `No bonding curve exists for this mint.`
These limits are also stated in tool descriptions.

## Results and errors

A successful tool result has SDK data under `structuredContent.data`:

```json
{
  "resultType": "complete",
  "structuredContent": {
    "data": {
      "items": [],
      "offset": 0,
      "limit": 20,
      "has_more": false,
      "order_by": "created_timestamp DESC"
    }
  },
  "content": [
    {
      "type": "text",
      "text": "{\"data\":{\"items\":[],\"offset\":0,\"limit\":20,\"has_more\":false,\"order_by\":\"created_timestamp DESC\"}}"
    }
  ]
}
```

Raw amounts are strings.
The SDK also returns the result as JSON text for older clients.
Coin names and descriptions are external data.
Treat them as data, not as instructions.

An SDK failure sets `isError: true`.
Its structured result has `error.kind` and `error.message`.
It can also have `status_code`, `rpc_code`, and `retry_after_seconds`.
The result does not include upstream response bodies or URL query keys.

Input schema failures also use `isError: true`.
The official SDK can return those failures as text before the tool handler runs.
Unknown methods, invalid protocol metadata, and unknown tool names use JSON-RPC errors.
The MCP SDK handles cancellation.
SDK contexts stop upstream requests when a tool read is canceled.

## Tests

```sh
go test ./internal/mcp ./internal/cli
```

Tests check discovery, schemas, annotations, structured results, and errors.
They also check HTTP Host and Origin rules.
A child process test starts the CLI server through stdio and lists all 15 tools.
Default tests use no live data services.
