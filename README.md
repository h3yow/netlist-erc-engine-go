# 网表电气规则检查（ERC）引擎

一个本地可运行的 Go 库与命令行工具。它把文本网表读入为「元件 / 引脚 / 网络」统一模型，
围绕连接关系维护一份**可查询的结论状态**，并在网表发生局部改动后做**增量重查**。
增量结论与从头全量重查完全一致——不漏报、不残留已失效结论；不支持的改动方式会被明确拒绝。

## 1. 构建与运行

```bash
go build ./...
go test ./...                 # 单元测试（含增量/全量一致性）
go run ./cmd/erc rules        # 列出内置规则
go run ./cmd/erc check x.net  # 检查网表，结论按稳定 ID 输出
go run ./cmd/erc check -json x.net
```

退出码：`0` 无 error 级结论；`1` 存在 error 级结论或解析失败；`2` 用法/IO 错误。

## 2. 网表文本格式约定

行导向、空白分词、`#` 起始为整行注释（空行忽略）。三种指令：

```
component <ref> type=<元件类型>
net <name> kind=signal|power|ground
pin <comp> <pinName> net=<netName> role=passive|driver|power_in|power_out
```

- `component`：一个位置参数 `ref`，必含具名字段 `type`。
- `net`：一个位置参数 `name`，必含 `kind`，取值仅允许 `signal` / `power` / `ground`。
- `pin`：两个位置参数 `comp`、`pinName`；必含 `role`；`net` **可省略**，省略表示该引脚
  悬空（未连接任何网络），会被 ERC001 报告。`role` 取值仅允许
  `passive` / `driver` / `power_in` / `power_out`。
- 引脚通过 `comp` 与 `net` 分别引用已定义的元件和网络；网络/元件必须先存在（顺序无关，
  但必须在文件内定义）。

示例：

```
net VCC kind=power
net GND kind=ground
net SIG kind=signal
component U1 type=IC
pin U1 1 net=VCC role=power_in
pin U1 2 net=GND role=power_in
pin U1 3 net=SIG role=driver
```

### 残缺输入不会被静默跳过

解析一次性收集**全部**错误，每条错误带 `行号:列号` 与可区分的错误码，只要存在非法输入
就整体拒绝（绝不返回部分模型）。错误码：

| 错误码 | 含义 |
| --- | --- |
| `malformed_line` | 未知指令、不支持的字段、多余的位置参数等行结构问题 |
| `missing_field` | 缺少必需的位置参数或具名字段 |
| `duplicate_field` | 同一行内字段重复 |
| `empty_id` | 字段值为空（悬空引脚应省略 `net`，而不是写 `net=`） |
| `bad_pin_role` | 引脚角色取值非法 |
| `bad_net_kind` | 网络类型取值非法 |
| `duplicate_component` | 元件位号重复定义 |
| `duplicate_net` | 网络重复定义 |
| `duplicate_pin` | 同一元件的同一引脚重复定义 |
| `pin_net_not_found` | 引脚指向不存在的网络 |
| `component_not_found` | 引脚属于不存在的元件 |

## 3. 内置规则

| 规则 | 严重级别 | 主体（scope） | 含义 |
| --- | --- | --- | --- |
| `ERC001` floating-pin | warning | `pin:<comp>.<pin>` | 引脚悬空（未连接网络） |
| `ERC002` orphan-net | warning | `net:<name>` | 网络上没有任何引脚 |
| `ERC003` multiple-drivers | error | `net:<name>` | 网络上 `driver` 引脚数超过 `MaxDrivers`（默认 1） |
| `ERC004` unpowered-comp | error | `comp:<ref>\|missing_power`、`comp:<ref>\|missing_ground` | 受电器件类型未同时连接 `power` 与 `ground` 网络 |

参数（`erc.DefaultConfig()`）：

- `MaxDrivers`：单网络允许的最大 `driver` 数。`power_out`（电源输出）不计入驱动冲突，
  因此电源轨上的 `power_out` 不会误报。
- `PoweredCompTypes`：需要电源/地连接的元件类型，默认 `IC`、`MCU`。电阻等无源类型不检查。
- `EnabledRules`：按规则 ID 开关规则。

### 稳定、可复现、可去重的结论标识

```
Finding.ID = <ruleID> "|" <scope>
例如 ERC003|net:SIG、ERC004|comp:U1|missing_ground
```

ID 只取决于规则与连接关系，不依赖检查次数或插入顺序，因此：

- 重复检查 / 全量重查产生同一条结论只保留一份（引擎以 ID 为主键存状态）；
- 查询按主体前缀进行（`net:`、`comp:U1`、`pin:U1.3`），且做了边界对齐，
  `comp:U1` 不会误命中 `comp:U10`；
- `Engine.History()` 保存每次修订（initial / 每次改动 / full_recheck）的结论快照，
  并附带规则集与参数的 SHA-256 `ConfigFingerprint`。规则集或参数变更后，
  旧结论仍可在对应历史修订下复现与核对。

## 4. 增量重查

引擎内部以「主体范围 scope」组织结论。一次局部改动先计算受影响范围，
然后只删除并在该范围内重算结论，范围外保持不动；删除时父范围（`comp:U1`）
会覆盖其全部子项（`comp:U1|missing_*`），保证删除元件后子结论不残留。

### 支持的改动（`Engine.Apply`）

| Change | 语义 | 影响范围 |
| --- | --- | --- |
| `AddComponentChange` | 新增元件及其引脚（引脚网络须已存在、角色合法、引脚不重复） | 新元件、其引脚、所连网络 |
| `RemoveComponentChange` | 删除元件并连同其全部引脚 | 元件、其引脚、原所连网络 |
| `MergeNetsChange` | 把 `From` 网络并入 `To`：引脚上的连接改指 `To`，删除 `From` | 两个网络、迁移引脚、相关元件 |
| `RenameNetChange` | 网络改名，同步更新所有引用该网络的引脚 | 旧网络名与新网络名 |

### 明确拒绝的改动

以下情况返回 `*erc.UnsupportedChangeError`，且**引擎状态与修订号保持不变**
（预校验在任何写入之前完成），不会返回可能错误的结果：

- 上述四种以外的改动，例如单独增删一个引脚、修改网络类型、修改元件类型等
  ——请重新解析网表后构建新引擎；
- 合并不存在的网络、把网络合并到自身、**合并不同 `kind` 的网络**；
- 重命名到已存在的网络（应使用合并）、重命名不存在的网络；
- 新增已存在的元件、删除不存在的元件；
- 新增引脚指向缺失网络、角色非法、引脚重复等。

### 一致性保证

- 每次成功改动 `Revision()` 加一；失败不加。
- 测试以「**文本侧重放改动 → 全新 `Parse` → 全新全量检查**」为对照真值，
  对每一步增量结果按结论 ID 集合逐一断言相等，覆盖：悬空引脚出现/消失、孤立网络
  被连接消除、重命名迁移结论、多驱动冲突随驱动源删除而解除、合并网络、缺电源/地
  结论随元件增删而出现/消失。
- 任何增量改动后都可调用 `FullRecheck()` 得到从头重算结果；测试保证二者始终一致。

## 5. 验证方法

```bash
go test -race -count=1 ./...   # 竞态 + 全量测试
go test -cover ./erc/          # 语句覆盖率（当前约 92%）
go vet ./...
```

测试失败信息会打印**输入网表全文**与**实际判定依据（结论 ID/消息或解析错误清单）**，
便于直接复盘。关键测试：

- `TestParseMalformedClassifications` / `TestParseReportsAllErrorsWithPositions`：
  畸形网表按可区分原因归类并带位置；
- `TestMultipleDrivers` / `TestPowerOutDoesNotCountAsDriver`：多驱动冲突与电源例外；
- `TestIncrementalMatchesFullRecheck` / `TestIncrementalScenarios`：
  局部改动与全量重查结果一致、不残留旧结论；
- `TestUnsupportedAndInvalidChangesRejected`：不支持的改动被明确拒绝且状态不变；
- `TestRepeatedChecksAreDeduplicatedAndStable` / `TestRuleToggleIsReproducible`：
  去重、稳定 ID、规则集变更与历史复现。

## 6. 代码结构

```
erc/model.go    统一模型、结论与配置类型
erc/errors.go   解析错误码、ParseError、UnsupportedChangeError
erc/parser.go   文本解析、全量校验（带位置）、模型构建
erc/rules.go    四条内置规则、默认配置、scope 工具
erc/engine.go   状态维护、查询、全量/范围重查、修订历史与配置指纹
erc/change.go   四种受支持局部改动、影响范围计算、拒绝策略
cmd/erc/main.go 命令行入口
```
