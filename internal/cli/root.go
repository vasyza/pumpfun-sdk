// Package cli provides the pumpfun command line interface.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/rs/zerolog"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	pumpfun "github.com/vasyza/pumpfun-sdk"
	"github.com/vasyza/pumpfun-sdk/internal/config"
	"github.com/vasyza/pumpfun-sdk/internal/logging"
	mcpserver "github.com/vasyza/pumpfun-sdk/internal/mcp"
)

type app struct {
	root       *cobra.Command
	configPath string
}

var flagKeys = map[string]string{
	"api-base-url": "api_base_url", "rpc-url": "rpc_url", "ws-url": "ws_url",
	"timeout": "timeout", "log-level": "log_level", "output": "output",
}

// NewRoot creates a fresh command tree. It does not read settings or use a network.
func NewRoot(out, errOut io.Writer) *cobra.Command {
	a := &app{}
	root := &cobra.Command{
		Use: "pumpfun", Short: "Read Pump.fun coin and market data.",
		Long:    "Read Pump.fun data through HTTP, WebSocket, and Solana RPC.\nUse a read command or start the MCP server.",
		Example: "  pumpfun coins new --output json\n  pumpfun config set timeout 30s\n  pumpfun mcp serve",
		Version: pumpfun.Version, SilenceErrors: true, SilenceUsage: true,
	}
	a.root = root
	root.SetOut(out)
	root.SetErr(errOut)
	root.CompletionOptions.DisableDefaultCmd = true
	root.SetVersionTemplate("{{.Version}}\n")
	root.PersistentFlags().StringVar(&a.configPath, "config", "", "Use this config file.")
	for _, flag := range []struct{ name, key, usage string }{
		{"api-base-url", "api_base_url", "Use this frontend API URL."},
		{"rpc-url", "rpc_url", "Use this Solana RPC URL."},
		{"ws-url", "ws_url", "Use this PumpPortal WebSocket URL."},
		{"timeout", "timeout", "Limit each read to this duration, such as 20s."},
		{"log-level", "log_level", "Set the log level: trace, debug, info, warn, error, or disabled."},
		{"output", "output", "Set the output format: table, json, or yaml."},
	} {
		if flag.name == "output" {
			root.PersistentFlags().StringP(flag.name, "o", config.Defaults()[flag.key], flag.usage)
		} else {
			root.PersistentFlags().String(flag.name, config.Defaults()[flag.key], flag.usage)
		}
	}
	root.Flags().Bool("version", false, "Show the program version.")
	root.RunE = func(cmd *cobra.Command, _ []string) error { return cmd.Help() }
	root.AddCommand(a.coinCommand(), a.coinsCommand(), a.configCommand(), a.streamCommand(), a.mcpCommand())
	for _, command := range []struct {
		use, short, long, example string
		read                      func(context.Context, *pumpfun.Client, string) (any, error)
	}{
		{"curve MINT", "Read bonding curve state.", "Read the confirmed Pump account for a Solana mint. Amounts use raw units.", "  pumpfun curve MINT --output json", func(ctx context.Context, c *pumpfun.Client, mint string) (any, error) {
			return c.GetBondingCurve(ctx, mint)
		}},
		{"progress MINT", "Read graduation progress.", "Estimate reserve depletion with current Global state. A complete curve can await pool migration.", "  pumpfun progress MINT --output json", func(ctx context.Context, c *pumpfun.Client, mint string) (any, error) {
			return c.GetGraduationProgress(ctx, mint)
		}},
		{"holders MINT", "Read the largest token accounts.", "Read at most 20 largest token accounts and their owners. The result is not a full holder list.", "  pumpfun holders MINT --output json", func(ctx context.Context, c *pumpfun.Client, mint string) (any, error) { return c.GetHolders(ctx, mint) }},
		{"creator MINT", "Read coin creator data.", "Read the creator address and its public profile, if available.", "  pumpfun creator MINT --output json", func(ctx context.Context, c *pumpfun.Client, mint string) (any, error) { return c.GetCreator(ctx, mint) }},
		{"user ADDRESS", "Read a public user profile.", "Read the public profile for a Solana address.", "  pumpfun user ADDRESS --output json", func(ctx context.Context, c *pumpfun.Client, address string) (any, error) {
			return c.GetUser(ctx, address)
		}},
	} {
		read := command.read
		cmd := &cobra.Command{Use: command.use, Short: command.short, Long: command.long, Example: command.example, Args: oneArg("address")}
		cmd.RunE = a.read(func(cmd *cobra.Command, c *pumpfun.Client, args []string) (any, error) {
			return read(cmd.Context(), c, args[0])
		})
		root.AddCommand(cmd)
	}
	root.AddCommand(a.searchCommand(), a.tradesCommand())
	help := &cobra.Command{
		Use: "help [COMMAND]", Short: "Show help for a command.",
		Long: "Show the command list, flags, and examples for a command.", Example: "  pumpfun help coin get",
		RunE: func(_ *cobra.Command, args []string) error {
			cmd, remaining, err := root.Find(args)
			if err != nil || len(remaining) != 0 {
				return errors.New("the command was not found")
			}
			return cmd.Help()
		},
	}
	setHelp(help)
	root.SetHelpCommand(help)
	setHelp(root)
	return root
}

func setHelp(cmd *cobra.Command) {
	cmd.InitDefaultHelpFlag()
	cmd.Flags().Lookup("help").Usage = "Show help for this command."
	cmd.SetHelpTemplate(`{{with .Long}}{{.}}{{else}}{{.Short}}{{end}}

Use:
  {{.UseLine}}
{{if .HasAvailableSubCommands}}
Commands:
{{range .Commands}}{{if .IsAvailableCommand}}  {{rpad .Name .NamePadding }} {{.Short}}
{{end}}{{end}}{{end}}{{if .HasExample}}
Examples:
{{.Example}}
{{end}}{{if .HasAvailableLocalFlags}}
Flags:
{{.LocalFlags.FlagUsages | trimTrailingWhitespaces}}
{{end}}{{if .HasAvailableInheritedFlags}}
Common flags:
{{.InheritedFlags.FlagUsages | trimTrailingWhitespaces}}
{{end}}`)
	for _, child := range cmd.Commands() {
		setHelp(child)
	}
}

// Execute runs the command and returns an exit code. All logs go to errOut.
func Execute(ctx context.Context, args []string, out, errOut io.Writer) int {
	root := NewRoot(out, errOut)
	root.SetArgs(args)
	if err := root.ExecuteContext(ctx); err != nil {
		logger := logging.New(errOut, zerolog.ErrorLevel)
		logger.Error().Err(err).Msg("The command failed.")
		if errors.Is(err, context.Canceled) {
			return 130
		}
		return 1
	}
	return 0
}

func oneArg(label string) cobra.PositionalArgs {
	return func(_ *cobra.Command, args []string) error {
		if len(args) != 1 {
			return fmt.Errorf("supply one %s", label)
		}
		return nil
	}
}

func (a *app) store() (config.Store, error) {
	path, err := config.Path(a.configPath)
	return config.Store{Path: path}, err
}

func (a *app) overrides() map[string]string {
	values := map[string]string{}
	a.root.PersistentFlags().VisitAll(func(flag *pflag.Flag) {
		if key, ok := flagKeys[flag.Name]; ok && flag.Changed {
			values[key] = flag.Value.String()
		}
	})
	return values
}

func (a *app) client(cmd *cobra.Command) (*pumpfun.Client, config.Config, zerolog.Logger, error) {
	store, err := a.store()
	if err != nil {
		return nil, config.Config{}, zerolog.Nop(), err
	}
	cfg, err := store.Load(a.overrides())
	if err != nil {
		return nil, cfg, zerolog.Nop(), err
	}
	logger := logging.New(cmd.ErrOrStderr(), cfg.LogLevel)
	client, err := pumpfun.NewClient(pumpfun.Options{APIBaseURL: cfg.APIBaseURL, RPCURL: cfg.RPCURL, WSURL: cfg.WSURL, Timeout: cfg.Timeout, Logger: logger})
	return client, cfg, logger, err
}

func (a *app) read(fn func(*cobra.Command, *pumpfun.Client, []string) (any, error)) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, args []string) error {
		client, cfg, _, err := a.client(cmd)
		if err != nil {
			return err
		}
		value, err := fn(cmd, client, args)
		if err != nil {
			return err
		}
		return render(cmd.OutOrStdout(), cfg.Output, value)
	}
}

func (a *app) coinCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "coin", Short: "Read one coin.", Long: "Read coin data by its mint address.", Example: "  pumpfun coin get MINT"}
	get := &cobra.Command{Use: "get MINT", Short: "Read coin data by mint.", Long: "Read coin data from the frontend API. Supply a Solana mint address.", Example: "  pumpfun coin get MINT --output json", Args: oneArg("mint address")}
	get.RunE = a.read(func(cmd *cobra.Command, c *pumpfun.Client, args []string) (any, error) {
		return c.GetCoin(cmd.Context(), args[0])
	})
	cmd.AddCommand(get)
	return cmd
}

func pageFlags(cmd *cobra.Command, options *pumpfun.PageOptions) {
	cmd.Flags().IntVar(&options.Limit, "limit", 20, "Read at most this many items, from 1 to 100.")
	cmd.Flags().IntVar(&options.Offset, "offset", 0, "Skip this many items, from 0 to 1000000.")
	cmd.Flags().BoolVar(&options.IncludeNSFW, "include-nsfw", false, "Include content marked NSFW.")
}

func (a *app) coinsCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "coins", Short: "Read coin lists.", Long: "Read a coin page. Use the next offset to read another page.", Example: "  pumpfun coins new --limit 10"}
	for _, sub := range []struct {
		name, short, long string
		read              func(context.Context, *pumpfun.Client, pumpfun.PageOptions) (any, error)
	}{
		{"new", "List new coins.", "List coins by creation time. Read the newest coins first.", func(ctx context.Context, c *pumpfun.Client, p pumpfun.PageOptions) (any, error) {
			return c.ListNewCoins(ctx, p)
		}},
		{"trending", "List coins by market cap.", "List coins by market cap, highest first. This ranking is a proxy for trends.", func(ctx context.Context, c *pumpfun.Client, p pumpfun.PageOptions) (any, error) {
			return c.ListTrendingCoins(ctx, p)
		}},
		{"graduated", "List complete curves.", "List complete curves by creation time. The API has no sort by graduation time.", func(ctx context.Context, c *pumpfun.Client, p pumpfun.PageOptions) (any, error) {
			return c.ListGraduatedCoins(ctx, p)
		}},
	} {
		options := pumpfun.PageOptions{}
		read := sub.read
		child := &cobra.Command{Use: sub.name, Short: sub.short, Long: sub.long, Example: "  pumpfun coins " + sub.name + " --limit 10 --output json", Args: cobra.NoArgs}
		pageFlags(child, &options)
		child.RunE = a.read(func(cmd *cobra.Command, c *pumpfun.Client, _ []string) (any, error) {
			return read(cmd.Context(), c, options)
		})
		cmd.AddCommand(child)
	}
	options := pumpfun.PageOptions{}
	created := &cobra.Command{Use: "created ADDRESS", Short: "List coins by creator.", Long: "Read coins created by a Solana address.", Example: "  pumpfun coins created ADDRESS --output json", Args: oneArg("creator address")}
	pageFlags(created, &options)
	created.RunE = a.read(func(cmd *cobra.Command, c *pumpfun.Client, args []string) (any, error) {
		return c.ListCreatedCoins(cmd.Context(), args[0], options)
	})
	cmd.AddCommand(created)
	return cmd
}

func (a *app) searchCommand() *cobra.Command {
	options := pumpfun.PageOptions{}
	cmd := &cobra.Command{Use: "search QUERY", Short: "Find coins.", Long: "Find coins by name, symbol, or mint. Results can include other assets that Pump.fun indexes.", Example: "  pumpfun search 'test coin' --output json", Args: oneArg("search text")}
	pageFlags(cmd, &options)
	cmd.RunE = a.read(func(cmd *cobra.Command, c *pumpfun.Client, args []string) (any, error) {
		return c.Search(cmd.Context(), args[0], options)
	})
	return cmd
}

func (a *app) tradesCommand() *cobra.Command {
	options := pumpfun.TradeOptions{}
	cmd := &cobra.Command{Use: "trades MINT", Short: "Read coin trades.", Long: "Read one indexed trade page. Use next_cursor with --cursor to read another page.", Example: "  pumpfun trades MINT --limit 10 --output json", Args: oneArg("mint address")}
	cmd.Flags().IntVar(&options.Limit, "limit", 20, "Read at most this many trades, from 1 to 100.")
	cmd.Flags().StringVar(&options.Cursor, "cursor", "", "Use the cursor from the previous trade page.")
	cmd.RunE = a.read(func(cmd *cobra.Command, c *pumpfun.Client, args []string) (any, error) {
		return c.GetTrades(cmd.Context(), args[0], options)
	})
	return cmd
}

func (a *app) configCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "config", Short: "Read or change settings.", Long: "Read effective settings or change values in the YAML config file.\nFlags have priority over environment, file, and default values.", Example: "  pumpfun config list\n  pumpfun config set output json"}
	get := &cobra.Command{Use: "get KEY", Short: "Read one setting.", Long: "Read the effective value of one config key.", Example: "  pumpfun config get timeout", Args: oneArg("config key")}
	get.RunE = func(cmd *cobra.Command, args []string) error {
		store, err := a.store()
		if err != nil {
			return err
		}
		values, err := store.Resolve(a.overrides())
		if err != nil {
			return err
		}
		value, ok := values[args[0]]
		if !ok {
			return errors.New("the config key is not supported")
		}
		if err := config.Validate("output", values["output"]); err != nil {
			return err
		}
		if values["output"] == "table" {
			_, err = fmt.Fprintln(cmd.OutOrStdout(), safeCell(value))
			return err
		}
		return render(cmd.OutOrStdout(), values["output"], map[string]string{args[0]: value})
	}
	list := &cobra.Command{Use: "list", Short: "Read all settings.", Long: "Read all effective config values, including defaults.", Example: "  pumpfun config list --output json", Args: cobra.NoArgs}
	list.RunE = func(cmd *cobra.Command, _ []string) error {
		store, err := a.store()
		if err != nil {
			return err
		}
		values, err := store.Resolve(a.overrides())
		if err != nil {
			return err
		}
		return render(cmd.OutOrStdout(), values["output"], values)
	}
	set := &cobra.Command{Use: "set KEY VALUE", Short: "Set a file value.", Long: "Check a value and save it in the config file. The write does not change environment values.", Example: "  pumpfun config set timeout 30s", Args: func(_ *cobra.Command, args []string) error {
		if len(args) != 2 {
			return errors.New("supply one config key and one value")
		}
		return nil
	}}
	set.RunE = func(cmd *cobra.Command, args []string) error {
		store, err := a.store()
		if err != nil {
			return err
		}
		return store.Set(cmd.Context(), args[0], args[1])
	}
	unset := &cobra.Command{Use: "unset KEY", Short: "Remove a file value.", Long: "Remove one key from the config file. The next read can use an environment or default value.", Example: "  pumpfun config unset timeout", Args: oneArg("config key")}
	unset.RunE = func(cmd *cobra.Command, args []string) error {
		store, err := a.store()
		if err != nil {
			return err
		}
		return store.Unset(cmd.Context(), args[0])
	}
	cmd.AddCommand(get, list, set, unset)
	return cmd
}

var errStreamComplete = errors.New("the stream read is complete")

func (a *app) streamCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "stream", Short: "Read live events.", Long: "Read events from one PumpPortal connection. Press Ctrl+C to stop.\nPumpPortal requires an API key and can charge for trade events.", Example: "  pumpfun stream new --count 5 --output json"}
	for _, sub := range []struct{ name, use, short, long string }{
		{"new", "new", "Read new coin events.", "Read token creation events from PumpPortal."},
		{"trades", "trades MINT [MINT...]", "Read trade events.", "Read trades for selected Solana mints. PumpPortal can charge for these events."},
		{"migrations", "migrations", "Read migration events.", "Read token migration events from PumpPortal."},
	} {
		var count int
		var duration time.Duration
		name := sub.name
		child := &cobra.Command{Use: sub.use, Short: sub.short, Long: sub.long, Example: "  pumpfun stream " + sub.name + map[bool]string{true: " MINT", false: ""}[sub.name == "trades"] + " --duration 30s --output json", Args: cobra.NoArgs}
		if name == "trades" {
			child.Args = cobra.MinimumNArgs(1)
		}
		child.Flags().IntVar(&count, "count", 0, "Stop after this many events. Zero has no event limit.")
		child.Flags().DurationVar(&duration, "duration", 0, "Stop after this duration. Zero has no time limit.")
		child.RunE = func(cmd *cobra.Command, args []string) error {
			if count < 0 || duration < 0 {
				return errors.New("count and duration must be zero or positive")
			}
			client, cfg, _, err := a.client(cmd)
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			if duration > 0 {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, duration)
				defer cancel()
			}
			options := pumpfun.StreamOptions{NewCoins: name == "new", Migrations: name == "migrations"}
			if name == "trades" {
				options.Trades = args
			}
			seen := 0
			err = client.Stream(ctx, options, func(_ context.Context, event pumpfun.StreamEvent) error {
				if err := renderEvent(cmd.OutOrStdout(), cfg.Output, event, seen == 0); err != nil {
					return err
				}
				seen++
				if count > 0 && seen >= count {
					return errStreamComplete
				}
				return nil
			})
			if errors.Is(err, errStreamComplete) || ctx.Err() != nil && cmd.Context().Err() == nil {
				return nil
			}
			return err
		}
		cmd.AddCommand(child)
	}
	return cmd
}

func (a *app) mcpCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "mcp", Short: "Run the MCP server.", Long: "Expose SDK reads as MCP tools. The server supports specification 2026-07-28.", Example: "  pumpfun mcp serve"}
	var transport, listen string
	serve := &cobra.Command{Use: "serve", Short: "Start the MCP server.", Long: "Use stdio for local MCP clients. Use HTTP on a loopback address if required.\nStandard output contains protocol messages only when stdio is selected.", Example: "  pumpfun mcp serve\n  pumpfun mcp serve --transport http --listen 127.0.0.1:8080", Args: cobra.NoArgs}
	serve.Flags().StringVar(&transport, "transport", "stdio", "Select stdio or http transport.")
	serve.Flags().StringVar(&listen, "listen", "127.0.0.1:8080", "Use this loopback host and port for HTTP.")
	serve.RunE = func(cmd *cobra.Command, _ []string) error {
		if transport != "stdio" && transport != "http" {
			return errors.New("transport must be stdio or http")
		}
		client, _, logger, err := a.client(cmd)
		if err != nil {
			return err
		}
		server := mcpserver.New(client, logger)
		if transport == "http" {
			return mcpserver.ServeHTTP(cmd.Context(), server, listen, logger)
		}
		return mcpserver.ServeStdio(cmd.Context(), server)
	}
	cmd.AddCommand(serve)
	return cmd
}
