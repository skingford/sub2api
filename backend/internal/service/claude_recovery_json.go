package service

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// Different JSON parsers disagree on duplicate keys. Reject ambiguity before
// resolving a session or modifying identity fields, including nested objects.
func recoveryUniqueJSON(raw []byte) error {
	d := json.NewDecoder(strings.NewReader(string(raw)))
	d.UseNumber()
	depth := 0
	var value func() error
	value = func() error {
		depth++
		defer func() { depth-- }()
		if depth > 128 {
			return ErrRecoveryConflict
		}
		token, e := d.Token()
		if e != nil {
			return e
		}
		delimiter, ok := token.(json.Delim)
		if !ok {
			return nil
		}
		switch delimiter {
		case '{':
			seen := map[string]bool{}
			for d.More() {
				key, e := d.Token()
				if e != nil {
					return e
				}
				name, ok := key.(string)
				if !ok || seen[name] {
					return fmt.Errorf("ambiguous duplicate JSON key: %w", ErrRecoveryConflict)
				}
				seen[name] = true
				if e = value(); e != nil {
					return e
				}
			}
		case '[':
			for d.More() {
				if e = value(); e != nil {
					return e
				}
			}
		default:
			return ErrRecoveryConflict
		}
		_, e = d.Token()
		return e
	}
	if e := value(); e != nil {
		return e
	}
	if _, e := d.Token(); e != io.EOF {
		return ErrRecoveryConflict
	}
	return nil
}
