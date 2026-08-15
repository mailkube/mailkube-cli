package output

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"

	"gopkg.in/yaml.v3"
)

// jsonToNode converts a value to a YAML node tree by way of its JSON encoding.
//
// The obvious implementation — unmarshal into `any`, hand the result to the YAML encoder — is
// wrong in a way that only shows up on screen: a JSON object becomes a Go map, and a map has no
// order, so every field comes out alphabetical. A detail block reads id, status, then subject
// because that is the order they matter in, and `-o yaml` sorting them differently would make the
// two machine formats disagree about the shape of the same value.
//
// Decoding the token stream instead keeps the order the struct declared, so `-o yaml` is `-o json`
// with different punctuation, which is the only thing it should ever be.
func jsonToNode(v any) (*yaml.Node, error) {
	encoded, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}

	dec := json.NewDecoder(bytes.NewReader(encoded))
	// Numbers stay in the spelling JSON used. Decoded as float64 they would come back as
	// 1e+06, and a message count is not a floating-point value.
	dec.UseNumber()

	token, err := dec.Token()
	if err != nil {
		return nil, err
	}
	return decodeValue(dec, token)
}

// decodeValue builds the node for one JSON value, whose first token has already been read.
func decodeValue(dec *json.Decoder, token json.Token) (*yaml.Node, error) {
	if delim, ok := token.(json.Delim); ok {
		switch delim {
		case '{':
			return decodeMapping(dec)
		case '[':
			return decodeSequence(dec)
		default:
			return nil, errors.New("unbalanced json")
		}
	}
	return scalarNode(token), nil
}

// decodeMapping builds a mapping node, preserving the order the keys arrived in.
func decodeMapping(dec *json.Decoder) (*yaml.Node, error) {
	node := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}

	for {
		key, err := dec.Token()
		if err != nil {
			return nil, err
		}
		if isClose(key) {
			return node, nil
		}

		value, err := nextValue(dec)
		if err != nil {
			return nil, err
		}
		node.Content = append(node.Content, scalarNode(key), value)
	}
}

// decodeSequence builds a sequence node.
func decodeSequence(dec *json.Decoder) (*yaml.Node, error) {
	node := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}

	for {
		token, err := dec.Token()
		if err != nil {
			return nil, err
		}
		if isClose(token) {
			return node, nil
		}

		value, err := decodeValue(dec, token)
		if err != nil {
			return nil, err
		}
		node.Content = append(node.Content, value)
	}
}

// nextValue reads and builds the next value in the stream.
func nextValue(dec *json.Decoder) (*yaml.Node, error) {
	token, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if isClose(token) {
		return nil, io.ErrUnexpectedEOF
	}
	return decodeValue(dec, token)
}

// isClose reports whether a token ends the container being read.
func isClose(token json.Token) bool {
	delim, ok := token.(json.Delim)
	return ok && (delim == '}' || delim == ']')
}

// scalarNode builds the node for a JSON scalar, carrying its type across as a YAML tag.
func scalarNode(token json.Token) *yaml.Node {
	switch v := token.(type) {
	case nil:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!null", Value: "null"}
	case bool:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: boolString(v)}
	case json.Number:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: numberTag(v), Value: v.String()}
	case string:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: v}
	default:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: ""}
	}
}

// boolString renders a boolean in the spelling YAML expects.
func boolString(v bool) string {
	if v {
		return "true"
	}
	return "false"
}

// numberTag tells an integer from a float, so a count is not rendered with a decimal point.
func numberTag(n json.Number) string {
	if strings.ContainsAny(n.String(), ".eE") {
		return "!!float"
	}
	return "!!int"
}
