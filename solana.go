package pumpfun

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"strconv"

	"filippo.io/edwards25519"
	"github.com/mr-tron/base58"
)

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
		return invalid("address", "The Solana address must contain 32 bytes in base58 form.")
	}
	data, err := base58.Decode(address)
	if err != nil || len(data) != 32 {
		return invalid("address", "The Solana address must contain 32 bytes in base58 form.")
	}
	return nil
}

// DeriveBondingCurveAddress finds the Pump program address for a mint.
func DeriveBondingCurveAddress(mint string) (string, error) {
	if err := ValidateAddress(mint); err != nil {
		return "", err
	}
	mintBytes, _ := base58.Decode(mint)
	program, _ := base58.Decode(PumpProgramID)
	for bump := 255; bump >= 0; bump-- {
		h := sha256.New()
		_, _ = h.Write([]byte("bonding-curve"))
		_, _ = h.Write(mintBytes)
		_, _ = h.Write([]byte{byte(bump)})
		_, _ = h.Write(program)
		_, _ = h.Write([]byte("ProgramDerivedAddress"))
		digest := h.Sum(nil)
		if _, err := new(edwards25519.Point).SetBytes(digest); err != nil {
			return base58.Encode(digest), nil
		}
	}
	return "", &Error{Kind: KindUnsupported, Operation: "derive_curve", Message: "The program address could not be derived."}
}

// BondingCurve contains on-chain reserves. Raw amounts retain all digits.
// A zero quote mint means that the quote asset is native SOL.
type BondingCurve struct {
	Mint                        string    `json:"mint" yaml:"mint"`
	Address                     string    `json:"address" yaml:"address"`
	Slot                        uint64    `json:"slot" yaml:"slot"`
	VirtualTokenReserves        RawAmount `json:"virtual_token_reserves" yaml:"virtual_token_reserves"`
	VirtualQuoteReserves        RawAmount `json:"virtual_quote_reserves" yaml:"virtual_quote_reserves"`
	RealTokenReserves           RawAmount `json:"real_token_reserves" yaml:"real_token_reserves"`
	RealQuoteReserves           RawAmount `json:"real_quote_reserves" yaml:"real_quote_reserves"`
	TotalSupply                 RawAmount `json:"total_supply" yaml:"total_supply"`
	Complete                    bool      `json:"complete" yaml:"complete"`
	Creator                     string    `json:"creator" yaml:"creator"`
	QuoteMint                   string    `json:"quote_mint" yaml:"quote_mint"`
	IsMayhemMode                bool      `json:"is_mayhem_mode" yaml:"is_mayhem_mode"`
	IsCashbackCoin              bool      `json:"is_cashback_coin" yaml:"is_cashback_coin"`
	CreatorFeeBPS               RawAmount `json:"creator_fee_bps" yaml:"creator_fee_bps"`
	IsHolderReward              bool      `json:"is_holder_reward" yaml:"is_holder_reward"`
	CreatorFee                  RawAmount `json:"creator_fee" yaml:"creator_fee"`
	ProtocolFees                RawAmount `json:"protocol_fees" yaml:"protocol_fees"`
	Depth                       uint8     `json:"depth" yaml:"depth"`
	InitialVirtualQuoteReserves RawAmount `json:"initial_virtual_quote_reserves" yaml:"initial_virtual_quote_reserves"`
	PostCompleteBaseOut         RawAmount `json:"post_complete_base_out" yaml:"post_complete_base_out"`
	PostCompleteQuoteIn         RawAmount `json:"post_complete_quote_in" yaml:"post_complete_quote_in"`
}

var curveDiscriminator = [8]byte{23, 183, 248, 55, 96, 216, 172, 96}
var globalDiscriminator = [8]byte{167, 232, 232, 177, 200, 108, 114, 127}

// DecodeBondingCurve reads the Pump account layout. Missing appended fields use
// zero values, as specified by the Pump program. Unknown trailing data is ignored.
func DecodeBondingCurve(data []byte) (*BondingCurve, error) {
	if len(data) < 49 || [8]byte(data[:8]) != curveDiscriminator {
		return nil, decodeError("decode_curve", errors.New("the curve header or size is not valid"))
	}
	// The current layout has 166 bytes. Old accounts can contain fewer fields.
	buf := make([]byte, 166)
	copy(buf, data)
	for _, i := range []int{48, 81, 82, 123, 124} {
		if buf[i] > 1 {
			return nil, decodeError("decode_curve", errors.New("the account boolean is not valid"))
		}
	}
	amount := func(offset int) RawAmount { return rawUint(binary.LittleEndian.Uint64(buf[offset : offset+8])) }
	return &BondingCurve{
		VirtualTokenReserves: amount(8), VirtualQuoteReserves: amount(16),
		RealTokenReserves: amount(24), RealQuoteReserves: amount(32), TotalSupply: amount(40),
		Complete: buf[48] == 1, Creator: base58.Encode(buf[49:81]),
		IsMayhemMode: buf[81] == 1, IsCashbackCoin: buf[82] == 1, QuoteMint: base58.Encode(buf[83:115]),
		CreatorFeeBPS: amount(115), IsHolderReward: buf[124] == 1,
		CreatorFee: amount(125), ProtocolFees: amount(133), Depth: buf[141],
		InitialVirtualQuoteReserves: amount(142), PostCompleteBaseOut: amount(150), PostCompleteQuoteIn: amount(158),
	}, nil
}

func rawUint(n uint64) RawAmount { return RawAmount(strconv.FormatUint(n, 10)) }

type rpcAccount struct {
	Owner      string          `json:"owner"`
	Executable bool            `json:"executable"`
	Data       json.RawMessage `json:"data"`
}

type rpcContext struct {
	Slot uint64 `json:"slot"`
}

type rpcValue[T any] struct {
	Context rpcContext `json:"context"`
	Value   T          `json:"value"`
}

func rpcCall[T any](ctx context.Context, c *Client, method string, params any) (T, error) {
	var zero T
	id := c.rpcID.Add(1)
	body, err := json.Marshal(struct {
		JSONRPC string `json:"jsonrpc"`
		ID      uint64 `json:"id"`
		Method  string `json:"method"`
		Params  any    `json:"params"`
	}{"2.0", id, method, params})
	if err != nil {
		return zero, invalid(method, "The RPC input is not valid.")
	}
	var wire struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      uint64          `json:"id"`
		Result  json.RawMessage `json:"result"`
		Error   *struct {
			Code int `json:"code"`
		} `json:"error"`
	}
	if err := c.do(ctx, method, http.MethodPost, c.rpcURL, body, &wire); err != nil {
		return zero, err
	}
	if wire.JSONRPC != "2.0" || wire.ID != id {
		return zero, decodeError(method, errors.New("the RPC version or request ID does not match"))
	}
	if wire.Error != nil {
		return zero, &Error{Kind: KindRPC, Operation: method, RPCCode: wire.Error.Code, Message: ErrRPC.Message}
	}
	if len(wire.Result) == 0 || string(wire.Result) == "null" {
		return zero, decodeError(method, errors.New("the RPC result is missing"))
	}
	var result T
	if err := json.Unmarshal(wire.Result, &result); err != nil {
		return zero, decodeError(method, err)
	}
	return result, nil
}

func programAccountData(account *rpcAccount, op string) ([]byte, error) {
	if account == nil {
		return nil, &Error{Kind: KindNotFound, Operation: op, Message: "The on-chain account was not found."}
	}
	if account.Owner != PumpProgramID || account.Executable {
		return nil, decodeError(op, errors.New("the account is not Pump program state"))
	}
	return accountBase64(account.Data, op)
}

func accountBase64(data json.RawMessage, op string) ([]byte, error) {
	var encoded []string
	if err := json.Unmarshal(data, &encoded); err != nil || len(encoded) != 2 || encoded[1] != "base64" {
		return nil, decodeError(op, errors.New("the account encoding must be base64"))
	}
	decoded, err := base64.StdEncoding.DecodeString(encoded[0])
	if err != nil {
		return nil, decodeError(op, err)
	}
	return decoded, nil
}

// GetBondingCurve reads the derived account through Solana RPC at confirmed state.
func (c *Client) GetBondingCurve(ctx context.Context, mint string) (*BondingCurve, error) {
	address, err := DeriveBondingCurveAddress(mint)
	if err != nil {
		return nil, err
	}
	wire, err := rpcCall[rpcValue[*rpcAccount]](ctx, c, "getAccountInfo", []any{address, map[string]any{"encoding": "base64", "commitment": "confirmed"}})
	if err != nil {
		return nil, err
	}
	data, err := programAccountData(wire.Value, "get_bonding_curve")
	if err != nil {
		return nil, err
	}
	curve, err := DecodeBondingCurve(data)
	if err != nil {
		return nil, err
	}
	curve.Mint, curve.Address, curve.Slot = mint, address, wire.Context.Slot
	return curve, nil
}

// GraduationProgress reports reserve depletion. Percent is an estimate that
// uses the current Global start reserve. A nil percent means no estimate is safe.
type GraduationProgress struct {
	Mint                       string    `json:"mint" yaml:"mint"`
	Address                    string    `json:"address" yaml:"address"`
	Slot                       uint64    `json:"slot" yaml:"slot"`
	Complete                   bool      `json:"complete" yaml:"complete"`
	Percent                    *float64  `json:"percent,omitempty" yaml:"percent,omitempty"`
	Estimated                  bool      `json:"estimated" yaml:"estimated"`
	InitialRealTokenReserves   RawAmount `json:"initial_real_token_reserves" yaml:"initial_real_token_reserves"`
	RemainingRealTokenReserves RawAmount `json:"remaining_real_token_reserves" yaml:"remaining_real_token_reserves"`
	Reason                     string    `json:"reason,omitempty" yaml:"reason,omitempty"`
}

// GetGraduationProgress reads the curve and Global account in one RPC snapshot.
// Curve completion does not prove that a PumpSwap pool is already open.
func (c *Client) GetGraduationProgress(ctx context.Context, mint string) (*GraduationProgress, error) {
	address, err := DeriveBondingCurveAddress(mint)
	if err != nil {
		return nil, err
	}
	wire, err := rpcCall[rpcValue[[]*rpcAccount]](ctx, c, "getMultipleAccounts", []any{[]string{address, PumpGlobal}, map[string]any{"encoding": "base64", "commitment": "confirmed"}})
	if err != nil {
		return nil, err
	}
	if len(wire.Value) != 2 {
		return nil, decodeError("get_progress", errors.New("the account count does not match"))
	}
	curveData, err := programAccountData(wire.Value[0], "get_progress")
	if err != nil {
		return nil, err
	}
	globalData, err := programAccountData(wire.Value[1], "get_progress")
	if err != nil {
		return nil, err
	}
	if len(globalData) < 97 || [8]byte(globalData[:8]) != globalDiscriminator {
		return nil, decodeError("get_progress", errors.New("the Global account is not valid"))
	}
	curve, err := DecodeBondingCurve(curveData)
	if err != nil {
		return nil, err
	}
	initial := binary.LittleEndian.Uint64(globalData[89:97])
	remaining, _ := curve.RealTokenReserves.Uint64()
	result := &GraduationProgress{
		Mint: mint, Address: address, Slot: wire.Context.Slot, Complete: curve.Complete, Estimated: !curve.Complete,
		InitialRealTokenReserves: rawUint(initial), RemainingRealTokenReserves: curve.RealTokenReserves,
	}
	switch {
	case curve.Complete:
		percent := 100.0
		result.Percent = &percent
	case curve.IsMayhemMode:
		result.Reason = "A reserve estimate is not available for Mayhem mode."
	case initial == 0 || remaining > initial:
		result.Reason = "The current start reserve cannot give a safe estimate."
	default:
		percent := 100 * float64(initial-remaining) / float64(initial)
		result.Percent = &percent
	}
	return result, nil
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
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	type largestAccount struct {
		Address  string    `json:"address"`
		Amount   RawAmount `json:"amount"`
		Decimals uint8     `json:"decimals"`
	}
	largest, err := rpcCall[rpcValue[[]largestAccount]](ctx, c, "getTokenLargestAccounts", []any{mint, map[string]any{"commitment": "confirmed"}})
	if err != nil {
		return nil, err
	}
	if largest.Value == nil || len(largest.Value) > 20 {
		return nil, decodeError("get_holders", errors.New("the largest account list is not valid"))
	}
	supply, err := rpcCall[rpcValue[struct {
		Amount RawAmount `json:"amount"`
	}]](ctx, c, "getTokenSupply", []any{mint, map[string]any{"commitment": "confirmed"}})
	if err != nil {
		return nil, err
	}
	total, ok := new(big.Int).SetString(string(supply.Value.Amount), 10)
	if !ok || total.Sign() < 0 {
		return nil, decodeError("get_holders", errors.New("the supply is not valid"))
	}
	result := &HolderInfo{Mint: mint, Accounts: []Holder{}, TotalSupply: supply.Value.Amount, Slot: largest.Context.Slot, SupplySlot: supply.Context.Slot}
	if len(largest.Value) == 0 {
		return result, nil
	}
	addresses := make([]string, len(largest.Value))
	for i, item := range largest.Value {
		if err := ValidateAddress(item.Address); err != nil {
			return nil, decodeError("get_holders", err)
		}
		addresses[i] = item.Address
	}
	owners, err := rpcCall[rpcValue[[]*rpcAccount]](ctx, c, "getMultipleAccounts", []any{addresses, map[string]any{"encoding": "base64", "commitment": "confirmed"}})
	if err != nil {
		return nil, err
	}
	if len(owners.Value) != len(largest.Value) {
		return nil, decodeError("get_holders", errors.New("the owner account count does not match"))
	}
	result.OwnersSlot = owners.Context.Slot
	for i, item := range largest.Value {
		holder := Holder{TokenAccount: item.Address, Amount: item.Amount, Decimals: item.Decimals}
		amount, ok := new(big.Int).SetString(string(item.Amount), 10)
		if !ok || amount.Sign() < 0 {
			return nil, decodeError("get_holders", errors.New("the holder amount is not valid"))
		}
		if total.Sign() > 0 {
			ratio := new(big.Rat).SetFrac(amount, total)
			ratio.Mul(ratio, big.NewRat(100, 1))
			holder.SharePercent, _ = ratio.Float64()
		}
		if account := owners.Value[i]; account != nil {
			if (account.Owner != TokenProgram && account.Owner != Token2022Program) || account.Executable {
				return nil, decodeError("get_holders", errors.New("the account is not token program state"))
			}
			data, err := accountBase64(account.Data, "get_holders")
			if err != nil {
				return nil, err
			}
			if len(data) < 165 || base58.Encode(data[:32]) != mint {
				return nil, decodeError("get_holders", fmt.Errorf("the token account mint or size is not valid"))
			}
			holder.Owner = base58.Encode(data[32:64])
		}
		result.Accounts = append(result.Accounts, holder)
	}
	return result, nil
}
