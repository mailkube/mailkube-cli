package routes_test

import (
	"strings"
	"testing"

	"github.com/mailkube/mailkube-cli/internal/kernel/routes"
)

func TestEveryAreaCarriesAnAddressAndAnExplanation(t *testing.T) {
	t.Parallel()

	areas := routes.Areas()
	if len(areas) == 0 {
		t.Fatal("no areas are listed, so every surface quoting this table says nothing")
	}

	seen := map[string]bool{}
	for _, area := range areas {
		if area.Name == "" || area.Summary == "" || area.Path == "" {
			t.Errorf("incomplete area: %+v", area)
		}
		if seen[area.Name] {
			t.Errorf("area %q is listed twice", area.Name)
		}
		seen[area.Name] = true

		// A path is what makes this table worth having: it is quoted by the referral
		// handler, the README, the docs and the agent skill, and a relative one would
		// resolve differently in each.
		if !strings.HasPrefix(area.URL(), "https://") {
			t.Errorf("area %q has no absolute address: %q", area.Name, area.URL())
		}
	}
}

func TestTheWordsPeopleReachForResolve(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"domains":  "domains",
		"domain":   "domains",
		"contacts": "audience",
		"segments": "audience",
		"topics":   "audience",
		"API-KEYS": "api-keys",
		"  logs  ": "logs",
	}

	for query, want := range tests {
		area, ok := routes.AreaFor(query)
		if !ok {
			t.Errorf("%q resolved to nothing", query)
			continue
		}
		if area.Name != want {
			t.Errorf("%q resolved to %q, want %q", query, area.Name, want)
		}
	}

	if _, ok := routes.AreaFor("definitely-not-an-area"); ok {
		t.Error("an unknown name resolved to an area")
	}
}

func TestAnAnchorKeepsItsShape(t *testing.T) {
	t.Parallel()

	// The credentials pages differ only by fragment, so a helper that "helpfully" inserted a
	// slash before the anchor would collapse two areas into one wrong address.
	area, ok := routes.AreaFor("api-keys")
	if !ok {
		t.Fatal("api-keys resolved to nothing")
	}
	if !strings.HasSuffix(area.URL(), "/domain/credentials#api") {
		t.Errorf("URL = %q, want the anchor intact", area.URL())
	}
}

func TestOnlyTwoHostsAreEverNamed(t *testing.T) {
	t.Parallel()

	// Everything the CLI points a user at is one of these two. Keeping that true here is what
	// makes it checkable at all.
	if !strings.HasPrefix(routes.Docs("/cli"), "https://docs.mailkube.com") {
		t.Errorf("Docs = %q", routes.Docs("/cli"))
	}
	if !strings.HasPrefix(routes.Dashboard("/domain/setup"), "https://app.mailkube.com") {
		t.Errorf("Dashboard = %q", routes.Dashboard("/domain/setup"))
	}
	// Written without a leading slash, because a caller should not have to remember.
	if routes.Docs("cli") != routes.Docs("/cli") {
		t.Error("a path without a leading slash produced a different address")
	}
}
