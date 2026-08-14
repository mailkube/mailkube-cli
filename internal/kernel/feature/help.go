package feature

// The groups the root screen organises commands under.
//
// Three, because a first-time reader is asking one of three questions: how do I send something,
// how do I develop against this, and how do I set it up. A longer list would be a table of
// contents rather than an orientation.
const (
	// GroupSend is for commands that put mail on the wire.
	GroupSend = "SEND"
	// GroupDevelop is for the local development loop.
	GroupDevelop = "DEVELOP"
	// GroupSetup is for configuration, diagnosis and help.
	GroupSetup = "SETUP"
)

// Entry is one line on the root screen.
type Entry struct {
	// Group is which heading the line appears under.
	Group string
	// Invocation is what the user would type, which is not always the feature's own name: a
	// feature whose useful verb is a subcommand lists the whole invocation, because a reader
	// scanning for "how do I send mail" is looking for `emails send`, not for `emails`.
	Invocation string
	// Summary is the one-line description beside it.
	Summary string
}

// Listed is implemented by a feature that wants to choose how it appears on the root screen.
//
// A feature that does not implement it still appears, as its command name and short description.
// The interface exists for the features whose entry point is a subcommand, and for the ones that
// deserve more than one line.
type Listed interface {
	// HelpEntries are this feature's lines on the root screen.
	HelpEntries() []Entry
}
