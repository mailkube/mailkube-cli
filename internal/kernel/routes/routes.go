// Package routes names the parts of the product that live outside this tool.
//
// The CLI is the send path and the development loop. Domain setup, credentials, templates,
// suppressions, audiences and webhook endpoints are managed in the dashboard, and several places
// have to say so: the handler that answers a command we deliberately do not have, the README, the
// documentation pages and the embedded agent skill.
//
// They say it from here. A wrong path shipped to four places at once lands a first-time user on a
// missing page at the exact moment they are looking for the thing they were just told about.
package routes

import "strings"

// dashboard and docs are the two hosts the CLI ever points a user at.
const (
	dashboard = "https://app.mailkube.com"
	docs      = "https://docs.mailkube.com"
)

// Dashboard returns an absolute dashboard URL for a path.
func Dashboard(path string) string { return dashboard + ensureLeadingSlash(path) }

// Docs returns an absolute documentation URL for a path.
func Docs(path string) string { return docs + ensureLeadingSlash(path) }

// Area is one part of the product the dashboard owns.
type Area struct {
	// Name is what someone would type at the CLI expecting to find it here.
	Name string
	// Path is where it actually lives.
	Path string
	// Summary says what it covers, in the words a user would recognise.
	Summary string
}

// URL is the absolute address of this area.
func (a Area) URL() string { return Dashboard(a.Path) }

// Areas is every part of the product managed outside this tool.
//
// The order is roughly the order a new user meets them: a domain first, then the credentials to
// send from it, then the things a send refers to.
func Areas() []Area {
	return []Area{
		{"domains", "/domain/setup", "Domain setup and DNS verification"},
		{"api-keys", "/domain/credentials#api", "API keys"},
		{"smtp-credentials", "/domain/credentials#smtp", "SMTP credentials"},
		{"webhooks", "/domain/webhooks", "Webhook endpoints and their signing secrets"},
		{"templates", "/domain/templates", "Saved templates"},
		{"suppressions", "/domain/suppressions", "Suppressed recipients"},
		{"audience", "/domain/audience", "Contacts, segments and topics"},
		{"logs", "/domain/logs", "Delivery history"},
	}
}

// AreaFor finds the area a name belongs to, including the names people reach for.
//
// The aliases are the point: someone typing `mailkube contacts` should land on the audience page
// rather than on an unknown-command error, and the whole value of this table is that it answers
// the word the user chose rather than the word we chose.
func AreaFor(name string) (Area, bool) {
	aliases := map[string]string{
		"domain": "domains", "api-key": "api-keys", "keys": "api-keys",
		"template": "templates", "suppression": "suppressions",
		"contacts": "audience", "contact": "audience",
		"segments": "audience", "segment": "audience",
		"topics": "audience", "audiences": "audience",
		"webhook": "webhooks", "log": "logs",
	}

	wanted := strings.ToLower(strings.TrimSpace(name))
	if canonical, ok := aliases[wanted]; ok {
		wanted = canonical
	}
	for _, area := range Areas() {
		if area.Name == wanted {
			return area, true
		}
	}
	return Area{}, false
}

// ensureLeadingSlash lets a caller write a path with or without one.
func ensureLeadingSlash(path string) string {
	if path == "" || strings.HasPrefix(path, "/") || strings.HasPrefix(path, "#") {
		return path
	}
	return "/" + path
}
