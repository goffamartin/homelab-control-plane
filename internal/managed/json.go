package managed

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"unicode/utf8"
)

var ErrInput = errors.New("invalid managed request")

// Reject duplicate keys before typed decoding; bounded nesting prevents parser
// recursion abuse. Unknown struct fields and trailing documents are refused.
func DecodeJSON(data []byte, value any, limit int) error {
	if len(data) == 0 || len(data) > limit || !utf8.Valid(data) {
		return ErrInput
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	if scanJSON(d, 0) != nil {
		return ErrInput
	}
	if _, err := d.Token(); err != io.EOF {
		return ErrInput
	}
	d = json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if d.Decode(value) != nil || d.Decode(new(any)) != io.EOF {
		return ErrInput
	}
	return nil
}
func scanJSON(d *json.Decoder, depth int) error {
	if depth > 12 {
		return ErrInput
	}
	t, err := d.Token()
	if err != nil {
		return err
	}
	delim, ok := t.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		keys := map[string]bool{}
		for d.More() {
			key, err := d.Token()
			s, ok := key.(string)
			if err != nil || !ok || keys[s] || s != strings.ToLower(s) {
				return ErrInput
			}
			keys[s] = true
			if scanJSON(d, depth+1) != nil {
				return ErrInput
			}
		}
	case '[':
		for d.More() {
			if scanJSON(d, depth+1) != nil {
				return ErrInput
			}
		}
	default:
		return ErrInput
	}
	_, err = d.Token()
	return err
}
