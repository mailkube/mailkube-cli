package input

import (
	"math"
	"strconv"
	"strings"

	"github.com/mailkube/mailkube-cli/internal/kernel/errs"
)

// sizeUnit is one accepted suffix and what it multiplies by.
type sizeUnit struct {
	suffix string
	scale  int64
}

// sizeUnits are the suffixes a size may carry, longest first.
//
// Order is load-bearing rather than cosmetic: "MiB" has to be tested before "MB", and both
// before "B", or the shorter suffix matches first and the remainder fails to parse as a number.
// Both the binary and decimal spellings are accepted because a person writing a limit means
// "about a megabyte" and should not have to know which one this program prefers.
func sizeUnits() []sizeUnit {
	return []sizeUnit{
		{"KIB", 1 << 10},
		{"MIB", 1 << 20},
		{"GIB", 1 << 30},
		{"KB", 1000},
		{"MB", 1000 * 1000},
		{"GB", 1000 * 1000 * 1000},
		{"K", 1 << 10},
		{"M", 1 << 20},
		{"G", 1 << 30},
		{"B", 1},
	}
}

// ParseSize reads a byte count that may carry a unit suffix.
//
// A limit is written by a human and read by a machine, so both spellings work: 1048576 and 1MiB
// are the same value. A size is always positive, so zero and negative values are refused rather
// than quietly meaning "no limit" — a cap that can be switched off by a typo is not a cap.
func ParseSize(value string) (int64, error) {
	trimmed := strings.ToUpper(strings.TrimSpace(value))
	if trimmed == "" {
		return 0, unusableSize(value)
	}

	for _, unit := range sizeUnits() {
		if !strings.HasSuffix(trimmed, unit.suffix) {
			continue
		}
		digits := strings.TrimSpace(strings.TrimSuffix(trimmed, unit.suffix))
		return scaleSize(digits, unit.scale, value)
	}
	return scaleSize(trimmed, 1, value)
}

// scaleSize turns the digits and the unit into a byte count, refusing anything unusable.
func scaleSize(digits string, scale int64, original string) (int64, error) {
	count, err := strconv.ParseInt(digits, 10, 64)
	if err != nil || count <= 0 {
		return 0, unusableSize(original)
	}
	// Multiplying first and checking afterwards would already have wrapped, and a negative
	// limit read as a cap accepts everything.
	if count > math.MaxInt64/scale {
		return 0, errs.Usagef("%q is larger than this program can represent as a size", original)
	}
	return count * scale, nil
}

// unusableSize is the one message every rejection here produces.
func unusableSize(value string) error {
	return errs.Usagef(
		"%q is not a usable size: give a positive byte count, optionally with a unit, as in 512KiB or 2MB",
		value)
}
