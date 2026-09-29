# Netlist ERC Engine (Go)

本地可运行的电路网表电气规则检查（ERC）引擎。它围绕电路的连接关系维护一份
可查询的状态；在网表被局部改动后做增量重查，增量结论与从头全量重查完全一致，
每条结论带稳定、可复现的标识、严重级别和判定依据。

## 构建与运行

```bash
go build ./...
go test ./...
go run ./cmd/erc examples/sample.net
go run ./cmd/erc -power VCC,VDD -ground GND,VSS examples/sample.net
```

退出码：`0` 无 error 级结论；`1` 存在 error 级结论；`2` 用法错误或网表被拒绝。

## 网表格式约定

行导向文本，`#` 之后为注释，`.end` 之后的内容被忽略。定义可按任意顺序出现，
解析分两遍：先收集元件/网络定义，再解析连接关系。

```text
.comp <位号> <型号> <引脚>:<方向>[/<类型>] ...
.net  <网络名> [signal|power|ground]
.join <网络> <位号>.<引脚> [<位号>.<引脚> ...]
.nc   <位号>.<引脚> ...
.end
```

- 引脚方向：`in`、`out`、`inout`、`pwr`（电源输入脚）、`pas`（无源）、`?`。
- 引脚类型：`vcc`、`gnd`、`signal`（缺省）。`pwr` 脚必须显式标注 `/vcc` 或
  `/gnd`；若引脚名包含 `VCC/VDD` 或 `GND/VSS` 则自动推断，否则拒绝。
- 网络类别：`signal`（缺省）、`power`、`ground`。`power`/`ground` 网络在多驱动
  规则中被视为电源轨，允许多个接收脚挂接。
- 引脚在被 `.join` 引用前处于未连接状态；`.nc` 表示**有意悬空**（不报 ERC001）。

### 残缺输入的拒绝原因（不静默跳过）

所有错误都带行号、列号和指令名，以 `ErrorKind` 区分：

| Kind | 触发条件 |
| --- | --- |
| `syntax_error` | 行首不是合法指令、引脚引用不是 `位号.引脚` 等 |
| `unknown_directive` | 无法识别的 `.xxx` 指令 |
| `missing_field` | 指令缺参数；`pwr` 脚无法推断 vcc/gnd 类型 |
| `invalid_value` | 非法方向、类型或网络类别 |
| `duplicate_definition` | 元件、网络或同一元件引脚重复定义 |
| `unknown_net` / `unknown_component` / `unknown_pin` | 连接指向不存在的对象 |
| `pin_already_joined` | 同一引脚被接到两个网络（或先 join 再 nc） |
| `conflict` | `.nc` 引脚又被 join；合并电源/地网络等 |

## 内置规则与结论标识

规则集带版本号（`RuleVersion`），结论 ID 形如 `ERCnnn-<scope>:<name>`，
按作用域稳定生成；同一状态重复检查不会产生重复条目，输出按 ID 排序。

| ID | 规则 | 级别 | 作用域 |
| --- | --- | --- | --- |
| ERC001 | 悬空引脚：未连接且未标 `.nc` | warning | `pin:<位号>.<引脚>` |
| ERC002 | 孤立网络：声明了但没有任何引脚 | warning | `net:<网络名>` |
| ERC003 | 多驱动冲突：信号网络上有 2 个及以上 `out`/`inout` 脚 | error | `net:<网络名>` |
| ERC004 | 缺少电源：有 `vcc` 脚的元件未接到 power 网络（或全设计无电源网络） | error | `component:<位号>` / `design:no_power_net` |
| ERC005 | 缺少地：同理针对 `gnd` | error | `component:<位号>` / `design:no_ground_net` |

判定依据写入 `Message`（例如 ERC003 会列出所有驱动脚的位号），`Report` 带上
所使用规则集的 `Version`。规则或参数变更后，旧 `Report` 仍带旧版本号可复现；
引擎用 `SetRuleSet` 切换后下次 `Recheck` 以新规则集完整重算。

电源/地网络的判定：网络类别为 `power`/`ground`，或网络名命中规则参数
`PowerNetNames`（默认 `VCC,VDD`）/ `GroundNetNames`（默认 `GND,VSS`）。

## 增量重查的适用范围

所有改动必须通过 `erc.Engine` 的方法进行（引擎才能记录受影响的作用域）：

| 方法 | 增量支持 | 受影响作用域 |
| --- | --- | --- |
| `AddComponent` | ✅ | 新元件所有引脚 + 全局电源/地规则 |
| `RemoveComponent` | ✅ | 被删引脚（含已消失结论）+ 其原所在网络 |
| `AddNet` | ✅ | 该网络 |
| `RemoveNet` | ✅ | 该网络（含孤立结论）+ 变悬空的引脚 |
| `RenameNet` | ✅ | 旧网络名结论失效，新名重算；引脚随之移动 |
| `MergeNets(src,dst)` | ✅ | 两个网络及其引脚；类别冲突（power×ground）直接拒绝 |
| `ConnectPin` | ✅ | 该引脚与目标网络 |
| `DisconnectPin` | ❌ 明确拒绝 | 模型被修改，但 `Recheck` 返回 `ErrFullRecheckRequired`，需调用 `Check()` 全量重查 |

正确性保证：增量重查先删除命中脏作用域的旧结论（因此不会残留失效结论），
再只重算受影响作用域（因此不会漏报）；ERC004/005 为全局规则，任何改动都会
重算。任何一次改动后，增量 `Report` 与把当前模型导出文本、重新解析后全量
`Check` 的结果逐条相同。

对暂不支持的改动方式（如 `DisconnectPin`）不返回可能错误的结果，而是
`Recheck` 明确返回 `ErrFullRecheckRequired`；调用 `Check()` 后即可得到正确
结果并恢复后续增量能力。

## 验证方法

- `pkg/netlist/parse_test.go`：合法解析、`pwr` 自动推断、15 类畸形输入的
  `ErrorKind` 与行列定位、`.end` 截断、改动原语的拒绝路径。
- `pkg/erc/rules_test.go`：五条规则的触发与豁免（电源轨不判多驱动）、干净
  设计零结论、重复检查幂等且无重复 ID。
- `pkg/erc/incremental_test.go`：增删元件、网络增删/重命名/合并、驱动冲突
  出现与消除、电源缺失、规则集变更、不支持改动的明确拒绝；每个用例都把
  增量结果与“等价输入文本的全量重查”逐条比对。
- `pkg/erc/fuzz_incremental_test.go`：固定随机种子的 400 步随机支持改动序列，
  每一步都导出当前模型、重新解析并全量检查，断言增量与全量完全一致。

任何断言失败时，测试会打印**输入网表全文**与**判定依据**（增量缺失、旧结论
残留、结论内容差异三类差异分别列出）。

```bash
go test -race -count=1 ./...
```
