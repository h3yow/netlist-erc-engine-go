package erc

import (
	"reflect"
	"testing"
)

// canonicalText 是增量基准网表；每一步改动都同时在这份文本上重放，
// 全新解析后做全量重查，作为增量结论的对照真值。
const canonicalText = `net VCC kind=power
net GND kind=ground
net SIG kind=signal
net DEAD kind=signal
component U1 type=IC
component U2 type=R
pin U1 1 net=VCC role=power_in
pin U1 2 net=GND role=power_in
pin U1 3 net=SIG role=driver
pin U2 1 net=SIG role=passive
`

// rebuildText 重新解析文本并全量检查，返回结论 ID 集合。
func rebuildText(t *testing.T, text string) []string {
	t.Helper()
	nl, err := Parse([]byte(text))
	if err != nil {
		t.Fatalf("对照网表解析失败:\n%s\n判定: %v", text, err)
	}
	eng, err := NewEngine(nl, DefaultConfig(), DefaultRules())
	if err != nil {
		t.Fatalf("对照引擎构建失败: %v", err)
	}
	return findingIDs(eng.Findings())
}

func assertSameFindings(t *testing.T, step string, text string, got []*Finding) {
	t.Helper()
	wantIDs := rebuildText(t, text)
	gotIDs := findingIDs(got)
	if !reflect.DeepEqual(wantIDs, gotIDs) {
		t.Fatalf("步骤 %s：增量结论与全量重查不一致。\n输入网表:\n%s\n增量结果: %v\n全量结果: %v",
			step, text, gotIDs, wantIDs)
	}
}

func TestIncrementalMatchesFullRecheck(t *testing.T) {
	eng := mustEngine(t, canonicalText)
	// 初始：DEAD 孤立。
	assertSameFindings(t, "initial", canonicalText, eng.Findings())

	// 1) 新增元件 U3(IC)：一个 driver 进 SIG（制造多驱动冲突），
	//    只接 VCC 不接 GND（制造缺地），不碰 DEAD（孤立保留）。
	add := AddComponentChange{ComponentInput{
		Ref: "U3", Type: "IC",
		Pins: []PinInput{
			{Name: "1", Net: "VCC", Role: RolePowerIn},
			{Name: "3", Net: "SIG", Role: RoleDriver},
		},
	}}
	textAfterAdd := canonicalText +
		"component U3 type=IC\n" +
		"pin U3 1 net=VCC role=power_in\n" +
		"pin U3 3 net=SIG role=driver\n"
	fs, err := eng.Apply(add)
	if err != nil {
		t.Fatalf("新增元件失败: %v", err)
	}
	assertSameFindings(t, "add_component", textAfterAdd, fs)

	// 2) 再新增元件 U4(R)，其 passive 引脚接到 DEAD：孤立结论应消失。
	addR := AddComponentChange{ComponentInput{
		Ref: "U4", Type: "R",
		Pins: []PinInput{{Name: "1", Net: "DEAD", Role: RolePassive}},
	}}
	textAfterR := textAfterAdd +
		"component U4 type=R\n" +
		"pin U4 1 net=DEAD role=passive\n"
	fs, err = eng.Apply(addR)
	if err != nil {
		t.Fatalf("新增 U4 失败: %v", err)
	}
	assertSameFindings(t, "add_component_orphan_resolved", textAfterR, fs)
	if contains(findingIDs(fs), "ERC002|net:DEAD") {
		t.Fatalf("U4 接上 DEAD 后孤立结论应已失效移除")
	}

	// 3) 重命名网络 DEAD -> N5：结论 ID 跟随网络名，且无残留旧 ID。
	ren := RenameNetChange{OldName: "DEAD", NewName: "N5"}
	textAfterRen := stringsRepl(textAfterR,
		"net DEAD kind=signal", "net N5 kind=signal",
		"net=DEAD", "net=N5")
	fs, err = eng.Apply(ren)
	if err != nil {
		t.Fatalf("重命名失败: %v", err)
	}
	assertSameFindings(t, "rename_net", textAfterRen, fs)
	if contains(findingIDs(fs), "ERC002|net:DEAD") {
		t.Fatalf("重命名后旧网络 ID 不应残留")
	}

	// 4) 删除元件 U3：多驱动冲突与缺地结论同时消失。
	rm := RemoveComponentChange{Ref: "U3"}
	textAfterRm := stringsRepl(textAfterRen,
		"component U3 type=IC\n", "",
		"pin U3 1 net=VCC role=power_in\n", "",
		"pin U3 3 net=SIG role=driver\n", "")
	fs, err = eng.Apply(rm)
	if err != nil {
		t.Fatalf("删除元件失败: %v", err)
	}
	assertSameFindings(t, "remove_component", textAfterRm, fs)
	if contains(findingIDs(fs), "ERC003|net:SIG") ||
		contains(findingIDs(fs), "ERC004|comp:U3|missing_ground") {
		t.Fatalf("删除 U3 后相关旧结论不应残留。判定:\n%s", dumpFindings(fs))
	}

	// 5) 合并网络：把 N5 并入 SIG（两者均 signal），U4 引脚随之改连 SIG。
	merge := MergeNetsChange{From: "N5", To: "SIG"}
	textAfterMerge := stringsRepl(textAfterRm,
		"net N5 kind=signal\n", "",
		"pin U4 1 net=N5 role=passive", "pin U4 1 net=SIG role=passive")
	fs, err = eng.Apply(merge)
	if err != nil {
		t.Fatalf("合并网络失败: %v", err)
	}
	assertSameFindings(t, "merge_nets", textAfterMerge, fs)

	// 6) 最终再做一次全量重查，必须与当前状态完全一致。
	full := eng.FullRecheck()
	assertSameFindings(t, "final_full_recheck", textAfterMerge, full)
}

func TestMergeDifferentKindRejected(t *testing.T) {
	text := `net VCC kind=power
net SIG kind=signal
component U1 type=R
pin U1 1 net=VCC role=passive
`
	eng := mustEngine(t, text)
	before := findingIDs(eng.Findings())
	_, err := eng.Apply(MergeNetsChange{From: "VCC", To: "SIG"})
	if err == nil {
		t.Fatalf("合并不同类型网络必须被拒绝")
	}
	if _, ok := err.(*UnsupportedChangeError); !ok {
		t.Fatalf("应为 UnsupportedChangeError，实际 %T: %v", err, err)
	}
	// 被拒绝后状态保持不变，修订号不增长。
	rev := eng.Revision()
	if !reflect.DeepEqual(before, findingIDs(eng.Findings())) {
		t.Fatalf("拒绝改动后状态不应变化")
	}
	if rev != eng.Revision() {
		t.Fatalf("被拒绝的改动不应推进修订号")
	}
	if eng.Netlist().Nets["VCC"] == nil || eng.Netlist().Nets["SIG"] == nil {
		t.Fatalf("被拒绝后网络不应被改动")
	}
}

type bogusChange struct{}

func (bogusChange) Op() string { return "rewrite_pins" }

func TestUnsupportedAndInvalidChangesRejected(t *testing.T) {
	eng := mustEngine(t, canonicalText)

	cases := []struct {
		name string
		c    Change
	}{
		{"未知改动类型", bogusChange{}},
		{"新增重复元件", AddComponentChange{ComponentInput{Ref: "U1", Type: "IC"}}},
		{"删除不存在元件", RemoveComponentChange{Ref: "NOPE"}},
		{"新增引脚指向缺失网络", AddComponentChange{ComponentInput{
			Ref: "X1", Type: "R",
			Pins: []PinInput{{Name: "1", Net: "GHOST", Role: RolePassive}},
		}}},
		{"新增引脚角色非法", AddComponentChange{ComponentInput{
			Ref: "X2", Type: "R",
			Pins: []PinInput{{Name: "1", Net: "SIG", Role: "wizard"}},
		}}},
		{"重命名到已存在网络", RenameNetChange{OldName: "SIG", NewName: "DEAD"}},
		{"重命名不存在网络", RenameNetChange{OldName: "GHOST", NewName: "Z"}},
		{"合并不存在网络", MergeNetsChange{From: "GHOST", To: "SIG"}},
		{"合并到自身", MergeNetsChange{From: "SIG", To: "SIG"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := findingIDs(eng.Findings())
			_, err := eng.Apply(tc.c)
			if err == nil {
				t.Fatalf("%s：应返回错误而不是可能错误的结果", tc.name)
			}
			if _, ok := err.(*UnsupportedChangeError); !ok {
				t.Fatalf("%s：应为 UnsupportedChangeError，实际 %T", tc.name, err)
			}
			if !reflect.DeepEqual(before, findingIDs(eng.Findings())) {
				t.Fatalf("%s：被拒绝后结论状态发生了变化", tc.name)
			}
		})
	}
}

func TestRevisionAndHistory(t *testing.T) {
	eng := mustEngine(t, canonicalText)
	if eng.Revision() != 1 {
		t.Fatalf("首次构建修订号应为 1，实际 %d", eng.Revision())
	}
	_, err := eng.Apply(AddComponentChange{ComponentInput{
		Ref: "U9", Type: "R",
		Pins: []PinInput{{Name: "1", Net: "DEAD", Role: RolePassive}},
	}})
	if err != nil {
		t.Fatalf("改动失败: %v", err)
	}
	if eng.Revision() != 2 {
		t.Fatalf("成功改动后修订号应为 2，实际 %d", eng.Revision())
	}
	hist := eng.History()
	if len(hist) != 2 || hist[0].ChangeOp != "initial" || hist[1].ChangeOp != "add_component" {
		t.Fatalf("历史记录不符: %+v", hist)
	}
	// 历史快照可复现：第 1 版包含 DEAD 孤立，第 2 版不再包含。
	if !contains(findingIDs(hist[0].Findings), "ERC002|net:DEAD") {
		t.Fatalf("历史第 1 版应保留当时的孤立结论")
	}
	if contains(findingIDs(hist[1].Findings), "ERC002|net:DEAD") {
		t.Fatalf("历史第 2 版不应再包含已失效的孤立结论")
	}
}

// stringsRepl 顺序执行多组替换（每组 old/new 成对）。
func stringsRepl(s string, pairs ...string) string {
	for i := 0; i+1 < len(pairs); i += 2 {
		s = replaceAll(s, pairs[i], pairs[i+1])
	}
	return s
}

func replaceAll(s, old, new string) string {
	out := s
	for {
		idx := indexOf(out, old)
		if idx < 0 {
			return out
		}
		out = out[:idx] + new + out[idx+len(old):]
	}
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// scenarioCase 描述一个独立场景：起始文本 + 一组改动 + 每步后的期望真值文本。
type scenarioCase struct {
	name  string
	start string
	steps []scenarioStep
}

type scenarioStep struct {
	change Change
	// expect 为该步完成后、以全新解析+全量重查得到的网表文本。
	expect string
}

func TestIncrementalScenarios(t *testing.T) {
	base := func(body string) string {
		return "net VCC kind=power\nnet GND kind=ground\nnet SIG kind=signal\n" + body
	}
	cases := []scenarioCase{
		{
			name: "新增悬空引脚元件后再删除",
			start: base(
				"component U1 type=IC\n" +
					"pin U1 1 net=VCC role=power_in\npin U1 2 net=GND role=power_in\n" +
					"pin U1 3 net=SIG role=driver\n"),
			steps: []scenarioStep{
				{
					change: AddComponentChange{ComponentInput{
						Ref: "X1", Type: "R",
						Pins: []PinInput{{Name: "7", Role: RolePassive}}, // 悬空
					}},
					expect: base(
						"component U1 type=IC\n" +
							"pin U1 1 net=VCC role=power_in\npin U1 2 net=GND role=power_in\n" +
							"pin U1 3 net=SIG role=driver\n" +
							"component X1 type=R\npin X1 7 role=passive\n"),
				},
				{
					change: RemoveComponentChange{Ref: "X1"},
					expect: base(
						"component U1 type=IC\n" +
							"pin U1 1 net=VCC role=power_in\npin U1 2 net=GND role=power_in\n" +
							"pin U1 3 net=SIG role=driver\n"),
				},
			},
		},
		{
			name: "网络重命名迁移孤立与多驱动结论",
			start: "net A kind=signal\nnet B kind=signal\nnet C kind=signal\n" +
				"component U1 type=R\ncomponent U2 type=R\n" +
				"pin U1 1 net=A role=driver\npin U2 1 net=A role=driver\n",
			steps: []scenarioStep{
				{
					change: RenameNetChange{OldName: "A", NewName: "A2"},
					expect: "net A2 kind=signal\nnet B kind=signal\nnet C kind=signal\n" +
						"component U1 type=R\ncomponent U2 type=R\n" +
						"pin U1 1 net=A2 role=driver\npin U2 1 net=A2 role=driver\n",
				},
				{
					change: MergeNetsChange{From: "B", To: "C"}, // B、C 都孤立，合并后 C 仍孤立，B 消失
					expect: "net A2 kind=signal\nnet C kind=signal\n" +
						"component U1 type=R\ncomponent U2 type=R\n" +
						"pin U1 1 net=A2 role=driver\npin U2 1 net=A2 role=driver\n",
				},
			},
		},
		{
			name: "删除驱动源使多驱动冲突解除",
			start: "net S kind=signal\n" +
				"component U1 type=IC\ncomponent U2 type=IC\n" +
				"pin U1 1 net=S role=driver\npin U2 1 net=S role=driver\n",
			steps: []scenarioStep{
				{
					change: RemoveComponentChange{Ref: "U1"},
					expect: "net S kind=signal\n" +
						"component U2 type=IC\npin U2 1 net=S role=driver\n",
				},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			eng := mustEngine(t, tc.start)
			assertSameFindings(t, tc.name+"/initial", tc.start, eng.Findings())
			for i, st := range tc.steps {
				fs, err := eng.Apply(st.change)
				if err != nil {
					t.Fatalf("步骤 %d 改动被拒绝: %v", i, err)
				}
				assertSameFindings(t, tc.name, st.expect, fs)
			}
		})
	}
}
