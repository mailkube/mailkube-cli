package emails_test

import (
	"strings"
	"testing"

	"github.com/mailkube/mailkube-cli/internal/features/emails"
)

func TestTheSameSeedGeneratesTheSameMessage(t *testing.T) {
	t.Parallel()

	// Reproducibility is what makes this testable at all, and it is why the seed is the
	// caller's rather than the package's: a generator that read the wall clock itself could
	// never be pinned by a golden file.
	first := emails.GenerateSample(42, nil, nil)
	second := emails.GenerateSample(42, nil, nil)

	if first != second {
		t.Error("the same seed produced two different messages")
	}
	if third := emails.GenerateSample(43, nil, nil); third == first {
		t.Error("two seeds produced the same message, so consecutive sample sends are identical")
	}
}

func TestAGeneratedMessageCarriesLinksAndImagesInBothParts(t *testing.T) {
	t.Parallel()

	// The links are the point rather than decoration: a test body of three plain words does
	// not exercise link reputation or image handling, so it passes while the message anyone
	// actually cares about stays untested.
	sample := emails.GenerateSample(1, nil, nil)

	for _, part := range []struct {
		name    string
		content string
	}{{"html", sample.HTML}, {"text", sample.Text}} {
		if !strings.Contains(part.content, "https://") {
			t.Errorf("the %s part carries no link:\n%s", part.name, part.content)
		}
	}
	if !strings.Contains(sample.HTML, "<img") {
		t.Error("the html part carries no image")
	}
	// A text part that quietly dropped the URLs would make the two parts different messages,
	// and a recipient reading the text one would see something the sender never previewed.
	if !strings.Contains(sample.Text, "sample-image.png") {
		t.Error("the image url is missing from the text part")
	}
}

func TestSuppliedLinksAndImagesReplaceTheDefaults(t *testing.T) {
	t.Parallel()

	sample := emails.GenerateSample(7,
		[]string{"https://acme.com/a", "https://acme.com/b"},
		[]string{"https://acme.com/logo.png"})

	for _, want := range []string{"https://acme.com/a", "https://acme.com/b", "https://acme.com/logo.png"} {
		if !strings.Contains(sample.HTML, want) {
			t.Errorf("the html part is missing %q", want)
		}
	}
	if strings.Contains(sample.HTML, "example.com") {
		t.Error("the defaults were kept alongside the supplied urls")
	}
}
