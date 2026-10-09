# Configuration

The CLI and MCP server use the same settings.
The SDK accepts options directly and does not read a file.

## Priority

Values have this priority, from highest to lowest:

1. An explicit command flag.
2. A `PUMPFUN_` environment variable.
3. A value in the YAML file.
4. The default value.

An empty environment value is still an explicit value.
The read fails if that value is not valid.
Unset the variable to use a file or default value.

## File path

Use `--config FILE` to select a file.
If this flag is absent, the client checks `PUMPFUN_CONFIG_FILE`.
Otherwise, it uses the OS config directory under `pumpfun/config.yaml`.

`XDG_CONFIG_HOME` has priority over the OS directory on all systems.
It must be an absolute path.
The normal paths are:

| System | File path |
| --- | --- |
| Linux | `~/.config/pumpfun/config.yaml` |
| macOS | `~/Library/Application Support/pumpfun/config.yaml` |
| Windows | `%AppData%\pumpfun\config.yaml` |
| With XDG | `$XDG_CONFIG_HOME/pumpfun/config.yaml` |

An absent file is permitted.
It does not cause a file write during a read.

## Keys

| Key | Environment variable | Default |
| --- | --- | --- |
| `api_base_url` | `PUMPFUN_API_BASE_URL` | `https://frontend-api-v3.pump.fun` |
| `rpc_url` | `PUMPFUN_RPC_URL` | `https://api.mainnet-beta.solana.com` |
| `ws_url` | `PUMPFUN_WS_URL` | `wss://pumpportal.fun/api/data` |
| `timeout` | `PUMPFUN_TIMEOUT` | `20s` |
| `log_level` | `PUMPFUN_LOG_LEVEL` | `warn` |
| `output` | `PUMPFUN_OUTPUT` | `table` |

API and RPC URLs must use `http` or `https`.
WebSocket URLs must use `ws` or `wss`.
URLs must have a host.
User data and fragments are not permitted in URLs.
Query parameters are permitted for service API keys.

The timeout must be greater than zero and at most `5m`.
Log levels are `trace`, `debug`, `info`, `warn`, `error`, and `disabled`.
Output formats are `table`, `json`, and `yaml`.
All logs go to standard error through zerolog.
Command failures always produce an error record.

## Change settings

```sh
pumpfun config set timeout 30s
pumpfun config get timeout
pumpfun config list --output json
pumpfun config unset timeout
```

`get` and `list` show effective values.
`list` includes defaults.
`set` validates one value and writes it to the file.
`unset` removes one file value.
It does not remove an environment value.
These writes have no effect on flags already supplied to a command.

The file contains one YAML map:

```yaml
api_base_url: https://frontend-api-v3.pump.fun
rpc_url: https://api.mainnet-beta.solana.com
ws_url: wss://pumpportal.fun/api/data
timeout: 20s
log_level: warn
output: table
```

The loader rejects unknown keys, duplicate keys, and extra YAML documents.
The maximum file size is 64 KiB.
Config reads can show invalid setting values to help you repair them.
Use `--output table` if an invalid output setting prevents a config read.

Writes use a file lock and atomic replacement.
A new config directory has mode `0700`.
A new config file has mode `0600` where the OS supports these modes.
The lock file can remain after a write.
The process releases the lock when it closes the file.

## Service API keys

Use an environment variable to supply a PumpPortal URL with an API key:

```sh
export PUMPFUN_WS_URL='wss://pumpportal.fun/api/data?api-key=API_KEY'
pumpfun stream trades MINT --duration 10s --output json
```

Replace `API_KEY` with the service API key.
This key is not a wallet private key.
The project does not accept wallet private keys.
The SDK does not log URLs, query parameters, or response bodies.
Config `get` and `list` show complete setting values, including URL query data.
