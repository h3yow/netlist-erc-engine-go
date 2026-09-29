package netlist

import (
	"bufio"
	"fmt"
	"io"
	"strings"
)

// Text netlist format (line oriented, '#' starts a comment):
//
//	.comp <ref> <type> <pin>:<dir>[/<kind>] ...
//	.net  <name> [signal|power|ground]
//	.join <net> <ref>.<pin> [<ref>.<pin> ...]
//	.nc   <ref>.<pin> ...
//	.end
//
// Pins are unconnected until referenced by a .join line. .nc marks a pin as
// intentionally not connected. Definitions may appear in any order.
func Parse(r io.Reader) (*Netlist, error) {
	nl := New()
	type lineT struct {
		no     int
		dir    string
		tokens []string
		cols   []int
	}
	var lines []lineT

	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	ln := 0
	for scanner.Scan() {
		ln++
		raw := scanner.Text()
		if i := strings.IndexByte(raw, '#'); i >= 0 {
			raw = raw[:i]
		}
		toks, cols := tokenize(raw)
		if len(toks) == 0 {
			continue
		}
		d := toks[0]
		if !strings.HasPrefix(d, ".") {
			return nil, &ParseError{Kind: ErrSyntax, Line: ln, Column: cols[0],
				Detail: fmt.Sprintf("expected a directive beginning with '.', got %q", d)}
		}
		switch d {
		case ".comp", ".net", ".join", ".nc", ".end":
		default:
			return nil, &ParseError{Kind: ErrUnknownDirective, Line: ln, Column: cols[0],
				Directive: d, Detail: "unknown directive"}
		}
		lines = append(lines, lineT{ln, d, toks, cols})
		if d == ".end" {
			break
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, &ParseError{Kind: ErrSyntax, Line: ln, Detail: err.Error()}
	}

	// Pass 1: definitions.
	for _, l := range lines {
		switch l.dir {
		case ".comp":
			if err := parseComp(nl, l); err != nil {
				return nil, err
			}
		case ".net":
			if err := parseNet(nl, l); err != nil {
				return nil, err
			}
		}
	}

	// Pass 2: connections.
	for _, l := range lines {
		switch l.dir {
		case ".join":
			if err := parseJoin(nl, l); err != nil {
				return nil, err
			}
		case ".nc":
			if err := parseNC(nl, l); err != nil {
				return nil, err
			}
		}
	}
	return nl, nil
}

// ParseString is a convenience wrapper around Parse.
func ParseString(s string) (*Netlist, error) {
	return Parse(strings.NewReader(s))
}

func tokenize(s string) (tokens []string, cols []int) {
	var start int = -1
	for i, r := range s {
		if r == ' ' || r == '\t' || r == '\r' {
			if start >= 0 {
				tokens = append(tokens, s[start:i])
				cols = append(cols, start+1)
				start = -1
			}
			continue
		}
		if start < 0 {
			start = i
		}
	}
	if start >= 0 {
		tokens = append(tokens, s[start:])
		cols = append(cols, start+1)
	}
	return tokens, cols
}

func parseComp(nl *Netlist, l struct {
	no     int
	dir    string
	tokens []string
	cols   []int
}) error {
	t := l.tokens
	if len(t) < 3 {
		return &ParseError{Kind: ErrMissingField, Line: l.no, Column: colAt(t, l.cols, len(t)),
			Directive: ".comp", Detail: "usage: .comp <ref> <type> <pin>:<dir>[/<kind>] ..."}
	}
	ref, ctype := t[1], t[2]
	if _, dup := nl.Components[ref]; dup {
		return &ParseError{Kind: ErrDuplicateDef, Line: l.no, Column: l.cols[1],
			Directive: ".comp", Detail: fmt.Sprintf("component %q already defined", ref)}
	}
	c := &Component{Ref: ref, Type: ctype, Pins: map[string]*Pin{}}
	for i := 3; i < len(t); i++ {
		name, dir, kind, err := parsePinSpec(t[i])
		if err != nil {
			return &ParseError{Kind: err.kind, Line: l.no, Column: l.cols[i],
				Directive: ".comp", Detail: err.Error()}
		}
		if _, dup := c.Pins[name]; dup {
			return &ParseError{Kind: ErrDuplicateDef, Line: l.no, Column: l.cols[i],
				Directive: ".comp", Detail: fmt.Sprintf("component %q pin %q listed twice", ref, name)}
		}
		c.Pins[name] = &Pin{Component: ref, Name: name, Dir: dir, Kind: kind}
	}
	nl.Components[ref] = c
	return nil
}

type pinSpecErr struct {
	kind ErrorKind
	msg  string
}

func (e *pinSpecErr) Error() string { return e.msg }

func parsePinSpec(tok string) (name string, dir PinDir, kind PinKind, err *pinSpecErr) {
	namePart, rest, ok := strings.Cut(tok, ":")
	if !ok || namePart == "" {
		return "", "", "", &pinSpecErr{ErrSyntax, fmt.Sprintf("pin spec %q must be <name>:<dir>[/<kind>]", tok)}
	}
	dirPart, kindPart, hasKind := strings.Cut(rest, "/")
	switch PinDir(dirPart) {
	case PinInput, PinOutput, PinBidirect, PinPowerIn, PinPassive, PinUnspec:
	default:
		return "", "", "", &pinSpecErr{ErrInvalidValue, fmt.Sprintf("pin %q has invalid direction %q", namePart, dirPart)}
	}
	kind = PinKindSignal
	if hasKind {
		switch PinKind(kindPart) {
		case PinKindVCC, PinKindGND, PinKindSignal:
			kind = PinKind(kindPart)
		default:
			return "", "", "", &pinSpecErr{ErrInvalidValue, fmt.Sprintf("pin %q has invalid kind %q", namePart, kindPart)}
		}
	}
	if PinDir(dirPart) == PinPowerIn && !hasKind {
		switch {
		case containsAny(strings.ToLower(namePart), "gnd", "vss"):
			kind = PinKindGND
		case containsAny(strings.ToLower(namePart), "vcc", "vdd"):
			kind = PinKindVCC
		default:
			return "", "", "", &pinSpecErr{ErrMissingField, fmt.Sprintf(
				"power pin %q must declare kind, e.g. %s:pwr/vcc or %s:pwr/gnd", namePart, namePart, namePart)}
		}
	}
	return namePart, PinDir(dirPart), kind, nil
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

func parseNet(nl *Netlist, l struct {
	no     int
	dir    string
	tokens []string
	cols   []int
}) error {
	t := l.tokens
	if len(t) < 2 {
		return &ParseError{Kind: ErrMissingField, Line: l.no, Column: colAt(t, l.cols, len(t)),
			Directive: ".net", Detail: "usage: .net <name> [signal|power|ground]"}
	}
	name := t[1]
	class := NetSignal
	if len(t) >= 3 {
		switch NetClass(t[2]) {
		case NetSignal, NetPower, NetGround:
			class = NetClass(t[2])
		default:
			return &ParseError{Kind: ErrInvalidValue, Line: l.no, Column: l.cols[2],
				Directive: ".net", Detail: fmt.Sprintf("net %q has invalid class %q", name, t[2])}
		}
		if len(t) > 3 {
			return &ParseError{Kind: ErrSyntax, Line: l.no, Column: l.cols[3],
				Directive: ".net", Detail: "unexpected extra tokens"}
		}
	}
	if _, dup := nl.Nets[name]; dup {
		return &ParseError{Kind: ErrDuplicateDef, Line: l.no, Column: l.cols[1],
			Directive: ".net", Detail: fmt.Sprintf("net %q already defined", name)}
	}
	nl.Nets[name] = &Net{Name: name, Class: class}
	return nil
}

func parseJoin(nl *Netlist, l struct {
	no     int
	dir    string
	tokens []string
	cols   []int
}) error {
	t := l.tokens
	if len(t) < 3 {
		return &ParseError{Kind: ErrMissingField, Line: l.no, Column: colAt(t, l.cols, len(t)),
			Directive: ".join", Detail: "usage: .join <net> <ref>.<pin> [<ref>.<pin> ...]"}
	}
	netName := t[1]
	n, ok := nl.Nets[netName]
	if !ok {
		return &ParseError{Kind: ErrUnknownNet, Line: l.no, Column: l.cols[1],
			Directive: ".join", Detail: fmt.Sprintf("net %q is not defined", netName)}
	}
	for i := 2; i < len(t); i++ {
		ref, pin, err := splitRefPin(t[i])
		if err != nil {
			return &ParseError{Kind: ErrSyntax, Line: l.no, Column: l.cols[i],
				Directive: ".join", Detail: err.Error()}
		}
		p, perr := lookupPin(nl, ref, pin, l.no, l.cols[i], ".join")
		if perr != nil {
			return perr
		}
		if p.Net != "" {
			return &ParseError{Kind: ErrPinAlreadyJoined, Line: l.no, Column: l.cols[i],
				Directive: ".join", Detail: fmt.Sprintf("pin %s.%s already joined to net %q", ref, pin, p.Net)}
		}
		if p.NoConnect {
			return &ParseError{Kind: ErrConflict, Line: l.no, Column: l.cols[i],
				Directive: ".join", Detail: fmt.Sprintf("pin %s.%s is marked .nc and cannot be joined", ref, pin)}
		}
		p.Net = netName
		n.Pins = append(n.Pins, PinRef{Component: ref, Pin: pin})
	}
	return nil
}

func parseNC(nl *Netlist, l struct {
	no     int
	dir    string
	tokens []string
	cols   []int
}) error {
	t := l.tokens
	if len(t) < 2 {
		return &ParseError{Kind: ErrMissingField, Line: l.no, Column: colAt(t, l.cols, len(t)),
			Directive: ".nc", Detail: "usage: .nc <ref>.<pin> [<ref>.<pin> ...]"}
	}
	for i := 1; i < len(t); i++ {
		ref, pin, err := splitRefPin(t[i])
		if err != nil {
			return &ParseError{Kind: ErrSyntax, Line: l.no, Column: l.cols[i],
				Directive: ".nc", Detail: err.Error()}
		}
		p, perr := lookupPin(nl, ref, pin, l.no, l.cols[i], ".nc")
		if perr != nil {
			return perr
		}
		if p.Net != "" {
			return &ParseError{Kind: ErrPinAlreadyJoined, Line: l.no, Column: l.cols[i],
				Directive: ".nc", Detail: fmt.Sprintf("pin %s.%s already joined to net %q", ref, pin, p.Net)}
		}
		p.NoConnect = true
	}
	return nil
}

func splitRefPin(tok string) (string, string, error) {
	ref, pin, ok := strings.Cut(tok, ".")
	if !ok || ref == "" || pin == "" {
		return "", "", fmt.Errorf("pin reference %q must be <ref>.<pin>", tok)
	}
	return ref, pin, nil
}

func lookupPin(nl *Netlist, ref, pin string, line, col int, dir string) (*Pin, error) {
	c, ok := nl.Components[ref]
	if !ok {
		return nil, &ParseError{Kind: ErrUnknownComponent, Line: line, Column: col,
			Directive: dir, Detail: fmt.Sprintf("component %q is not defined", ref)}
	}
	p, ok := c.Pins[pin]
	if !ok {
		return nil, &ParseError{Kind: ErrUnknownPin, Line: line, Column: col,
			Directive: dir, Detail: fmt.Sprintf("component %q has no pin %q", ref, pin)}
	}
	return p, nil
}

func colAt(tokens []string, cols []int, i int) int {
	if i < len(cols) {
		return cols[i]
	}
	if len(cols) == 0 {
		return 1
	}
	// Point just past the last token.
	return cols[len(cols)-1] + len(tokens[len(tokens)-1])
}
