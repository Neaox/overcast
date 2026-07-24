package dynamodb

// Tests for encodeOrderableNumber (docs/plans/dynamodb-gsi-design.md §2/§6).
//
// The property test compares encoded-string order against an *independent*
// reference: a big.Rat built from the same decimal grammar but via exact
// rational arithmetic (Mul/Quo by powers of ten), not by reusing
// encodeOrderableNumber's own digit-trim-and-complement logic. Sharing only
// the "parse the decimal grammar into (sign, digits, exponent)" step keeps
// the test meaningful — the part actually at risk of a subtle bug is the
// fixed-width/complement encoding, and that part has no shared code with the
// oracle.

import (
	"fmt"
	"math/big"
	"math/rand"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// ---- independent big.Rat oracle --------------------------------------------

var refPattern = regexp.MustCompile(`^([+-]?)(\d+)(?:\.(\d+))?(?:[eE]([+-]?\d+))?$`)

// decimalToRat is a from-scratch decimal-string-to-big.Rat converter used
// only by tests, so numeric_encoding_test.go has an oracle that doesn't
// share arithmetic with encodeOrderableNumber.
func decimalToRat(t *testing.T, s string) *big.Rat {
	t.Helper()
	m := refPattern.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		t.Fatalf("decimalToRat: invalid fixture %q", s)
	}
	sign, intPart, fracPart, expPart := m[1], m[2], m[3], m[4]

	exp := 0
	if expPart != "" {
		e, err := strconv.Atoi(expPart)
		if err != nil {
			t.Fatalf("decimalToRat: bad exponent in %q: %v", s, err)
		}
		exp = e
	}

	digits := intPart + fracPart
	n := new(big.Int)
	if _, ok := n.SetString(digits, 10); !ok {
		t.Fatalf("decimalToRat: bad digits in %q", s)
	}
	if sign == "-" {
		n.Neg(n)
	}
	r := new(big.Rat).SetInt(n)

	e10 := exp - len(fracPart)
	if e10 != 0 {
		pow := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(abs(e10))), nil)
		powRat := new(big.Rat).SetInt(pow)
		if e10 > 0 {
			r.Mul(r, powRat)
		} else {
			r.Quo(r, powRat)
		}
	}
	return r
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

func sign3(n int) int {
	switch {
	case n < 0:
		return -1
	case n > 0:
		return 1
	default:
		return 0
	}
}

// ---- curated edge cases -----------------------------------------------------

// TestEncodeOrderableNumber_CuratedOrdering pins the classic decimal-ordering
// traps called out in dynamodb-gsi-design.md §6: 0 vs -0, 9 vs 10, -9 vs
// -10, 0.5 vs 0.05, and equal-value alternate representations. Every
// adjacent pair here must encode in strictly increasing order except where
// noted as an equal-value pair (which must encode identically).
func TestEncodeOrderableNumber_CuratedOrdering(t *testing.T) {
	ascending := []string{
		"-1e130", "-999999999999999999999999999999999999", "-50", "-10", "-9", "-1", "-0.5", "-0.05",
		"0",
		"0.05", "0.5", "1", "9", "10", "50",
		"999999999999999999999999999999999999", "1e125",
	}
	var prev string
	var prevRaw string
	for i, raw := range ascending {
		enc, err := encodeOrderableNumber(raw)
		if err != nil {
			t.Fatalf("encodeOrderableNumber(%q): unexpected error: %v", raw, err)
		}
		if i > 0 && enc <= prev {
			t.Errorf("ordering violated: encode(%q)=%q should be > encode(%q)=%q", raw, enc, prevRaw, prev)
		}
		prev, prevRaw = enc, raw
	}
}

// TestEncodeOrderableNumber_ZeroNormalization asserts every textual spelling
// of zero collapses to one canonical encoding, including negative zero —
// DynamoDB has no distinct "-0" key.
func TestEncodeOrderableNumber_ZeroNormalization(t *testing.T) {
	zeros := []string{"0", "-0", "0.0", "-0.0", "0.000", "0e5", "-0e10", "0.0e-3", "00", "-00.00"}
	want, err := encodeOrderableNumber("0")
	if err != nil {
		t.Fatalf("encodeOrderableNumber(\"0\"): %v", err)
	}
	for _, z := range zeros {
		got, err := encodeOrderableNumber(z)
		if err != nil {
			t.Fatalf("encodeOrderableNumber(%q): unexpected error: %v", z, err)
		}
		if got != want {
			t.Errorf("encodeOrderableNumber(%q) = %q, want canonical zero %q", z, got, want)
		}
	}
}

// TestEncodeOrderableNumber_EqualRepresentationsCollide is the "decide and
// pin" requirement from the task: DynamoDB compares Number keys by numeric
// value even for equality, so alternate textual representations of the same
// value ("1e2", "100", "100.00", "1.00e2") must produce byte-identical
// encodings — otherwise two writes with the same logical key would land in
// different storage slots.
func TestEncodeOrderableNumber_EqualRepresentationsCollide(t *testing.T) {
	groups := [][]string{
		{"100", "1e2", "100.00", "1.00e2", "0.1e3", "10E1"},
		{"5", "5.0", "5.00", "0.5e1", "50e-1"},
		{"-42", "-42.0", "-4.2e1", "-420e-1"},
		{"0.05", "5e-2", "0.050", "50e-3"},
	}
	for _, g := range groups {
		want, err := encodeOrderableNumber(g[0])
		if err != nil {
			t.Fatalf("encodeOrderableNumber(%q): %v", g[0], err)
		}
		for _, alt := range g[1:] {
			got, err := encodeOrderableNumber(alt)
			if err != nil {
				t.Fatalf("encodeOrderableNumber(%q): %v", alt, err)
			}
			if got != want {
				t.Errorf("encodeOrderableNumber(%q) = %q, want same as encodeOrderableNumber(%q) = %q", alt, got, g[0], want)
			}
		}
	}
}

// TestEncodeOrderableNumber_FixedWidth pins the "fixed-width" invariant the
// task calls out explicitly: every valid encoding is exactly
// 1+numExpWidth+numMantissaWidth characters, regardless of magnitude, sign,
// or digit count.
func TestEncodeOrderableNumber_FixedWidth(t *testing.T) {
	wantLen := 1 + numExpWidth + numMantissaWidth
	inputs := []string{
		"0", "-0", "1", "-1",
		"12345678901234567890123456789012345678", // 38 significant digits
		"-12345678901234567890123456789012345678",
		"1e125", "-1e125", "1e-130", "-1e-130", "0.0000001", "-100000000",
	}
	for _, raw := range inputs {
		enc, err := encodeOrderableNumber(raw)
		if err != nil {
			t.Fatalf("encodeOrderableNumber(%q): unexpected error: %v", raw, err)
		}
		if len(enc) != wantLen {
			t.Errorf("encodeOrderableNumber(%q) length = %d, want %d (value %q)", raw, len(enc), wantLen, enc)
		}
	}
}

// TestEncodeOrderableNumber_RejectsNonNumeric confirms garbage input errors
// out instead of silently encoding something.
func TestEncodeOrderableNumber_RejectsNonNumeric(t *testing.T) {
	bad := []string{"", "abc", "1.2.3", "--5", "1e", "e5", "1,000", "NaN", "Infinity", "0x10", " ", "1 2"}
	for _, s := range bad {
		if _, err := encodeOrderableNumber(s); err == nil {
			t.Errorf("encodeOrderableNumber(%q): expected an error, got none", s)
		}
	}
}

// TestEncodeOrderableNumber_PropertyOrderingMatchesBigRat generates a mix of
// random and structured decimal fixtures and checks, for every pair, that
// string comparison of the encoded forms agrees in sign with big.Rat
// comparison of the exact decimal values — the core correctness property
// from dynamodb-gsi-design.md §6.
func TestEncodeOrderableNumber_PropertyOrderingMatchesBigRat(t *testing.T) {
	rng := rand.New(rand.NewSource(20260725))
	fixtures := []string{"0", "-0"}

	for i := 0; i < 200; i++ {
		fixtures = append(fixtures, randomDecimal(rng))
	}
	// Structured fixtures around common trap boundaries.
	for _, base := range []int{0, 1, 5, 9, 10, 49, 50, 99, 100, 999, 1000} {
		fixtures = append(fixtures,
			fmt.Sprintf("%d", base),
			fmt.Sprintf("-%d", base),
			fmt.Sprintf("%d.5", base),
			fmt.Sprintf("-%d.5", base),
			fmt.Sprintf("0.%d", base),
			fmt.Sprintf("-0.%d", base),
		)
	}

	type fixture struct {
		raw string
		enc string
		rat *big.Rat
	}
	var parsed []fixture
	for _, raw := range fixtures {
		enc, err := encodeOrderableNumber(raw)
		if err != nil {
			t.Fatalf("encodeOrderableNumber(%q): unexpected error: %v", raw, err)
		}
		if len(enc) != 1+numExpWidth+numMantissaWidth {
			t.Fatalf("encodeOrderableNumber(%q) produced non-fixed-width result %q (len %d)", raw, enc, len(enc))
		}
		parsed = append(parsed, fixture{raw: raw, enc: enc, rat: decimalToRat(t, raw)})
	}

	checked := 0
	for i := range parsed {
		for j := range parsed {
			if i == j {
				continue
			}
			a, b := parsed[i], parsed[j]
			wantSign := sign3(a.rat.Cmp(b.rat))
			gotSign := sign3(strings.Compare(a.enc, b.enc))
			if wantSign != gotSign {
				t.Fatalf("ordering mismatch: %q (rat %s) vs %q (rat %s): big.Rat sign=%d, encoded string sign=%d\n  encode(%q)=%q\n  encode(%q)=%q",
					a.raw, a.rat.RatString(), b.raw, b.rat.RatString(), wantSign, gotSign, a.raw, a.enc, b.raw, b.enc)
			}
			checked++
		}
	}
	t.Logf("checked %d ordered pairs across %d fixtures", checked, len(parsed))
}

// randomDecimal generates a syntactically valid, randomly shaped decimal
// Number string: random sign, digit count, optional fraction, optional
// exponent — staying within DynamoDB's documented ~38-significant-digit /
// ~-130..126-exponent bounds so every generated fixture is expected to
// encode successfully.
func randomDecimal(rng *rand.Rand) string {
	var b strings.Builder
	if rng.Intn(2) == 0 {
		b.WriteByte('-')
	}
	intDigits := 1 + rng.Intn(10)
	for i := 0; i < intDigits; i++ {
		if i == 0 {
			b.WriteByte(byte('1' + rng.Intn(9))) // no leading zero in the generator itself
		} else {
			b.WriteByte(byte('0' + rng.Intn(10)))
		}
	}
	if rng.Intn(2) == 0 {
		b.WriteByte('.')
		fracDigits := 1 + rng.Intn(10)
		for i := 0; i < fracDigits; i++ {
			b.WriteByte(byte('0' + rng.Intn(10)))
		}
	}
	if rng.Intn(3) == 0 {
		exp := rng.Intn(60) - 30 // stay well inside the supported exponent range
		b.WriteString(fmt.Sprintf("e%+d", exp))
	}
	return b.String()
}
