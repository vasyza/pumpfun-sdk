# Endpoint sources

The sources below were checked on 2026-10-09.
The frontend checks used direct HTTP requests.
All SDK requests are read operations.

## Frontend API

The default base URL is `https://frontend-api-v3.pump.fun`.
These public routes have no published stability contract.
A route can change or require access control.
The client returns a typed error when a request fails.

| Method and path | Request data | Source |
| --- | --- | --- |
| `GET /coins-v2/{mint}` | A Solana mint in the path. | [Coin response](https://frontend-api-v3.pump.fun/coins-v2/DZQPU9RmUyCSyUMmy611562SJToJknBzQSg2pGqapump) |
| `GET /coins` | `limit`, `offset`, `includeNsfw`, `sort`, `order`. Use `created_timestamp` for new coins. | [New coin response](https://frontend-api-v3.pump.fun/coins?offset=0&limit=2&sort=created_timestamp&order=DESC&includeNsfw=false) |
| `GET /coins` | Use `market_cap` and `DESC` for the trending proxy. | [Market cap response](https://frontend-api-v3.pump.fun/coins?offset=0&limit=2&sort=market_cap&order=DESC&includeNsfw=false) |
| `GET /coins` | Add `complete=true`. Sort by `created_timestamp`, with `DESC`. | [Complete curve response](https://frontend-api-v3.pump.fun/coins?offset=0&limit=2&sort=created_timestamp&order=DESC&includeNsfw=false&complete=true) |
| `GET /coins/search-v2` | `searchTerm`, `limit`, `offset`, `includeNsfw`, `sort=market_cap`, `order=DESC`. | [Search response](https://frontend-api-v3.pump.fun/coins/search-v2?searchTerm=bonk&offset=0&limit=2&sort=market_cap&order=DESC&includeNsfw=false) |
| `GET /trades/{chainId}/{mint}` | `limit` and an optional `cursor`. Read `trades`, `cursor`, and `source` from the result. | [Trade response](https://frontend-api-v3.pump.fun/trades/solana:5eykt4UsFv8P8NJdTREpY1vzqKqZKvdp/DZQPU9RmUyCSyUMmy611562SJToJknBzQSg2pGqapump?limit=2) |
| `GET /users/{address}` | A Solana wallet address. A profile can be absent. | [Profile response](https://frontend-api-v3.pump.fun/users/HKSXVMLXFe6vjNp9sxkiUDNtuSrWzjN4yDwFaRYn9FHj) |
| `GET /coins-v2/user-created-coins/{address}` | `limit`, `offset`, `includeNsfw`. The result has `coins` and `count`. | [Creator coin response](https://frontend-api-v3.pump.fun/coins-v2/user-created-coins/HKSXVMLXFe6vjNp9sxkiUDNtuSrWzjN4yDwFaRYn9FHj?limit=2&offset=0&includeNsfw=false) |

Trade reads use the Solana mainnet chain ID:
`solana:5eykt4UsFv8P8NJdTREpY1vzqKqZKvdp`.
The API uses a cursor for trade pages.
It uses offsets for coin pages.
Search can return other assets that Pump.fun indexes.

The coin API permits these sort fields:
`created_timestamp`, `market_cap`, `ath_market_cap`, `reply_count`, `last_reply`, and `last_trade_timestamp`.
It rejected `completed_timestamp` with HTTP 400 during the source check.
The graduated list therefore uses creation time, not migration time.

## PumpPortal

Use `wss://pumpportal.fun/api/data` for WebSocket data.
An API key can be supplied in the URL query.
Use the `api-key` query name when the service requires it.

| Subscription method | Event data | Source |
| --- | --- | --- |
| `subscribeNewToken` | New token events. | [PumpPortal data API](https://pumpportal.fun/data-api/real-time/) |
| `subscribeTokenTrade` | Trade events for the `keys` mint array. | [PumpPortal data API](https://pumpportal.fun/data-api/real-time/) |
| `subscribeMigration` | Migration events. | [PumpPortal data API](https://pumpportal.fun/data-api/real-time/) |

PumpPortal requests one connection for all subscriptions.
Trade subscriptions require an API key and a funded linked wallet.
The service states a charge of 0.01 SOL per 10000 trade events.
Check the service terms before use.
The SDK does not create a wallet or accept a private key.

The client closes the connection when a read stops.
It sends periodic WebSocket pings and checks subscription error messages.
It can connect again after a temporary failure.
The service has no replay cursor for these streams.
Events can be lost or repeated around a connection failure.

## Solana RPC

The default RPC URL is `https://api.mainnet-beta.solana.com`.
Every method uses JSON-RPC 2.0 through HTTP POST.
The client selects `confirmed` commitment.

| Method | Use | Source |
| --- | --- | --- |
| `getAccountInfo` | Read a derived curve account in base64 form. | [Solana method](https://solana.com/docs/rpc/http/getaccountinfo) |
| `getMultipleAccounts` | Read curve and Global state together, or resolve token account owners. | [Solana method](https://solana.com/docs/rpc/http/getmultipleaccounts) |
| `getTokenLargestAccounts` | Read at most 20 largest token accounts for a mint. | [Solana method](https://solana.com/docs/rpc/http/gettokenlargestaccounts) |
| `getTokenSupply` | Read the raw supply for holder share estimates. | [Solana method](https://solana.com/docs/rpc/http/gettokensupply) |

Use the [Pump program IDL](https://github.com/pump-fun/pump-public-docs/blob/main/idl/pump.json) for the account layout and discriminator.
Use the [Pump program description](https://github.com/pump-fun/pump-public-docs/blob/main/docs/PUMP_PROGRAM_README.md) for curve seeds and Global reserves.
The curve address uses the `bonding-curve` seed, mint bytes, and the Pump program ID.
The client checks the owner before it decodes state.
Old accounts can omit appended fields.
These fields then have zero values.

The token account base layout stores the mint in bytes 0 to 31.
It stores the wallet owner in bytes 32 to 63.
Use the [SPL Token account source](https://docs.rs/spl-token/latest/src/spl_token/state.rs.html) for this layout.
Token-2022 retains that base layout.
See [Token-2022 extensions](https://www.solana-program.com/docs/token-2022).

Holder reads use separate RPC calls.
Their slots can differ.
The result includes each slot and can include a closed account with no owner.

## MCP protocol

The server uses the [official Go SDK v1.8.0](https://github.com/modelcontextprotocol/go-sdk/tree/v1.8.0).
This SDK supports specification 2026-07-28.
See the [protocol specification](https://modelcontextprotocol.io/specification/2026-07-28) and [tool specification](https://modelcontextprotocol.io/specification/2026-07-28/server/tools).
The SDK handles request metadata, capability discovery, cancellation, and result types.
