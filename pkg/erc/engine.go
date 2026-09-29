package erc

import (
	"errors"
	"sort"
	"strings"

	"github.com/h3yow/netlist-erc-engine-go/pkg/netlist"
)

// Engine maintains queryable ERC state across local netlist mutations.
// All mutations must be performed through the engine so that the next
// Recheck knows which conclusions may have changed.
type Engine struct {
	nl       *netlist.Netlist
	ruleSet  RuleSet
	findings map[string]Finding

	// dirty scopes accumulated since the last (re)check; suffix keys such as
	// "net:VCC" or "pin:U1.A".
	dirty map[string]bool
	// globalDirty marks findings of design/component-wide rules (ERC004/005).
	globalDirty bool
	// allDirty forces Recheck to behave like a full check (rule set change).
	allDirty bool
	// fullRequired means the last mutation cannot be handled incrementally.
	fullRequired bool
	fullReason   string
}

// NewEngine binds an engine to an already parsed netlist and a rule set.
// It does not run any checks until Check is called.
func NewEngine(nl *netlist.Netlist, rs RuleSet) *Engine {
	return &Engine{
		nl:       nl,
		ruleSet:  rs,
		findings: map[string]Finding{},
		dirty:    map[string]bool{},
	}
}

// Netlist returns the underlying netlist.
func (e *Engine) Netlist() *netlist.Netlist { return e.nl }

// SetRuleSet replaces the rule set (rules or parameters changed). The next
// Recheck recomputes every conclusion with the new set; previously returned
// Reports remain reproducible because they embed the old set version.
func (e *Engine) SetRuleSet(rs RuleSet) {
	e.ruleSet = rs
	e.allDirty = true
	e.fullRequired = false
	e.fullReason = ""
}

// ErrFullRecheckRequired is returned when an incremental recheck cannot be
// performed safely for the last mutation. Check still produces the correct,
// complete result and clears this condition.
var ErrFullRecheckRequired = errors.New("incremental recheck unsupported for this change; full recheck required")

// ---- full check -------------------------------------------------------------

// Check runs a full check from scratch and replaces the stored state.
func (e *Engine) Check() (Report, error) {
	e.findings = map[string]Finding{}
	for _, r := range builtinRules() {
		if !e.ruleSet.Enabled(r.ID()) {
			continue
		}
		for _, f := range r.Run(e.nl, e.ruleSet) {
			f.RuleVersion = e.ruleSet.Version
			e.findings[f.ID] = f
		}
	}
	e.dirty = map[string]bool{}
	e.globalDirty = false
	e.allDirty = false
	e.fullRequired = false
	e.fullReason = ""
	return e.report(), nil
}

// Recheck incrementally re-evaluates the netlist after the last mutation.
// The returned report is identical to Check(): no stale findings remain and
// no new findings are missed. If the mutation cannot be tracked,
// ErrFullRecheckRequired is returned; call Check() in that case.
func (e *Engine) Recheck() (Report, error) {
	if e.fullRequired {
		return Report{}, ErrFullRecheckRequired
	}
	if len(e.dirty) == 0 && !e.globalDirty && !e.allDirty {
		return e.report(), nil
	}
	if e.allDirty {
		// Rule set changed: a stored full Report from before still carries the
		// old version and remains reproducible; the new run is computed fresh.
		rep, err := e.Check()
		return rep, err
	}

	// Invalidate scoped findings that touched a changed net/pin.
	for id := range e.findings {
		ruleID, scope, ok := strings.Cut(id, "-")
		if !ok {
			continue
		}
		if RuleID(ruleID) == RuleMissingPowerRail || RuleID(ruleID) == RuleMissingGroundRail {
			if e.globalDirty {
				delete(e.findings, id)
			}
			continue
		}
		if e.dirty[scope] {
			delete(e.findings, id)
		}
	}

	// Re-run enabled rules, accepting only findings in dirty scopes (or all
	// findings of the always-rerun global rules).
	for _, r := range builtinRules() {
		if !e.ruleSet.Enabled(r.ID()) {
			continue
		}
		global := r.ID() == RuleMissingPowerRail || r.ID() == RuleMissingGroundRail
		if global && !e.globalDirty {
			continue
		}
		for _, f := range r.Run(e.nl, e.ruleSet) {
			_, scope, _ := strings.Cut(f.ID, "-")
			if !global && !e.dirty[scope] {
				continue
			}
			f.RuleVersion = e.ruleSet.Version
			e.findings[f.ID] = f
		}
	}

	e.dirty = map[string]bool{}
	e.globalDirty = false
	return e.report(), nil
}

// Findings returns the current conclusions sorted by stable ID.
func (e *Engine) Findings() []Finding {
	out := make([]Finding, 0, len(e.findings))
	for _, f := range e.findings {
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (e *Engine) report() Report {
	return Report{RuleSetVersion: e.ruleSet.Version, Findings: e.Findings()}
}

func (e *Engine) markNet(name string) {
	e.dirty[scopeKey("net", name)] = true
	e.globalDirty = true
}

func (e *Engine) markPin(ref, pin string) {
	e.dirty[scopeKey("pin", ref+"."+pin)] = true
	e.globalDirty = true
}

func (e *Engine) markComponentPins(ref string) {
	if c, ok := e.nl.Components[ref]; ok {
		for p := range c.Pins {
			e.markPin(ref, p)
		}
	}
}

func (e *Engine) markNetPins(name string) {
	if n, ok := e.nl.Nets[name]; ok {
		for _, pr := range n.Pins {
			e.markPin(pr.Component, pr.Pin)
		}
	}
}

func (e *Engine) requireFull(reason string) {
	e.fullRequired = true
	e.fullReason = reason
}

// FullRecheckReason explains why Recheck is refusing.
func (e *Engine) FullRecheckReason() string { return e.fullReason }

// ---- supported local mutations ---------------------------------------------

// AddComponent adds a component with its pin definitions.
func (e *Engine) AddComponent(ref, ctype string, pins []netlist.PinSpec) error {
	nets := e.pinNets(ref) // empty for a new component; kept for symmetry
	if err := e.nl.AddComponent(ref, ctype, pins); err != nil {
		return err
	}
	e.markComponentPins(ref)
	for _, n := range nets {
		e.markNet(n)
	}
	return nil
}

// RemoveComponent removes a component and disconnects its pins.
func (e *Engine) RemoveComponent(ref string) error {
	nets := e.pinNets(ref)
	pinScopes := e.allPinScopesOf(ref)
	if err := e.nl.RemoveComponent(ref); err != nil {
		return err
	}
	for _, s := range pinScopes {
		e.dirty[s] = true
	}
	e.globalDirty = true
	for _, n := range nets {
		e.markNet(n)
	}
	return nil
}

// AddNet declares a new net.
func (e *Engine) AddNet(name string, class netlist.NetClass) error {
	if err := e.nl.AddNet(name, class); err != nil {
		return err
	}
	e.markNet(name)
	return nil
}

// RemoveNet removes a net; its pins become floating.
func (e *Engine) RemoveNet(name string) error {
	e.markNetPins(name)
	if err := e.nl.RemoveNet(name); err != nil {
		return err
	}
	e.dirty[scopeKey("net", name)] = true
	e.globalDirty = true
	return nil
}

// RenameNet renames an existing net.
func (e *Engine) RenameNet(oldName, newName string) error {
	e.markNetPins(oldName)
	if err := e.nl.RenameNet(oldName, newName); err != nil {
		return err
	}
	e.dirty[scopeKey("net", oldName)] = true
	e.markNet(newName)
	return nil
}

// MergeNets merges src into dst.
func (e *Engine) MergeNets(src, dst string) error {
	e.markNetPins(src)
	e.markNetPins(dst)
	if err := e.nl.MergeNets(src, dst); err != nil {
		return err
	}
	e.dirty[scopeKey("net", src)] = true
	e.markNet(dst)
	return nil
}

// ConnectPin joins an existing pin to an existing net.
func (e *Engine) ConnectPin(ref, pin, netName string) error {
	if err := e.nl.ConnectPin(ref, pin, netName); err != nil {
		return err
	}
	e.markPin(ref, pin)
	e.markNet(netName)
	return nil
}

// DisconnectPin removes a pin from its net. Incremental tracking of this edit
// is not supported; the model is changed, but Recheck refuses with
// ErrFullRecheckRequired and Check must be used. This is an explicit refusal
// rather than a potentially wrong incremental answer.
func (e *Engine) DisconnectPin(ref, pin string) error {
	c, ok := e.nl.Components[ref]
	if !ok {
		return &netlist.MutationError{Kind: netlist.ErrNotFound, Op: "disconnect_pin",
			Detail: "component " + ref + " does not exist"}
	}
	p, ok := c.Pins[pin]
	if !ok {
		return &netlist.MutationError{Kind: netlist.ErrNotFound, Op: "disconnect_pin",
			Detail: "pin " + ref + "." + pin + " does not exist"}
	}
	netName := p.Net
	if netName == "" {
		return nil // already unconnected
	}
	n := e.nl.Nets[netName]
	filtered := n.Pins[:0]
	for _, pr := range n.Pins {
		if !(pr.Component == ref && pr.Pin == pin) {
			filtered = append(filtered, pr)
		}
	}
	n.Pins = filtered
	p.Net = ""
	e.requireFull("disconnect_pin is not supported incrementally")
	return nil
}

func (e *Engine) pinNets(ref string) []string {
	c, ok := e.nl.Components[ref]
	if !ok {
		return nil
	}
	seen := map[string]bool{}
	for _, p := range c.Pins {
		if p.Net != "" {
			seen[p.Net] = true
		}
	}
	out := make([]string, 0, len(seen))
	for n := range seen {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

func (e *Engine) allPinScopesOf(ref string) []string {
	c, ok := e.nl.Components[ref]
	if !ok {
		return nil
	}
	var out []string
	for p := range c.Pins {
		out = append(out, scopeKey("pin", ref+"."+p))
	}
	sort.Strings(out)
	return out
}
