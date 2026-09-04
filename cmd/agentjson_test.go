package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"
)

// captureAgentOut redirects the agent JSON writer to a buffer for the test.
func captureAgentOut(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := agentOut
	agentOut = &buf
	t.Cleanup(func() { agentOut = prev })
	return &buf
}

func TestEmitJSONCarriesSchema(t *testing.T) {
	buf := captureAgentOut(t)
	if err := emitJSON(map[string]any{"results": []any{}}); err != nil {
		t.Fatalf("emitJSON: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("output is not valid JSON: %v (%q)", err, buf.String())
	}
	if got["schema"] != float64(1) {
		t.Fatalf("schema = %v, want 1", got["schema"])
	}
}

// A caller-supplied "schema" key must never win over the envelope's own
// schema marker; the guarantee has to be enforced by emitJSON, not by
// callers remembering not to use that key.
func TestEmitJSONSchemaIsAuthoritative(t *testing.T) {
	buf := captureAgentOut(t)
	if err := emitJSON(map[string]any{"schema": 99, "results": []any{"ok"}}); err != nil {
		t.Fatalf("emitJSON: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("output is not valid JSON: %v (%q)", err, buf.String())
	}
	if got["schema"] != float64(1) {
		t.Fatalf("schema = %v, want 1 (caller's schema key must not win)", got["schema"])
	}
	results, ok := got["results"].([]any)
	if !ok || len(results) != 1 || results[0] != "ok" {
		t.Fatalf("results = %v, want [\"ok\"] to survive alongside the enforced schema", got["results"])
	}
}

// Errors must be machine-readable on stdout too, so an agent can parse
// unconditionally instead of guessing whether a run produced JSON.
func TestEmitErrWritesEnvelopeAndCarriesExitCode(t *testing.T) {
	buf := captureAgentOut(t)
	err := emitErr("no_results", exitNoResults, "nothing matched %q", "the matirx")

	var ee *exitError
	if !errors.As(err, &ee) {
		t.Fatalf("emitErr returned %T, want *exitError", err)
	}
	if ee.code != exitNoResults {
		t.Fatalf("exit code = %d, want %d", ee.code, exitNoResults)
	}

	var got struct {
		Schema int `json:"schema"`
		Error  struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("error output is not valid JSON: %v (%q)", err, buf.String())
	}
	if got.Schema != 1 {
		t.Fatalf("schema = %d, want 1", got.Schema)
	}
	if got.Error.Code != "no_results" {
		t.Fatalf("code = %q, want no_results", got.Error.Code)
	}
	if got.Error.Message != `nothing matched "the matirx"` {
		t.Fatalf("message = %q", got.Error.Message)
	}
}
