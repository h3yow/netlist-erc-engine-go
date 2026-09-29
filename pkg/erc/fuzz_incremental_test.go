package erc

import (
	"fmt"
	"math/rand"
	"testing"

	"github.com/h3yow/netlist-erc-engine-go/pkg/netlist"
)

// TestRandomMutationSequence drives a long random sequence of the supported
// mutations through the incremental engine. After every single step it
// exports the model to text, re-parses it, runs a full check and asserts the
// incremental conclusions are identical.
func TestRandomMutationSequence(t *testing.T) {
	seed := int64(20260929)
	rng := rand.New(rand.NewSource(seed))

	start := `.comp U1 MCU V:pwr/vcc G:pwr/gnd A:in B:out Y:out
.net VCC power
.net GND ground
.net N1
.join VCC U1.V
.join GND U1.G
.join N1 U1.B
.nc U1.A
`
	nl, err := netlist.ParseString(start)
	if err != nil {
		t.Fatal(err)
	}
	eng := NewEngine(nl, DefaultRuleSet())
	eng.Check()

	uid := 2 // next U number
	// candidate unconnected pins available for connecting, refreshed per step.
	type pr struct{ ref, pin, net string }

	for step := 0; step < 400; step++ {
		// Apply one random supported mutation.
		switch rng.Intn(5) {
		case 0: // add component
			ref := fmt.Sprintf("U%d", uid)
			uid++
			var pins []netlist.PinSpec
			switch rng.Intn(3) {
			case 0:
				pins = []netlist.PinSpec{{Name: "A", Dir: netlist.PinInput}}
			case 1:
				pins = []netlist.PinSpec{{Name: "Y", Dir: netlist.PinOutput}}
			default:
				pins = []netlist.PinSpec{
					{Name: "V", Dir: netlist.PinPowerIn, Kind: netlist.PinKindVCC},
					{Name: "G", Dir: netlist.PinPowerIn, Kind: netlist.PinKindGND},
					{Name: "A", Dir: netlist.PinInput},
				}
			}
			if err := eng.AddComponent(ref, "X", pins); err != nil {
				t.Fatalf("step %d add: %v", step, err)
			}
		case 1: // remove a random component
			refs := allRefs(nl)
			if len(refs) > 1 {
				ref := refs[rng.Intn(len(refs))]
				if err := eng.RemoveComponent(ref); err != nil {
					t.Fatalf("step %d remove %s: %v", step, ref, err)
				}
			}
		case 2: // add a random net
			name := fmt.Sprintf("NET%d", rng.Intn(5))
			_ = eng.AddNet(name, netlist.NetSignal)
		case 3: // merge two random signal nets
			names := signalNets(nl)
			if len(names) >= 2 {
				i, j := rng.Intn(len(names)), rng.Intn(len(names))
				if i != j {
					_ = eng.MergeNets(names[i], names[j])
				}
			}
		case 4: // connect a random floating pin to a random net
			var cands []pr
			for _, ref := range allRefs(nl) {
				c := nl.Components[ref]
				for pname, p := range c.Pins {
					if p.Net == "" && !p.NoConnect {
						cands = append(cands, pr{ref: ref, pin: pname})
					}
				}
			}
			names := signalNets(nl)
			if len(cands) > 0 && len(names) > 0 {
				c := cands[rng.Intn(len(cands))]
				n := names[rng.Intn(len(names))]
				_ = eng.ConnectPin(c.ref, c.pin, n)
			}
		}

		rep, rerr := eng.Recheck()
		if rerr != nil {
			t.Fatalf("step %d: recheck refused although only supported edits were made: %v", step, rerr)
		}

		text := netlist.Export(nl)
		assertEqualFindingSets(t, rep.Findings, freshFull(t, text), text,
			fmt.Sprintf("random step %d (seed %d)", step, seed))
	}
}

func allRefs(nl *netlist.Netlist) []string {
	var out []string
	for r := range nl.Components {
		out = append(out, r)
	}
	return out
}

func signalNets(nl *netlist.Netlist) []string {
	var out []string
	for n, net := range nl.Nets {
		if net.Class == netlist.NetSignal {
			out = append(out, n)
		}
	}
	return out
}
