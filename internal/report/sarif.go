package report

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/arhuman/spproof/internal/engine"
	"github.com/arhuman/spproof/internal/rules"
	"github.com/arhuman/spproof/internal/version"
)

// SARIF schema identity. Pinned to 2.1.0, the version GitHub code scanning and
// every current consumer read; the schema URI is part of the contract, since a
// validator resolves it rather than guessing.
const (
	sarifVersion = "2.1.0"
	// The OASIS-hosted errata URI, not the sarif-spec GitHub raw path: the
	// latter moved branches and now 404s, and a $schema a validator cannot
	// resolve is worse than none.
	sarifSchema = "https://docs.oasis-open.org/sarif/sarif/v2.1.0/errata01/os/schemas/sarif-schema-2.1.0.json"
	toolName    = "spproof"
	toolURI     = "https://github.com/arhuman/spproof"
)

// sarifLog is the root of a SARIF document. Runs always holds exactly one
// element: a run is one invocation of one tool, which is what a spproof check
// is.
type sarifLog struct {
	Schema  string     `json:"$schema"`
	Version string     `json:"version"`
	Runs    []sarifRun `json:"runs"`
}

type sarifRun struct {
	Tool    sarifTool     `json:"tool"`
	Results []sarifResult `json:"results"`
	Props   sarifRunProps `json:"properties"`
}

// sarifRunProps carries the run-level facts SARIF has no field for. A property
// bag is the format's own extension point, so a consumer that ignores it still
// reads a valid document and one that knows spproof gets the ratchet accounting.
type sarifRunProps struct {
	Ratchets []sarifRatchet `json:"ratchets,omitempty"`
}

type sarifRatchet struct {
	RuleID   string `json:"ruleId"`
	Found    int    `json:"found"`
	Limit    int    `json:"limit"`
	Scope    string `json:"scope"`
	Exceeded bool   `json:"exceeded"`
}

type sarifTool struct {
	Driver sarifDriver `json:"driver"`
}

type sarifDriver struct {
	Name           string          `json:"name"`
	InformationURI string          `json:"informationUri"`
	Version        string          `json:"version"`
	Rules          []sarifRuleDesc `json:"rules"`
}

// sarifRuleDesc is a reportingDescriptor: the catalogue entry a result points
// back to through ruleId. Every policy rule appears here, including one that
// produced no result, because the catalogue is what the policy declared rather
// than what happened to fire.
type sarifRuleDesc struct {
	ID    string        `json:"id"`
	Name  string        `json:"name"`
	Props sarifRuleProp `json:"properties"`
}

// sarifRuleProp carries the coverage fact SARIF cannot express. A rule that
// evaluated zero files held vacuously, which must never read like a rule that
// ran and found nothing: SARIF alone renders both as an absence of results, so
// the distinction lives here or nowhere.
type sarifRuleProp struct {
	EvaluatedFiles int `json:"evaluatedFiles"`
}

type sarifResult struct {
	RuleID string `json:"ruleId"`
	// RuleIndex is omitted rather than defaulted when the rule is absent from
	// the catalogue: index 0 is a valid descriptor, so a zero written on a
	// missing rule would attribute the finding to whichever rule sorted first.
	RuleIndex *int            `json:"ruleIndex,omitempty"`
	Level     string          `json:"level"`
	Message   sarifMessage    `json:"message"`
	Locations []sarifLocation `json:"locations"`
	// Suppressions marks a result the run did not fail on. SARIF's own
	// construct for "found, but not counted against the verdict" is exactly
	// what a ratchet does, so a tolerated violation renders as a suppressed
	// result rather than being dropped from the document.
	Suppressions []sarifSuppression `json:"suppressions,omitempty"`
}

type sarifSuppression struct {
	Kind          string `json:"kind"`
	Justification string `json:"justification,omitempty"`
}

type sarifMessage struct {
	Text string `json:"text"`
}

type sarifLocation struct {
	PhysicalLocation sarifPhysical `json:"physicalLocation"`
}

type sarifPhysical struct {
	ArtifactLocation sarifArtifact `json:"artifactLocation"`
	Region           *sarifRegion  `json:"region,omitempty"`
}

type sarifArtifact struct {
	URI string `json:"uri"`
}

type sarifRegion struct {
	StartLine   int           `json:"startLine"`
	StartColumn int           `json:"startColumn"`
	Snippet     *sarifSnippet `json:"snippet,omitempty"`
}

type sarifSnippet struct {
	Text string `json:"text"`
}

// SARIF writes the result as a SARIF 2.1.0 log with a trailing newline.
//
// Output is deterministic: the engine has already ordered violations and
// coverage, and nothing here introduces map iteration into the document. Two
// runs over an unchanged tree are byte-identical apart from the driver version,
// which is fixed for a given binary.
//
// Tolerated violations are emitted as results carrying a SARIF suppression
// rather than omitted, so a run that absorbed something never renders like a
// run that found nothing. A consumer honouring suppressions (GitHub code
// scanning does) shows them as dismissed rather than failing on them.
func SARIF(w io.Writer, r engine.Result) error {
	ratchets := make(map[string]engine.RatchetStatus, len(r.Ratchets))
	for _, s := range r.Ratchets {
		ratchets[s.RuleID] = s
	}

	// index maps a rule id onto its position in the catalogue, which is what
	// ruleIndex must carry: a consumer resolves the descriptor by index, and a
	// wrong one silently attributes a finding to another rule.
	index := make(map[string]int, len(r.Coverage))
	descs := make([]sarifRuleDesc, 0, len(r.Coverage))
	for _, c := range r.Coverage {
		index[c.RuleID] = len(descs)
		descs = append(descs, sarifRuleDesc{
			ID:    c.RuleID,
			Name:  c.Check,
			Props: sarifRuleProp{EvaluatedFiles: c.EvaluatedFiles},
		})
	}

	results := make([]sarifResult, 0, len(r.Violations)+len(r.Tolerated))
	for _, v := range r.Violations {
		results = append(results, newSarifResult(v, index, nil))
	}
	for _, v := range r.Tolerated {
		s := &sarifSuppression{Kind: "external", Justification: ratchetJustification(ratchets[v.RuleID])}
		results = append(results, newSarifResult(v, index, s))
	}

	props := sarifRunProps{}
	for _, s := range r.Ratchets {
		props.Ratchets = append(props.Ratchets, sarifRatchet{
			RuleID: s.RuleID, Found: s.Found, Limit: s.Limit, Scope: s.Scope, Exceeded: s.Exceeded,
		})
	}

	log := sarifLog{
		Schema:  sarifSchema,
		Version: sarifVersion,
		Runs: []sarifRun{{
			Tool: sarifTool{Driver: sarifDriver{
				Name:           toolName,
				InformationURI: toolURI,
				Version:        version.Build.Version,
				Rules:          descs,
			}},
			Results: results,
			Props:   props,
		}},
	}

	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(log); err != nil {
		return fmt.Errorf("report: encode: %w", err)
	}
	return nil
}

// ratchetJustification states which declared baseline absorbed the finding. A
// bare suppression says a result was dismissed without saying by what, which is
// the half a reader acting on it needs.
func ratchetJustification(s engine.RatchetStatus) string {
	if s.RuleID == "" {
		return ""
	}
	scope := "across the run"
	if s.Scope == "file" {
		scope = "in the worst file"
	}
	return fmt.Sprintf("within the declared baseline of %d %s", s.Limit, scope)
}

// newSarifResult renders one violation as a SARIF result. A file-scoped
// violation omits the region entirely: a physicalLocation with an
// artifactLocation and no region is SARIF's way of saying the finding is about
// the whole artifact, and a zero region would instead claim line 0.
//
// Level is always "error". spproof proves a declared property holds or does
// not; it has no severity axis, and emitting a softer level would let a
// consumer's threshold silently drop a finding the policy declared load-bearing.
func newSarifResult(v rules.Violation, index map[string]int, s *sarifSuppression) sarifResult {
	var region *sarifRegion
	if !v.FileScoped {
		region = &sarifRegion{StartLine: v.Line, StartColumn: v.Column}
		if v.Match != "" {
			region.Snippet = &sarifSnippet{Text: v.Match}
		}
	}
	var ruleIndex *int
	if i, ok := index[v.RuleID]; ok {
		ruleIndex = &i
	}
	res := sarifResult{
		RuleID:    v.RuleID,
		RuleIndex: ruleIndex,
		Level:     "error",
		Message:   sarifMessage{Text: v.Message},
		Locations: []sarifLocation{{PhysicalLocation: sarifPhysical{
			ArtifactLocation: sarifArtifact{URI: v.Path},
			Region:           region,
		}}},
	}
	if s != nil {
		res.Suppressions = []sarifSuppression{*s}
	}
	return res
}
