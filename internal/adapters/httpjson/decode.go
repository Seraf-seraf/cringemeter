package httpjson

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
)

// DecodeObject rejects duplicate envelope keys and multiple JSON values, while
// raw Telegram message fields remain compatible with future Bot API additions.
func DecodeObject(reader io.Reader, target any) error {
	dec := json.NewDecoder(reader)
	token, err := dec.Token()
	if err != nil {
		return err
	}
	if token != json.Delim('{') {
		return errors.New("expected object")
	}
	fields := make(map[string]json.RawMessage)
	for dec.More() {
		token, err = dec.Token()
		if err != nil {
			return err
		}
		key, ok := token.(string)
		if !ok {
			return errors.New("expected key")
		}
		if _, exists := fields[key]; exists {
			return errors.New("duplicate key")
		}
		var value json.RawMessage
		if err := dec.Decode(&value); err != nil {
			return err
		}
		fields[key] = value
	}
	if token, err = dec.Token(); err != nil {
		return err
	} else if token != json.Delim('}') {
		return errors.New("expected object end")
	}
	var trailing any
	if err := dec.Decode(&trailing); err != io.EOF {
		if err != nil {
			return err
		}
		return errors.New("trailing JSON")
	}
	data, err := json.Marshal(fields)
	if err != nil {
		return err
	}
	strict := json.NewDecoder(bytes.NewReader(data))
	strict.DisallowUnknownFields()
	return strict.Decode(target)
}
