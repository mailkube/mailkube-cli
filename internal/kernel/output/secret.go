package output

// secretHead is how many leading characters of a secret are shown.
const secretHead = 6

// secretTail is how many trailing characters of a secret are shown.
const secretTail = 4

// Secret renders a credential so it can be recognised but not used.
//
// Both ends are shown, not just the prefix. Every key issued by one platform shares its prefix,
// so a prefix-only form renders two different keys identically — and telling two keys apart is
// the entire reason this value is printed at all.
//
// A value too short to show both ends without revealing most of it is masked completely. That
// case is not a real key, so the loss of recognisability costs nothing, and guessing at a
// shorter split would leak more of a short secret than of a long one.
func Secret(s string, ellipsis string) string {
	runes := []rune(s)
	if len(runes) == 0 {
		return ""
	}
	if len(runes) <= secretHead+secretTail {
		return maskAll(len(runes))
	}
	return string(runes[:secretHead]) + ellipsis + string(runes[len(runes)-secretTail:])
}

// maskAll replaces every character, preserving only the length.
func maskAll(n int) string {
	mask := make([]rune, n)
	for i := range mask {
		mask[i] = '*'
	}
	return string(mask)
}
