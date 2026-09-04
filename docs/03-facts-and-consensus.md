# 03 · 事实与共识：声称模型、裁决机与共识机

## 1. 核心主张

系统里**没有"被存储的事实"**，只有**声称（claims）**。事实是裁决过程的**状态**，不是存储的**对象**：

> 事实 = 在一个 world 内、对某个 scope 而言，暂时未被反例推翻、有多重独立证据支撑、且可（在可计算世界内）被复现的声称。

裁决以追加方式留痕（谁、何时、基于什么证据、裁成什么状态），永不改写历史。**可纠错比正确更耐久。**

## 2. 声称模型（Claim）

所有断言（不论来自人、Agent、观测还是文本）进入系统时都只是一条带归属的声称：

```ts
interface Claim {
  id: string
  // quad：谁说、关于什么、断言什么、在哪个世界为真
  subject: EntityRef
  predicate: string            // 尽量来自 world ontology 的受控谓词
  object: Value | EntityRef
  worldId: string              // 真值所在世界
  // 归属
  attributedBy: ParticipantRef // 人/Agent/来源文本/观测器
  scope: ScopeRef              // 声称者所在的 scope（见 §4）
  observedAt: string           // valid time
  recordedAt: string           // transaction time
  schemaVersion: string
}
```

### 2.1 论证图（Argumentation Graph）

声称之上建立论证图，边类型决定裁决如何流动：

| 边 | 含义 | 示例 |
|---|---|---|
| `supports` | 支撑：证据/子声称支持父声称 | 事件流里的观测 → "上周三下雨" |
| `attacks` | 攻击：与父声称矛盾 | "上周三没下雨" 的声称 |
| `undercuts` | 釜底抽薪：攻击证据本身的可信度 | "那个传感器当时在维护" |
| `corroborates` | 佐证：独立来源一致（不互为证据） | 两人独立目击同一事件 |

**约束：论证边不得跨 world。** real 世界的反例不能 `attack` 虚构世界的声称（范畴错误）。跨世界的质疑只能经 World Bridge 显式路由。

### 2.2 置信度是可推演状态，不是字段

每条声称的置信度由论证图**动态推演**，不落库为固定值。推演输入：

- 证据链强度（指向基岩事件的引用数、独立性）；
- 反例与 undercut 的数量与强度；
- 共识支持度（见下）；
- 世界内自洽度（与宪章/canon 的一致性）；
- 观察者可信度（历史准确率——这本身也是可修正的记忆）。

## 3. 事实裁决机（Truth Machine）

### 3.1 声称状态机

```
        ┌─ 新证据/佐证 ──────────────┐
alleged ──▶ corroborated(scope内) ──▶ world_fact(达到阈值)
   │                │                    │
   └─ 反例/undercut ┴──▶ contested ──▶ demoted / archived
```

- `alleged`：单源声称，不自动注入任何上下文；
- `corroborated`：在某个 scope 内被多方支持，可注入该 scope 的低预算槽位；
- `world_fact`：该 world 内高置信、长期可用；
- `contested / demoted / archived`：状态迁移全部追加 `AdjudicationRecord`，原声称与证据永不删除。

### 3.2 裁决记录（一切裁决也是记忆）

```ts
AdjudicationRecord {
  id, claimId, verdict,          // verdict: 升级/降级/维持 contested
  basis: { evidences: [], attacks: [], consensus: ConsensusRef[],
           charterConsistency: number, oracleResult?: SimulationRef },
  adjudicatedBy: "human" | "agent" | "pipeline",
  at: string, supersedes?: string
}
```

### 3.3 世界内判据路由

| world 类型 | 裁决基准 | 主要证据 |
|---|---|---|
| real | 观测三角验证 + scope 共识 | 基岩 real 事件流 |
| 虚构（canon） | 文本证据 + 宪章自洽 | 作品文本事件流（realm=fiction） |
| 代码 | 可计算 oracle（测试/类型/编译） | 运行结果事件流（realm=virtual） |
| 混合 | 综合上述 | — |

## 4. 共识机（Consensus Machine）

### 4.1 Scope

Scope 是"世界成立"的最小单元，也是共识的作用域：

```ts
ScopeRef = "user:garden"           // 个人
         | "dyad:garden+alaya"     // 用户 × 助手（size=2 的最小共识体）
         | "family:lin"
         | "team:alpha"
         | "community:<作品粉丝会>"
         | "world:<worldId>:canon" // 世界内权威层
         | "humanity"
```

Scope 与佛教"业"的对应：**个业 = 个人 scope 的记忆；共业 = scope 内共享共识——一个世界因其中参与者的共识（共业）而成立。**

### 4.2 共识对象

```ts
ConsensusObject {
  id, scope: ScopeRef, claimId: string,
  participants: ParticipantRef[],      // 参与共识者
  level: "conflict" | "majority" | "unanimous" | "cross_scope",
  dissent: [{ participant, reason, claimId }],  // 异议与共识同存，永不删除
  achievedAt: string,
  supersedes?: string,
}
```

### 4.3 共识协议（推理面概要，详见 05）

1. 同一 scope 内出现 `attacks` 时触发；
2. 加权聚合：每个参与者的权重 = 其在该 world/主题上的可信度（可修正的历史准确率）× 角色权重（canon 权威 scope 权重更高）；
3. 分歧不强制消灭：可达成 `agree-to-disagree`，双方异议进入 `dissent`；
4. 共识对象本身可被超越（新证据、scope 扩大、canon 更新），用 `supersedes` 留链，不删除旧共识；
5. **跨 scope 收敛**：低层 scope 的一致意见向上迭代（个人 → 家庭 → 共同体 → …），形成更高层的共识——物理世界法则正是这种收敛的极限情形。

### 4.4 共识 vs 事实的关系

- 共识是**社会性事实的成立机制**（约定、规范、共同历史、canon）；
- 单靠共识不足以下"自然事实"的结论（需要观测/可检验性）；
- 单靠观测也不足以支撑社会性事实（需要 scope 认可）。
- 两者交叉才构成完整裁决——对应"correspondence（观测对应）+ coherence（共识融贯）"双判据。

## 5. 蒸馏管道对事实层的写入

事实层不是只靠裁决长出来的，还靠**蒸馏**（Consolidation）主动从证据中提取候选声称：

```
基岩事件批 → 蒸馏(LLM/规则，schema 化输出) → 候选声称(带证据引用) → 进入论证图等待裁决
```

- 蒸馏输出必须带 `confidence` 与 `importance` 初值（供预算与保留使用）；
- 蒸馏产生的声称必须有指向基岩的证据引用，否则视为无源声称降权；
- 蒸馏可反复重跑：模型换代后，用更好的模型对同一基岩重新蒸馏——记忆只变好，不锁死在旧模型水平（基岩不灭的价值所在）。

## 6. 设计约束小结

1. 一切写入只追加；任何"更新"= 新对象 + supersedes 指针；
2. 论证边不跨 world；跨世界质疑必须显式路由；
3. 事实是状态不是存储；置信度是可推演量；
4. 异议与共识同存；被推翻的声称与存留的异议是未来修正的原料（记忆的免疫系统）；
5. 共识是 scope 化的——没有无主真理，也没有全球统一裁决。
