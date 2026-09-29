package erc

import (
	"fmt"
	"strings"
	"testing"
)

// requireParse 解析网表，失败时打印完整输入与全部错误，便于定位。
func requireParse(t *testing.T, text string) *Netlist {
	t.Helper()
	nl, err := Parse([]byte(text))
	if err != nil {
		t.Fatalf("解析失败，输入网表:\n%s\n判定依据: %v", text, err)
	}
	return nl
}

// expectParseError 断言解析失败且包含一个指定 code/行号的错误；
// 失败时打印输入与实际错误清单。
func expectParseError(t *testing.T, text string, wantCode ParseErrorCode, wantLine int) {
	t.Helper()
	_, err := Parse([]byte(text))
	if err == nil {
		t.Fatalf("期望解析失败（%s），但解析成功。输入网表:\n%s", wantCode, text)
	}
	pe, ok := err.(ParseErrors)
	if !ok {
		t.Fatalf("期望 ParseErrors，得到 %T。输入网表:\n%s", err, text)
	}
	for _, e := range pe {
		if e.Code == wantCode && (wantLine == 0 || e.Line == wantLine) {
			return
		}
	}
	var codes []string
	for _, e := range pe {
		codes = append(codes, fmt.Sprintf("%s@%d:%s", e.Code, e.Line, e.Message))
	}
	t.Fatalf("未找到期望错误 %s@line%d。输入网表:\n%s\n实际判定依据:\n  %s",
		wantCode, wantLine, text, strings.Join(codes, "\n  "))
}

func TestParseValidNetlist(t *testing.T) {
	text := `
net VCC kind=power
net GND kind=ground
net S1 kind=signal
component U1 type=IC
pin U1 1 net=VCC role=power_in
pin U1 2 net=GND role=power_in
pin U1 3 net=S1 role=driver
`
	nl := requireParse(t, text)
	if len(nl.Components) != 1 || len(nl.Nets) != 3 || len(nl.Pins) != 3 {
		t.Fatalf("模型规模不符：comps=%d nets=%d pins=%d。输入:\n%s",
			len(nl.Components), len(nl.Nets), len(nl.Pins), text)
	}
	if nl.Nets["VCC"].Kind != "power" {
		t.Fatalf("VCC kind=%q 期望 power", nl.Nets["VCC"].Kind)
	}
}

func TestParseMalformedClassifications(t *testing.T) {
	// 未知指令 / 行结构非法
	expectParseError(t, "wire X", ErrMalformedLine, 1)
	// 缺少位置参数
	expectParseError(t, "component type=R", ErrMissingField, 1)
	// 缺少具名字段
	expectParseError(t, "net VCC", ErrMissingField, 1)
	// 字段重复
	expectParseError(t, "net VCC kind=power kind=ground", ErrDuplicateField, 1)
	// 空值
	expectParseError(t, "net VCC kind=", ErrEmptyID, 1)
	// 非法 role
	expectParseError(t,
		"net N1 kind=signal\ncomponent U1 type=IC\npin U1 1 net=N1 role=output",
		ErrBadPinRole, 3)
	// 非法 kind
	expectParseError(t, "net N1 kind=magic", ErrBadNetKind, 1)
	// 元件重复
	expectParseError(t,
		"component U1 type=IC\ncomponent U1 type=R", ErrDuplicateComponent, 2)
	// 网络重复
	expectParseError(t,
		"net N1 kind=signal\nnet N1 kind=signal", ErrDuplicateNet, 2)
	// 引脚重复
	expectParseError(t,
		"net N1 kind=signal\ncomponent U1 type=IC\npin U1 1 net=N1 role=driver\npin U1 1 net=N1 role=passive",
		ErrDuplicatePin, 4)
	// 引脚指向不存在的网络
	expectParseError(t,
		"component U1 type=IC\npin U1 1 net=GHOST role=passive",
		ErrPinNetNotFound, 2)
	// 引脚指向不存在的元件
	expectParseError(t,
		"net N1 kind=signal\npin U9 1 net=N1 role=passive",
		ErrComponentNotFound, 2)
	// 不支持的字段
	expectParseError(t, "net N1 kind=signal value=3v3", ErrMalformedLine, 1)
}

func TestParseReportsAllErrorsWithPositions(t *testing.T) {
	// 同一输入里的多个缺陷应一次性全部报告，且位置准确。
	text := "net N1 kind=signal\ncomponent U1 type=IC\npin U1 1 net=GHOST role=banana\nnet N1 kind=signal"
	_, err := Parse([]byte(text))
	if err == nil {
		t.Fatalf("期望多个解析错误。输入:\n%s", text)
	}
	pe := err.(ParseErrors)
	want := map[ParseErrorCode]int{
		ErrPinNetNotFound: 3,
		ErrBadPinRole:     3,
		ErrDuplicateNet:   4,
	}
	got := map[ParseErrorCode]int{}
	for _, e := range pe {
		got[e.Code]++
	}
	for code, line := range want {
		found := false
		for _, e := range pe {
			if e.Code == code && e.Line == line && e.Column > 0 {
				found = true
			}
		}
		if !found {
			t.Fatalf("缺少带位置的 %s@%d。输入:\n%s\n实际: %v", code, line, text, pe)
		}
	}
}

func TestParseNoSilentSkip(t *testing.T) {
	// 未知指令绝不能被静默忽略。
	text := "net N1 kind=signal\nfoobar this is ignored\ncomponent U1 type=IC"
	expectParseError(t, text, ErrMalformedLine, 2)
}

func TestParseErrorOrderingStable(t *testing.T) {
	text := "net N2 kind=signal\nnet N1 kind=signal\nnet N2 kind=signal"
	_, err := Parse([]byte(text))
	pe := err.(ParseErrors)
	if len(pe) != 1 || pe[0].Line != 3 || pe[0].Code != ErrDuplicateNet {
		t.Fatalf("错误归类不符: %v。输入:\n%s", pe, text)
	}
}
