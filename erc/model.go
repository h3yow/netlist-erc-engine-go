package erc

// PinRole 描述引脚的电气角色。
type PinRole string

const (
	RolePassive  PinRole = "passive"   // 无源引脚
	RoleDriver   PinRole = "driver"    // 驱动输出引脚
	RolePowerIn  PinRole = "power_in"  // 受电引脚
	RolePowerOut PinRole = "power_out" // 电源输出引脚
)

// Severity 是结论的严重级别。
type Severity string

const (
	SevError   Severity = "error"
	SevWarning Severity = "warning"
	SevInfo    Severity = "info"
)

// Component 表示网表中的一个元件实例。
type Component struct {
	Ref  string
	Type string
}

// Pin 表示元件上的一个引脚及其连接到的网络。
type Pin struct {
	Component string
	Name      string
	Net       string
	Role      PinRole
}

// Net 表示一个命名网络及其类型。
type Net struct {
	Name string
	Kind string // signal / power / ground
}

// Netlist 是元件、引脚、网络的统一内存模型。
type Netlist struct {
	Components map[string]*Component
	Pins       []*Pin // 以追加顺序保存，保证输出稳定
	Nets       map[string]*Net
}

// Finding 是一条可复现的 ERC 结论。
type Finding struct {
	ID       string // 稳定标识：ruleID + "|" + scope
	RuleID   string
	Severity Severity
	Scope    string // 结论所附着的主体，如 pin:U1.3 / net:N1 / comp:U1
	Message  string
	Detail   map[string]string
}

// Config 是规则集与参数。
type Config struct {
	EnabledRules     map[string]bool
	MaxDrivers       int             // 单个网络允许的最大 driver 数（power_out 不计入）
	PowerNetKinds    map[string]bool // 被视为电源轨的网络 kind
	PoweredCompTypes map[string]bool // 需要电源/地连接的元件类型
}
