// Package erc implements electrical rules checking over a netlist.Netlist,
// including deterministic findings and incremental re-checking after the
// supported local mutations.
package erc

import "github.com/h3yow/netlist-erc-engine-go/pkg/netlist"

// Severity of a finding.
type Severity string

const (
	SeverityError   Severity = "error"
	SeverityWarning Severity = "warning"
)

// RuleID identifies a built-in rule.
type RuleID string

const (
	RuleFloatingPin       RuleID = "ERC001"
	RuleOrphanNet         RuleID = "ERC002"
	RuleMultipleDrivers   RuleID = "ERC003"
	RuleMissingPowerRail  RuleID = "ERC004"
	RuleMissingGroundRail RuleID = "ERC005"
)

// RuleVersion is the version of the built-in rule semantics. Bump this when a
// rule's condition or message changes, so old conclusions stay reproducible.
const RuleVersion = 1

// RuleParams holds configurable parameters of a rule set.
type RuleParams struct {
	// PowerNetNames and GroundNetNames additionally classify signal-class nets
	// by name (e.g. "VCC", "GND") for the rail and driver rules.
	PowerNetNames  []string
	GroundNetNames []string
}

// RuleSet is an immutable selection of rules and their parameters.
type RuleSet struct {
	Version int
	Rules   map[RuleID]bool
	Params  RuleParams
}

// Enabled reports whether a rule participates in checks.
func (rs RuleSet) Enabled(id RuleID) bool {
	if rs.Rules == nil {
		return true // nil map: built-in default of all rules enabled
	}
	return rs.Rules[id]
}

// DefaultRuleSet returns the built-in rule set with all rules enabled.
func DefaultRuleSet() RuleSet {
	return RuleSet{
		Version: RuleVersion,
		Rules: map[RuleID]bool{
			RuleFloatingPin:       true,
			RuleOrphanNet:         true,
			RuleMultipleDrivers:   true,
			RuleMissingPowerRail:  true,
			RuleMissingGroundRail: true,
		},
		Params: RuleParams{
			PowerNetNames:  []string{"VCC", "VDD"},
			GroundNetNames: []string{"GND", "VSS"},
		},
	}
}

// Finding is a single ERC conclusion. ID is stable and reproducible.
type Finding struct {
	ID          string
	Rule        RuleID
	Severity    Severity
	Message     string
	Net         string
	Component   string
	Pin         string
	RuleVersion int
}

// Report is a reproducible bundle of findings for one check run.
type Report struct {
	RuleSetVersion int
	Findings       []Finding
}

// rule implementations fill this interface internally.
type rule interface {
	ID() RuleID
	Run(nl *netlist.Netlist, rs RuleSet) []Finding
}

// builtinRules lists all rules; run order does not matter because findings are
// keyed and sorted before being returned.
func builtinRules() []rule {
	return []rule{
		floatingPinRule{},
		orphanNetRule{},
		multipleDriversRule{},
		missingPowerRailRule{},
		missingGroundRailRule{},
	}
}
