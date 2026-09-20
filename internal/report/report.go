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
	"github.com/arhuman/spproof/internal/rules"
)

// Text writes one editor-jumpable line per violation:
// file:line:col: [rule-id] message. A file-scoped violation has no position, so
// it drops the line and column and renders as file: [rule-id] message.
//
// A ratcheted rule that stayed within its baseline adds one summary line, so a
// run carrying tolerated violations never renders identically to a run with
// none. Silence has to mean nothing was found, not that something was absorbed.
// It writes nothing when the result is clean and no ratchet applied.
func Text(w io.Writer, r engine.Result) error {
	for _, v := range r.Violations {
		var err error
		if v.FileScoped {
			_, err = fmt.Fprintf(w, "%s: [%s] %s\n", v.Path, v.RuleID, v.Message)
		} else {
			_, err = fmt.Fprintf(w, "%s:%d:%d: [%s] %s\n", v.Path, v.Line, v.Column, v.RuleID, v.Message)
		}
		if err != nil {
			return fmt.Errorf("report: write: %w", err)
		}
	}
	for _, s := range r.Ratchets {
		if s.Exceeded {
			continue
		}
		scope := "across the run"
		if s.Scope == "file" {
			scope = "in the worst file"
		}
		if _, err := fmt.Fprintf(w, "%s: %d tolerated %s, baseline %d\n", s.RuleID, s.Found, scope, s.Limit); err != nil {
			return fmt.Errorf("report: write: %w", err)
		}
	}
	return nil
}

type jsonReport struct {
	Version string     `json:"version"`
	Rules   []jsonRule `json:"rules"`
	Results []jsonHit  `json:"results"`
	// Tolerated carries the findings a ratchet absorbed. They are kept out of
	// results, which is the set that failed the run, but present in the
	// document: a consumer that ignores this field sees the verdict, and one
	// that reads it sees what the baseline is hiding.
	Tolerated []jsonHit `json:"tolerated,omitempty"`
}

// jsonRule is the rule catalogue entry, kept separate from results exactly as
// SARIF keeps tool.driver.rules separate from results. EvaluatedFiles is the
// spproof addition that distinguishes a rule that held from one that never ran.
type jsonRule struct {
	ID             string       `json:"id"`
	Check          string       `json:"check"`
	EvaluatedFiles int          `json:"evaluatedFiles"`
	Ratchet        *jsonRatchet `json:"ratchet,omitempty"`
}

// jsonRatchet reports what a ratcheted rule counted against its declared
// tolerance. Absent for a rule that declared none.
type jsonRatchet struct {
	Found    int    `json:"found"`
	Limit    int    `json:"limit"`
	Scope    string `json:"scope"`
	Exceeded bool   `json:"exceeded"`
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

// jsonPhysical carries an optional Region exactly as SARIF does: a location
// with an artifactLocation and no region is the standard way to say "this is
// about the whole artifact", so a file-scoped violation omits it rather than
// emitting a zero one.
type jsonPhysical struct {
	ArtifactLocation jsonArtifact `json:"artifactLocation"`
	Region           *jsonRegion  `json:"region,omitempty"`
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
	ratchets := make(map[string]engine.RatchetStatus, len(r.Ratchets))
	for _, s := range r.Ratchets {
		ratchets[s.RuleID] = s
	}
	for _, c := range r.Coverage {
		rule := jsonRule{ID: c.RuleID, Check: c.Check, EvaluatedFiles: c.EvaluatedFiles}
		if s, ok := ratchets[c.RuleID]; ok {
			rule.Ratchet = &jsonRatchet{Found: s.Found, Limit: s.Limit, Scope: s.Scope, Exceeded: s.Exceeded}
		}
		out.Rules = append(out.Rules, rule)
	}
	for _, v := range r.Violations {
		out.Results = append(out.Results, newHit(v))
	}
	for _, v := range r.Tolerated {
		out.Tolerated = append(out.Tolerated, newHit(v))
	}

	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(out); err != nil {
		return fmt.Errorf("report: encode: %w", err)
	}
	return nil
}

// newHit renders one violation as a SARIF-shaped result. A file-scoped
// violation has no position, so it omits the region entirely rather than
// emitting a zero one.
func newHit(v rules.Violation) jsonHit {
	var region *jsonRegion
	if !v.FileScoped {
		region = &jsonRegion{StartLine: v.Line, StartColumn: v.Column, Snippet: v.Match}
	}
	return jsonHit{
		RuleID:  v.RuleID,
		Message: jsonMessage{Text: v.Message},
		Locations: []jsonLocRef{{PhysicalLocation: jsonPhysical{
			ArtifactLocation: jsonArtifact{URI: v.Path},
			Region:           region,
		}}},
	}
}
