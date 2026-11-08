package office

import (
	"strconv"
	"strings"
)

// csvNumberExact compares the original decimal with the number cellXML would write.
// Comparing floats here would already have lost the digits CSV needs to preserve.
func csvNumberExact(src string, f float64) bool {
	digits, scale, negative, ok := csvDecimal(src)
	if !ok {
		return false
	}
	written, writtenScale, writtenNegative, ok := csvDecimal(strconv.FormatFloat(f, 'g', 15, 64))
	return ok && digits == written && scale == writtenScale && negative == writtenNegative
}

// csvDecimal represents a decimal as signed digits times 10^scale, ignoring
// insignificant zeros. Exponents are bounded by input length and the float64 range;
// even hostile exponent strings require only a scan, never a power-of-ten allocation.
func csvDecimal(src string) (digits string, scale int, negative, ok bool) {
	limit := len(src) + 400
	if len(src) > 0 && (src[0] == '-' || src[0] == '+') {
		negative = src[0] == '-'
		src = src[1:]
	}
	if i := strings.IndexAny(src, "eE"); i >= 0 {
		exponent, err := strconv.Atoi(src[i+1:])
		if err != nil || exponent < -limit || exponent > limit {
			return "", 0, false, false
		}
		scale = exponent
		src = src[:i]
	}
	if i := strings.IndexByte(src, '.'); i >= 0 {
		scale -= len(src) - i - 1
		src = src[:i] + src[i+1:]
	}
	if src == "" {
		return "", 0, false, false
	}
	for i := range len(src) {
		if src[i] < '0' || src[i] > '9' {
			return "", 0, false, false
		}
	}
	src = strings.TrimLeft(src, "0")
	if src == "" {
		return "0", 0, false, true
	}
	digits = strings.TrimRight(src, "0")
	scale += len(src) - len(digits)
	return digits, scale, negative, true
}
