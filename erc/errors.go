package erc

import "fmt"

// ParseErrorCode 以可区分的原因标识残缺/非法输入。
type ParseErrorCode string

const (
	ErrMalformedLine      ParseErrorCode = "malformed_line"      // 行结构无法识别
	ErrMissingField       ParseErrorCode = "missing_field"       // 必需字段缺失
	ErrDuplicateField     ParseErrorCode = "duplicate_field"     // 同一行内字段重复
	ErrBadPinRole         ParseErrorCode = "bad_pin_role"        // 引脚电气角色非法
	ErrBadNetKind         ParseErrorCode = "bad_net_kind"        // 网络类型非法
	ErrDuplicateComponent ParseErrorCode = "duplicate_component" // 元件重复定义
	ErrDuplicatePin       ParseErrorCode = "duplicate_pin"       // 同一元件引脚重复定义
	ErrDuplicateNet       ParseErrorCode = "duplicate_net"       // 同一网络重复定义
	ErrPinNetNotFound     ParseErrorCode = "pin_net_not_found"   // 引脚指向不存在的网络
	ErrComponentNotFound  ParseErrorCode = "component_not_found" // 引脚指向不存在的元件
	ErrEmptyID            ParseErrorCode = "empty_id"            // 标识符为空
)

// ParseError 指出一条非法输入的确切位置与原因。
type ParseError struct {
	Line    int
	Column  int
	Code    ParseErrorCode
	Message string
}

func (e *ParseError) Error() string {
	return fmt.Sprintf("line %d col %d: %s: %s", e.Line, e.Column, e.Code, e.Message)
}

// ParseErrors 聚合一次解析中发现的全部错误，避免只报第一个。
type ParseErrors []*ParseError

func (es ParseErrors) Error() string {
	if len(es) == 0 {
		return "no parse errors"
	}
	return fmt.Sprintf("%d parse error(s), first: %s", len(es), es[0].Error())
}

// UnsupportedChangeError 表示暂不支持的局部改动方式。
type UnsupportedChangeError struct {
	Op     string
	Reason string
}

func (e *UnsupportedChangeError) Error() string {
	return fmt.Sprintf("unsupported change %q: %s", e.Op, e.Reason)
}
