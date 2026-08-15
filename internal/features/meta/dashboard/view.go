package dashboard

import (
	"github.com/mailkube/mailkube-cli/internal/kernel/output"
	"github.com/mailkube/mailkube-cli/internal/kernel/routes"
)

// AreaView is one part of the product and where it is managed.
type AreaView struct {
	// Name is what it is called.
	Name string `json:"name"`
	// Summary says what it covers.
	Summary string `json:"summary"`
	// URL is the page that owns it.
	URL string `json:"url"`
}

// View is the whole map.
type View struct {
	// Areas are the parts of the product managed outside this tool.
	Areas []AreaView `json:"areas"`
}

// view builds the map from the shared route table.
func view() View {
	areas := routes.Areas()
	rows := make([]AreaView, 0, len(areas))

	for _, area := range areas {
		rows = append(rows, AreaView{Name: area.Name, Summary: area.Summary, URL: area.URL()})
	}
	return View{Areas: rows}
}

// RenderText implements output.TextRenderer.
func (v View) RenderText(_ output.Caps) []string {
	table := output.Table{}
	for _, area := range v.Areas {
		table.Rows = append(table.Rows, []string{area.Name, area.Summary, area.URL})
	}

	lines := []string{"Managed in the dashboard:", ""}
	for _, line := range table.Lines() {
		lines = append(lines, "  "+line)
	}
	return append(lines, "",
		"The CLI covers sending, scheduled sends and the local webhook loop.")
}
