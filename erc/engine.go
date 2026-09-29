package erc

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
)

// RevisionRecord 记录某次修订时的结论快照与规则配置指纹，
// 使得规则集或参数变更后，历史结论仍可被复现与核对。
type RevisionRecord struct {
	Revision          int64
	ChangeOp          string // 首次构建为 "initial"，全量重查为 "full_recheck"
	ConfigFingerprint string
	Findings          []*Finding // 已按 ID 排序的快照
}

// Engine 围绕一份网表维护可查询的检查状态。
type Engine struct {
	nl       *Netlist
	cfg      *Config
	rules    []Rule
	findings map[string]*Finding // ID -> 当前有效结论
	revision int64
	history  []*RevisionRecord
}

// NewEngine 以已解析的网表与配置构建引擎并完成首次全量检查。
func NewEngine(nl *Netlist, cfg *Config, rules []Rule) (*Engine, error) {
	if nl == nil {
		return nil, &UnsupportedChangeError{Op: "new", Reason: "网表不能为空"}
	}
	if cfg == nil {
		cfg = DefaultConfig()
	}
	if rules == nil {
		rules = DefaultRules()
	}
	e := &Engine{
		nl:       nl,
		cfg:      cfg,
		rules:    rules,
		findings: map[string]*Finding{},
	}
	e.revision = 1
	fs := e.runRules(nil)
	e.replaceFindings(fs)
	e.recordHistory("initial")
	return e, nil
}

// Netlist 返回当前网表指针（调用方不应直接修改；请走 Apply 变更入口）。
func (e *Engine) Netlist() *Netlist { return e.nl }

// Config 返回当前规则配置。
func (e *Engine) Config() *Config { return e.cfg }

// Findings 返回当前全部有效结论（按 ID 排序）。
func (e *Engine) Findings() []*Finding {
	out := make([]*Finding, 0, len(e.findings))
	for _, f := range e.findings {
		out = append(out, cloneFinding(f))
	}
	sortFindings(out)
	return out
}

// Query 按主体前缀查询结论：
//
//	"net:" 查所有网络级结论；"comp:U1" 查 U1 全部子项；"pin:U1.3" 精确匹配。
//
// 前缀匹配到 scope 的整段为止（"comp:U1" 不会匹配 "comp:U10"）。
func (e *Engine) Query(prefix string) []*Finding {
	var out []*Finding
	for _, f := range e.findings {
		if scopeMatchesPrefix(f.Scope, prefix) {
			out = append(out, cloneFinding(f))
		}
	}
	sortFindings(out)
	return out
}

// Revision 返回当前状态修订号，每次成功改动或重查递增。
func (e *Engine) Revision() int64 { return e.revision }

// History 返回历次修订记录（含首次检查、每次增量改动与全量重查）。
func (e *Engine) History() []*RevisionRecord {
	out := make([]*RevisionRecord, len(e.history))
	copy(out, e.history)
	return out
}

// FullRecheck 丢弃缓存，从头重算全部结论；结论集合应与当前状态一致。
func (e *Engine) FullRecheck() []*Finding {
	fs := e.runRules(nil)
	e.replaceFindings(fs)
	e.revision++
	e.recordHistory("full_recheck")
	return e.Findings()
}

// runRules 以固定规则顺序在给定范围上执行启用的规则。
func (e *Engine) runRules(affected ScopeSet) []*Finding {
	var out []*Finding
	for _, r := range e.rules {
		if !ruleEnabled(e.cfg, r.ID()) {
			continue
		}
		out = append(out, r.Check(e.nl, e.cfg, affected)...)
	}
	return out
}

// replaceFindings 用给定结论整体替换当前状态（全量路径）。
func (e *Engine) replaceFindings(fs []*Finding) {
	e.findings = map[string]*Finding{}
	for _, f := range fs {
		e.findings[f.ID] = cloneFinding(f)
	}
}

// recheckScoped 在指定影响范围上增量重查：
// 删除这些范围内的旧结论，用新结果替换；范围外结论保持不变。
func (e *Engine) recheckScoped(affected ScopeSet) []*Finding {
	if len(affected) == 0 {
		return e.Findings()
	}
	for id, f := range e.findings {
		if scopeAffected(f.Scope, affected) {
			delete(e.findings, id)
		}
	}
	fs := e.runRules(affected)
	for _, f := range fs {
		e.findings[f.ID] = cloneFinding(f)
	}
	return e.Findings()
}

func (e *Engine) recordHistory(op string) {
	snap := e.Findings()
	e.history = append(e.history, &RevisionRecord{
		Revision:          e.revision,
		ChangeOp:          op,
		ConfigFingerprint: ConfigFingerprint(e.cfg, e.rules),
		Findings:          snap,
	})
}

// ConfigFingerprint 计算规则集顺序与配置参数的稳定指纹，
// 用于判断两次检查是否在同一规则语义下完成、复现历史结论。
func ConfigFingerprint(cfg *Config, rules []Rule) string {
	h := sha256.New()
	var ruleIDs []string
	for _, r := range rules {
		ruleIDs = append(ruleIDs, r.ID())
	}
	write := func(s string) { h.Write([]byte(s)); h.Write([]byte{0}) }
	write("rules")
	for _, id := range ruleIDs {
		write(id)
		write(boolStr(ruleEnabled(cfg, id)))
	}
	write("maxDrivers")
	write(itoa(cfg.MaxDrivers))
	write("poweredTypes")
	pTypes := sortedKeys(cfg.PoweredCompTypes)
	for _, t := range pTypes {
		write(t)
	}
	write("powerKinds")
	pKinds := sortedKeys(cfg.PowerNetKinds)
	for _, k := range pKinds {
		write(k)
	}
	return hex.EncodeToString(h.Sum(n0))
}

var n0 []byte

func cloneFinding(f *Finding) *Finding {
	cp := *f
	if f.Detail != nil {
		cp.Detail = map[string]string{}
		for k, v := range f.Detail {
			cp.Detail[k] = v
		}
	}
	return &cp
}

func sortFindings(fs []*Finding) {
	sort.SliceStable(fs, func(i, j int) bool {
		if fs[i].ID != fs[j].ID {
			return fs[i].ID < fs[j].ID
		}
		return fs[i].Message < fs[j].Message
	})
}

func scopeMatchesPrefix(scope, prefix string) bool {
	if len(scope) < len(prefix) {
		return false
	}
	if scope[:len(prefix)] != prefix {
		return false
	}
	if len(scope) == len(prefix) {
		return true
	}
	// 前缀以分隔符结尾（如 "net:"）时，余下任意内容都属于该段。
	if last := prefix[len(prefix)-1]; last == ':' || last == '|' || last == '.' {
		return true
	}
	// 否则要求恰好在分界处结束，避免 comp:U1 误匹配 comp:U10。
	c := scope[len(prefix)]
	return c == ':' || c == '|' || c == '.'
}

func boolStr(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
