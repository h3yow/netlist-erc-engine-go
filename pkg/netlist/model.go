// Package netlist defines the unified circuit model: components, pins and nets,
// plus text parsing and supported local mutations.
package netlist

import "sort"

// PinDir describes the electrical direction of a component pin.
type PinDir string

const (
	PinInput    PinDir = "in"
	PinOutput   PinDir = "out"
	PinBidirect PinDir = "inout"
	PinPowerIn  PinDir = "pwr" // power input pin (VCC / GND rail of a part)
	PinPassive  PinDir = "pas"
	PinUnspec   PinDir = "?"
)

// PinKind classifies the power role of a pin for the missing-rail rules.
type PinKind string

const (
	PinKindSignal PinKind = "signal"
	PinKindVCC    PinKind = "vcc"
	PinKindGND    PinKind = "gnd"
)

// Pin is a single pin of a component.
type Pin struct {
	Component string
	Name      string
	Dir       PinDir
	Kind      PinKind
	Net       string // name of the connected net; "" means unconnected
	NoConnect bool   // intentionally left unconnected (.nc)
}

// PinRef identifies a pin within a netlist.
type PinRef struct {
	Component string
	Pin       string
}

// NetClass classifies a net.
type NetClass string

const (
	NetSignal NetClass = "signal"
	NetPower  NetClass = "power"
	NetGround NetClass = "ground"
)

// Net is an electrical node connecting pins.
type Net struct {
	Name  string
	Class NetClass
	Pins  []PinRef
}

// Component is an instance in the design.
type Component struct {
	Ref  string
	Type string
	Pins map[string]*Pin
}

// Netlist is the parsed, validated circuit model.
type Netlist struct {
	Components map[string]*Component
	Nets       map[string]*Net
}

// New returns an empty netlist.
func New() *Netlist {
	return &Netlist{
		Components: map[string]*Component{},
		Nets:       map[string]*Net{},
	}
}

func pinLess(a, b PinRef) bool {
	if a.Component != b.Component {
		return a.Component < b.Component
	}
	return a.Pin < b.Pin
}

// PinsOnNet returns the pins connected to the given net in stable order
// (sorted by component reference then pin name).
func (nl *Netlist) PinsOnNet(name string) []*Pin {
	n, ok := nl.Nets[name]
	if !ok {
		return nil
	}
	refs := make([]PinRef, len(n.Pins))
	copy(refs, n.Pins)
	sort.Slice(refs, func(i, j int) bool { return pinLess(refs[i], refs[j]) })
	out := make([]*Pin, 0, len(refs))
	for _, r := range refs {
		if c, ok := nl.Components[r.Component]; ok {
			if p, ok := c.Pins[r.Pin]; ok {
				out = append(out, p)
			}
		}
	}
	return out
}

// UnconnectedPins returns pins not attached to any net in stable order.
func (nl *Netlist) UnconnectedPins() []*Pin {
	var out []*Pin
	for _, ref := range sortedComponentRefs(nl) {
		c := nl.Components[ref]
		for _, pname := range sortedPinNames(c) {
			p := c.Pins[pname]
			if p.Net == "" {
				out = append(out, p)
			}
		}
	}
	return out
}

func sortedComponentRefs(nl *Netlist) []string {
	refs := make([]string, 0, len(nl.Components))
	for r := range nl.Components {
		refs = append(refs, r)
	}
	sort.Strings(refs)
	return refs
}

func sortedPinNames(c *Component) []string {
	names := make([]string, 0, len(c.Pins))
	for n := range c.Pins {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}
