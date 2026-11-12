package relocate

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestTransformBranchesAndPreservation(t *testing.T) {
	source, destination := "/work/perry", "/work/perry/root"
	mapping := map[string]string{
		"/sessions/perry/parent.jsonl": "/sessions/root/parent.jsonl",
		"/sessions/perry/child.jsonl":  "/sessions/root/child.jsonl",
	}
	lines := []string{
		`{ "type":"session", "id":"same-session", "cwd":"/work/perry", "parentSession":"/sessions/perry/parent.jsonl", "unknown":{"big":9007199254740993123456789,"fraction":1.2300e+42} }`,
		` {"type":"session_info","id":"root","name":"Keep /work/perry"} `,
		`{"type":"custom","id":"branch-a","parentId":"root","customType":"pi-sessions.subagent_launched","data":{"childSessionFile":"/sessions/perry/child.jsonl","cwd":"/work/perry","sessionId":"legacy-child","writerSessionId":"writer","ownerSessionId":"owner","parentId":"legacy-parent","unknown":{"number":123456789012345678901234567890,"cwd":"/work/perry"}}}`,
		`  {"type":"message","id":"message","parentId":"branch-a","message":{"role":"toolResult","toolName":"subagent","details":{"childSessionFile":"/sessions/perry/child.jsonl","cwd":"/work/perry"},"content":[{"text":"/work/perry /sessions/perry/parent.jsonl"}]}}  `,
		`{"type":"custom","id":"branch-b","parentId":"root","customType":"pi-sessions.subagent_launched","data":{"childSessionFile":"/sessions/engine1/child.jsonl","cwd":"/work/engine1","childSessionId":"unrelated"}}`,
		`{"type":"custom","id":"bootstrap","parentId":"branch-b","customType":"pi-sessions.handoff-bootstrap","data":{"parentSessionFile":"/sessions/perry/parent.jsonl","parentSession":"legacy-id","writer":"original","cwd":"/work/perry","extra":18446744073709551616}}`,
		`{"type":"custom","id":"branch-c","parentId":"root","customType":"pi-sessions.subagent_launched","data":{"childSessionFile":"/sessions/perry/child.jsonl","cwd":"/work/perry","childSessionId":"modern-child","legacyFlag":true}}`,
		`{"type":"custom","customType":"pi-sessions.message_received","data":{"from":{"sessionFile":"/sessions/perry/parent.jsonl","cwd":"/work/perry"},"to":{"childSessionFile":"/sessions/perry/child.jsonl"}}}`,
		`{"type":"compaction","summary":"Old cwd /work/perry and parent /sessions/perry/parent.jsonl","research":{"cwd":"/work/perry"}}`,
	}
	want := append([]string(nil), lines...)
	want[0] = strings.ReplaceAll(want[0], `"cwd":"/work/perry"`, `"cwd":"/work/perry/root"`)
	want[0] = strings.ReplaceAll(want[0], `"parentSession":"/sessions/perry/parent.jsonl"`, `"parentSession":"/sessions/root/parent.jsonl"`)
	// Only the top-level operational cwd changes, not an unknown nested cwd.
	want[2] = strings.Replace(want[2], `"cwd":"/work/perry"`, `"cwd":"/work/perry/root"`, 1)
	want[2] = strings.ReplaceAll(want[2], `"childSessionFile":"/sessions/perry/child.jsonl"`, `"childSessionFile":"/sessions/root/child.jsonl"`)
	want[5] = strings.ReplaceAll(want[5], `"parentSessionFile":"/sessions/perry/parent.jsonl"`, `"parentSessionFile":"/sessions/root/parent.jsonl"`)
	want[6] = strings.ReplaceAll(want[6], `"cwd":"/work/perry"`, `"cwd":"/work/perry/root"`)
	want[6] = strings.ReplaceAll(want[6], `"childSessionFile":"/sessions/perry/child.jsonl"`, `"childSessionFile":"/sessions/root/child.jsonl"`)
	input := []byte(strings.Join(lines, "\n") + "\n")
	original := bytes.Clone(input)
	output, updates, err := Transform(input, source, destination, mapping, true)
	if err != nil || updates != 7 {
		t.Fatalf("updates=%d err=%v, want 7 and nil", updates, err)
	}
	if !bytes.Equal(input, original) {
		t.Fatal("input was mutated")
	}
	got := strings.Split(strings.TrimSuffix(string(output), "\n"), "\n")
	if len(got) != len(want) {
		t.Fatalf("got %d lines, want %d", len(got), len(want))
	}
	for i := range want {
		transformEqualJSON(t, got[i], want[i])
	}
	for _, i := range []int{1, 3, 4, 7, 8} {
		if got[i] != lines[i] {
			t.Errorf("untouched line %d changed bytes", i+1)
		}
	}
}

func TestTransformExternalInbound(t *testing.T) {
	input := []byte(`{"type":"session","id":"dtcc","cwd":"/work/dtcc","parentSession":"/sessions/perry/parent.jsonl"}` + "\n" +
		`{"type":"custom","customType":"pi-sessions.handoff-bootstrap","data":{"parentSessionFile":"/sessions/perry/parent.jsonl","writerSessionId":"dtcc"}}` + "\n" +
		`{"type":"custom","customType":"pi-sessions.subagent_launched","data":{"childSessionFile":"/sessions/perry/parent.jsonl","cwd":"/work/perry","ownerSessionId":"dtcc"}}`)
	mapping := map[string]string{"/sessions/perry/parent.jsonl": "/sessions/root/parent.jsonl"}
	output, updates, err := Transform(input, "/work/perry", "/work/perry/root", mapping, false)
	if err != nil || updates != 4 {
		t.Fatalf("updates=%d err=%v, want 4 and nil", updates, err)
	}
	want := strings.ReplaceAll(string(input), "/sessions/perry/parent.jsonl", "/sessions/root/parent.jsonl")
	want = strings.ReplaceAll(want, `"cwd":"/work/perry"`, `"cwd":"/work/perry/root"`)
	gotLines, wantLines := strings.Split(string(output), "\n"), strings.Split(want, "\n")
	for i := range wantLines {
		transformEqualJSON(t, gotLines[i], wantLines[i])
	}
	if bytes.HasSuffix(output, []byte("\n")) {
		t.Fatal("added a trailing newline")
	}
}

func TestTransformExactMatchesOnly(t *testing.T) {
	mapping := map[string]string{"/sessions/old/a.jsonl": "/sessions/new/a.jsonl"}
	cases := []struct {
		name, line string
		moved      bool
	}{
		{"nested cwd", `{"type":"session","cwd":"/old/nested"}`, true},
		{"sibling cwd", `{"type":"session","cwd":"/older"}`, true},
		{"external header cwd", `{"type":"session","cwd":"/old"}`, false},
		{"parent prefix", `{"type":"session","cwd":"/other","parentSession":"/sessions/old/a.jsonl.extra"}`, false},
		{"child prefix", `{"type":"custom","customType":"pi-sessions.subagent_launched","data":{"childSessionFile":"/sessions/old/a.jsonl/child","cwd":"/old/nested"}}`, false},
		{"handoff prefix", `{"type":"custom","customType":"pi-sessions.handoff-bootstrap","data":{"parentSessionFile":"/sessions/old/a.jsonl.extra","cwd":"/old"}}`, false},
		{"unknown custom", `{"type":"custom","customType":"another-extension","data":{"childSessionFile":"/sessions/old/a.jsonl","cwd":"/old"}}`, true},
		{"unknown custom shape", `{"type":"custom","customType":"historical-research","data":[null,42,"/old"]}`, true},
		{"history", `{"type":"message","cwd":"/old","parentSession":"/sessions/old/a.jsonl","message":{"role":"assistant","content":"/old"}}`, true},
		{"no path normalization", `{"type":"session","cwd":"/old/","parentSession":"/sessions/old/./a.jsonl"}`, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			input := []byte("  " + tc.line + "  \r\n")
			output, updates, err := Transform(input, "/old", "/new", mapping, tc.moved)
			if err != nil || updates != 0 || !bytes.Equal(output, input) {
				t.Fatalf("unexpected edit: updates=%d err=%v output=%s", updates, err, output)
			}
			if &output[0] != &input[0] {
				t.Fatal("no-op did not return original bytes")
			}
		})
	}
}

func TestTransformLocationFieldsIndependent(t *testing.T) {
	cases := []struct {
		name, input, want string
	}{
		{"cwd without mapped child", `{"type":"custom","customType":"pi-sessions.subagent_launched","data":{"childSessionFile":"/unrelated/child.jsonl","cwd":"/old"}}`, `{"type":"custom","customType":"pi-sessions.subagent_launched","data":{"childSessionFile":"/unrelated/child.jsonl","cwd":"/new"}}`},
		{"child without moved cwd", `{"type":"custom","customType":"pi-sessions.subagent_launched","data":{"childSessionFile":"/sessions/old/a.jsonl","cwd":"/other"}}`, `{"type":"custom","customType":"pi-sessions.subagent_launched","data":{"childSessionFile":"/sessions/new/a.jsonl","cwd":"/other"}}`},
		{"legacy absent cwd", `{"type":"custom","customType":"pi-sessions.subagent_launched","data":{"childSessionFile":"/sessions/old/a.jsonl","sessionId":"legacy","writerId":"writer","cwdLegacy":"/old"}}`, `{"type":"custom","customType":"pi-sessions.subagent_launched","data":{"childSessionFile":"/sessions/new/a.jsonl","sessionId":"legacy","writerId":"writer","cwdLegacy":"/old"}}`},
		{"legacy absent child path", `{"type":"custom","customType":"pi-sessions.subagent_launched","data":{"sessionFile":"/sessions/old/a.jsonl","cwd":"/old"}}`, `{"type":"custom","customType":"pi-sessions.subagent_launched","data":{"sessionFile":"/sessions/old/a.jsonl","cwd":"/new"}}`},
		{"legacy bootstrap", `{"type":"custom","customType":"pi-sessions.handoff-bootstrap","data":{"parentSessionFile":"/sessions/old/a.jsonl","parentSessionId":"legacy","parentCwd":"/old","childSessionFile":"/sessions/old/a.jsonl"}}`, `{"type":"custom","customType":"pi-sessions.handoff-bootstrap","data":{"parentSessionFile":"/sessions/new/a.jsonl","parentSessionId":"legacy","parentCwd":"/old","childSessionFile":"/sessions/old/a.jsonl"}}`},
		{"escaped exact match", `{"type":"session","cwd":"\/old","text":"\/old"}`, `{"type":"session","cwd":"/new","text":"\/old"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			output, updates, err := Transform([]byte(tc.input), "/old", "/new", map[string]string{"/sessions/old/a.jsonl": "/sessions/new/a.jsonl"}, true)
			if err != nil || updates != 1 {
				t.Fatalf("updates=%d err=%v, want 1 and nil", updates, err)
			}
			transformEqualJSON(t, string(output), tc.want)
		})
	}
}

func TestTransformLongRecordsAndLineEndings(t *testing.T) {
	large := strings.Repeat("history /old ", 20000)
	for _, ending := range []string{"", "\n", "\r\n"} {
		t.Run("ending="+strings.ReplaceAll(strings.ReplaceAll(ending, "\r", "CR"), "\n", "LF"), func(t *testing.T) {
			first := ` {"type":"message","content":"` + large + `"} ` + "\r\n"
			changed := `{"type":"custom","customType":"pi-sessions.subagent_launched","data":{"cwd":"/old","unknown":"` + large + `","number":9999999999999999999999999999999999999}}`
			input := []byte(first + " \t\n" + changed + ending)
			output, updates, err := Transform(input, "/old", "/new", nil, false)
			if err != nil || updates != 1 {
				t.Fatalf("updates=%d err=%v, want 1 and nil", updates, err)
			}
			prefix := first + " \t\n"
			if !bytes.HasPrefix(output, []byte(prefix)) {
				t.Fatal("untouched long line or blank line changed")
			}
			last := string(output[len(prefix):])
			if !strings.HasSuffix(last, ending) || (ending == "" && strings.HasSuffix(last, "\n")) {
				t.Fatal("changed trailing newline")
			}
			transformEqualJSON(t, strings.TrimSuffix(last, ending), strings.Replace(changed, `"cwd":"/old"`, `"cwd":"/new"`, 1))
		})
	}
}

func TestTransformMixedLineEndings(t *testing.T) {
	input := []byte("\r\n" + `{"type":"session","cwd":"/old"}` + "\r\n" +
		` {"type":"message","content":"/old"} ` + "\n" +
		`{"type":"custom","customType":"pi-sessions.subagent_launched","data":{"cwd":"/old"}}` + "\n")
	output, updates, err := Transform(input, "/old", "/new", nil, true)
	if err != nil || updates != 2 {
		t.Fatalf("updates=%d err=%v, want 2 and nil", updates, err)
	}
	want := strings.ReplaceAll(string(input), `"cwd":"/old"`, `"cwd":"/new"`)
	// Raw maps sort object keys, so compare each record separately, but keep the
	// newline and unchanged-message checks byte-exact.
	if bytes.Count(output, []byte("\r\n")) != 2 || bytes.Count(output, []byte("\n")) != 4 {
		t.Fatal("mixed newline endings changed")
	}
	gotLines, wantLines := strings.Split(string(output), "\n"), strings.Split(want, "\n")
	for i := 1; i <= 3; i++ {
		transformEqualJSON(t, gotLines[i], wantLines[i])
	}
	if gotLines[2] != wantLines[2] {
		t.Fatal("untouched message changed bytes")
	}
}

func TestTransformRejectsMalformedMetadata(t *testing.T) {
	cases := []struct {
		name, line, field string
	}{
		{"invalid JSON", `{"type":"session",`, "object"},
		{"trailing JSON", `{"type":"session","cwd":"/old"} {}`, "after"},
		{"not object", `[]`, "object"},
		{"missing type", `{"cwd":"/old"}`, "type"},
		{"non-string type", `{"type":42}`, "type"},
		{"header cwd", `{"type":"session","cwd":42}`, "cwd"},
		{"missing header cwd", `{"type":"session"}`, "cwd"},
		{"header parent", `{"type":"session","cwd":"/old","parentSession":null}`, "parentSession"},
		{"custom type", `{"type":"custom","customType":null}`, "customType"},
		{"missing custom type", `{"type":"custom"}`, "customType"},
		{"missing payload", `{"type":"custom","customType":"pi-sessions.subagent_launched"}`, "data"},
		{"null payload", `{"type":"custom","customType":"pi-sessions.subagent_launched","data":null}`, "data"},
		{"array payload", `{"type":"custom","customType":"pi-sessions.handoff-bootstrap","data":[]}`, "data"},
		{"child path", `{"type":"custom","customType":"pi-sessions.subagent_launched","data":{"childSessionFile":{},"cwd":"/old"}}`, "childSessionFile"},
		{"launch cwd", `{"type":"custom","customType":"pi-sessions.subagent_launched","data":{"childSessionFile":"/sessions/old/a.jsonl","cwd":null}}`, "cwd"},
		{"bootstrap path", `{"type":"custom","customType":"pi-sessions.handoff-bootstrap","data":{"parentSessionFile":false}}`, "parentSessionFile"},
		{"duplicate header field", `{"type":"session","cwd":"/old","cwd":"/other"}`, "duplicate"},
		{"duplicate payload field", `{"type":"custom","customType":"pi-sessions.subagent_launched","data":{"cwd":"/old","cwd":"/other"}}`, "duplicate"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			input := []byte(`{"type":"session","cwd":"/old"}` + "\n" + tc.line)
			original := bytes.Clone(input)
			output, updates, err := Transform(input, "/old", "/new", map[string]string{"/sessions/old/a.jsonl": "/sessions/new/a.jsonl"}, true)
			if err == nil || !strings.Contains(err.Error(), "line 2") || !strings.Contains(err.Error(), tc.field) {
				t.Fatalf("err=%v, want line 2 and %q", err, tc.field)
			}
			if output != nil || updates != 0 {
				t.Fatalf("partial result on error: output=%s updates=%d", output, updates)
			}
			if !bytes.Equal(input, original) {
				t.Fatal("error mutated input")
			}
		})
	}
}

func TestTransformExternalMalformedUnrelatedRecords(t *testing.T) {
	lines := []string{
		`{"type":"custom","customType":"unrelated.history","data":`,
		`{"type":"custom","customType":"unrelated.history","data":null}`,
		`{"type":"custom","data":["historical legacy shape"]}`,
		`not JSON history`,
		`{"type":false,"research":"unrelated"}`,
	}
	input := []byte(strings.Join(lines, "\r\n") + "\r\n")
	output, updates, err := Transform(input, "/old", "/new", nil, false)
	if err != nil || updates != 0 || !bytes.Equal(output, input) || &output[0] != &input[0] {
		t.Fatalf("unrelated external history changed: updates=%d err=%v", updates, err)
	}
}

func TestTransformExternalMalformedRecognizedRecords(t *testing.T) {
	for _, line := range []string{
		`{"type":"session","parentSession":`,
		`{"type":"session","parentSession":42}`,
		`{"type":"custom","customType":"pi-sessions.subagent_launched","data":`,
		`{"type":"custom","customType":"pi-sessions.handoff-bootstrap","data":null}`,
		`{"type":"session","\u0070arentSession":false}`,
		`{"type":"custom","customType":"pi-sessions.\u0073ubagent_launched","data":null}`,
	} {
		output, updates, err := Transform([]byte(line), "/old", "/new", nil, false)
		if err == nil || output != nil || updates != 0 {
			t.Errorf("malformed external reference was not refused: output=%s updates=%d err=%v", output, updates, err)
		}
	}
}

func TestTransformNoOpAndIdempotence(t *testing.T) {
	for _, input := range [][]byte{nil, {}, []byte("\n \t\r\n"), []byte(`{"type":"custom","customType":"pi-sessions.handoff-bootstrap","data":{"legacyParentId":"id"}}`)} {
		output, updates, err := Transform(input, "/old", "/new", nil, false)
		if err != nil || updates != 0 || !reflect.DeepEqual(output, input) {
			t.Fatalf("no-op changed input: output=%s updates=%d err=%v", output, updates, err)
		}
	}
	input := []byte(`{"type":"session","cwd":"/old","parentSession":"/sessions/old/a.jsonl"}`)
	mapping := map[string]string{"/sessions/old/a.jsonl": "/sessions/new/a.jsonl"}
	output, updates, err := Transform(input, "/old", "/new", mapping, true)
	if err != nil || updates != 2 {
		t.Fatalf("first transform: updates=%d err=%v", updates, err)
	}
	again, updates, err := Transform(output, "/old", "/new", mapping, true)
	if err != nil || updates != 0 || !bytes.Equal(again, output) || &again[0] != &output[0] {
		t.Fatalf("second transform: updates=%d err=%v", updates, err)
	}
	output, updates, err = Transform(input, "/old", "/old", map[string]string{"/sessions/old/a.jsonl": "/sessions/old/a.jsonl"}, true)
	if err != nil || updates != 0 || !bytes.Equal(output, input) {
		t.Fatalf("identity mapping: updates=%d err=%v", updates, err)
	}
}

func transformEqualJSON(t *testing.T, got, want string) {
	t.Helper()
	decode := func(raw string) any {
		decoder := json.NewDecoder(strings.NewReader(raw))
		decoder.UseNumber()
		var value any
		if err := decoder.Decode(&value); err != nil {
			t.Fatalf("decode JSON: %v", err)
		}
		return value
	}
	if !reflect.DeepEqual(decode(got), decode(want)) {
		t.Errorf("JSON differs:\n got: %.1000s\nwant: %.1000s", got, want)
	}
}
