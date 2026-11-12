package relocate

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
)

// These installed pi-sessions consumers are outside the agreed first-version
// rewrite scope. Report a concrete affected dependency rather than silently
// leaving an operational reference dangling. Historical subagent receipts are
// deliberately excluded: their operational source of truth is the launch ledger.
func unsupportedReference(data []byte, source string, mapping map[string]string) error {
	number := 0
	for line := range bytes.SplitSeq(data, []byte{'\n'}) {
		number++
		var entry struct {
			Type       string          `json:"type"`
			CustomType string          `json:"customType"`
			Data       json.RawMessage `json:"data"`
			Message    json.RawMessage `json:"message"`
		}
		if json.Unmarshal(line, &entry) != nil {
			continue
		}
		if entry.Type == "custom" && entry.CustomType == "pi-sessions.lineage" {
			var payload struct {
				Parent string `json:"parentSessionFile"`
			}
			if json.Unmarshal(entry.Data, &payload) == nil && mapping[payload.Parent] != "" {
				return fmt.Errorf("line %d: unsupported operational dependency pi-sessions.lineage.parentSessionFile targets a moving session", number)
			}
		}
		if entry.Type == "custom" && entry.CustomType == "pi-sessions.subagent_launched" {
			var payload struct {
				Child  string `json:"childSessionFile"`
				Cwd    string `json:"cwd"`
				Resume string `json:"resumeCommand"`
			}
			if json.Unmarshal(entry.Data, &payload) == nil && (payload.Cwd == source || mapping[payload.Child] != "") {
				dependent := containsShellPath(payload.Resume, source)
				for old := range mapping {
					if containsShellPath(payload.Resume, filepath.Dir(old)) {
						dependent = true
					}
				}
				if dependent {
					return fmt.Errorf("line %d: unsupported path-dependent subagent resumeCommand; shell commands are not rewritten", number)
				}
			}
		}
		if entry.Type == "message" {
			var message struct {
				Role    string `json:"role"`
				Tool    string `json:"toolName"`
				Details struct {
					Child  string `json:"childSessionFile"`
					Launch string `json:"launch"`
				} `json:"details"`
			}
			if json.Unmarshal(entry.Message, &message) != nil {
				continue
			}
			if message.Role == "toolResult" && (message.Tool == "session_handoff" || message.Tool == "subagent_launch") && message.Details.Launch != "" && message.Details.Launch != "subagent" && mapping[message.Details.Child] != "" {
				return fmt.Errorf("line %d: unsupported non-subagent handoff receipt targets a moving session (used by the handoff board)", number)
			}
		}
	}
	return nil
}

// Match the installed pi-sessions shellQuote representation too. Do not parse
// or rewrite commands; an embedded moving path is an unsupported dependency.
func containsShellPath(command, path string) bool {
	quoted := "'" + strings.ReplaceAll(path, "'", `'"'"'`) + "'"
	return strings.Contains(command, path) || strings.Contains(command, quoted)
}
