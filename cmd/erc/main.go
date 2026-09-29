// Command erc is a local netlist ERC runner.
//
// Usage:
//
//	erc [flags] <netlist-file>
//
// It parses the textual netlist, runs the built-in rule set and prints every
// finding with its stable ID and severity. Exit code is 0 when there are no
// error-severity findings and 1 otherwise (or when parsing fails).
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/h3yow/netlist-erc-engine-go/pkg/erc"
	"github.com/h3yow/netlist-erc-engine-go/pkg/netlist"
)

func main() {
	power := flag.String("power", "VCC,VDD", "comma-separated names treated as power rails")
	ground := flag.String("ground", "GND,VSS", "comma-separated names treated as ground rails")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "usage: %s [flags] <netlist-file>\n", os.Args[0])
		flag.PrintDefaults()
	}
	flag.Parse()
	if flag.NArg() != 1 {
		flag.Usage()
		os.Exit(2)
	}

	f, err := os.Open(flag.Arg(0))
	if err != nil {
		fmt.Fprintf(os.Stderr, "cannot open %s: %v\n", flag.Arg(0), err)
		os.Exit(2)
	}
	defer f.Close()

	nl, perr := netlist.Parse(f)
	if perr != nil {
		fmt.Fprintf(os.Stderr, "netlist rejected: %v\n", perr)
		os.Exit(2)
	}

	rs := erc.DefaultRuleSet()
	rs.Params.PowerNetNames = splitCSV(*power)
	rs.Params.GroundNetNames = splitCSV(*ground)

	eng := erc.NewEngine(nl, rs)
	rep, _ := eng.Check()

	errs, warns := 0, 0
	for _, fd := range rep.Findings {
		fmt.Printf("[%s] %-7s %s\n", fd.Rule, fd.Severity, fd.Message)
		switch fd.Severity {
		case erc.SeverityError:
			errs++
		case erc.SeverityWarning:
			warns++
		}
	}
	fmt.Printf("%d finding(s): %d error(s), %d warning(s) (rule set v%d)\n",
		len(rep.Findings), errs, warns, rep.RuleSetVersion)
	if errs > 0 {
		os.Exit(1)
	}
}

func splitCSV(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
