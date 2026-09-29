package netlist

import (
	"sort"
	"strings"
)

// Export renders the current model as canonical textual netlist. The output is
// deterministic (all definitions sorted) and re-parseable; it is used to
// compare an incrementally edited model against a fresh full parse.
func Export(nl *Netlist) string {
	var b strings.Builder

	refs := make([]string, 0, len(nl.Components))
	for r := range nl.Components {
		refs = append(refs, r)
	}
	sort.Strings(refs)
	for _, ref := range refs {
		c := nl.Components[ref]
		b.WriteString(".comp ")
		b.WriteString(ref)
		b.WriteByte(' ')
		b.WriteString(c.Type)
		pins := make([]string, 0, len(c.Pins))
		for pn := range c.Pins {
			pins = append(pins, pn)
		}
		sort.Strings(pins)
		for _, pn := range pins {
			p := c.Pins[pn]
			b.WriteByte(' ')
			b.WriteString(pn)
			b.WriteByte(':')
			b.WriteString(string(p.Dir))
			if p.Kind != PinKindSignal {
				b.WriteByte('/')
				b.WriteString(string(p.Kind))
			}
		}
		b.WriteByte('\n')
	}

	names := make([]string, 0, len(nl.Nets))
	for n := range nl.Nets {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		n := nl.Nets[name]
		b.WriteString(".net ")
		b.WriteString(name)
		if n.Class != NetSignal {
			b.WriteByte(' ')
			b.WriteString(string(n.Class))
		}
		b.WriteByte('\n')
	}
	for _, name := range names {
		n := nl.Nets[name]
		if len(n.Pins) == 0 {
			continue
		}
		refs2 := append([]PinRef(nil), n.Pins...)
		sort.Slice(refs2, func(i, j int) bool { return pinLess(refs2[i], refs2[j]) })
		b.WriteString(".join ")
		b.WriteString(name)
		for _, pr := range refs2 {
			b.WriteByte(' ')
			b.WriteString(pr.Component)
			b.WriteByte('.')
			b.WriteString(pr.Pin)
		}
		b.WriteByte('\n')
	}

	for _, ref := range refs {
		c := nl.Components[ref]
		var ncs []string
		for pn, p := range c.Pins {
			if p.NoConnect {
				ncs = append(ncs, ref+"."+pn)
			}
		}
		sort.Strings(ncs)
		for _, x := range ncs {
			b.WriteString(".nc ")
			b.WriteString(x)
			b.WriteByte('\n')
		}
	}
	return b.String()
}
