package erc

import (
	"strings"
	"testing"

	"github.com/h3yow/netlist-erc-engine-go/pkg/netlist"
)

func ruleIDs(fs []Finding) map[RuleID][]string {
	m := map[RuleID][]string{}
	for _, f := range fs {
		m[f.Rule] = append(m[f.Rule], f.ID)
	}
	return m
}

func check(t *testing.T, src string) []Finding {
	t.Helper()
	nl, err := netlist.ParseString(src)
	if err != nil {
		t.Fatalf("parse failed: %v\nnetlist:\n%s", err, src)
	}
	eng := NewEngine(nl, DefaultRuleSet())
	rep, err := eng.Check()
	if err != nil {
		t.Fatalf("check failed: %v", err)
	}
	return rep.Findings
}

func TestFloatingPin(t *testing.T) {
	fs := check(t, ".comp U1 X A:in B:out\n.nc U1.A")
	ids := ruleIDs(fs)[RuleFloatingPin]
	if len(ids) != 1 || ids[0] != "ERC001-pin:U1.B" {
		t.Fatalf("floating ids = %v, want only ERC001-pin:U1.B", ids)
	}
}

func TestOrphanNet(t *testing.T) {
	fs := check(t, ".comp U1 X A:in\n.net FLOATING\n.net N1\n.join N1 U1.A")
	ids := ruleIDs(fs)[RuleOrphanNet]
	if len(ids) != 1 || ids[0] != "ERC002-net:FLOATING" {
		t.Fatalf("orphan ids = %v", ids)
	}
}

func TestMultipleDrivers(t *testing.T) {
	src := `.comp U1 X A:out B:in
.comp U2 X A:out B:in
.comp U3 X A:out
.net N1
.join N1 U1.A U2.A U3.A U1.B U2.B`
	fs := check(t, src)
	var f *Finding
	for i := range fs {
		if fs[i].Rule == RuleMultipleDrivers {
			f = &fs[i]
		}
	}
	if f == nil {
		t.Fatalf("expected multi-driver finding\nnetlist:\n%s", src)
	}
	if f.ID != "ERC003-net:N1" {
		t.Fatalf("id = %s", f.ID)
	}
	if f.Severity != SeverityError {
		t.Fatalf("severity = %s", f.Severity)
	}
	for _, want := range []string{"U1.A", "U2.A", "U3.A"} {
		if !strings.Contains(f.Message, want) {
			t.Fatalf("message %q must list driver %s\nnetlist:\n%s", f.Message, want, src)
		}
	}
}

func TestMultipleDriversIgnoredOnRails(t *testing.T) {
	src := `.comp U1 X V:pwr/vcc
.comp U2 X V:pwr/vcc
.net VCC power
.join VCC U1.V U2.V`
	fs := check(t, src)
	for _, f := range fs {
		if f.Rule == RuleMultipleDrivers {
			t.Fatalf("power rail must not trigger driver conflict:\n%v\nnetlist:\n%s", f, src)
		}
	}
}

func TestMissingRails(t *testing.T) {
	src := `.comp U1 X V:pwr/vcc G:pwr/gnd A:in
.net VCC power
.join VCC U1.V`
	fs := check(t, src)
	got := ruleIDs(fs)
	var sawComp, sawDesign bool
	for _, id := range got[RuleMissingGroundRail] {
		if id == "ERC005-component:U1" {
			sawComp = true
		}
		if id == "ERC005-design:no_ground_net" {
			sawDesign = true
		}
	}
	if !sawComp || !sawDesign {
		t.Fatalf("missing-ground ids = %v\nnetlist:\n%s", got[RuleMissingGroundRail], src)
	}
	if len(got[RuleMissingPowerRail]) != 0 {
		t.Fatalf("power is connected, got %v", got[RuleMissingPowerRail])
	}
}

func TestNoFindingsOnCleanDesign(t *testing.T) {
	src := `.comp U1 X V:pwr/vcc G:pwr/gnd A:in Y:out
.comp U2 X A:in
.net VCC power
.net GND ground
.net N1
.join VCC U1.V
.join GND U1.G
.join N1 U1.Y U2.A
.nc U1.A`
	if fs := check(t, src); len(fs) != 0 {
		t.Fatalf("clean design produced findings: %+v\nnetlist:\n%s", fs, src)
	}
}

func TestRepeatCheckIsIdempotent(t *testing.T) {
	src := `.comp U1 X V:pwr/vcc G:pwr/gnd A:in
.net VCC power
.join VCC U1.V`
	nl, _ := netlist.ParseString(src)
	eng := NewEngine(nl, DefaultRuleSet())
	r1, _ := eng.Check()
	r2, _ := eng.Check()
	if !equalReports(r1, r2) {
		t.Fatalf("repeat check differs:\n%+v\n%+v", r1, r2)
	}
	seen := map[string]bool{}
	for _, f := range r1.Findings {
		if seen[f.ID] {
			t.Fatalf("duplicate finding id %s", f.ID)
		}
		seen[f.ID] = true
		if f.RuleVersion == 0 {
			t.Fatalf("finding %s has no rule version", f.ID)
		}
	}
}

func equalReports(a, b Report) bool {
	if a.RuleSetVersion != b.RuleSetVersion || len(a.Findings) != len(b.Findings) {
		return false
	}
	for i := range a.Findings {
		if a.Findings[i] != b.Findings[i] {
			return false
		}
	}
	return true
}
