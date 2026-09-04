# 02 · 世界模型：World、世界宪章与多世界平级

## 1. 为什么需要 World 轴

传统记忆系统隐含假设"只有一个现实"。但真实场景里，Agent 同时穿行于多个**各自成体**的世界：

- `world=real`：物理现实（用户生活、传感器观测）；
- `world=<作品>`：二次元/小说/游戏剧情，如 `world:fate`、`world:one-piece`；
- `world=<代码库>`：如 `world:repo:alaya`——有自己法则（架构约定）、历史（legacy）、"雷区"；
- `world=<社群虚构共识>`：一个群体的共同叙事、黑话、内部史。

关键性质：**每个世界有一套内部为真的判据（diegetic truth）**。"咒力存在于《咒术回战》"在其世界内为真，现实世界的反例无权推翻它——跨世界使用反例是范畴错误。

因此引入与 `scope` 平级的一根轴：**`world` 轴**。所有陈述、事件、共识都必须标注其所属世界。

## 2. 世界容器 World

```ts
interface World {
  id: string                    // world:real | world:fate | world:repo:alaya | world:<namespace>
  charter: WorldCharter         // 世界宪章（见下）
  canonLayers: CanonLayer[]     // canon 分层（见 §4）
  status: "forming" | "abiding" | "declining" | "empty" | "rebooted"
  parent?: string               // 平行世界/衍生世界可指向上游世界
  createdAt: string
}
```

### 2.1 世界宪章（World Charter）

宪章是一个世界的"法"：它定义该世界内**什么是真、如何推演**。宪章本身也是记忆对象（可追溯、可 supersede）。

```ts
interface WorldCharter {
  worldId: string
  version: string
  // 规则集：世界内的"物理/法则"。real 世界 = 物理定律 + 常识模型；
  // 虚构世界 = 作者设定；代码世界 = 架构约束与语言语义。
  rules: Rule[]
  // 本体：该世界承认的实体类型与关系类型（角色、国家、API、模块…）
  ontology: OntologyRef
  // 时间模型：线性 / 多时间线 / 循环
  timeModel: "linear" | "branching" | "cyclic"
  // 真值基准：该世界内的裁决以什么为准
  truthBasis: "observation" | "canon" | "simulation" | "hybrid"
  supersedes?: string
}
```

宪章的裁决意义：**事实裁决机在 world 内路由判据**。real 世界以观测与共识为准；虚构世界以 canon 与文本证据为准；代码世界以"可运行的测试/编译/类型检查"为准（可计算的 oracle）。

## 3. 实在性梯度（替代"真/假"二值）

一个声称在某个世界的"实"程度：

```
reality(world, claim) = f( 自洽度 × scope 共识强度 × 可参与检验度 )
```

- 自洽度：与宪章、既有 canon 的一致性；
- scope 共识：支持该声称的 scope 规模与强度（现实中"全世界"，家庭中"全家人"，作品里"官方+粉丝共同体"）；
- 可检验：能否被新的观测/运行/推演重复验证。

因此 **real 不占据真值垄断**，它只是这个梯度上取值最高的世界之一。结构上所有世界平级；差异只在参数。

## 4. Canon 分层：一个世界内部的 scope 化

虚构世界（以及代码库、社群）内部的"权威性"并不是铁板一块，必须分层。每层是一个 scope，有自己的共识机制：

| 层 | 代码世界例子 | 虚构世界例子 | 共识机制 |
|---|---|---|---|
| 官方 canon | 主干分支 + 维护者共识 | 原作文本 + 作者声明 | 权威 scope |
| 共识解读 | 社区公认的最佳实践/约定 | 粉丝共同体考据共识 | 共同体 scope |
| 个人解读 | 个人 fork 的理解 | 个人二创/解读 | 个人 scope |

- 层间允许冲突且**并存**（supersede 不互删）："原作如此"与"我的解读如此"都合法，只是 scope 不同；
- 世界内的 canon 冲突检测是推理面的重要任务（见 05）；
- 平行世界（multiverse / fork / 重启）用 `world.parent` 表达分叉：共享上游基岩，各自成体。

## 5. 世界生命周期：成住坏空

| 阶段 | 虚构世界 | 代码世界 |
|---|---|---|
| 成 forming | 连载/世界观铺开 | 项目初始化、架构成形 |
| 住 abiding | 设定稳定、粉丝共识成熟 | 约定成熟、文档与测试齐备 |
| 坏 declining | 烂尾/设定崩坏/热度消退 | 技术债堆积、约定失效 |
| 空 empty | 冷门、共识消散 | 废弃、无人维护 |
| 再成 rebooted | 重启/平行世界 | 重写/新架构时代 |

- 基岩不随世界消亡而删除——世界"空"了只是共识消散，其痕迹仍是历史；
- 世界可因新的共识"再成"（重启），旧 canon 保留为 `superseded` 层，供仲裁与考古。

## 6. 跨世界参照（World Bridge 的数据基础）

世界之间不隔离，而是通过显式边互相关联：

```ts
// 结构映射：世界 A 的结构可作为理解世界 B 的模板
WorldMapping {
  id, from: worldId, to: worldId,
  kind: "analogy" | "metaphor" | "instance" | "allegory"
  edges: [{ fromNode, toNode, confidence }]   // 节点/关系对应
  rationale: ClaimRef,                        // 为什么这样映射（必须可溯源）
}
```

现实意义：**虚构世界是理解用户意义系统的高密度入口**（用户用热血漫结构组织自己的行动）；代码世界与现实世界通过"线上事故 ↔ 测试复现"互相校验。跨世界映射边承担"隐喻索引"的职能——它让记忆能回答"用户说的'这剧情太宿命论了'，在指他现实里的哪件事"。
