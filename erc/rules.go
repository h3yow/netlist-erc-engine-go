package erc

import (
	"fmt"
	"sort"
	"strings"
)

// 内置规则：
//   ERC001 floating-pin     悬空引脚：引脚未连接到任何网络（net 名为空）。
//   ERC002 orphan-net       孤立网络：网络上没有任何引脚。
//   ERC003 multiple-drivers 多驱动冲突：同一网络上 driver 角色引脚数超过阈值。
//   ERC004 unpowered-comp   缺电源/地：受电器件没有连到 power 与 ground 网络。
//
// 每条规则以「主体范围（scope）」为单位产出结论；scope 由网表内容决定，
// 因此 Finding.ID 稳定、可复现，且天然可按主体去重。

// Rule 是一条电气规则。
type Rule interface {
	ID() string
	// Check 在给定影响范围上检查；affected 为 nil 表示全量。
	Check(nl *Netlist, cfg *Config, affected ScopeSet) []*Finding
}

// ScopeSet 表示本次需要检查的主体集合；nil 表示全量。
type ScopeSet map[string]bool

// scope 编码约定：
//
//	pin:<comp>.<pin>  引脚主体
//	net:<name>        网络主体
//	comp:<ref>        元件主体
//	comp:<ref>|missing_power / comp:<ref>|missing_ground  元件下的缺陷子项
const (
	RuleFloatingPin    = "ERC001"
	RuleOrphanNet      = "ERC002"
	RuleMultipleDriver = "ERC003"
	RuleUnpoweredComp  = "ERC004"
)

type floatingPinRule struct{}

func (floatingPinRule) ID() string { return RuleFloatingPin }

func (floatingPinRule) Check(nl *Netlist, cfg *Config, affected ScopeSet) []*Finding {
	var out []*Finding
	for _, p := range nl.Pins {
		scope := pinScope(p)
		if !scopeIn(affected, scope) {
			continue
		}
		if p.Net == "" {
			out = append(out, &Finding{
				ID:       RuleFloatingPin + "|" + scope,
				RuleID:   RuleFloatingPin,
				Severity: SevWarning,
				Scope:    scope,
				Message:  fmt.Sprintf("引脚 %s.%s 悬空（未连接网络）", p.Component, p.Name),
				Detail: map[string]string{
					"component": p.Component, "pin": p.Name,
				},
			})
		}
	}
	return out
}

type orphanNetRule struct{}

func (orphanNetRule) ID() string { return RuleOrphanNet }

func (orphanNetRule) Check(nl *Netlist, cfg *Config, affected ScopeSet) []*Finding {
	var out []*Finding
	used := map[string]int{}
	for _, p := range nl.Pins {
		used[p.Net]++
	}
	names := make([]string, 0, len(nl.Nets))
	for name := range nl.Nets {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		scope := netScope(name)
		if !scopeIn(affected, scope) {
			continue
		}
		if used[name] == 0 {
			out = append(out, &Finding{
				ID:       RuleOrphanNet + "|" + scope,
				RuleID:   RuleOrphanNet,
				Severity: SevWarning,
				Scope:    scope,
				Message:  fmt.Sprintf("网络 %s 孤立（没有引脚连接）", name),
				Detail:   map[string]string{"net": name},
			})
		}
	}
	return out
}

type multipleDriversRule struct{}

func (multipleDriversRule) ID() string { return RuleMultipleDriver }

func (multipleDriversRule) Check(nl *Netlist, cfg *Config, affected ScopeSet) []*Finding {
	var out []*Finding

	type drv struct {
		comp, pin string
	}
	drivers := map[string][]drv{}
	powerOut := map[string][]drv{}
	for _, p := range nl.Pins {
		switch p.Role {
		case RoleDriver:
			drivers[p.Net] = append(drivers[p.Net], drv{p.Component, p.Name})
		case RolePowerOut:
			powerOut[p.Net] = append(powerOut[p.Net], drv{p.Component, p.Name})
		}
	}

	names := make([]string, 0, len(nl.Nets))
	for name := range nl.Nets {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		scope := netScope(name)
		if !scopeIn(affected, scope) {
			continue
		}
		ds := drivers[name]
		limit := cfg.MaxDrivers
		if len(ds) > limit {
			parts := make([]string, len(ds))
			for i, d := range ds {
				parts[i] = d.comp + "." + d.pin
			}
			sort.Strings(parts)
			out = append(out, &Finding{
				ID:       RuleMultipleDriver + "|" + scope,
				RuleID:   RuleMultipleDriver,
				Severity: SevError,
				Scope:    scope,
				Message: fmt.Sprintf("网络 %s 上有 %d 个驱动源，超过允许的 %d 个：%s",
					name, len(ds), limit, strings.Join(parts, ", ")),
				Detail: map[string]string{
					"net": name, "driver_count": fmt.Sprintf("%d", len(ds)),
					"max_drivers": fmt.Sprintf("%d", limit), "drivers": strings.Join(parts, ","),
				},
			})
		}
	}
	return out
}

type unpoweredCompRule struct{}

func (unpoweredCompRule) ID() string { return RuleUnpoweredComp }

func (unpoweredCompRule) Check(nl *Netlist, cfg *Config, affected ScopeSet) []*Finding {
	var out []*Finding

	// 预聚合每个元件引脚上的网络 kind。
	hasPower := map[string]bool{}
	hasGround := map[string]bool{}
	for _, p := range nl.Pins {
		n, ok := nl.Nets[p.Net]
		if !ok {
			continue
		}
		switch n.Kind {
		case "power":
			hasPower[p.Component] = true
		case "ground":
			hasGround[p.Component] = true
		}
	}

	refs := make([]string, 0, len(nl.Components))
	for ref := range nl.Components {
		refs = append(refs, ref)
	}
	sort.Strings(refs)
	for _, ref := range refs {
		c := nl.Components[ref]
		if !cfg.PoweredCompTypes[c.Type] {
			continue
		}
		if !hasPower[ref] {
			scope := compScope(ref) + "|missing_power"
			if scopeIn(affected, scope) || scopeIn(affected, compScope(ref)) {
				out = append(out, &Finding{
					ID:       RuleUnpoweredComp + "|" + scope,
					RuleID:   RuleUnpoweredComp,
					Severity: SevError,
					Scope:    scope,
					Message:  fmt.Sprintf("受电元件 %s（类型 %s）未连接任何 power 网络", ref, c.Type),
					Detail:   map[string]string{"component": ref, "type": c.Type, "missing": "power"},
				})
			}
		}
		if !hasGround[ref] {
			scope := compScope(ref) + "|missing_ground"
			if scopeIn(affected, scope) || scopeIn(affected, compScope(ref)) {
				out = append(out, &Finding{
					ID:       RuleUnpoweredComp + "|" + scope,
					RuleID:   RuleUnpoweredComp,
					Severity: SevError,
					Scope:    scope,
					Message:  fmt.Sprintf("受电元件 %s（类型 %s）未连接任何 ground 网络", ref, c.Type),
					Detail:   map[string]string{"component": ref, "type": c.Type, "missing": "ground"},
				})
			}
		}
	}
	return out
}

// DefaultRules 返回按固定顺序排列的内置规则。
func DefaultRules() []Rule {
	return []Rule{
		floatingPinRule{},
		orphanNetRule{},
		multipleDriversRule{},
		unpoweredCompRule{},
	}
}

// DefaultConfig 返回内置默认参数。
func DefaultConfig() *Config {
	return &Config{
		EnabledRules: map[string]bool{
			RuleFloatingPin:    true,
			RuleOrphanNet:      true,
			RuleMultipleDriver: true,
			RuleUnpoweredComp:  true,
		},
		MaxDrivers: 1,
		PowerNetKinds: map[string]bool{
			"power": true, "ground": true,
		},
		PoweredCompTypes: map[string]bool{
			"IC": true, "MCU": true,
		},
	}
}

// ruleEnabled 判断规则在配置下是否启用。
func ruleEnabled(cfg *Config, id string) bool {
	if cfg == nil || cfg.EnabledRules == nil {
		return true
	}
	return cfg.EnabledRules[id]
}

func pinScope(p *Pin) string      { return fmt.Sprintf("pin:%s.%s", p.Component, p.Name) }
func netScope(name string) string { return "net:" + name }
func compScope(ref string) string { return "comp:" + ref }

// scopeIn 判断 scope 是否落在检查集合内；集合为 nil 表示全量。
func scopeIn(set ScopeSet, scope string) bool {
	if set == nil {
		return true
	}
	return set[scope]
}

// scopeAffected 判断结论 scope 是否需要失效重查：
// 精确命中，或命中其父级（comp:U1 覆盖 comp:U1|missing_ground 这类子项）。
func scopeAffected(scope string, set ScopeSet) bool {
	if set[scope] {
		return true
	}
	if i := strings.IndexByte(scope, '|'); i > 0 {
		return set[scope[:i]]
	}
	return false
}
