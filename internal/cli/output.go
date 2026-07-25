package cli

import (
	"encoding/json"
	"fmt"
	"io"
)

// output.go is the machine/human stream split (PRD FR4, task 2.8) and the versioned machine-output
// contract (task 1.2).
//
// Two streams, one rule: STDOUT carries the machine-consumable result and nothing else; STDERR carries
// every word a human reads — progress, warnings, the success URL, the reason for a failure. A CI job
// consumes stdout without ever parsing prose, and a developer reads stderr without their pipeline
// seeing it. Mixing them is the classic way a "just add a log line" change silently breaks a downstream
// parser (design Decision 9).

// OutputContractVersion is the machine-output envelope version. A consumer detects a format change
// rather than misparsing it: every stdout document carries this string, and the value changes when the
// SHAPE changes, never for content. The moment a customer's pipeline parses stdout it is a public
// contract, so it is versioned explicitly (task 1.2).
const OutputContractVersion = "p11.cli.v1"

// Envelope is the single machine-output shape every command emits on stdout. The data field is the
// command-specific payload; everything around it is stable across commands so a consumer can read
// contract_version, command and ok before it knows anything about the body.
type Envelope struct {
	ContractVersion string `json:"contract_version"`
	// Command is the subcommand that produced this document ("discover", "eval", "link", …).
	Command string `json:"command"`
	// OK is the machine mirror of the exit code's success axis: true iff the exit code is ExitOK.
	OK bool `json:"ok"`
	// ExitCode is the process exit code, echoed into the document so a consumer that captured stdout
	// but not the code still has it.
	ExitCode int `json:"exit_code"`
	// Data is the command-specific result. Never contains narration.
	Data any `json:"data,omitempty"`
	// Gate, when present, names the customer-configured gate outcome that set a non-zero exit — so a
	// gate failure is legible in the machine document, not only in the exit code (FR23).
	Gate *GateResult `json:"gate,omitempty"`
	// Error, when present, is a machine-readable failure summary. It obeys the same allowlist discipline
	// as everything else: it never carries source, prompts, diffs, env values, or credentials (FR13).
	Error *OutputError `json:"error,omitempty"`
}

// GateResult names a customer-configured quality gate and whether it passed. Emitted by eval/link/CI so
// "which gate failed" is a field, not a substring a consumer must grep out of a message.
type GateResult struct {
	Name   string  `json:"name"`
	Passed bool    `json:"passed"`
	Metric string  `json:"metric,omitempty"`
	Value  float64 `json:"value,omitempty"`
	Bound  float64 `json:"bound,omitempty"`
	Detail string  `json:"detail,omitempty"`
}

// OutputError is the machine failure summary. Code mirrors the exit code; Message is a short,
// content-free reason.
type OutputError struct {
	Code    int    `json:"code"`
	Kind    string `json:"kind"` // gate | operational | invalid_config
	Message string `json:"message"`
}

// Streams bundles the two output destinations so a command never reaches for the process globals and a
// test can capture both. Out is machine (stdout), Err is human (stderr).
type Streams struct {
	Out io.Writer
	Err io.Writer
}

// EmitJSON writes the envelope to the machine stream as a single indented JSON document followed by a
// newline. It is the ONLY way a command writes machine output, so the envelope shape cannot fork.
func (s Streams) EmitJSON(command string, exitCode int, data any, gate *GateResult, oerr *OutputError) error {
	env := Envelope{
		ContractVersion: OutputContractVersion,
		Command:         command,
		OK:              exitCode == ExitOK,
		ExitCode:        exitCode,
		Data:            data,
		Gate:            gate,
		Error:           oerr,
	}
	enc := json.NewEncoder(s.Out)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(env); err != nil {
		return fmt.Errorf("emit machine output: %w", err)
	}
	return nil
}

// Narratef writes human narration to the error stream. Progress, warnings, and the human-readable
// success line all go here — never to stdout.
func (s Streams) Narratef(format string, args ...any) {
	_, _ = fmt.Fprintf(s.Err, format+"\n", args...)
}
