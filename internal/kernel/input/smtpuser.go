package input

import (
	"strings"

	"github.com/mailkube/mailkube-cli/internal/kernel/errs"
)

// ValidateSMTPUsername checks the one credential shape that must never reach the wire malformed.
//
// The SMTP principal is localpart@verified-domain, and a bare name is the mistake people make
// because it is what most other systems accept. Repeated malformed sign-ins may get an IP
// blocked, so this is checked here, locally, before anything opens a socket — which is also why
// it lives beside the config keys rather than inside the command that dials.
func ValidateSMTPUsername(v string) error {
	local, domain, found := strings.Cut(v, "@")
	if !found || local == "" || domain == "" || strings.Contains(domain, "@") ||
		!strings.Contains(domain, ".") {
		return errs.Validationf(
			"Invalid SMTP username %q\n"+
				"The username must be localpart@verified-domain, for example myapp01@acme.com.\n"+
				"Repeated malformed sign-ins may get your IP blocked. Not attempting to connect.",
			v)
	}
	return nil
}
