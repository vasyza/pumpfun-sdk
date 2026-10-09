# Project decisions

## 2026-10-09

- Use Go 1.27.1. The installed Go 1.24.4 can download this version with `GOTOOLCHAIN=auto`.
- Use the root package, `pumpfun`, for the SDK. Keep CLI, MCP, and file configuration code in separate internal packages.
- Provide read operations only. The project does not accept private keys or send transactions.
- Use the official MCP Go SDK v1.8.0. It supports specification 2026-07-28. Use its protocol code to process requests.
- Use short sentences and direct instructions in all project text. Treat API names, protocol names, and code terms as technical names.
- Use endpoints that were checked with direct HTTP requests on this date. Use `/coins-v2/{mint}` and `/coins/search-v2`. The older routes returned HTTP 404.
- Use `/trades/{chainId}/{mint}` and its cursor. The older `/trades/all/{mint}` route matched an invalid chain ID.
- Define the trending list as the public market cap ranking. The frontend API does not expose a stable trend score.
- Define the graduated list as complete curves, with the newest creation time first. The API rejects a sort by completion time. Use migration events to observe new migrations. Do not report creation time as graduation time.
- Read curve state through RPC. Check the account owner and discriminator. Decode the published core layout and appended fields.
- Estimate progress from token reserve depletion and the current Global start reserve. Read both accounts in one snapshot. Do not estimate incomplete Mayhem curves or reserves above that baseline.
- Report the largest 20 token accounts as holder data. Resolve account owners. Do not report this sample as all holders or as a count of unique wallets.
- Retain raw amounts as strings. This prevents JSON clients from losing integer digits. Stream display amounts also retain source decimal text.
- Permit one active WebSocket stream per SDK client. Combine subscriptions on that connection. This follows the PumpPortal connection rule.
- Limit MCP event reads to 60 seconds and 100 events. The caller can cancel a read. Concurrent stream reads on one client return `stream_active`.
- Use stateless Streamable HTTP for specification 2026-07-28. Keep the 2025-11-25 handshake for older MCP clients.
- Bind the HTTP server to loopback only. Check Host and Origin. Remote access and remote authentication are outside this release.
- Adapt the MCP SDK's standard `slog` interface to zerolog. Zerolog is the only log writer. Use a writer adapter for HTTP server errors.
- Lock config writes across processes. Replace the YAML file atomically with mode `0600`. Config commands can read invalid values to help repair a file.
- Write project text in ASD-STE100 style. No licensed dictionary checker is available in the workspace. Review help and documentation with short sentences and direct verbs.
