// Package money uses nanodollars internally; decimal USD exists only at wire boundaries.
package money

import (
	"fmt"
	"math/big"
	"regexp"
)

const UnitsPerUSD int64 = 1_000_000_000

// MaxExact also bounds Redis Lua arithmetic, which uses IEEE-754 numbers.
const MaxExact int64 = 1<<53 - 1

var decimalUSD = regexp.MustCompile(`^[0-9]+(?:\.[0-9]{1,9})?$`)

func ParseUSD(value string) (int64, error) {
	if !decimalUSD.MatchString(value) {
		return 0, fmt.Errorf("USD must be a non-negative decimal with at most nine fractional digits")
	}
	r, ok := new(big.Rat).SetString(value)
	if !ok || r.Sign() < 0 {
		return 0, fmt.Errorf("invalid non-negative USD amount")
	}
	r.Mul(r, new(big.Rat).SetInt64(UnitsPerUSD))
	if !r.IsInt() || !r.Num().IsInt64() || r.Num().Int64() > MaxExact {
		return 0, fmt.Errorf("USD amount exceeds nanodollar precision or range")
	}
	return r.Num().Int64(), nil
}

// LegacyUSD rounds historic decimal charges upward by at most one nanodollar.
func LegacyUSD(value string) (int64, error) {
	r, ok := new(big.Rat).SetString(value)
	if !ok || r.Sign() < 0 {
		return 0, fmt.Errorf("invalid legacy USD amount")
	}
	r.Mul(r, new(big.Rat).SetInt64(UnitsPerUSD))
	q, rem := new(big.Int), new(big.Int)
	q.QuoRem(r.Num(), r.Denom(), rem)
	if rem.Sign() != 0 {
		q.Add(q, big.NewInt(1))
	}
	if !q.IsInt64() || q.Int64() > MaxExact {
		return 0, fmt.Errorf("legacy USD amount out of range")
	}
	return q.Int64(), nil
}

func USD(units int64) float64 { return float64(units) / float64(UnitsPerUSD) }

func Cost(input, output, rateIn, rateOut int64) (int64, error) {
	for _, v := range []int64{input, output, rateIn, rateOut} {
		if v < 0 || v > MaxExact {
			return 0, fmt.Errorf("invalid token count or price")
		}
	}
	in := new(big.Int).Mul(big.NewInt(input), big.NewInt(rateIn))
	in.Add(in, new(big.Int).Mul(big.NewInt(output), big.NewInt(rateOut)))
	if !in.IsInt64() || in.Int64() > MaxExact {
		return 0, fmt.Errorf("charge out of range")
	}
	return in.Int64(), nil
}
