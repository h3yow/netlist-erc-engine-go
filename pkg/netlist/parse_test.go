package netlist

import (
	"errors"
	"strings"
	"testing"
)

func TestParseValid(t *testing.T) {
	src := `
.comp U1 MCU A:in B:out VCC:pwr/vcc GND:pwr/gnd
.net VCC power
.net GND ground
.net N1
.join VCC U1.VCC
.join GND U1.GND
.join N1 U1.A
.nc U1.B
`
	nl, err := ParseString(src)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(nl.Components) != 1 || len(nl.Nets) != 3 {
		t.Fatalf("unexpected model: %+v", nl)
	}
	if nl.Components["U1"].Pins["VCC"].Kind != PinKindVCC {
		t.Fatalf("VCC pin kind = %v", nl.Components["U1"].Pins["VCC"].Kind)
	}
	if nl.Nets["N1"].Class != NetSignal {
		t.Fatalf("default class = %v", nl.Nets["N1"].Class)
	}
	if !nl.Components["U1"].Pins["B"].NoConnect {
		t.Fatalf("B should be marked .nc")
	}
	if got := nl.Components["U1"].Pins["A"].Net; got != "N1" {
		t.Fatalf("A net = %q", got)
	}
}

func TestParseAutoKind(t *testing.T) {
	nl, err := ParseString(".comp U1 X VDD:pwr VSS:pwr\n.net V power\n.net G ground\n.join V U1.VDD\n.join G U1.VSS")
	if err != nil {
		t.Fatal(err)
	}
	if nl.Components["U1"].Pins["VDD"].Kind != PinKindVCC {
		t.Fatalf("VDD auto kind = %v", nl.Components["U1"].Pins["VDD"].Kind)
	}
	if nl.Components["U1"].Pins["VSS"].Kind != PinKindGND {
		t.Fatalf("VSS auto kind = %v", nl.Components["U1"].Pins["VSS"].Kind)
	}
}

func TestMalformedInputsClassified(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want ErrorKind
		line int
	}{
		{"syntax no directive", ".comp U1 X A:in\nB:out", ErrSyntax, 2},
		{"unknown directive", ".foo U1", ErrUnknownDirective, 1},
		{"missing field comp", ".comp U1", ErrMissingField, 1},
		{"invalid pin dir", ".comp U1 X A:weird", ErrInvalidValue, 1},
		{"invalid net class", ".net N1 wat", ErrInvalidValue, 1},
		{"power pin missing kind", ".comp U1 X P:pwr", ErrMissingField, 1},
		{"invalid pin kind", ".comp U1 X A:in/foo", ErrInvalidValue, 1},
		{"duplicate component", ".comp U1 X A:in\n.comp U1 Y B:in", ErrDuplicateDef, 2},
		{"duplicate net", ".net N1\n.net N1", ErrDuplicateDef, 2},
		{"unknown net", ".comp U1 X A:in\n.join N1 U1.A", ErrUnknownNet, 2},
		{"unknown component", ".net N1\n.join N1 U9.A", ErrUnknownComponent, 2},
		{"unknown pin", ".comp U1 X A:in\n.net N1\n.join N1 U1.Z", ErrUnknownPin, 3},
		{"pin joined twice", ".comp U1 X A:in B:in\n.net N1\n.net N2\n.join N1 U1.A\n.join N2 U1.A", ErrPinAlreadyJoined, 5},
		{"nc then join", ".comp U1 X A:in\n.net N1\n.nc U1.A\n.join N1 U1.A", ErrConflict, 4},
		{"bad pin reference", ".comp U1 X A:in\n.net N1\n.join N1 U1A", ErrSyntax, 3},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseString(tc.src)
			if err == nil {
				t.Fatalf("input accepted, want %s\nnetlist:\n%s", tc.want, tc.src)
			}
			var pe *ParseError
			if !errors.As(err, &pe) {
				t.Fatalf("error %v is not *ParseError\nnetlist:\n%s", err, tc.src)
			}
			if pe.Kind != tc.want {
				t.Fatalf("kind = %s, want %s at %d:%d\ninput:\n%s\nerror: %v",
					pe.Kind, tc.want, pe.Line, pe.Column, tc.src, err)
			}
			if tc.line != 0 && pe.Line != tc.line {
				t.Fatalf("line = %d, want %d\ninput:\n%s", pe.Line, tc.line, tc.src)
			}
			if !strings.Contains(err.Error(), "line") {
				t.Fatalf("error must point at a position: %v", err)
			}
		})
	}
}

func TestCommentsAndEnd(t *testing.T) {
	nl, err := ParseString("# comment\n.net N1\n.end\n.net N2\n")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := nl.Nets["N2"]; ok {
		t.Fatalf("content after .end must be ignored")
	}
}

func TestMutationRejections(t *testing.T) {
	nl, _ := ParseString(".comp U1 X A:in\n.net N1\n.join N1 U1.A")
	if err := nl.AddComponent("U1", "Y", nil); !isKind(err, ErrConflict) {
		t.Fatalf("dup component err = %v", err)
	}
	if err := nl.AddComponent("", "Y", nil); !isKind(err, ErrMissingField) {
		t.Fatalf("empty ref err = %v", err)
	}
	if err := nl.RemoveComponent("NOPE"); !isKind(err, ErrNotFound) {
		t.Fatalf("missing comp err = %v", err)
	}
	if err := nl.RenameNet("N1", "N1"); err != nil {
		t.Fatalf("rename to self should be no-op, got %v", err)
	}
	if err := nl.MergeNets("N1", "N1"); !isKind(err, ErrConflict) {
		t.Fatalf("merge self err = %v", err)
	}
}

func isKind(err error, k ErrorKind) bool {
	var me *MutationError
	if errors.As(err, &me) {
		return me.Kind == k
	}
	return false
}
