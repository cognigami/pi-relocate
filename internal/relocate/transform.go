package relocate

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

// Transform rewrites only supported operational location fields in session JSONL.
// moved controls header cwd changes, not inbound reference or launch cwd changes.
// updates counts changed fields, rather than changed lines. Unchanged input is
// returned directly; on error no partial output or update count is returned.
func Transform(data []byte, source, destination string, mapping map[string]string, moved bool) (output []byte, updates int, err error) {
	var rewritten []byte
	for start, lineNumber := 0, 1; start < len(data); lineNumber++ {
		end := len(data)
		if i := bytes.IndexByte(data[start:], '\n'); i >= 0 {
			end = start + i + 1
		}
		contentEnd := end
		if contentEnd > start && data[contentEnd-1] == '\n' {
			contentEnd--
		}
		if contentEnd > start && data[contentEnd-1] == '\r' {
			contentEnd--
		}
		line := data[start:contentEnd]
		changed, count, lineErr := transformLine(line, source, destination, mapping, moved)
		if lineErr != nil && !moved && !transformHasReferenceMetadata(line) {
			// External files may contain unrelated malformed historical data.
			// Refuse ambiguous supported references, not every broken record in
			// storage merely because the planner inspected its file.
			lineErr = nil
		}
		if lineErr != nil {
			return nil, 0, fmt.Errorf("line %d: %w", lineNumber, lineErr)
		}
		if count != 0 {
			if rewritten == nil {
				rewritten = make([]byte, 0, len(data))
				rewritten = append(rewritten, data[:start]...)
			}
			rewritten = append(rewritten, changed...)
			rewritten = append(rewritten, data[contentEnd:end]...)
			updates += count
		} else if rewritten != nil {
			rewritten = append(rewritten, data[start:end]...)
		}
		start = end
	}
	if rewritten == nil {
		return data, 0, nil
	}
	return rewritten, updates, nil
}

func transformHasReferenceMetadata(line []byte) bool {
	if bytes.Contains(line, []byte("parentSession")) ||
		bytes.Contains(line, []byte("pi-sessions.subagent_launched")) ||
		bytes.Contains(line, []byte("pi-sessions.handoff-bootstrap")) {
		return true
	}
	// Also recognize JSON-escaped field names/custom types when the prefix can
	// be decoded, even if a later part of the external record is malformed.
	decoder := json.NewDecoder(bytes.NewReader(line))
	decoder.UseNumber()
	for {
		token, err := decoder.Token()
		if err != nil {
			return false
		}
		if token == "parentSession" || token == "parentSessionFile" ||
			token == "pi-sessions.subagent_launched" || token == "pi-sessions.handoff-bootstrap" {
			return true
		}
	}
}

func transformLine(line []byte, source, destination string, mapping map[string]string, moved bool) ([]byte, int, error) {
	if len(bytes.TrimSpace(line)) == 0 {
		return nil, 0, nil
	}
	record, err := transformObject(line)
	if err != nil {
		return nil, 0, err
	}
	kind, present, err := transformString(record, "type")
	if err != nil {
		return nil, 0, err
	}
	if !present {
		return nil, 0, fmt.Errorf("missing type")
	}
	updates := 0
	switch kind {
	case "session":
		cwd, exists, err := transformString(record, "cwd")
		if err != nil {
			return nil, 0, err
		}
		if moved && !exists {
			return nil, 0, fmt.Errorf("missing header cwd")
		}
		if moved && exists && cwd == source && cwd != destination {
			record["cwd"], _ = json.Marshal(destination)
			updates++
		}
		n, err := transformReference(record, "parentSession", mapping)
		if err != nil {
			return nil, 0, err
		}
		updates += n
	case "custom":
		customType, exists, err := transformString(record, "customType")
		if err != nil {
			return nil, 0, err
		}
		if !exists {
			return nil, 0, fmt.Errorf("missing customType")
		}
		var reference string
		switch customType {
		case "pi-sessions.subagent_launched":
			reference = "childSessionFile"
		case "pi-sessions.handoff-bootstrap":
			reference = "parentSessionFile"
		default:
			return nil, 0, nil
		}
		payload, err := transformObject(record["data"])
		if err != nil {
			return nil, 0, fmt.Errorf("%s data: %w", customType, err)
		}
		n, err := transformReference(payload, reference, mapping)
		if err != nil {
			return nil, 0, fmt.Errorf("%s data: %w", customType, err)
		}
		updates += n
		if customType == "pi-sessions.subagent_launched" {
			cwd, exists, err := transformString(payload, "cwd")
			if err != nil {
				return nil, 0, fmt.Errorf("%s data: %w", customType, err)
			}
			if exists && cwd == source && cwd != destination {
				payload["cwd"], _ = json.Marshal(destination)
				updates++
			}
		}
		if updates != 0 {
			record["data"], err = json.Marshal(payload)
			if err != nil {
				return nil, 0, err
			}
		}
	}
	if updates == 0 {
		return nil, 0, nil
	}
	changed, err := json.Marshal(record)
	return changed, updates, err
}

// Decode only the object being edited. RawMessage retains numeric literals and
// unknown nested values without converting numbers to float64. Duplicate keys
// are refused so rewriting cannot silently discard an ambiguous field.
func transformObject(raw []byte) (map[string]json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	token, err := decoder.Token()
	if err != nil {
		return nil, fmt.Errorf("expected JSON object: %w", err)
	}
	if token != json.Delim('{') {
		return nil, fmt.Errorf("expected JSON object")
	}
	fields := make(map[string]json.RawMessage)
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return nil, fmt.Errorf("invalid object key: %w", err)
		}
		key, ok := token.(string)
		if !ok {
			return nil, fmt.Errorf("expected object key")
		}
		if _, exists := fields[key]; exists {
			return nil, fmt.Errorf("duplicate field %q", key)
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return nil, fmt.Errorf("invalid field %q: %w", key, err)
		}
		fields[key] = value
	}
	if _, err := decoder.Token(); err != nil {
		return nil, fmt.Errorf("invalid object end: %w", err)
	}
	if _, err := decoder.Token(); err != io.EOF {
		return nil, fmt.Errorf("unexpected data after JSON object")
	}
	return fields, nil
}

// Missing optional fields are retained as absent, including in legacy records;
// present operational fields must be strings (JSON null is not a string).
func transformString(fields map[string]json.RawMessage, key string) (string, bool, error) {
	raw, exists := fields[key]
	if !exists {
		return "", false, nil
	}
	if raw = bytes.TrimSpace(raw); len(raw) == 0 || raw[0] != '"' {
		return "", true, fmt.Errorf("%s must be a string", key)
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", true, fmt.Errorf("invalid %s: %w", key, err)
	}
	return value, true, nil
}

func transformReference(fields map[string]json.RawMessage, key string, mapping map[string]string) (int, error) {
	value, exists, err := transformString(fields, key)
	if err != nil || !exists {
		return 0, err
	}
	if destination, found := mapping[value]; found && destination != value {
		fields[key], _ = json.Marshal(destination)
		return 1, nil
	}
	return 0, nil
}
