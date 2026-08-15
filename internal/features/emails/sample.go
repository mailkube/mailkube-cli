package emails

import (
	"math/rand/v2"
	"strings"
)

// SampleSubject is the subject a generated message carries when none was given.
const SampleSubject = "Mailkube sample message"

// defaultSampleLink and defaultSampleImage are the URLs a generated body carries by default.
//
// Both are on a domain reserved by RFC 2606, so a sample message never points a recipient at
// something real and never depends on a host anyone has to keep serving. Pass --link and --image
// to use your own.
const (
	defaultSampleLink  = "https://example.com/sample-link"
	defaultSampleImage = "https://example.com/sample-image.png"
)

// Sample is a generated message body, in both forms.
type Sample struct {
	// HTML is the rich body.
	HTML string
	// Text is the plain-text alternative, carrying the same words and the same URLs.
	Text string
}

// GenerateSample builds a body worth sending as a test.
//
// It carries links and images on purpose rather than being plain lorem text: a test send whose
// body is three words does not exercise link reputation, image proxying or click tracking, so it
// passes while the message a user actually cares about is still untested. The consequence is
// worth stating where a user will read it, and `topic testing` does: those links are scanned
// against whatever domain you send from.
//
// The generator is seeded by the caller so a run is reproducible: the same seed produces the same
// message, which is what lets a golden file pin this at all.
func GenerateSample(seed uint64, links, images []string) Sample {
	rnd := rand.New(rand.NewPCG(seed, seed>>1))

	if len(links) == 0 {
		links = []string{defaultSampleLink}
	}
	if len(images) == 0 {
		images = []string{defaultSampleImage}
	}

	paragraphs := []string{
		sentences(rnd, 3),
		sentences(rnd, 2),
	}
	return Sample{
		HTML: sampleHTML(paragraphs, links, images),
		Text: sampleText(paragraphs, links, images),
	}
}

// sampleHTML assembles the rich form.
func sampleHTML(paragraphs, links, images []string) string {
	var b strings.Builder
	b.WriteString("<html><body>\n")
	b.WriteString("<h1>" + SampleSubject + "</h1>\n")

	for _, p := range paragraphs {
		b.WriteString("<p>" + p + "</p>\n")
	}
	for i, link := range links {
		b.WriteString(`<p><a href="` + link + `">Sample link ` + ordinal(i) + "</a></p>\n")
	}
	for i, image := range images {
		b.WriteString(`<p><img src="` + image + `" alt="Sample image ` + ordinal(i) + `"></p>` + "\n")
	}

	b.WriteString("</body></html>")
	return b.String()
}

// sampleText assembles the plain-text alternative.
//
// The URLs appear here too. A text part that quietly dropped them would make the two parts
// different messages, and a recipient reading the text one would be looking at something the
// sender never previewed.
func sampleText(paragraphs, links, images []string) string {
	parts := append([]string{SampleSubject, ""}, paragraphs...)
	parts = append(parts, "")
	parts = append(parts, links...)
	parts = append(parts, images...)
	return strings.Join(parts, "\n")
}

// sentences builds one paragraph of n sentences.
func sentences(rnd *rand.Rand, n int) string {
	built := make([]string, 0, n)
	for range n {
		built = append(built, sentence(rnd))
	}
	return strings.Join(built, " ")
}

// sentence builds one capitalised, full-stopped sentence of between six and fourteen words.
func sentence(rnd *rand.Rand) string {
	vocabulary := words()
	count := 6 + rnd.IntN(9)

	built := make([]string, 0, count)
	for range count {
		built = append(built, vocabulary[rnd.IntN(len(vocabulary))])
	}

	first := built[0]
	built[0] = strings.ToUpper(first[:1]) + first[1:]
	return strings.Join(built, " ") + "."
}

// ordinal numbers the generated links and images from one, for their visible labels.
func ordinal(index int) string {
	const digits = "123456789"
	if index < len(digits) {
		return digits[index : index+1]
	}
	return "n"
}

// words is the vocabulary a generated body is assembled from.
func words() []string {
	return []string{
		"message", "delivery", "sender", "recipient", "subject", "content", "header",
		"template", "schedule", "webhook", "signature", "transport", "campaign", "topic",
		"reputation", "throughput", "bounce", "complaint", "attachment", "encoding",
	}
}
