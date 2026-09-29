package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/h3yow/netlist-erc-engine-go/erc"
)

// 命令行用法：
//
//	erc check <netlist.txt>        全量检查并打印结论
//	erc check -json <netlist.txt>  以 JSON 输出
//	erc rules                      列出内置规则与默认参数
func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "check":
		runCheck(os.Args[2:])
	case "rules":
		runRules()
	case "-h", "--help", "help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "未知子命令 %q\n", os.Args[1])
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "用法:")
	fmt.Fprintln(os.Stderr, "  erc check [flags] <netlist.txt>  解析并全量 ERC 检查")
	fmt.Fprintln(os.Stderr, "  erc rules                        列出内置规则与默认参数")
	fmt.Fprintln(os.Stderr, "标志:")
	fmt.Fprintln(os.Stderr, "  -max-drivers N  单网络最大驱动源数（默认 1）")
	fmt.Fprintln(os.Stderr, "  -json           以 JSON 输出结论")
}

func runCheck(args []string) {
	fs := flag.NewFlagSet("check", flag.ExitOnError)
	asJSON := fs.Bool("json", false, "以 JSON 输出")
	maxDrivers := fs.Int("max-drivers", 1, "单网络最大驱动源数")
	_ = fs.Parse(args)
	if fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "需要恰好一个网表文件参数")
		os.Exit(2)
	}
	data, err := os.ReadFile(fs.Arg(0))
	if err != nil {
		fmt.Fprintf(os.Stderr, "读取文件失败: %v\n", err)
		os.Exit(2)
	}

	nl, perr := erc.Parse(data)
	if perr != nil {
		fmt.Fprintln(os.Stderr, "网表解析失败：")
		if pe, ok := perr.(erc.ParseErrors); ok {
			for _, e := range pe {
				fmt.Fprintf(os.Stderr, "  %s\n", e.Error())
			}
		} else {
			fmt.Fprintf(os.Stderr, "  %v\n", perr)
		}
		os.Exit(1)
	}

	cfg := erc.DefaultConfig()
	cfg.MaxDrivers = *maxDrivers
	eng, err := erc.NewEngine(nl, cfg, erc.DefaultRules())
	if err != nil {
		fmt.Fprintf(os.Stderr, "引擎初始化失败: %v\n", err)
		os.Exit(2)
	}
	findings := eng.Findings()
	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(findings); err != nil {
			fmt.Fprintf(os.Stderr, "JSON 输出失败: %v\n", err)
			os.Exit(2)
		}
	} else {
		if len(findings) == 0 {
			fmt.Println("未发现电气规则问题。")
		}
		for _, f := range findings {
			fmt.Printf("[%s] %s %s\n", f.Severity, f.ID, f.Message)
		}
	}
	if hasError(findings) {
		os.Exit(1)
	}
}

func runRules() {
	cfg := erc.DefaultConfig()
	descs := map[string]string{
		erc.RuleFloatingPin:    "悬空引脚：引脚未连接到任何网络",
		erc.RuleOrphanNet:      "孤立网络：网络上没有任何引脚",
		erc.RuleMultipleDriver: fmt.Sprintf("多驱动冲突：网络上 driver 引脚数超过阈值（默认 %d）", cfg.MaxDrivers),
		erc.RuleUnpoweredComp:  "缺电源/地：受电器件未同时连到 power 与 ground 网络",
	}
	for _, r := range erc.DefaultRules() {
		enabled := "启用"
		if !cfg.EnabledRules[r.ID()] {
			enabled = "禁用"
		}
		fmt.Printf("%s [%s] %s\n", r.ID(), enabled, descs[r.ID()])
	}
}

func hasError(fs []*erc.Finding) bool {
	for _, f := range fs {
		if f.Severity == erc.SevError {
			return true
		}
	}
	return false
}
