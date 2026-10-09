package curve

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"strconv"

	"filippo.io/edwards25519"
	"github.com/mr-tron/base58"
	"github.com/vasyza/pumpfun-sdk/internal/errs"
	"github.com/vasyza/pumpfun-sdk/internal/models"
	"github.com/vasyza/pumpfun-sdk/internal/solana"
)

// Service reads Pump curve state and graduation progress.
type Service struct{ rpc *solana.Client }

// New creates a curve service with the supplied RPC client.
func New(rpc *solana.Client) *Service { return &Service{rpc: rpc} }

type RawAmount = models.RawAmount

// DeriveAddress finds the Pump program address for a mint.
func DeriveAddress(mint string) (string, error) {
	if err := solana.ValidateAddress(mint); err != nil {
		return "", err
	}
	mintBytes, _ := base58.Decode(mint)
	program, _ := base58.Decode(solana.PumpProgramID)
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
	return "", &errs.Error{Kind: errs.KindUnsupported, Operation: "derive_curve", Message: "The program address could not be derived."}
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
		return nil, errs.Decode("decode_curve", errors.New("the curve header or size is not valid"))
	}
	// The current layout has 166 bytes. Old accounts can contain fewer fields.
	buf := make([]byte, 166)
	copy(buf, data)
	for _, i := range []int{48, 81, 82, 123, 124} {
		if buf[i] > 1 {
			return nil, errs.Decode("decode_curve", errors.New("the account boolean is not valid"))
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

// GetBondingCurve reads the derived account through Solana RPC at confirmed state.
func (c *Service) GetBondingCurve(ctx context.Context, mint string) (*BondingCurve, error) {
	address, err := DeriveAddress(mint)
	if err != nil {
		return nil, err
	}
	wire, err := solana.Call[solana.Value[*solana.Account]](ctx, c.rpc, "getAccountInfo", []any{address, map[string]any{"encoding": "base64", "commitment": "confirmed"}})
	if err != nil {
		return nil, err
	}
	data, err := solana.ProgramAccountData(wire.Value, "get_bonding_curve")
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
func (c *Service) GetGraduationProgress(ctx context.Context, mint string) (*GraduationProgress, error) {
	address, err := DeriveAddress(mint)
	if err != nil {
		return nil, err
	}
	wire, err := solana.Call[solana.Value[[]*solana.Account]](ctx, c.rpc, "getMultipleAccounts", []any{[]string{address, solana.PumpGlobal}, map[string]any{"encoding": "base64", "commitment": "confirmed"}})
	if err != nil {
		return nil, err
	}
	if len(wire.Value) != 2 {
		return nil, errs.Decode("get_progress", errors.New("the account count does not match"))
	}
	curveData, err := solana.ProgramAccountData(wire.Value[0], "get_progress")
	if err != nil {
		return nil, err
	}
	globalData, err := solana.ProgramAccountData(wire.Value[1], "get_progress")
	if err != nil {
		return nil, err
	}
	if len(globalData) < 97 || [8]byte(globalData[:8]) != globalDiscriminator {
		return nil, errs.Decode("get_progress", errors.New("the Global account is not valid"))
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
