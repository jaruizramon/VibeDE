package analyze

import (
	"fmt"
	"strings"
)

// Complexity is a product of polynomial and logarithmic factors. Exponential
// complexity (2^n) is tracked separately because it dominates every product.
type Complexity struct {
	Exponential bool
	NPow        int
	LogPow      int
}

func O1() Complexity     { return Complexity{} }
func ON() Complexity     { return Complexity{NPow: 1} }
func OLogN() Complexity  { return Complexity{LogPow: 1} }
func ONLogN() Complexity { return Complexity{NPow: 1, LogPow: 1} }
func OExp() Complexity   { return Complexity{Exponential: true} }

// Mul multiplies two complexities. Exponential wins.
func (c Complexity) Mul(o Complexity) Complexity {
	if c.Exponential || o.Exponential {
		return Complexity{Exponential: true}
	}
	return Complexity{NPow: c.NPow + o.NPow, LogPow: c.LogPow + o.LogPow}
}

// Max returns the dominant of two complexities.
func (c Complexity) Max(o Complexity) Complexity {
	if c.Exponential || o.Exponential {
		if c.Exponential && o.Exponential {
			return c
		}
		if c.Exponential {
			return c
		}
		return o
	}
	if c.NPow != o.NPow {
		if c.NPow > o.NPow {
			return c
		}
		return o
	}
	if c.LogPow >= o.LogPow {
		return c
	}
	return o
}

// IsConstant reports O(1).
func (c Complexity) IsConstant() bool { return !c.Exponential && c.NPow == 0 && c.LogPow == 0 }

// String renders conventional Big-O notation.
func (c Complexity) String() string {
	if c.Exponential {
		if c.NPow > 0 || c.LogPow > 0 {
			return fmt.Sprintf("O(2^n · %s)", c.polyLog())
		}
		return "O(2^n)"
	}
	if c.NPow == 0 && c.LogPow == 0 {
		return "O(1)"
	}
	return "O(" + c.polyLog() + ")"
}

func (c Complexity) polyLog() string {
	var parts []string
	if c.NPow > 0 {
		parts = append(parts, nTerm(c.NPow))
	}
	if c.LogPow > 0 {
		parts = append(parts, logTerm(c.LogPow))
	}
	return strings.Join(parts, " ")
}

func nTerm(p int) string {
	switch p {
	case 1:
		return "n"
	case 2:
		return "n²"
	case 3:
		return "n³"
	case 4:
		return "n⁴"
	default:
		return fmt.Sprintf("n^%d", p)
	}
}

func logTerm(p int) string {
	switch p {
	case 1:
		return "log n"
	case 2:
		return "log² n"
	case 3:
		return "log³ n"
	default:
		return fmt.Sprintf("log^%d n", p)
	}
}

// joinUnique appends strings not already present, preserving order.
func joinUnique(dst []string, add ...string) []string {
	for _, s := range add {
		if s == "" {
			continue
		}
		dup := false
		for _, have := range dst {
			if have == s {
				dup = true
				break
			}
		}
		if !dup {
			dst = append(dst, s)
		}
	}
	return dst
}
