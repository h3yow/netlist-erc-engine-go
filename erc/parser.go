package erc

import (
	"bytes"
	"sort"
	"strings"
)

// 网表文本格式（行导向，# 起始为注释）：
//
//	component <ref> type=<元件类型>
//	net <name> kind=signal|power|ground
//	pin <comp> <name> net=<netName> role=passive|driver|power_in|power_out
//
// 解析不会静默跳过任何残缺输入：一次解析返回发现的全部错误，
// 每条错误带行号、列号与可区分的错误码。

type token struct {
	text string
	col  int // 1-based 起始列
}

type field struct {
	key string // 位置参数为 ""
	val string
	col int
}

type rawRecord struct {
	line   int
	kind   string
	fields []field
}

var validPinRoles = map[PinRole]bool{
	RolePassive: true, RoleDriver: true, RolePowerIn: true, RolePowerOut: true,
}

var validNetKinds = map[string]bool{"signal": true, "power": true, "ground": true}

// Parse 把文本网表读入为统一模型。
// 只要存在任何非法输入即返回 (nil, ParseErrors)，绝不部分成功。
func Parse(text []byte) (*Netlist, error) {
	recs, errs := parseLines(text)
	if len(errs) > 0 {
		sortErrors(errs)
		return nil, ParseErrors(errs)
	}
	return buildNetlist(recs), nil
}

func parseLines(text []byte) ([]*rawRecord, ParseErrors) {
	var errs ParseErrors
	var recs []*rawRecord

	lines := bytes.Split(text, []byte("\n"))
	for i, raw := range lines {
		lineNo := i + 1
		raw = bytes.TrimRight(raw, "\r") // 容忍 CRLF
		trimmed := bytes.TrimSpace(raw)
		if len(trimmed) == 0 || trimmed[0] == '#' {
			continue
		}
		toks := tokenize(string(raw))
		if len(toks) == 0 {
			continue
		}
		rec, lineErrs := parseRecord(lineNo, toks)
		errs = append(errs, lineErrs...)
		if rec != nil {
			recs = append(recs, rec)
		}
	}

	errs = append(errs, validateSemantics(recs)...)
	return recs, errs
}

func tokenize(line string) []token {
	var toks []token
	i := 0
	for i < len(line) {
		for i < len(line) && (line[i] == ' ' || line[i] == '\t') {
			i++
		}
		if i >= len(line) {
			break
		}
		start := i
		for i < len(line) && line[i] != ' ' && line[i] != '\t' {
			i++
		}
		toks = append(toks, token{text: line[start:i], col: start + 1})
	}
	return toks
}

// parseRecord 解析单行；字段非法时返回错误，rec 为部分可用记录，
// 供语义阶段继续查错（整行无法识别时 rec 为 nil）。
func parseRecord(lineNo int, toks []token) (*rawRecord, ParseErrors) {
	var errs ParseErrors
	head := toks[0]
	rec := &rawRecord{line: lineNo, kind: head.text}

	var allowedOpts map[string]bool
	var positional int
	switch head.text {
	case "component":
		allowedOpts = map[string]bool{"type": true}
		positional = 1
	case "net":
		allowedOpts = map[string]bool{"kind": true}
		positional = 1
	case "pin":
		allowedOpts = map[string]bool{"net": true, "role": true}
		positional = 2
	default:
		return nil, ParseErrors{&ParseError{
			Line: lineNo, Column: head.col, Code: ErrMalformedLine,
			Message: "未知指令 " + head.text + "（应为 component / net / pin）",
		}}
	}

	seenOpt := map[string]bool{}
	posCount := 0
	for _, t := range toks[1:] {
		if key, val, ok := splitKV(t.text); ok {
			if !allowedOpts[key] {
				errs = append(errs, &ParseError{
					Line: lineNo, Column: t.col, Code: ErrMalformedLine,
					Message: rec.kind + " 行不支持的字段 " + key,
				})
				continue
			}
			if seenOpt[key] {
				errs = append(errs, &ParseError{
					Line: lineNo, Column: t.col, Code: ErrDuplicateField,
					Message: "字段 " + key + " 在同一行重复出现",
				})
				continue
			}
			seenOpt[key] = true
			if val == "" {
				errs = append(errs, &ParseError{
					Line: lineNo, Column: t.col, Code: ErrEmptyID,
					Message: "字段 " + key + " 的值为空",
				})
			}
			rec.fields = append(rec.fields, field{key: key, val: val, col: t.col})
		} else {
			if posCount >= positional {
				errs = append(errs, &ParseError{
					Line: lineNo, Column: t.col, Code: ErrMalformedLine,
					Message: "多余的位置参数 " + t.text,
				})
				continue
			}
			rec.fields = append(rec.fields, field{val: t.text, col: t.col})
			posCount++
		}
	}

	if posCount < positional {
		names := map[string]string{
			"component": "元件位号 ref",
			"net":       "网络名 name",
			"pin":       "元件位号与引脚名",
		}
		errs = append(errs, &ParseError{
			Line: lineNo, Column: head.col, Code: ErrMissingField,
			Message: "缺少必需的位置参数：" + names[head.text],
		})
	}
	for _, req := range requiredOpts(head.text) {
		if !seenOpt[req] {
			errs = append(errs, &ParseError{
				Line: lineNo, Column: head.col, Code: ErrMissingField,
				Message: "缺少必需字段 " + req,
			})
		}
	}
	return rec, errs
}

func requiredOpts(kind string) []string {
	switch kind {
	case "component":
		return []string{"type"}
	case "net":
		return []string{"kind"}
	case "pin":
		return []string{"role"} // net 可省略：省略即悬空引脚（未连接网络）
	}
	return nil
}

func splitKV(tok string) (key, val string, ok bool) {
	idx := strings.IndexByte(tok, '=')
	if idx <= 0 {
		return "", "", false
	}
	return tok[:idx], tok[idx+1:], true
}

func getField(r *rawRecord, key string) (field, bool) {
	for _, f := range r.fields {
		if f.key == key {
			return f, true
		}
	}
	return field{}, false
}

func validateSemantics(recs []*rawRecord) ParseErrors {
	var errs ParseErrors

	type compDef struct {
		ref  string
		typ  string
		line int
		col  int
	}
	type netDef struct {
		name string
		kind string
		line int
		col  int
	}
	type pinDef struct {
		comp string
		name string
		net  string
		role PinRole
		line int
		cCol int
		nCol int
	}

	compSet := map[string]int{}
	netSet := map[string]int{}
	pinSeen := map[string]int{}
	var compDefs []compDef
	var netDefs []netDef
	var pinDefs []pinDef

	for _, r := range recs {
		switch r.kind {
		case "component":
			if len(r.fields) < 1 {
				continue
			}
			ref := r.fields[0]
			typ := ""
			if tf, ok := getField(r, "type"); ok {
				typ = tf.val
			}
			if _, dup := compSet[ref.val]; dup {
				errs = append(errs, &ParseError{
					Line: r.line, Column: ref.col, Code: ErrDuplicateComponent,
					Message: "元件 " + ref.val + " 重复定义",
				})
				continue
			}
			compSet[ref.val] = r.line
			compDefs = append(compDefs, compDef{ref: ref.val, typ: typ, line: r.line, col: ref.col})
		case "net":
			if len(r.fields) < 1 {
				continue
			}
			name := r.fields[0]
			kind := ""
			kCol := name.col
			if kf, ok := getField(r, "kind"); ok {
				kind = kf.val
				kCol = kf.col
				if kf.val != "" && !validNetKinds[kf.val] {
					errs = append(errs, &ParseError{
						Line: r.line, Column: kf.col, Code: ErrBadNetKind,
						Message: "网络类型 " + kf.val + " 非法（应为 signal/power/ground）",
					})
				}
			}
			if _, dup := netSet[name.val]; dup {
				errs = append(errs, &ParseError{
					Line: r.line, Column: name.col, Code: ErrDuplicateNet,
					Message: "网络 " + name.val + " 重复定义",
				})
				continue
			}
			netSet[name.val] = r.line
			netDefs = append(netDefs, netDef{name: name.val, kind: kind, line: r.line, col: kCol})
		case "pin":
			if len(r.fields) < 2 {
				continue
			}
			comp := r.fields[0]
			pname := r.fields[1]
			d := pinDef{comp: comp.val, name: pname.val, line: r.line, cCol: comp.col}
			if nf, ok := getField(r, "net"); ok {
				d.net = nf.val
				d.nCol = nf.col
				if nf.val == "" {
					errs = append(errs, &ParseError{
						Line: r.line, Column: nf.col, Code: ErrEmptyID,
						Message: "字段 net 的值为空（悬空引脚请直接省略 net 字段）",
					})
				}
			}
			if rf, ok := getField(r, "role"); ok && rf.val != "" {
				d.role = PinRole(rf.val)
				if !validPinRoles[d.role] {
					errs = append(errs, &ParseError{
						Line: r.line, Column: rf.col, Code: ErrBadPinRole,
						Message: "引脚角色 " + rf.val + " 非法（应为 passive/driver/power_in/power_out）",
					})
				}
			}
			key := d.comp + "\x00" + d.name
			if _, dup := pinSeen[key]; dup {
				errs = append(errs, &ParseError{
					Line: r.line, Column: comp.col, Code: ErrDuplicatePin,
					Message: "元件 " + d.comp + " 的引脚 " + d.name + " 重复定义",
				})
				continue
			}
			pinSeen[key] = r.line
			pinDefs = append(pinDefs, d)
		}
	}

	// 引用完整性。
	for _, p := range pinDefs {
		if _, ok := compSet[p.comp]; !ok {
			errs = append(errs, &ParseError{
				Line: p.line, Column: p.cCol, Code: ErrComponentNotFound,
				Message: "引脚属于不存在的元件 " + p.comp,
			})
		}
		if p.net != "" {
			if _, ok := netSet[p.net]; !ok {
				errs = append(errs, &ParseError{
					Line: p.line, Column: p.nCol, Code: ErrPinNetNotFound,
					Message: "引脚 " + p.comp + "." + p.name + " 指向不存在的网络 " + p.net,
				})
			}
		}
	}

	return errs
}

func buildNetlist(recs []*rawRecord) *Netlist {
	nl := &Netlist{
		Components: map[string]*Component{},
		Nets:       map[string]*Net{},
	}
	for _, r := range recs {
		switch r.kind {
		case "component":
			ref := r.fields[0].val
			typ := ""
			if tf, ok := getField(r, "type"); ok {
				typ = tf.val
			}
			nl.Components[ref] = &Component{Ref: ref, Type: typ}
		case "net":
			name := r.fields[0].val
			kind := "signal"
			if kf, ok := getField(r, "kind"); ok && kf.val != "" {
				kind = kf.val
			}
			nl.Nets[name] = &Net{Name: name, Kind: kind}
		case "pin":
			d := &Pin{
				Component: r.fields[0].val,
				Name:      r.fields[1].val,
			}
			if nf, ok := getField(r, "net"); ok {
				d.Net = nf.val
			}
			if rf, ok := getField(r, "role"); ok {
				d.Role = PinRole(rf.val)
			}
			nl.Pins = append(nl.Pins, d)
		}
	}
	return nl
}

func sortErrors(errs ParseErrors) {
	sort.SliceStable(errs, func(i, j int) bool {
		if errs[i].Line != errs[j].Line {
			return errs[i].Line < errs[j].Line
		}
		if errs[i].Column != errs[j].Column {
			return errs[i].Column < errs[j].Column
		}
		return errs[i].Code < errs[j].Code
	})
}
