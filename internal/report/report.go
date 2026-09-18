// Package report renders an engine result as text or JSON.
//
// The JSON shape mirrors SARIF's separation of the rule catalogue
// (tool.driver.rules) from the findings (results), and uses SARIF's field
// names, so emitting real SARIF later is a projection rather than a refactor.
package report

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/arhuman/spproof/internal/engine"
)

// Text writes one editor-jumpable line per violation:
// file:line:col: [rule-id] message. It writes nothing when the result is clean.
func Text(w io.Writer, r engine.Result) error {
	for _, v := range r.Violations {
		if _, err := fmt.Fprintf(w, "%s:%d:%d: [%s] %s\n", v.Path, v.Line, v.Column, v.RuleID, v.Message); err != nil {
			return fmt.Errorf("report: write: %w", err)
		}
	}
	return nil
}

type jsonReport struct {
	Version string     `json:"version"`
	Rules   []jsonRule `json:"rules"`
	Results []jsonHit  `json:"results"`
}

// jsonRule is the rule catalogue entry, kept separate from results exactly as
// SARIF keeps tool.driver.rules separate from results. EvaluatedFiles is the
// spproof addition that distinguishes a rule that held from one that never ran.
type jsonRule struct {
	ID             string `json:"id"`
	Check          string `json:"check"`
	EvaluatedFiles int    `json:"evaluatedFiles"`
}

type jsonHit struct {
	RuleID    string       `json:"ruleId"`
	Message   jsonMessage  `json:"message"`
	Locations []jsonLocRef `json:"locations"`
}

type jsonMessage struct {
	Text string `json:"text"`
}

type jsonLocRef struct {
	PhysicalLocation jsonPhysical `json:"physicalLocation"`
}

type jsonPhysical struct {
	ArtifactLocation jsonArtifact `json:"artifactLocation"`
	Region           jsonRegion   `json:"region"`
}

type jsonArtifact struct {
	URI string `json:"uri"`
}

type jsonRegion struct {
	StartLine   int    `json:"startLine"`
	StartColumn int    `json:"startColumn"`
	Snippet     string `json:"snippet,omitempty"`
}

// JSON writes the result as SARIF-shaped JSON with a trailing newline. Output
// is deterministic: the engine has already ordered violations and coverage.
func JSON(w io.Writer, r engine.Result) error {
	out := jsonReport{
		Version: "1",
		Rules:   make([]jsonRule, 0, len(r.Coverage)),
		Results: make([]jsonHit, 0, len(r.Violations)),
	}
	for _, c := range r.Coverage {
		out.Rules = append(out.Rules, jsonRule{ID: c.RuleID, Check: c.Check, EvaluatedFiles: c.EvaluatedFiles})
	}
	for _, v := range r.Violations {
		out.Results = append(out.Results, jsonHit{
			RuleID:  v.RuleID,
			Message: jsonMessage{Text: v.Message},
			Locations: []jsonLocRef{{PhysicalLocation: jsonPhysical{
				ArtifactLocation: jsonArtifact{URI: v.Path},
				Region:           jsonRegion{StartLine: v.Line, StartColumn: v.Column, Snippet: v.Match},
			}}},
		})
	}

	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(out); err != nil {
		return fmt.Errorf("report: encode: %w", err)
	}
	return nil
}
