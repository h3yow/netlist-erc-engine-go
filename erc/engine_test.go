package erc

import (
	"reflect"
	"sort"
	"strings"
	"testing"
)

// findingIDs 返回排序后的结论 ID，便于做集合对比。
func findingIDs(fs []*Finding) []string {
	ids := make([]string, len(fs))
	for i, f := range fs {
		ids[i] = f.ID
	}
	sort.Strings(ids)
	return ids
}

func mustEngine(t *testing.T, text string) *Engine {
	t.Helper()
	nl := requireParse(t, text)
	eng, err := NewEngine(nl, DefaultConfig(), DefaultRules())
	if err != nil {
		t.Fatalf("引擎初始化失败: %v。输入:\n%s", err, text)
	}
	return eng
}

// dumpFindings 在断言失败时打印判定依据。
func dumpFindings(fs []*Finding) string {
	var b strings.Builder
	for _, f := range fs {
		b.WriteString("  " + f.ID + " [" + string(f.Severity) + "] " + f.Message + "\n")
	}
	if b.Len() == 0 {
		return "  (无结论)"
	}
	return b.String()
}

func TestFloatingPin(t *testing.T) {
	text := `
net S kind=signal
component U1 type=IC
pin U1 1 net=S role=passive
pin U1 2 role=passive
`
	eng := mustEngine(t, text)
	if ids := findingIDs(eng.Findings()); !contains(ids, "ERC001|pin:U1.2") {
		t.Fatalf("应报 U1.2 悬空。输入:\n%s\n判定:\n%s", text, dumpFindings(eng.Findings()))
	}
	if contains(findingIDs(eng.Findings()), "ERC001|pin:U1.1") {
		t.Fatalf("U1.1 已连接不应报悬空。输入:\n%s", text)
	}
}

func TestOrphanNet(t *testing.T) {
	text := `
net USED kind=signal
net DEAD kind=signal
component U1 type=R
pin U1 1 net=USED role=passive
`
	eng := mustEngine(t, text)
	ids := findingIDs(eng.Findings())
	if !contains(ids, "ERC002|net:DEAD") {
		t.Fatalf("应报 DEAD 孤立。输入:\n%s\n判定:\n%s", text, dumpFindings(eng.Findings()))
	}
	if contains(ids, "ERC002|net:USED") {
		t.Fatalf("USED 有引脚不应报孤立")
	}
}

func TestMultipleDrivers(t *testing.T) {
	text := `
net S kind=signal
component U1 type=IC
component U2 type=IC
pin U1 1 net=S role=driver
pin U2 1 net=S role=driver
`
	eng := mustEngine(t, text)
	ids := findingIDs(eng.Findings())
	if !contains(ids, "ERC003|net:S") {
		t.Fatalf("应报 S 多驱动冲突。输入:\n%s\n判定:\n%s", text, dumpFindings(eng.Findings()))
	}
	var f *Finding
	for _, x := range eng.Findings() {
		if x.ID == "ERC003|net:S" {
			f = x
		}
	}
	if f.Severity != SevError {
		t.Fatalf("多驱动应为 error，实际 %s", f.Severity)
	}
	if f.Detail["driver_count"] != "2" {
		t.Fatalf("driver_count 应为 2，实际 %q", f.Detail["driver_count"])
	}

	// 放宽阈值到 2 后冲突消失（参数变更）。
	cfg := DefaultConfig()
	cfg.MaxDrivers = 2
	eng2, _ := NewEngine(eng.Netlist(), cfg, DefaultRules())
	if contains(findingIDs(eng2.Findings()), "ERC003|net:S") {
		t.Fatalf("阈值放宽后不应再报多驱动。判定:\n%s", dumpFindings(eng2.Findings()))
	}
}

func TestPowerOutDoesNotCountAsDriver(t *testing.T) {
	text := `
net VCC kind=power
component REG1 type=REG
component U1 type=IC
pin REG1 1 net=VCC role=power_out
pin U1 1 net=VCC role=power_in
`
	eng := mustEngine(t, text)
	if ids := findingIDs(eng.Findings()); contains(ids, "ERC003|net:VCC") {
		t.Fatalf("power_out 不应计入 driver 冲突。判定:\n%s", dumpFindings(eng.Findings()))
	}
}

func TestUnpoweredComponent(t *testing.T) {
	text := `
net S kind=signal
net VCC kind=power
component U1 type=IC
pin U1 1 net=VCC role=power_in
pin U1 2 net=S role=driver
`
	eng := mustEngine(t, text)
	ids := findingIDs(eng.Findings())
	if !contains(ids, "ERC004|comp:U1|missing_ground") {
		t.Fatalf("应报 U1 缺地。输入:\n%s\n判定:\n%s", text, dumpFindings(eng.Findings()))
	}
	if contains(ids, "ERC004|comp:U1|missing_power") {
		t.Fatalf("U1 已接 VCC 不应报缺电源")
	}
}

func TestCleanNetlistNoFindings(t *testing.T) {
	text := `
net VCC kind=power
net GND kind=ground
net S kind=signal
component U1 type=IC
component R1 type=R
pin U1 1 net=VCC role=power_in
pin U1 2 net=GND role=power_in
pin U1 3 net=S role=driver
pin R1 1 net=S role=passive
pin R1 2 net=GND role=passive
`
	eng := mustEngine(t, text)
	if len(eng.Findings()) != 0 {
		t.Fatalf("干净网表不应有结论。输入:\n%s\n判定:\n%s", text, dumpFindings(eng.Findings()))
	}
}

func TestRepeatedChecksAreDeduplicatedAndStable(t *testing.T) {
	text := `
net DEAD kind=signal
component U1 type=R
`
	eng := mustEngine(t, text)
	first := eng.Findings()
	second := eng.FullRecheck()
	third := eng.FullRecheck()
	if !reflect.DeepEqual(findingIDs(first), findingIDs(second)) ||
		!reflect.DeepEqual(findingIDs(second), findingIDs(third)) {
		t.Fatalf("重复检查结论不稳定:\n%v\n%v\n%v", first, second, third)
	}
	count := 0
	for _, f := range second {
		if f.ID == "ERC002|net:DEAD" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("孤立网络结论重复出现 %d 次", count)
	}
}

func TestQueryByPrefix(t *testing.T) {
	text := `
net DEAD1 kind=signal
net DEAD2 kind=signal
net DEAD10 kind=signal
`
	eng := mustEngine(t, text)
	all := eng.Query("net:")
	if len(all) != 3 {
		t.Fatalf("net: 前缀应命中 3 个，实际 %d", len(all))
	}
	// DEAD1 不应误匹配 DEAD10。
	one := eng.Query("net:DEAD1")
	if len(one) != 1 || one[0].Scope != "net:DEAD1" {
		t.Fatalf("net:DEAD1 应只命中自身，实际 %v", findingIDs(one))
	}
}

func TestRuleToggleIsReproducible(t *testing.T) {
	text := `
net DEAD kind=signal
component U1 type=R
`
	nl := requireParse(t, text)
	cfg := DefaultConfig()
	full, _ := NewEngine(nl, cfg, DefaultRules())
	if !contains(findingIDs(full.Findings()), "ERC002|net:DEAD") {
		t.Fatalf("默认应启用孤立网络规则")
	}

	cfg2 := DefaultConfig()
	cfg2.EnabledRules[RuleOrphanNet] = false
	off, _ := NewEngine(nl, cfg2, DefaultRules())
	if contains(findingIDs(off.Findings()), "ERC002|net:DEAD") {
		t.Fatalf("关闭规则后不应再报孤立网络")
	}

	// 历史记录带不同配置指纹，证明规则集变更可区分、历史可复现。
	if full.History()[0].ConfigFingerprint == off.History()[0].ConfigFingerprint {
		t.Fatalf("不同规则集的配置指纹不应相同")
	}
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}
