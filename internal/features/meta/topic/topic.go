// Package topic implements `mailkube topic`: the long-form explanations that belong to no one
// command.
//
// Some things a user needs to know are properties of the whole tool rather than of any verb:
// what the exit codes mean, why delivery outcomes arrive as events, that every send is charged.
// Attaching those to a command's --help would either repeat them on every command or hide them
// on one, so they live here, and commands point at them by name.
package topic

import (
	"embed"
	"io/fs"
	"path"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mailkube/mailkube-cli/internal/kernel/errs"
	"github.com/mailkube/mailkube-cli/internal/kernel/feature"
	"github.com/mailkube/mailkube-cli/internal/kernel/output"
)

// topics is the help text, compiled into the binary.
//
// Embedded rather than shipped alongside, because a binary installed with `go install` has no
// alongside: it is one file on a path, and help that only existed in a release archive would be
// missing from a channel people actually use.
//
//go:embed topics/*.md
var topics embed.FS

// topicDir is where the embedded files live inside the binary.
const topicDir = "topics"

// Feature serves the long-form help.
type Feature struct{}

// New returns the topic feature.
func New() *Feature { return &Feature{} }

// Name implements feature.Feature.
func (*Feature) Name() string { return "topic" }

// HelpEntries implements feature.Listed.
func (*Feature) HelpEntries() []feature.Entry {
	return []feature.Entry{{
		Group:      feature.GroupSetup,
		Invocation: "topic",
		Summary:    "Long-form help topics — run `mailkube topic` to list them",
	}}
}

// Command implements feature.Feature.
func (f *Feature) Command(deps *feature.Deps) *cobra.Command {
	return &cobra.Command{
		Use:   "topic [name]",
		Short: "Read a long-form help topic",
		Args:  cobra.MaximumNArgs(1),
		// Completion over the topic names, because the whole point of the listing is that a
		// user does not know what to type, and a shell that can finish it for them is better
		// than one that makes them run the listing first.
		ValidArgsFunction: func(_ *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
			return names(), cobra.ShellCompDirectiveNoFileComp
		},
		RunE: func(_ *cobra.Command, args []string) error {
			if len(args) == 0 {
				return deps.Emit(listView())
			}
			view, err := read(args[0])
			if err != nil {
				return err
			}
			return deps.Emit(view)
		},
	}
}

// ListView is the set of available topics.
type ListView struct {
	// Topics are the topics, each with its first line as a summary.
	Topics []Summary `json:"topics"`
}

// Summary is one topic in the listing.
type Summary struct {
	// Name is what to pass to `mailkube topic`.
	Name string `json:"name"`
	// Title is the topic's first line.
	Title string `json:"title"`
}

// RenderText implements output.TextRenderer.
func (v ListView) RenderText(_ output.Caps) []string {
	table := output.Table{}
	for _, t := range v.Topics {
		table.Rows = append(table.Rows, []string{"  " + t.Name, t.Title})
	}
	return append([]string{"Available topics:"}, table.Lines()...)
}

// Content is one topic's text.
type Content struct {
	// Name is the topic's name.
	Name string `json:"name"`
	// Body is the whole text.
	Body string `json:"body"`
}

// RenderText implements output.TextRenderer.
func (v Content) RenderText(_ output.Caps) []string {
	return strings.Split(strings.TrimRight(v.Body, "\n"), "\n")
}

// listView builds the listing, with each topic's own first line as its summary.
//
// Deriving the summary from the file rather than repeating it in a table here is what stops the
// two drifting: a topic whose subject changes cannot keep an old description in the listing.
func listView() ListView {
	view := ListView{}
	for _, name := range names() {
		content, err := read(name)
		if err != nil {
			continue
		}
		view.Topics = append(view.Topics, Summary{Name: name, Title: firstLine(content.Body)})
	}
	return view
}

// names is every embedded topic, sorted, so the listing is stable across builds.
func names() []string {
	entries, err := fs.ReadDir(topics, topicDir)
	if err != nil {
		return nil
	}

	var found []string
	for _, e := range entries {
		found = append(found, strings.TrimSuffix(e.Name(), ".md"))
	}
	sort.Strings(found)
	return found
}

// read returns one topic, or names the ones that exist.
func read(name string) (Content, error) {
	body, err := topics.ReadFile(path.Join(topicDir, name+".md"))
	if err != nil {
		return Content{}, errs.Usagef("no topic named %q\nAvailable topics: %s.",
			name, strings.Join(names(), ", "))
	}
	return Content{Name: name, Body: string(body)}, nil
}

// firstLine is a topic's title.
func firstLine(body string) string {
	line, _, _ := strings.Cut(body, "\n")
	return strings.TrimSpace(line)
}
