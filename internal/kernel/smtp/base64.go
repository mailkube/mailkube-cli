package smtp

import (
	"bytes"
	"encoding/base64"
)

// base64LineLength is the line length RFC 2045 sets for base64 body parts.
//
// 76 characters, which is not a style preference: a line longer than 998 octets is illegal, and
// several relays fold or reject long lines rather than passing them through. Folding here means
// the message that arrives is the message that was built.
const base64LineLength = 76

// wrapBase64 encodes bytes and folds them to the line length a body part may carry.
func wrapBase64(content []byte) []byte {
	encoded := base64.StdEncoding.EncodeToString(content)

	var out bytes.Buffer
	for start := 0; start < len(encoded); start += base64LineLength {
		end := min(start+base64LineLength, len(encoded))
		out.WriteString(encoded[start:end])
		out.WriteString("\r\n")
	}
	return out.Bytes()
}
