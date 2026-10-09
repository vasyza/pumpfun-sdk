# Project decisions

## 2026-10-09

- Use Go 1.27.1. The installed Go 1.24.4 can download this version with `GOTOOLCHAIN=auto`.
- Use the root package, `pumpfun`, for the SDK. Keep CLI, MCP, and file configuration code in separate internal packages.
- Provide read operations only. The project does not accept private keys or send transactions.
- Use the official MCP Go SDK v1.8.0. It supports specification 2026-07-28. Use its protocol code to process requests.
- Use short sentences and direct instructions in all project text. Treat API names, protocol names, and code terms as technical names.
