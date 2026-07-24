package dynamodb

// Order-preserving encoding for DynamoDB Number ("N") key values — see
// docs/plans/dynamodb-gsi-design.md §2 "Key type constraints".
//
// AWS's Query contract: "If the data type of the sort key is Number, the
// results are returned in numeric order; otherwise, the results are returned
// in order of UTF-8 bytes." DynamoDB Numbers are arbitrary-precision decimals
// (up to 38 significant digits, exponent range roughly -130..126) — not IEEE
// floats — so ordering them correctly requires a decimal-aware,
// order-preserving encoding rather than a parse into float64 (silent
// precision loss) or a plain compare of the raw decimal text (sorts
// lexicographically: "10" < "5", the bug this file exists to fix).
//
// Encoding layout (fixed width, ASCII digits only — memcmp/string-comparable):
//
//	[1 char sign]      '0' = negative, '1' = zero, '2' = positive
//	[4 chars exponent] biased decimal exponent of the leading significant
//	                   digit, zero-padded; digit-complemented (9-d) when sign
//	                   is negative so a larger-magnitude negative sorts first
//	[40 chars mantissa] normalized significant digits (no leading/trailing
//	                   zero), right-padded with '0' to fixed width;
//	                   digit-complemented (9-d) when sign is negative
//
// Total length is always 1+4+40 = 45 characters, for every valid input —
// the fixed width means two encoded keys are always memcmp/string-comparable
// field-by-field, and the whole encoded value is a legal component of the
// NUL-separated composite keys itemCompositeKey/dynamodb_items already use.
//
// Canonicalization: the value is normalized to scientific form
// (sign * 0.d1d2...dn * 10^(exponent+1), d1 != 0) with trailing zeros
// stripped from the significant-digit string before encoding, so numerically
// equal values with different textual representations ("1e2", "100",
// "100.00") produce byte-identical encodings. This is required, not just
// nice-to-have: DynamoDB compares Number keys — including for equality — by
// numeric value, so two representations of the same key must collide in
// storage exactly like two identical strings would for an S key.
//
// Zero is a special case: "0", "-0", "0.0", "0e5", etc. all encode to the
// same canonical all-zero-field representation (sign='1', exponent and
// mantissa left at their zero value) — there is no "negative zero" in this
// encoding, matching DynamoDB's decimal-numeric-equality semantics where -0
// and 0 are the same key.

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

const (
	numExpWidth      = 4    // decimal digits in the biased-exponent field
	numExpBias       = 1000 // biased exponent = adjustedExponent + numExpBias
	numMantissaWidth = 40   // decimal digits in the mantissa field (DynamoDB caps at 38 significant digits)
)

// numberPattern matches the decimal notation DynamoDB Number attributes use:
// an optional sign, an integer part, an optional fractional part, and an
// optional exponent (e.g. "42", "-3.14", "1.5E+10", "0").
var numberPattern = regexp.MustCompile(`^([+-]?)(\d+)(?:\.(\d+))?(?:[eE]([+-]?\d+))?$`)

// encodeOrderableNumber converts a DynamoDB Number attribute's raw decimal
// string into a fixed-width string whose ordinary Go string ordering matches
// the number's numeric order (see the package-level doc comment above for
// the layout and the equal-value canonicalization guarantee). Returns an
// error if s is not a valid DynamoDB Number or falls outside the encoding's
// supported exponent/precision range (both comfortably wider than
// DynamoDB's own documented ~38-significant-digit, ~-130..126-exponent
// bounds).
func encodeOrderableNumber(s string) (string, error) {
	trimmed := strings.TrimSpace(s)
	m := numberPattern.FindStringSubmatch(trimmed)
	if m == nil {
		return "", fmt.Errorf("dynamodb: %q is not a valid Number", s)
	}
	negative := m[1] == "-"
	intPart, fracPart, expPart := m[2], m[3], m[4]

	exp := 0
	if expPart != "" {
		var err error
		exp, err = parseSmallSignedInt(expPart)
		if err != nil {
			return "", fmt.Errorf("dynamodb: %q has an invalid exponent: %w", s, err)
		}
	}

	// value = sign * digits(as integer) * 10^e10
	digits := intPart + fracPart
	e10 := exp - len(fracPart)

	digits = strings.TrimLeft(digits, "0") // leading zeros don't affect value or e10
	if digits == "" {
		return zeroEncoding(), nil // value is exactly zero, regardless of sign
	}

	trimmedDigits := strings.TrimRight(digits, "0")
	e10 += len(digits) - len(trimmedDigits) // compensate for stripped trailing zeros
	digits = trimmedDigits

	if len(digits) > numMantissaWidth {
		return "", fmt.Errorf("dynamodb: %q has more than %d significant digits", s, numMantissaWidth)
	}

	// adjustedExponent is the power of 10 of the leading digit — value =
	// sign * 0.<digits> * 10^(adjustedExponent+1).
	adjustedExponent := e10 + len(digits) - 1
	biased := adjustedExponent + numExpBias
	if biased < 0 || biased >= pow10(numExpWidth) {
		return "", fmt.Errorf("dynamodb: %q exponent is outside the supported range", s)
	}

	expField := fmt.Sprintf("%0*d", numExpWidth, biased)
	mantField := digits + strings.Repeat("0", numMantissaWidth-len(digits))

	if negative {
		return "0" + complementDigits(expField) + complementDigits(mantField), nil
	}
	return "2" + expField + mantField, nil
}

func zeroEncoding() string {
	return "1" + strings.Repeat("0", numExpWidth) + strings.Repeat("0", numMantissaWidth)
}

// complementDigits maps each ASCII decimal digit d to '0'+(9-d) — the
// 9's-complement used to reverse field ordering for negative numbers (see
// the package doc comment's layout section: a larger true exponent/mantissa
// value must produce a *smaller* encoded field so more-negative numbers sort
// first).
func complementDigits(s string) string {
	b := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		b[i] = '0' + ('9' - s[i])
	}
	return string(b)
}

func pow10(n int) int {
	p := 1
	for i := 0; i < n; i++ {
		p *= 10
	}
	return p
}

// parseSmallSignedInt parses a decimal exponent string (as matched by
// numberPattern's exponent group) into an int, guarding against overflow on
// pathological input rather than relying on encodeOrderableNumber's later
// range check to catch it after the fact.
func parseSmallSignedInt(s string) (int, error) {
	neg := false
	switch {
	case strings.HasPrefix(s, "+"):
		s = s[1:]
	case strings.HasPrefix(s, "-"):
		neg = true
		s = s[1:]
	}
	if s == "" {
		return 0, errors.New("empty exponent")
	}
	n := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < '0' || c > '9' {
			return 0, errors.New("invalid exponent digit")
		}
		n = n*10 + int(c-'0')
		if n > 1_000_000 {
			return 0, errors.New("exponent magnitude too large")
		}
	}
	if neg {
		n = -n
	}
	return n, nil
}
