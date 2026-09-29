package erc

import (
	"errors"
	"fmt"
	"sort"
	"testing"

	"github.com/h3yow/netlist-erc-engine-go/pkg/netlist"
)

func idMap(fs []Finding) map[string]Finding {
	m := map[string]Finding{}
	for _, f := range fs {
		m[f.ID] = f
	}
	return m
}

// freshFull re-parses the given source and runs a full check, simulating
// "re-check from scratch" against the expected post-mutation netlist text.
func freshFull(t *testing.T, src string) map[string]Finding {
	t.Helper()
	nl, err := netlist.ParseString(src)
	if err != nil {
		t.Fatalf("fresh parse failed: %v\nnetlist:\n%s", err, src)
	}
	rep, _ := NewEngine(nl, DefaultRuleSet()).Check()
	return idMap(rep.Findings)
}

// assertEqualFindingSets compares incremental and full results and on failure
// prints the input netlist text plus the judgement basis (diffs both ways).
func assertEqualFindingSets(t *testing.T, inc []Finding, full map[string]Finding, src, step string) {
	t.Helper()
	incM := idMap(inc)
	var missing, stale, changed []string
	for id, f := range full {
		g, ok := incM[id]
		switch {
		case !ok:
			missing = append(missing, id)
		case g != f:
			changed = append(changed, fmt.Sprintf("%s: inc=%+v full=%+v", id, g, f))
		}
	}
	for id := range incM {
		if _, ok := full[id]; !ok {
			stale = append(stale, id)
		}
	}
	sort.Strings(missing)
	sort.Strings(stale)
	sort.Strings(changed)
	if len(missing)+len(stale)+len(changed) > 0 {
		t.Fatalf("incremental != full after %s\n"+
			"--- input netlist ---\n%s\n"+
			"--- judgement basis ---\nmissing by incremental: %v\nstale from old state:   %v\nchanged findings:       %v\n"+
			"incremental IDs: %v\nfull IDs:        %v",
			step, src, missing, stale, changed,
			sortedKeys(incM), sortedKeys(full))
	}
}

func sortedKeys(m map[string]Finding) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

const baseDesign = `
.comp U1 MCU V:pwr/vcc G:pwr/gnd A:in B:out
.comp U2 BUF A:in Y:out
.net VCC power
.net GND ground
.net N1
.join VCC U1.V
.join GND U1.G
.join N1 U1.B U2.A
.nc U1.A
.nc U2.Y
`

func TestIncrementalAddComponent(t *testing.T) {
	nl, _ := netlist.ParseString(baseDesign)
	eng := NewEngine(nl, DefaultRuleSet())
	eng.Check()

	if err := eng.AddComponent("U3", "OSC", []netlist.PinSpec{
		{Name: "A", Dir: netlist.PinOutput},
	}); err != nil {
		t.Fatal(err)
	}
	rep, err := eng.Recheck()
	if err != nil {
		t.Fatalf("recheck: %v", err)
	}
	full := baseDesign + ".comp U3 OSC A:out\n"
	assertEqualFindingSets(t, rep.Findings, freshFull(t, full), full, "add U3 (A floating)")

	// Second mutation: connect U3.A onto N1 -> now two drivers on N1.
	if err := eng.ConnectPin("U3", "A", "N1"); err != nil {
		t.Fatal(err)
	}
	rep, err = eng.Recheck()
	if err != nil {
		t.Fatalf("recheck: %v", err)
	}
	joined := baseDesign + ".comp U3 OSC A:out\n.join N1 U3.A\n"
	assertEqualFindingSets(t, rep.Findings, freshFull(t, joined), joined, "connect U3.A -> N1")
}

func TestIncrementalRemoveComponent(t *testing.T) {
	// U3 drives N1 together with U1.B; removing U2 (an input on N1) must
	// keep the conflict; removing U3 must clear it.
	design := baseDesign + ".comp U3 OSC A:out\n.join N1 U3.A\n"
	nl, _ := netlist.ParseString(design)
	eng := NewEngine(nl, DefaultRuleSet())
	eng.Check()

	if err := eng.RemoveComponent("U2"); err != nil {
		t.Fatal(err)
	}
	rep, err := eng.Recheck()
	if err != nil {
		t.Fatalf("recheck: %v", err)
	}
	afterRemoveU2 := `
.comp U1 MCU V:pwr/vcc G:pwr/gnd A:in B:out
.net VCC power
.net GND ground
.net N1
.join VCC U1.V
.join GND U1.G
.join N1 U1.B
.nc U1.A
.comp U3 OSC A:out
.join N1 U3.A
`
	assertEqualFindingSets(t, rep.Findings, freshFull(t, afterRemoveU2), afterRemoveU2, "remove U2")

	if err := eng.RemoveComponent("U3"); err != nil {
		t.Fatal(err)
	}
	rep, err = eng.Recheck()
	if err != nil {
		t.Fatalf("recheck: %v", err)
	}
	afterRemoveU3 := `
.comp U1 MCU V:pwr/vcc G:pwr/gnd A:in B:out
.net VCC power
.net GND ground
.net N1
.join VCC U1.V
.join GND U1.G
.join N1 U1.B
.nc U1.A
`
	assertEqualFindingSets(t, rep.Findings, freshFull(t, afterRemoveU3), afterRemoveU3, "remove U3")
}

func TestIncrementalNetAddRenameMergeRemove(t *testing.T) {
	nl, _ := netlist.ParseString(baseDesign)
	eng := NewEngine(nl, DefaultRuleSet())
	eng.Check()

	// Orphan net appears.
	if err := eng.AddNet("ORPH", netlist.NetSignal); err != nil {
		t.Fatal(err)
	}
	rep, err := eng.Recheck()
	if err != nil {
		t.Fatalf("recheck: %v", err)
	}
	withOrph := baseDesign + ".net ORPH\n"
	assertEqualFindingSets(t, rep.Findings, freshFull(t, withOrph), withOrph, "add orphan net")

	// Rename the orphan: old finding must vanish, new one appear.
	if err := eng.RenameNet("ORPH", "ORPH2"); err != nil {
		t.Fatal(err)
	}
	rep, err = eng.Recheck()
	if err != nil {
		t.Fatalf("recheck: %v", err)
	}
	renamed := baseDesign + ".net ORPH2\n"
	assertEqualFindingSets(t, rep.Findings, freshFull(t, renamed), renamed, "rename orphan")

	// Merge orphan into N1: no orphan left.
	if err := eng.MergeNets("ORPH2", "N1"); err != nil {
		t.Fatal(err)
	}
	rep, err = eng.Recheck()
	if err != nil {
		t.Fatalf("recheck: %v", err)
	}
	merged := `
.comp U1 MCU V:pwr/vcc G:pwr/gnd A:in B:out
.comp U2 BUF A:in Y:out
.net VCC power
.net GND ground
.net N1
.join VCC U1.V
.join GND U1.G
.join N1 U1.B U2.A
.nc U1.A
.nc U2.Y
`
	assertEqualFindingSets(t, rep.Findings, freshFull(t, merged), merged, "merge ORPH2 into N1")

	// Remove N1: pins float, net finding disappears, floating findings appear.
	if err := eng.RemoveNet("N1"); err != nil {
		t.Fatal(err)
	}
	rep, err = eng.Recheck()
	if err != nil {
		t.Fatalf("recheck: %v", err)
	}
	removed := `
.comp U1 MCU V:pwr/vcc G:pwr/gnd A:in B:out
.comp U2 BUF A:in Y:out
.net VCC power
.net GND ground
.join VCC U1.V
.join GND U1.G
.nc U1.A
.nc U2.Y
`
	assertEqualFindingSets(t, rep.Findings, freshFull(t, removed), removed, "remove N1")
}

func TestIncrementalMergeConflictRejected(t *testing.T) {
	src := `.comp U1 X V:pwr/vcc G:pwr/gnd
.net VCC power
.net GND ground
.join VCC U1.V
.join GND U1.G`
	nl, err := netlist.ParseString(src)
	if err != nil {
		t.Fatal(err)
	}
	eng := NewEngine(nl, DefaultRuleSet())
	eng.Check()
	err = eng.MergeNets("VCC", "GND")
	var me *netlist.MutationError
	if !errors.As(err, &me) || me.Kind != netlist.ErrConflict {
		t.Fatalf("merge rails err = %v, want conflict", err)
	}
}

func TestUnsupportedChangeIsRefused(t *testing.T) {
	nl, _ := netlist.ParseString(baseDesign)
	eng := NewEngine(nl, DefaultRuleSet())
	eng.Check()

	if err := eng.DisconnectPin("U2", "A"); err != nil {
		t.Fatalf("mutation applied: %v", err)
	}
	if _, err := eng.Recheck(); !errors.Is(err, ErrFullRecheckRequired) {
		t.Fatalf("recheck err = %v, want ErrFullRecheckRequired", err)
	}
	// Full check still gives the correct answer and clears the refusal.
	rep, err := eng.Check()
	if err != nil {
		t.Fatalf("check after unsupported edit: %v", err)
	}
	disconnected := `
.comp U1 MCU V:pwr/vcc G:pwr/gnd A:in B:out
.comp U2 BUF A:in Y:out
.net VCC power
.net GND ground
.net N1
.join VCC U1.V
.join GND U1.G
.join N1 U1.B
.nc U1.A
.nc U2.Y
`
	assertEqualFindingSets(t, rep.Findings, freshFull(t, disconnected), disconnected, "full check after disconnect")

	// A subsequent incremental recheck works again.
	rep2, err := eng.Recheck()
	if err != nil {
		t.Fatalf("recheck after full check: %v", err)
	}
	assertEqualFindingSets(t, rep2.Findings, idMap(rep.Findings), disconnected, "recheck no-op after full")
}

func TestRuleSetChangeReproducibility(t *testing.T) {
	nl, _ := netlist.ParseString(baseDesign)
	eng := NewEngine(nl, DefaultRuleSet())
	old, _ := eng.Check()
	if old.RuleSetVersion != RuleVersion {
		t.Fatalf("old report version = %d", old.RuleSetVersion)
	}

	// Disable the floating-pin rule.
	rs := DefaultRuleSet()
	rs.Rules[RuleFloatingPin] = false
	eng.SetRuleSet(rs)
	rep, err := eng.Recheck()
	if err != nil {
		t.Fatalf("recheck after rule set change: %v", err)
	}
	for _, f := range rep.Findings {
		if f.Rule == RuleFloatingPin {
			t.Fatalf("disabled rule still reported: %v", f)
		}
	}
	// Parameter change: rename the power rail names list and re-check missing
	// rails; the old report remains the historical reproducible artifact.
	rs2 := DefaultRuleSet()
	rs2.Params.PowerNetNames = []string{"MAINS"}
	eng.SetRuleSet(rs2)
	rep2, _ := eng.Recheck()
	if rep2.RuleSetVersion != old.RuleSetVersion {
		t.Fatalf("report should still tag the rule-set version it ran with")
	}
	if len(old.Findings) != 0 {
		t.Fatalf("base design should be clean under default set, got %v", old.Findings)
	}
}
