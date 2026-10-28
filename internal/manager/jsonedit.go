package manager

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// GetJSONString reads the string value at a dotted key path (metadata.version)
// of an object. It returns the decoded value; a missing key is ErrKey.
func GetJSONString(content []byte, keyPath string) (string, error) {
	start, end, err := findJSONString(content, keyPath)
	if err != nil {
		return "", err
	}
	var v string
	if err := json.Unmarshal(content[start:end], &v); err != nil {
		return "", fmt.Errorf("%s: %w", keyPath, err)
	}
	return v, nil
}

// SetJSONString replaces exactly the string value at a dotted key path. Every
// other byte — indentation, key order, line endings, a byte-order mark — is
// preserved. JSON is never marshalled.
func SetJSONString(content []byte, keyPath, value string) ([]byte, error) {
	start, end, err := findJSONString(content, keyPath)
	if err != nil {
		return nil, err
	}
	quoted, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	out := make([]byte, 0, len(content)-(end-start)+len(quoted))
	out = append(out, content[:start]...)
	out = append(out, quoted...)
	out = append(out, content[end:]...)
	return out, nil
}

// findJSONString returns the byte range, quotes included, of the string value
// at keyPath. The document is validated first, so the scanner below only has
// to find token boundaries, never report syntax errors.
func findJSONString(content []byte, keyPath string) (int, int, error) {
	body := bytes.TrimPrefix(content, []byte("\xef\xbb\xbf"))
	offset := len(content) - len(body)
	if !json.Valid(body) {
		return 0, 0, errors.New("not valid JSON")
	}
	s := &jsonScanner{data: body}
	keys := strings.Split(keyPath, ".")
	s.space()
	for i, key := range keys {
		if s.peek() != '{' {
			return 0, 0, fmt.Errorf("%s: %s is not an object", keyPath, strings.Join(keys[:i], "."))
		}
		if !s.findKey(key) {
			return 0, 0, fmt.Errorf("%s: %w", keyPath, ErrKey)
		}
	}
	if s.peek() != '"' {
		return 0, 0, fmt.Errorf("%s: value is not a string", keyPath)
	}
	start := s.pos
	s.skipString()
	return offset + start, offset + s.pos, nil
}

// jsonScanner walks a document already known to be valid JSON.
type jsonScanner struct {
	data []byte
	pos  int
}

func (s *jsonScanner) peek() byte {
	if s.pos >= len(s.data) {
		return 0
	}
	return s.data[s.pos]
}

func (s *jsonScanner) space() {
	for s.pos < len(s.data) {
		switch s.data[s.pos] {
		case ' ', '\t', '\r', '\n':
			s.pos++
		default:
			return
		}
	}
}

// findKey scans the object at pos for key and, when found, leaves pos at the
// start of its value. Duplicate keys resolve to the first, as the scan stops
// there.
func (s *jsonScanner) findKey(key string) bool {
	s.pos++ // {
	for {
		s.space()
		if s.peek() != '"' {
			return false // }
		}
		start := s.pos
		s.skipString()
		var name string
		_ = json.Unmarshal(s.data[start:s.pos], &name)
		s.space()
		s.pos++ // :
		s.space()
		if name == key {
			return true
		}
		s.skipValue()
		s.space()
		if s.peek() == ',' {
			s.pos++
		}
	}
}

func (s *jsonScanner) skipString() {
	s.pos++ // opening quote
	for s.pos < len(s.data) {
		switch s.data[s.pos] {
		case '\\':
			s.pos += 2
		case '"':
			s.pos++
			return
		default:
			s.pos++
		}
	}
}

func (s *jsonScanner) skipValue() {
	switch s.peek() {
	case '"':
		s.skipString()
	case '{', '[':
		depth := 0
		for s.pos < len(s.data) {
			switch s.data[s.pos] {
			case '"':
				s.skipString()
				continue
			case '{', '[':
				depth++
			case '}', ']':
				depth--
				if depth == 0 {
					s.pos++
					return
				}
			}
			s.pos++
		}
	default:
		// number, true, false, null: runs to the next delimiter
		for s.pos < len(s.data) {
			switch s.data[s.pos] {
			case ',', '}', ']', ' ', '\t', '\r', '\n':
				return
			}
			s.pos++
		}
	}
}
