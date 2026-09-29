package erc

import (
	"fmt"
	"sort"
	"strings"

	"github.com/h3yow/netlist-erc-engine-go/pkg/netlist"
)

// scopeKey builds the stable, reproducible finding ID suffix for a scope.
// Scopes:
//   - pin-scoped:    pin:<ref>.<pin>
//   - net-scoped:    net:<name>
//   - design-scoped: design:<name>
func scopeKey(parts ...string) string { return strings.Join(parts, ":") }

// ---- ERC001 floating pin ----------------------------------------------------

type floatingPinRule struct{}

func (floatingPinRule) ID() RuleID { return RuleFloatingPin }

func (floatingPinRule) Run(nl *netlist.Netlist, _ RuleSet) []Finding {
	var out []Finding
	for _, p := range nl.UnconnectedPins() {
		if p.NoConnect {
			continue
		}
		out = append(out, Finding{
			ID:        string(RuleFloatingPin) + "-" + scopeKey("pin", p.Component+"."+p.Name),
			Rule:      RuleFloatingPin,
			Severity:  SeverityWarning,
			Message:   fmt.Sprintf("pin %s.%s (%s) is not connected to any net", p.Component, p.Name, p.Dir),
			Component: p.Component,
			Pin:       p.Name,
		})
	}
	return out
}

// ---- ERC002 orphan net ------------------------------------------------------

type orphanNetRule struct{}

func (orphanNetRule) ID() RuleID { return RuleOrphanNet }

func (orphanNetRule) Run(nl *netlist.Netlist, _ RuleSet) []Finding {
	var out []Finding
	for _, name := range sortedNetNames(nl) {
		n := nl.Nets[name]
		if len(n.Pins) == 0 {
			out = append(out, Finding{
				ID:       string(RuleOrphanNet) + "-" + scopeKey("net", name),
				Rule:     RuleOrphanNet,
				Severity: SeverityWarning,
				Message:  fmt.Sprintf("net %q is declared but has no pins", name),
				Net:      name,
			})
		}
	}
	return out
}

// ---- ERC003 multiple drivers ------------------------------------------------

type multipleDriversRule struct{}

func (multipleDriversRule) ID() RuleID { return RuleMultipleDrivers }

func (multipleDriversRule) Run(nl *netlist.Netlist, rs RuleSet) []Finding {
	var out []Finding
	for _, name := range sortedNetNames(nl) {
		n := nl.Nets[name]
		if n.Class == netlist.NetPower || n.Class == netlist.NetGround || namedRail(name, rs) {
			continue // rails are allowed to fan out to many receivers
		}
		var drivers []*netlist.Pin
		for _, p := range nl.PinsOnNet(name) {
			if p.Dir == netlist.PinOutput || p.Dir == netlist.PinBidirect {
				drivers = append(drivers, p)
			}
		}
		if len(drivers) > 1 {
			refs := make([]string, len(drivers))
			for i, p := range drivers {
				refs[i] = p.Component + "." + p.Name
			}
			sort.Strings(refs)
			out = append(out, Finding{
				ID:       string(RuleMultipleDrivers) + "-" + scopeKey("net", name),
				Rule:     RuleMultipleDrivers,
				Severity: SeverityError,
				Message:  fmt.Sprintf("net %q has %d driving pins: %s", name, len(drivers), strings.Join(refs, ", ")),
				Net:      name,
			})
		}
	}
	return out
}

// ---- ERC004 / ERC005 missing power / ground rail ----------------------------

type missingPowerRailRule struct{}

func (missingPowerRailRule) ID() RuleID { return RuleMissingPowerRail }

func (missingPowerRailRule) Run(nl *netlist.Netlist, rs RuleSet) []Finding {
	return missingRail(nl, rs, netlist.NetPower, netlist.PinKindVCC,
		RuleMissingPowerRail, "power", "VCC")
}

type missingGroundRailRule struct{}

func (missingGroundRailRule) ID() RuleID { return RuleMissingGroundRail }

func (missingGroundRailRule) Run(nl *netlist.Netlist, rs RuleSet) []Finding {
	return missingRail(nl, rs, netlist.NetGround, netlist.PinKindGND,
		RuleMissingGroundRail, "ground", "GND")
}

func missingRail(nl *netlist.Netlist, rs RuleSet, class netlist.NetClass,
	kind netlist.PinKind, id RuleID, label, hint string) []Finding {

	var railPins []*netlist.Pin
	for _, name := range sortedNetNames(nl) {
		n := nl.Nets[name]
		if n.Class != class && !namedAs(name, railNames(rs, class)) {
			continue
		}
		railPins = append(railPins, nl.PinsOnNet(name)...)
	}

	var offenders []string
	componentsNeeding := map[string]bool{}
	for _, ref := range sortedComponents(nl) {
		c := nl.Components[ref]
		hasPin := false
		connected := false
		for _, pname := range sortedPins(c) {
			p := c.Pins[pname]
			if p.Kind == kind {
				hasPin = true
				for _, rp := range railPins {
					if rp.Component == ref && rp.Name == pname {
						connected = true
					}
				}
			}
		}
		if hasPin {
			componentsNeeding[ref] = true
			if !connected {
				offenders = append(offenders, ref)
			}
		}
	}
	sort.Strings(offenders)

	var out []Finding
	for _, ref := range offenders {
		out = append(out, Finding{
			ID:        string(id) + "-" + scopeKey("component", ref),
			Rule:      id,
			Severity:  SeverityError,
			Message:   fmt.Sprintf("component %s has a %s pin but no connection to a %s net (e.g. %s)", ref, label, label, hint),
			Component: ref,
		})
	}
	if len(componentsNeeding) > 0 && len(railPins) == 0 {
		out = append(out, Finding{
			ID:       string(id) + "-" + scopeKey("design", "no_"+label+"_net"),
			Rule:     id,
			Severity: SeverityError,
			Message:  fmt.Sprintf("design contains %s pins but declares no %s net (e.g. .net %s %s)", label, label, hint, classOf(class)),
		})
	}
	return out
}

func classOf(c netlist.NetClass) string {
	switch c {
	case netlist.NetPower:
		return "power"
	case netlist.NetGround:
		return "ground"
	default:
		return "signal"
	}
}

func railNames(rs RuleSet, class netlist.NetClass) []string {
	if class == netlist.NetPower {
		return rs.Params.PowerNetNames
	}
	return rs.Params.GroundNetNames
}

func namedRail(name string, rs RuleSet) bool {
	return namedAs(name, rs.Params.PowerNetNames) || namedAs(name, rs.Params.GroundNetNames)
}

func namedAs(name string, names []string) bool {
	upper := strings.ToUpper(name)
	for _, n := range names {
		if upper == strings.ToUpper(n) {
			return true
		}
	}
	return false
}

// ---- stable ordering helpers ------------------------------------------------

func sortedNetNames(nl *netlist.Netlist) []string {
	names := make([]string, 0, len(nl.Nets))
	for n := range nl.Nets {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

func sortedComponents(nl *netlist.Netlist) []string {
	refs := make([]string, 0, len(nl.Components))
	for r := range nl.Components {
		refs = append(refs, r)
	}
	sort.Strings(refs)
	return refs
}

func sortedPins(c *netlist.Component) []string {
	names := make([]string, 0, len(c.Pins))
	for n := range c.Pins {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}
