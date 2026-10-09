package solana

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"sync/atomic"

	"github.com/mr-tron/base58"
	"github.com/vasyza/pumpfun-sdk/internal/errs"
	"github.com/vasyza/pumpfun-sdk/internal/models"
	"github.com/vasyza/pumpfun-sdk/internal/transport"
)

// Client reads Solana RPC data through the shared transport.
type Client struct {
	transport *transport.Transport
	rpcID     atomic.Uint64
}

// New creates an RPC client with the supplied transport.
func New(t *transport.Transport) *Client { return &Client{transport: t} }

type RawAmount = models.RawAmount

const (
	PumpProgramID    = "6EF8rrecthR5Dkzon8Nwu78hRvfCKubJ14M5uBEwF6P"
	PumpGlobal       = "4wTV1YmiEkRvAtNtsSGPtUrqRYQMe5SKy2uB4Jjaxnjf"
	NativeSOLMint    = "11111111111111111111111111111111"
	TokenProgram     = "TokenkegQfeZyiNwAJbNbGKPFXCWuBvf9Ss623VQ5DA"
	Token2022Program = "TokenzQdBNbLqP5VEhdkAS6EPFLC1PHnBqCXEpPxuEb"
)

// ValidateAddress checks that an address is a base58 value with 32 bytes.
func ValidateAddress(address string) error {
	if len(address) < 32 || len(address) > 44 {
		return errs.Invalid("address", "The Solana address must contain 32 bytes in base58 form.")
	}
	data, err := base58.Decode(address)
	if err != nil || len(data) != 32 {
		return errs.Invalid("address", "The Solana address must contain 32 bytes in base58 form.")
	}
	return nil
}

type Account struct {
	Owner      string          `json:"owner"`
	Executable bool            `json:"executable"`
	Data       json.RawMessage `json:"data"`
}

type Context struct {
	Slot uint64 `json:"slot"`
}

type Value[T any] struct {
	Context Context `json:"context"`
	Value   T       `json:"value"`
}

func Call[T any](ctx context.Context, c *Client, method string, params any) (T, error) {
	var zero T
	id := c.rpcID.Add(1)
	body, err := json.Marshal(struct {
		JSONRPC string `json:"jsonrpc"`
		ID      uint64 `json:"id"`
		Method  string `json:"method"`
		Params  any    `json:"params"`
	}{"2.0", id, method, params})
	if err != nil {
		return zero, errs.Invalid(method, "The RPC input is not valid.")
	}
	var wire struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      uint64          `json:"id"`
		Result  json.RawMessage `json:"result"`
		Error   *struct {
			Code int `json:"code"`
		} `json:"error"`
	}
	if err := c.transport.Do(ctx, method, http.MethodPost, c.transport.RPCURL(), body, &wire); err != nil {
		return zero, err
	}
	if wire.JSONRPC != "2.0" || wire.ID != id {
		return zero, errs.Decode(method, errors.New("the RPC version or request ID does not match"))
	}
	if wire.Error != nil {
		return zero, &errs.Error{Kind: errs.KindRPC, Operation: method, RPCCode: wire.Error.Code, Message: errs.ErrRPC.Message}
	}
	if len(wire.Result) == 0 || string(wire.Result) == "null" {
		return zero, errs.Decode(method, errors.New("the RPC result is missing"))
	}
	var result T
	if err := json.Unmarshal(wire.Result, &result); err != nil {
		return zero, errs.Decode(method, err)
	}
	return result, nil
}

func ProgramAccountData(account *Account, op string) ([]byte, error) {
	if account == nil {
		return nil, &errs.Error{Kind: errs.KindNotFound, Operation: op, Message: "The on-chain account was not found."}
	}
	if account.Owner != PumpProgramID || account.Executable {
		return nil, errs.Decode(op, errors.New("the account is not Pump program state"))
	}
	return AccountBase64(account.Data, op)
}

func AccountBase64(data json.RawMessage, op string) ([]byte, error) {
	var encoded []string
	if err := json.Unmarshal(data, &encoded); err != nil || len(encoded) != 2 || encoded[1] != "base64" {
		return nil, errs.Decode(op, errors.New("the account encoding must be base64"))
	}
	decoded, err := base64.StdEncoding.DecodeString(encoded[0])
	if err != nil {
		return nil, errs.Decode(op, err)
	}
	return decoded, nil
}

// Holder contains one of the largest token accounts and its wallet owner.
type Holder struct {
	TokenAccount string    `json:"token_account" yaml:"token_account"`
	Owner        string    `json:"owner,omitempty" yaml:"owner,omitempty"`
	Amount       RawAmount `json:"amount" yaml:"amount"`
	Decimals     uint8     `json:"decimals" yaml:"decimals"`
	SharePercent float64   `json:"share_percent" yaml:"share_percent"`
}

// HolderInfo reports at most 20 token accounts. It does not report all holders.
// Each slot identifies a separate RPC read. Wallet owners can occur more than once.
type HolderInfo struct {
	Mint        string    `json:"mint" yaml:"mint"`
	Accounts    []Holder  `json:"accounts" yaml:"accounts"`
	TotalSupply RawAmount `json:"total_supply" yaml:"total_supply"`
	Slot        uint64    `json:"slot" yaml:"slot"`
	SupplySlot  uint64    `json:"supply_slot" yaml:"supply_slot"`
	OwnersSlot  uint64    `json:"owners_slot" yaml:"owners_slot"`
}

// GetHolders reads the 20 largest token accounts, the supply, and their owners.
// RPC can return fewer than 20 accounts. A closed account has no owner in the result.
func (c *Client) GetHolders(ctx context.Context, mint string) (*HolderInfo, error) {
	if err := ValidateAddress(mint); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, c.transport.Timeout())
	defer cancel()
	type largestAccount struct {
		Address  string    `json:"address"`
		Amount   RawAmount `json:"amount"`
		Decimals uint8     `json:"decimals"`
	}
	largest, err := Call[Value[[]largestAccount]](ctx, c, "getTokenLargestAccounts", []any{mint, map[string]any{"commitment": "confirmed"}})
	if err != nil {
		return nil, err
	}
	if largest.Value == nil || len(largest.Value) > 20 {
		return nil, errs.Decode("get_holders", errors.New("the largest account list is not valid"))
	}
	supply, err := Call[Value[struct {
		Amount RawAmount `json:"amount"`
	}]](ctx, c, "getTokenSupply", []any{mint, map[string]any{"commitment": "confirmed"}})
	if err != nil {
		return nil, err
	}
	total, ok := new(big.Int).SetString(string(supply.Value.Amount), 10)
	if !ok || total.Sign() < 0 {
		return nil, errs.Decode("get_holders", errors.New("the supply is not valid"))
	}
	result := &HolderInfo{Mint: mint, Accounts: []Holder{}, TotalSupply: supply.Value.Amount, Slot: largest.Context.Slot, SupplySlot: supply.Context.Slot}
	if len(largest.Value) == 0 {
		return result, nil
	}
	addresses := make([]string, len(largest.Value))
	for i, item := range largest.Value {
		if err := ValidateAddress(item.Address); err != nil {
			return nil, errs.Decode("get_holders", err)
		}
		addresses[i] = item.Address
	}
	owners, err := Call[Value[[]*Account]](ctx, c, "getMultipleAccounts", []any{addresses, map[string]any{"encoding": "base64", "commitment": "confirmed"}})
	if err != nil {
		return nil, err
	}
	if len(owners.Value) != len(largest.Value) {
		return nil, errs.Decode("get_holders", errors.New("the owner account count does not match"))
	}
	result.OwnersSlot = owners.Context.Slot
	for i, item := range largest.Value {
		holder := Holder{TokenAccount: item.Address, Amount: item.Amount, Decimals: item.Decimals}
		amount, ok := new(big.Int).SetString(string(item.Amount), 10)
		if !ok || amount.Sign() < 0 {
			return nil, errs.Decode("get_holders", errors.New("the holder amount is not valid"))
		}
		if total.Sign() > 0 {
			ratio := new(big.Rat).SetFrac(amount, total)
			ratio.Mul(ratio, big.NewRat(100, 1))
			holder.SharePercent, _ = ratio.Float64()
		}
		if account := owners.Value[i]; account != nil {
			if (account.Owner != TokenProgram && account.Owner != Token2022Program) || account.Executable {
				return nil, errs.Decode("get_holders", errors.New("the account is not token program state"))
			}
			data, err := AccountBase64(account.Data, "get_holders")
			if err != nil {
				return nil, err
			}
			if len(data) < 165 || base58.Encode(data[:32]) != mint {
				return nil, errs.Decode("get_holders", fmt.Errorf("the token account mint or size is not valid"))
			}
			holder.Owner = base58.Encode(data[32:64])
		}
		result.Accounts = append(result.Accounts, holder)
	}
	return result, nil
}
