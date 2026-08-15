package input

import (
	"strings"

	"github.com/mailkube/mailkube-cli/internal/kernel/errs"
)

// Pair is one key/value a repeatable flag collected, in the order it was given.
type Pair struct {
	// Key is the text before the separator, trimmed.
	Key string
	// Value is everything after the first separator, trimmed.
	Value string
}

// PairFlag collects repeatable key/value flags: --tag, --var and --header.
//
// It implements pflag.Value, so the flag package does the collecting and every such flag on every
// command splits its input identically. Three flags with three hand-rolled splits is three
// slightly different answers to "what if the value contains the separator".
//
// Validation of the keys and values themselves is not done here. What makes a valid tag name is a
// property of tags, not of the syntax "a=b", and putting it here would mean this package knowing
// the rules for every flag that uses it.
type PairFlag struct {
	// Separator divides key from value. Header names cannot contain a colon and tag names
	// cannot contain an equals sign, so in both cases splitting on the first one is exact.
	Separator string
	// Noun names what is being collected, for error messages: "tag", "variable", "header".
	Noun string
	// Example is a well-formed value shown when parsing fails.
	Example string

	pairs []Pair
}

// Set parses one occurrence of the flag. It satisfies pflag.Value.
func (f *PairFlag) Set(raw string) error {
	key, value, found := strings.Cut(raw, f.Separator)
	if !found {
		return errs.Usagef("%s %q has no %q: write it as %s", f.Noun, raw, f.Separator, f.Example)
	}

	key = strings.TrimSpace(key)
	if key == "" {
		return errs.Usagef("%s %q has an empty name: write it as %s", f.Noun, raw, f.Example)
	}

	// Only the first separator splits, so a value containing another one survives intact. A
	// header value is free to contain a colon, and a template variable is free to contain an
	// equals sign, and neither should have to be escaped to say so.
	f.pairs = append(f.pairs, Pair{Key: key, Value: strings.TrimSpace(value)})
	return nil
}

// String renders the collected pairs, as pflag requires for help output.
func (f *PairFlag) String() string {
	parts := make([]string, 0, len(f.pairs))
	for _, p := range f.pairs {
		parts = append(parts, p.Key+f.Separator+p.Value)
	}
	return strings.Join(parts, ",")
}

// Type names the flag's value in help output, as pflag requires.
func (f *PairFlag) Type() string { return f.Noun }

// Pairs returns what the flag collected, in the order it was given.
//
// Order is preserved rather than sorted into a map, because a repeated key is a question this
// package cannot answer: repeating a header is meaningful, and repeating a tag name is an error.
// Whoever owns the flag decides which.
func (f *PairFlag) Pairs() []Pair { return f.pairs }

// NewTagFlag returns the flag behind --tag: name=value.
func NewTagFlag() *PairFlag {
	return &PairFlag{Separator: "=", Noun: "tag", Example: "--tag campaign=launch"}
}

// NewVarFlag returns the flag behind --var: key=value.
func NewVarFlag() *PairFlag {
	return &PairFlag{Separator: "=", Noun: "variable", Example: "--var first_name=Alice"}
}

// NewHeaderFlag returns the flag behind --header: 'Name: value'.
func NewHeaderFlag() *PairFlag {
	return &PairFlag{Separator: ":", Noun: "header", Example: `--header 'X-Campaign: launch'`}
}
