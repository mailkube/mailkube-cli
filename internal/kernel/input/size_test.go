package input_test

import (
	"math"
	"strconv"
	"testing"

	"github.com/mailkube/mailkube-cli/internal/kernel/errs"
	"github.com/mailkube/mailkube-cli/internal/kernel/input"
)

func TestParseSizeReadsBothSpellingsOfTheSameLimit(t *testing.T) {
	t.Parallel()

	tests := []struct {
		value string
		want  int64
	}{
		{"1", 1},
		{"1048576", 1 << 20},
		{"512B", 512},
		{"64KiB", 64 << 10},
		{"1MiB", 1 << 20},
		{"2GiB", 2 << 30},
		{"1KB", 1000},
		{"2MB", 2_000_000},
		{"1GB", 1_000_000_000},
		{"8K", 8 << 10},
		{"4M", 4 << 20},
		// Case and surrounding space are a person's writing, not a different value.
		{"1mib", 1 << 20},
		{" 2 MiB ", 2 << 20},
	}

	for _, tt := range tests {
		t.Run(tt.value, func(t *testing.T) {
			t.Parallel()

			got, err := input.ParseSize(tt.value)
			if err != nil {
				t.Fatalf("ParseSize(%q): %v", tt.value, err)
			}
			if got != tt.want {
				t.Errorf("ParseSize(%q) = %d, want %d", tt.value, got, tt.want)
			}
		})
	}
}

func TestParseSizeRefusesAnythingThatIsNotACap(t *testing.T) {
	t.Parallel()

	// Zero and the negatives are refused rather than read as "no limit". A cap that can be
	// switched off by a typo is not a cap, and the value it guards here is a body this machine
	// will otherwise read into memory.
	for _, value := range []string{"", "   ", "lots", "MB", "1.5MB", "0", "0MiB", "-1", "-2MB", "1XB", "1 2 MB"} {
		t.Run(strconv.Quote(value), func(t *testing.T) {
			t.Parallel()

			if _, err := input.ParseSize(value); err == nil {
				t.Fatalf("ParseSize(%q) was accepted", value)
			} else if got := errs.CodeFor(err); got != errs.CodeUsage {
				t.Errorf("exit code = %d, want %d", got, errs.CodeUsage)
			}
		})
	}
}

func TestParseSizeRefusesAValueThatWouldOverflowItsUnit(t *testing.T) {
	t.Parallel()

	// Multiplying first and checking afterwards has already wrapped, and a negative limit read
	// as a cap accepts everything.
	value := strconv.FormatInt(math.MaxInt64/1000, 10) + "GB"

	got, err := input.ParseSize(value)
	if err == nil {
		t.Fatalf("ParseSize(%q) = %d, want a refusal", value, got)
	}
}
