// Public curve API; implementation lives in internal/curve.
package pumpfun

import (
	sdkCurve "github.com/vasyza/pumpfun-sdk/internal/curve"
)

type BondingCurve = sdkCurve.BondingCurve
type GraduationProgress = sdkCurve.GraduationProgress

// DeriveBondingCurveAddress finds the Pump program address for a mint.
func DeriveBondingCurveAddress(mint string) (string, error) { return sdkCurve.DeriveAddress(mint) }

// DecodeBondingCurve reads the published Pump account layout.
func DecodeBondingCurve(data []byte) (*BondingCurve, error) { return sdkCurve.DecodeBondingCurve(data) }
