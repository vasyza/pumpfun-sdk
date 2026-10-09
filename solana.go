// Public solana API; implementation lives in internal/solana.
package pumpfun

import (
	sdkSolana "github.com/vasyza/pumpfun-sdk/internal/solana"
)

type Holder = sdkSolana.Holder
type HolderInfo = sdkSolana.HolderInfo

const (
	PumpProgramID    = sdkSolana.PumpProgramID
	PumpGlobal       = sdkSolana.PumpGlobal
	NativeSOLMint    = sdkSolana.NativeSOLMint
	TokenProgram     = sdkSolana.TokenProgram
	Token2022Program = sdkSolana.Token2022Program
)

// ValidateAddress checks a Solana address in base58 form.
func ValidateAddress(address string) error { return sdkSolana.ValidateAddress(address) }
