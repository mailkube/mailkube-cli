package smtp

import (
	"errors"
	"strings"
)

// ParsePort reads a submission port from its configured text form.
//
// It lives here rather than in the command that first needed it because a port and an encryption
// mode are part of this package's vocabulary, and two callers each parsing them their own way is
// how two commands come to disagree about what "587 " means.
//
// The error is a plain one: this package is below kernel/errs in the import graph, so the caller
// attaches the exit code, which is also the caller's to decide.
func ParsePort(value string) (int, error) {
	trimmed := strings.TrimSpace(value)

	port := 0
	for _, r := range trimmed {
		if r < '0' || r > '9' {
			return 0, errors.New(quote(value) + " is not a usable SMTP port")
		}
		port = port*10 + int(r-'0')
	}
	if port <= 0 || port > 65535 {
		return 0, errors.New(quote(value) + " is not a usable SMTP port")
	}
	return port, nil
}

// ParseTLSMode reads an encryption mode from its configured text form.
//
// There are two modes and no third: an unencrypted option would exist only to be found by someone
// debugging a handshake, and left on afterwards.
func ParseTLSMode(value string) (TLSMode, error) {
	switch TLSMode(strings.ToLower(strings.TrimSpace(value))) {
	case STARTTLS:
		return STARTTLS, nil
	case Implicit:
		return Implicit, nil
	default:
		return "", errors.New(quote(value) + " is not a usable TLS mode: use starttls or implicit")
	}
}

// quote renders a value the way the message reads best, without pulling in a formatter for it.
func quote(value string) string { return `"` + value + `"` }
