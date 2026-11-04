// Expression parser for --when clauses.
//
// Supports a minimal grammar for MVP:
//
//	<metric> <op> <number>
//
// where:
//   <metric> is any identifier readable from the indicator panel
//     (cycle.mayer_multiple, valuation.ahr999_compressed, etc.) OR the
//     short form without the domain prefix (mayer_multiple, ahr999_compressed).
//   <op> ∈ { < <= > >= = != }
//   <number> is any parseable float.
//
// Examples:
//	mayer_multiple < 0.8
//	ahr999_compressed > 3.344
//	sma_200w_dev < -10
//
// More complex composition (AND/OR) is explicitly out of scope for MVP —
// the user writes one condition per `guanfu watch` invocation.

package alerts

import (
	"fmt"
	"strconv"
	"strings"
)

// Condition is a parsed --when expression.
type Condition struct {
	Metric    string // short key, not domain-qualified
	Operator  string // one of < <= > >= = !=
	Threshold float64
}

// Parse returns a Condition from an expression like "mayer_multiple < 0.8".
// Whitespace is normalized; metric names are kept lowercase.
func Parse(expr string) (*Condition, error) {
	s := strings.TrimSpace(expr)
	if s == "" {
		return nil, fmt.Errorf("empty expression")
	}
	// Find operator (longest-match first so "<=" beats "<").
	ops := []string{"<=", ">=", "!=", "<", ">", "="}
	var op string
	var idx int
	for _, cand := range ops {
		if i := strings.Index(s, cand); i > 0 {
			op = cand
			idx = i
			break
		}
	}
	if op == "" {
		return nil, fmt.Errorf("no operator in %q (expected one of < <= > >= = !=)", expr)
	}
	lhs := strings.TrimSpace(s[:idx])
	rhs := strings.TrimSpace(s[idx+len(op):])
	if lhs == "" || rhs == "" {
		return nil, fmt.Errorf("expression must be <metric> <op> <number>, got %q", expr)
	}
	val, err := strconv.ParseFloat(rhs, 64)
	if err != nil {
		return nil, fmt.Errorf("threshold not a number: %q", rhs)
	}
	// Strip optional "domain." prefix so callers don't have to care.
	if dot := strings.Index(lhs, "."); dot >= 0 {
		lhs = lhs[dot+1:]
	}
	return &Condition{
		Metric:    strings.ToLower(lhs),
		Operator:  op,
		Threshold: val,
	}, nil
}

// Evaluate returns true when the observed value satisfies the condition.
func (c *Condition) Evaluate(observed float64) bool {
	if c == nil {
		return false
	}
	switch c.Operator {
	case "<":
		return observed < c.Threshold
	case "<=":
		return observed <= c.Threshold
	case ">":
		return observed > c.Threshold
	case ">=":
		return observed >= c.Threshold
	case "=":
		return observed == c.Threshold
	case "!=":
		return observed != c.Threshold
	}
	return false
}
