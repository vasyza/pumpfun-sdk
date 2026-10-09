package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"unicode"

	pumpfun "github.com/vasyza/pumpfun-sdk"
	"go.yaml.in/yaml/v3"
)

func render(w io.Writer, format string, value any) error {
	switch format {
	case "json":
		encoder := json.NewEncoder(w)
		encoder.SetIndent("", "  ")
		encoder.SetEscapeHTML(false)
		return encoder.Encode(value)
	case "yaml":
		encoder := yaml.NewEncoder(w)
		encoder.SetIndent(2)
		if err := encoder.Encode(value); err != nil {
			_ = encoder.Close()
			return err
		}
		return encoder.Close()
	case "table":
		return renderTable(w, value)
	default:
		return fmt.Errorf("output must be table, json, or yaml")
	}
}

func safeCell(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return ' '
		}
		return r
	}, s)
}

func rows(w io.Writer, data [][]string) error {
	var buffer bytes.Buffer
	table := tabwriter.NewWriter(&buffer, 0, 4, 2, ' ', 0)
	for _, row := range data {
		cells := make([]string, len(row))
		for i, cell := range row {
			cells[i] = safeCell(cell)
		}
		if _, err := io.WriteString(table, strings.Join(cells, "\t")+"\n"); err != nil {
			return err
		}
	}
	if err := table.Flush(); err != nil {
		return err
	}
	_, err := w.Write(buffer.Bytes())
	return err
}

func renderTable(w io.Writer, value any) error {
	data := [][]string{}
	switch v := value.(type) {
	case *pumpfun.Coin:
		data = [][]string{{"MINT", v.Mint}, {"NAME", v.Name}, {"SYMBOL", v.Symbol}, {"CREATOR", v.Creator}, {"COMPLETE", strconv.FormatBool(v.Complete)}, {"USD_CAP", strconv.FormatFloat(v.USDMarketCap, 'f', 2, 64)}}
	case *pumpfun.Page[pumpfun.Coin]:
		data = append(data, []string{"MINT", "SYMBOL", "NAME", "USD_CAP", "COMPLETE"})
		for _, coin := range v.Items {
			data = append(data, []string{coin.Mint, coin.Symbol, coin.Name, strconv.FormatFloat(coin.USDMarketCap, 'f', 2, 64), strconv.FormatBool(coin.Complete)})
		}
		if v.NextOffset != nil {
			data = append(data, []string{"Next offset:", strconv.Itoa(*v.NextOffset)})
		}
	case *pumpfun.TradePage:
		data = append(data, []string{"TX", "SIDE", "TRADER", "RAW_BASE", "RAW_QUOTE"})
		for _, trade := range v.Items {
			data = append(data, []string{trade.TxID, trade.Side, trade.Trader.Address, string(trade.BaseAmount.Raw), string(trade.QuoteAmount.Raw)})
		}
		if v.NextCursor != "" {
			data = append(data, []string{"Next cursor:", v.NextCursor})
		}
	case *pumpfun.BondingCurve:
		data = [][]string{{"MINT", v.Mint}, {"ADDRESS", v.Address}, {"SLOT", strconv.FormatUint(v.Slot, 10)}, {"REAL_TOKEN_RESERVES", string(v.RealTokenReserves)}, {"REAL_QUOTE_RESERVES", string(v.RealQuoteReserves)}, {"VIRTUAL_TOKEN_RESERVES", string(v.VirtualTokenReserves)}, {"VIRTUAL_QUOTE_RESERVES", string(v.VirtualQuoteReserves)}, {"QUOTE_MINT", v.QuoteMint}, {"CREATOR", v.Creator}, {"COMPLETE", strconv.FormatBool(v.Complete)}}
	case *pumpfun.GraduationProgress:
		percent := "unavailable"
		if v.Percent != nil {
			percent = strconv.FormatFloat(*v.Percent, 'f', 2, 64)
		}
		data = [][]string{{"MINT", v.Mint}, {"PERCENT", percent}, {"ESTIMATED", strconv.FormatBool(v.Estimated)}, {"COMPLETE", strconv.FormatBool(v.Complete)}, {"REMAINING_RAW", string(v.RemainingRealTokenReserves)}}
		if v.Reason != "" {
			data = append(data, []string{"REASON", v.Reason})
		}
	case *pumpfun.HolderInfo:
		data = append(data, []string{"OWNER", "TOKEN_ACCOUNT", "RAW_AMOUNT", "SHARE_PERCENT"})
		for _, holder := range v.Accounts {
			data = append(data, []string{holder.Owner, holder.TokenAccount, string(holder.Amount), strconv.FormatFloat(holder.SharePercent, 'f', 4, 64)})
		}
	case *pumpfun.CreatorInfo:
		data = [][]string{{"MINT", v.Mint}, {"CREATOR", v.Address}}
		if v.Profile != nil {
			data = append(data, []string{"USERNAME", v.Profile.Username})
		}
	case *pumpfun.User:
		data = [][]string{{"ADDRESS", v.Address}, {"USERNAME", v.Username}, {"FOLLOWERS", strconv.Itoa(v.Followers)}, {"FOLLOWING", strconv.Itoa(v.Following)}}
	case map[string]string:
		keys := make([]string, 0, len(v))
		for key := range v {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			data = append(data, []string{key, v[key]})
		}
	default:
		return fmt.Errorf("the table output type is not supported")
	}
	return rows(w, data)
}

func renderEvent(w io.Writer, format string, event pumpfun.StreamEvent, first bool) error {
	switch format {
	case "json":
		return json.NewEncoder(w).Encode(event)
	case "yaml":
		if _, err := io.WriteString(w, "---\n"); err != nil {
			return err
		}
		return render(w, format, event)
	case "table":
		data := [][]string{}
		if first {
			data = append(data, []string{"TYPE", "MINT", "SYMBOL", "SOL_AMOUNT", "SIGNATURE"})
		}
		data = append(data, []string{event.Type, event.Mint, event.Symbol, string(event.SOLAmount), event.Signature})
		return rows(w, data)
	default:
		return fmt.Errorf("output must be table, json, or yaml")
	}
}
