package netlist

import (
	"fmt"
	"sort"
	"strings"
)

// PinSpec is used when adding a component via mutation.
type PinSpec struct {
	Name string
	Dir  PinDir
	Kind PinKind
}

// AddComponent adds a new component to the netlist.
func (nl *Netlist) AddComponent(ref, ctype string, pins []PinSpec) error {
	if ref == "" {
		return &MutationError{Kind: ErrMissingField, Op: "add_component", Detail: "component reference is empty"}
	}
	if _, dup := nl.Components[ref]; dup {
		return &MutationError{Kind: ErrConflict, Op: "add_component",
			Detail: fmt.Sprintf("component %q already exists", ref)}
	}
	c := &Component{Ref: ref, Type: ctype, Pins: map[string]*Pin{}}
	for _, ps := range pins {
		dir, kind, err := normalizePinSpec(ps.Name, ps.Dir, ps.Kind)
		if err != nil {
			return err
		}
		if _, dup := c.Pins[ps.Name]; dup {
			return &MutationError{Kind: ErrDuplicateDef, Op: "add_component",
				Detail: fmt.Sprintf("component %q pin %q listed twice", ref, ps.Name)}
		}
		c.Pins[ps.Name] = &Pin{Component: ref, Name: ps.Name, Dir: dir, Kind: kind}
	}
	nl.Components[ref] = c
	return nil
}

func normalizePinSpec(name string, dir PinDir, kind PinKind) (PinDir, PinKind, error) {
	switch dir {
	case PinInput, PinOutput, PinBidirect, PinPowerIn, PinPassive, PinUnspec:
	default:
		return "", "", &MutationError{Kind: ErrInvalidValue, Op: "add_component",
			Detail: fmt.Sprintf("pin %q has invalid direction %q", name, dir)}
	}
	switch kind {
	case "", PinKindSignal, PinKindVCC, PinKindGND:
	default:
		return "", "", &MutationError{Kind: ErrInvalidValue, Op: "add_component",
			Detail: fmt.Sprintf("pin %q has invalid kind %q", name, kind)}
	}
	if kind == "" {
		kind = PinKindSignal
	}
	if dir == PinPowerIn && kind == PinKindSignal {
		switch {
		case containsAny(strings.ToLower(name), "gnd", "vss"):
			kind = PinKindGND
		case containsAny(strings.ToLower(name), "vcc", "vdd"):
			kind = PinKindVCC
		default:
			return "", "", &MutationError{Kind: ErrMissingField, Op: "add_component",
				Detail: fmt.Sprintf("power pin %q must declare kind vcc or gnd", name)}
		}
	}
	return dir, kind, nil
}

// netsOfComponent returns the names of nets the component is joined to.
func (nl *Netlist) netsOfComponent(ref string) []string {
	seen := map[string]bool{}
	c := nl.Components[ref]
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

// RemoveComponent removes a component and disconnects its pins.
func (nl *Netlist) RemoveComponent(ref string) error {
	c, ok := nl.Components[ref]
	if !ok {
		return &MutationError{Kind: ErrNotFound, Op: "remove_component",
			Detail: fmt.Sprintf("component %q does not exist", ref)}
	}
	for _, p := range c.Pins {
		if p.Net == "" {
			continue
		}
		if n, ok := nl.Nets[p.Net]; ok {
			filtered := n.Pins[:0]
			for _, pr := range n.Pins {
				if pr.Component != ref {
					filtered = append(filtered, pr)
				}
			}
			n.Pins = filtered
		}
	}
	delete(nl.Components, ref)
	return nil
}

// AddNet declares a new net.
func (nl *Netlist) AddNet(name string, class NetClass) error {
	if name == "" {
		return &MutationError{Kind: ErrMissingField, Op: "add_net", Detail: "net name is empty"}
	}
	switch class {
	case "", NetSignal, NetPower, NetGround:
	default:
		return &MutationError{Kind: ErrInvalidValue, Op: "add_net",
			Detail: fmt.Sprintf("net %q has invalid class %q", name, class)}
	}
	if class == "" {
		class = NetSignal
	}
	if _, dup := nl.Nets[name]; dup {
		return &MutationError{Kind: ErrConflict, Op: "add_net",
			Detail: fmt.Sprintf("net %q already exists", name)}
	}
	nl.Nets[name] = &Net{Name: name, Class: class}
	return nil
}

// RemoveNet removes a net; its pins become unconnected.
func (nl *Netlist) RemoveNet(name string) error {
	n, ok := nl.Nets[name]
	if !ok {
		return &MutationError{Kind: ErrNotFound, Op: "remove_net",
			Detail: fmt.Sprintf("net %q does not exist", name)}
	}
	for _, pr := range n.Pins {
		if c, ok := nl.Components[pr.Component]; ok {
			if p, ok := c.Pins[pr.Pin]; ok {
				p.Net = ""
			}
		}
	}
	delete(nl.Nets, name)
	return nil
}

// RenameNet renames a net in place.
func (nl *Netlist) RenameNet(oldName, newName string) error {
	if oldName == "" || newName == "" {
		return &MutationError{Kind: ErrMissingField, Op: "rename_net", Detail: "net names must not be empty"}
	}
	if oldName == newName {
		return nil
	}
	n, ok := nl.Nets[oldName]
	if !ok {
		return &MutationError{Kind: ErrNotFound, Op: "rename_net",
			Detail: fmt.Sprintf("net %q does not exist", oldName)}
	}
	if _, dup := nl.Nets[newName]; dup {
		return &MutationError{Kind: ErrConflict, Op: "rename_net",
			Detail: fmt.Sprintf("net %q already exists; use merge_nets to join nets", newName)}
	}
	n.Name = newName
	for _, pr := range n.Pins {
		if c, ok := nl.Components[pr.Component]; ok {
			if p, ok := c.Pins[pr.Pin]; ok {
				p.Net = newName
			}
		}
	}
	delete(nl.Nets, oldName)
	nl.Nets[newName] = n
	return nil
}

// MergeNets merges src into dst. All pins on src move to dst and src is
// removed. power and ground nets cannot be merged with each other.
func (nl *Netlist) MergeNets(src, dst string) error {
	if src == dst {
		return &MutationError{Kind: ErrConflict, Op: "merge_nets", Detail: "cannot merge a net into itself"}
	}
	ns, ok1 := nl.Nets[src]
	nd, ok2 := nl.Nets[dst]
	if !ok1 || !ok2 {
		missing := src
		if !ok2 {
			missing = dst
		}
		return &MutationError{Kind: ErrNotFound, Op: "merge_nets",
			Detail: fmt.Sprintf("net %q does not exist", missing)}
	}
	merged, errKind := mergeClass(ns.Class, nd.Class)
	if errKind != "" {
		return &MutationError{Kind: errKind, Op: "merge_nets",
			Detail: fmt.Sprintf("cannot merge %s net %q into %s net %q", ns.Class, src, nd.Class, dst)}
	}
	existing := map[PinRef]bool{}
	for _, pr := range nd.Pins {
		existing[pr] = true
	}
	for _, pr := range ns.Pins {
		if c, ok := nl.Components[pr.Component]; ok {
			if p, ok := c.Pins[pr.Pin]; ok {
				p.Net = dst
			}
		}
		if !existing[pr] {
			nd.Pins = append(nd.Pins, pr)
			existing[pr] = true
		}
	}
	nd.Class = merged
	delete(nl.Nets, src)
	return nil
}

func mergeClass(a, b NetClass) (NetClass, ErrorKind) {
	if a == b || a == NetSignal {
		return b, ""
	}
	if b == NetSignal {
		return a, ""
	}
	return "", ErrConflict
}

// ConnectPin attaches an existing pin to an existing net.
func (nl *Netlist) ConnectPin(ref, pin, netName string) error {
	c, ok := nl.Components[ref]
	if !ok {
		return &MutationError{Kind: ErrNotFound, Op: "connect_pin",
			Detail: fmt.Sprintf("component %q does not exist", ref)}
	}
	p, ok := c.Pins[pin]
	if !ok {
		return &MutationError{Kind: ErrNotFound, Op: "connect_pin",
			Detail: fmt.Sprintf("component %q has no pin %q", ref, pin)}
	}
	if _, ok := nl.Nets[netName]; !ok {
		return &MutationError{Kind: ErrNotFound, Op: "connect_pin",
			Detail: fmt.Sprintf("net %q does not exist", netName)}
	}
	if p.Net != "" {
		return &MutationError{Kind: ErrPinAlreadyJoined, Op: "connect_pin",
			Detail: fmt.Sprintf("pin %s.%s already joined to net %q", ref, pin, p.Net)}
	}
	if p.NoConnect {
		return &MutationError{Kind: ErrConflict, Op: "connect_pin",
			Detail: fmt.Sprintf("pin %s.%s is marked .nc", ref, pin)}
	}
	p.Net = netName
	n := nl.Nets[netName]
	n.Pins = append(n.Pins, PinRef{Component: ref, Pin: pin})
	return nil
}
