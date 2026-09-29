package erc

import "sort"

// Change 表示一次局部改动；通过 Engine.Apply 执行并增量重查。
type Change interface {
	Op() string
}

// 受支持的局部改动（其它方式一律以 UnsupportedChangeError 明确拒绝）：
//   AddComponentChange  增加元件（可同时携带其引脚）
//   RemoveComponentChange 删除元件（连同其引脚）
//   MergeNetsChange     把两个同类型网络合并为一个
//   RenameNetChange     网络重命名（同步更新引用它的引脚）

// ComponentInput 是新增元件时的元件与其引脚的载体。
type ComponentInput struct {
	Ref  string
	Type string
	Pins []PinInput
}

// PinInput 描述新增元件携带的一个引脚。
type PinInput struct {
	Name string
	Net  string
	Role PinRole
}

// AddComponentChange 增加一个元件。
type AddComponentChange struct{ C ComponentInput }

func (AddComponentChange) Op() string { return "add_component" }

// RemoveComponentChange 删除一个元件及其全部引脚。
type RemoveComponentChange struct{ Ref string }

func (RemoveComponentChange) Op() string { return "remove_component" }

// MergeNetsChange 将 From 网络合并进 To 网络：From 上的引脚改连 To，From 删除。
type MergeNetsChange struct {
	From string
	To   string
}

func (MergeNetsChange) Op() string { return "merge_nets" }

// RenameNetChange 将网络 OldName 改名为 NewName。
type RenameNetChange struct {
	OldName string
	NewName string
}

func (RenameNetChange) Op() string { return "rename_net" }

// Apply 在引擎上执行一次受支持的局部改动并做增量重查。
// 改动非法时返回错误且引擎状态（含修订号）保持不变。
// 返回的结论与对改动后网表做一次全量重查完全一致。
func (e *Engine) Apply(c Change) ([]*Finding, error) {
	var (
		affected ScopeSet
		op       string
		err      error
	)
	switch ch := c.(type) {
	case AddComponentChange:
		affected, err = e.applyAddComponent(ch.C)
		op = ch.Op()
	case RemoveComponentChange:
		affected, err = e.applyRemoveComponent(ch.Ref)
		op = ch.Op()
	case MergeNetsChange:
		affected, err = e.applyMergeNets(ch.From, ch.To)
		op = ch.Op()
	case RenameNetChange:
		affected, err = e.applyRenameNet(ch.OldName, ch.NewName)
		op = ch.Op()
	default:
		return nil, &UnsupportedChangeError{
			Op: c.Op(),
			Reason: "仅支持 add_component / remove_component / merge_nets / rename_net；" +
				"引脚级增删与网络类型修改请重新解析网表",
		}
	}
	if err != nil {
		return nil, err
	}

	e.recheckScoped(affected)
	e.revision++
	e.recordHistory(op)
	return e.Findings(), nil
}

func (e *Engine) applyAddComponent(c ComponentInput) (ScopeSet, error) {
	if c.Ref == "" {
		return nil, &UnsupportedChangeError{Op: "add_component", Reason: "元件位号不能为空"}
	}
	if _, exists := e.nl.Components[c.Ref]; exists {
		return nil, &UnsupportedChangeError{
			Op: "add_component", Reason: "元件 " + c.Ref + " 已存在",
		}
	}
	// 预校验全部引脚：网络必须存在、角色合法、引脚不重复。
	seen := map[string]bool{}
	for _, p := range c.Pins {
		if p.Name == "" {
			return nil, &UnsupportedChangeError{Op: "add_component", Reason: "引脚名不能为空"}
		}
		if seen[p.Name] {
			return nil, &UnsupportedChangeError{
				Op:     "add_component",
				Reason: "元件 " + c.Ref + " 的引脚 " + p.Name + " 重复",
			}
		}
		seen[p.Name] = true
		for _, ex := range e.nl.Pins {
			if ex.Component == c.Ref && ex.Name == p.Name {
				return nil, &UnsupportedChangeError{
					Op:     "add_component",
					Reason: "元件 " + c.Ref + " 的引脚 " + p.Name + " 已存在",
				}
			}
		}
		if p.Net != "" {
			if _, ok := e.nl.Nets[p.Net]; !ok {
				return nil, &UnsupportedChangeError{
					Op:     "add_component",
					Reason: "引脚 " + c.Ref + "." + p.Name + " 指向不存在的网络 " + p.Net,
				}
			}
		}
		if p.Role != "" && !validPinRoles[p.Role] {
			return nil, &UnsupportedChangeError{
				Op: "add_component", Reason: "引脚角色 " + string(p.Role) + " 非法",
			}
		}
	}

	e.nl.Components[c.Ref] = &Component{Ref: c.Ref, Type: c.Type}
	affected := ScopeSet{compScope(c.Ref): true}
	for _, p := range c.Pins {
		pin := &Pin{Component: c.Ref, Name: p.Name, Net: p.Net, Role: p.Role}
		e.nl.Pins = append(e.nl.Pins, pin)
		affected[pinScope(pin)] = true
		if p.Net != "" {
			affected[netScope(p.Net)] = true
		}
	}
	return affected, nil
}

func (e *Engine) applyRemoveComponent(ref string) (ScopeSet, error) {
	if _, ok := e.nl.Components[ref]; !ok {
		return nil, &UnsupportedChangeError{
			Op: "remove_component", Reason: "元件 " + ref + " 不存在",
		}
	}

	affected := ScopeSet{compScope(ref): true}
	touchedNets := map[string]bool{}
	var kept []*Pin
	for _, p := range e.nl.Pins {
		if p.Component == ref {
			affected[pinScope(p)] = true
			touchedNets[p.Net] = true
			continue
		}
		kept = append(kept, p)
	}
	for n := range touchedNets {
		if n != "" {
			affected[netScope(n)] = true
		}
	}
	e.nl.Pins = kept
	delete(e.nl.Components, ref)
	return affected, nil
}

func (e *Engine) applyMergeNets(from, to string) (ScopeSet, error) {
	if from == "" || to == "" {
		return nil, &UnsupportedChangeError{Op: "merge_nets", Reason: "网络名不能为空"}
	}
	if from == to {
		return nil, &UnsupportedChangeError{Op: "merge_nets", Reason: "不能将网络合并到自身"}
	}
	nf, ok1 := e.nl.Nets[from]
	nt, ok2 := e.nl.Nets[to]
	if !ok1 || !ok2 {
		missing := from
		if !ok2 {
			missing = to
		}
		return nil, &UnsupportedChangeError{
			Op: "merge_nets", Reason: "网络 " + missing + " 不存在",
		}
	}
	if nf.Kind != nt.Kind {
		return nil, &UnsupportedChangeError{
			Op: "merge_nets",
			Reason: "暂不支持合并不同类型的网络（" + from + ":" + nf.Kind + " 与 " +
				to + ":" + nt.Kind + "）",
		}
	}

	affected := ScopeSet{netScope(from): true, netScope(to): true}
	compTouched := map[string]bool{}
	for _, p := range e.nl.Pins {
		if p.Net == from {
			p.Net = to
			affected[pinScope(p)] = true
			compTouched[p.Component] = true
		}
	}
	for ref := range compTouched {
		affected[compScope(ref)] = true
	}
	delete(e.nl.Nets, from)
	return affected, nil
}

func (e *Engine) applyRenameNet(oldName, newName string) (ScopeSet, error) {
	if oldName == "" || newName == "" {
		return nil, &UnsupportedChangeError{Op: "rename_net", Reason: "网络名不能为空"}
	}
	if oldName == newName {
		return nil, &UnsupportedChangeError{Op: "rename_net", Reason: "新名称与旧名称相同"}
	}
	n, ok := e.nl.Nets[oldName]
	if !ok {
		return nil, &UnsupportedChangeError{
			Op: "rename_net", Reason: "网络 " + oldName + " 不存在",
		}
	}
	if _, dup := e.nl.Nets[newName]; dup {
		return nil, &UnsupportedChangeError{
			Op: "rename_net", Reason: "网络 " + newName + " 已存在（合并请使用 merge_nets）",
		}
	}

	n.Name = newName
	e.nl.Nets[newName] = n
	delete(e.nl.Nets, oldName)
	for _, p := range e.nl.Pins {
		if p.Net == oldName {
			p.Net = newName
		}
	}
	return ScopeSet{netScope(oldName): true, netScope(newName): true}, nil
}

// sortedScope 供测试/调试使用：返回排序后的范围列表。
func sortedScope(s ScopeSet) []string {
	out := make([]string, 0, len(s))
	for k := range s {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
