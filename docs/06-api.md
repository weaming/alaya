# 06 · 接口契约草案（数据面 + 推理面）

本文给出 Alaya 对外的最小 API 面草案，供实现时演进。原则：**数据面薄而稳，推理面厚而可配**；所有写操作在存储层只追加，语义层负责 supersede。

## 1. 数据面（Data Plane）

### 1.1 写入

```ts
// 事件入基岩（一切输入的入口）
POST /events
{
  realm: "real" | "virtual" | "fiction" | "report",  // 事件来源性质
  worldId: string,              // world:real / world:<作品> / world:repo:<x>
  subject: EntityRef,           // 涉及主体
  payload: { ... },             // 事件负载（schema 化）
  observedAt?: string,          // valid time；缺省=now
  attributedBy: ParticipantRef, // 谁报告的（传感器/人/agent/文本）
}
→ event_id（内容哈希）

// 显式记忆/声称写入（人或 agent 直接断言）
POST /claims  { quad + scope + confidence 初值 } → claim_id

// 共识操作
POST /consensus { scope, claimId, stance: "agree"|"dissent", reason }
→ 更新 ConsensusObject 状态（只追加记录）
```

### 1.2 读取

```ts
GET /events/{eventId}                     // 单事件
GET /events?world=&subject=&since=        // 事件流查询（游标分页）
GET /claims?subject=&predicate=&world=    // 声称查询（含裁决状态）
GET /claims/{id}/evidence                 // 下钻到证据链
GET /consensus?scope=&claimId=            // 共识状态 + dissent 列表
GET /worlds/{worldId}/charter             // 世界宪章（版本化）
GET /entities/{urn}/timeline              // 实体演化时间线
```

### 1.3 导出与删除（可携带、可遗忘）

```ts
GET  /export?scope=user:garden&format=json|markdown   // 完整导出
POST /forget { scope, targets: [eventId|claimId|entityUrn] }
// → 写 tombstone；派生层清理；基岩内容擦除、哈希与元数据留存
```

## 2. 推理面（Reasoning Plane）

### 2.1 裁决

```ts
POST /adjudicate { claimId, scope }         // 触发/获取裁决（惰性计算）
→ { verdict, scorecard, basis }            // 得分卡可解释

POST /adjudicate/batch { claimIds, scope }  // 批量（蒸馏后调用）
```

### 2.2 蒸馏与反思

```ts
POST /consolidate { worldId, window: "session"|"day"|"week"|"idle" }
→ 生成的候选声称列表（各带证据引用），自动进入待裁决队列
```

### 2.3 检索与注入（Recall）

```ts
POST /recall
{
  query: "…", worldId, scope,
  budget: { core?: 600, semantic?: 800, fiction?: 300 },
}
→ { slots: [{ kind, entries: [...] }], spentTokens, meta }
// entries 已按 rerank 打分排序、格式化完毕，可直接拼入 prompt

POST /search   // agent 按需深挖（不受注入预算约束，受结果上限约束）
{ query, worldId?, scope?, topK, filter?: {...} }
```

### 2.4 世界推演与跨世界

```ts
POST /simulate { worldId, assumption, horizon? }   // 世界内反事实推演
→ 派生事件流 + canon 冲突报告（如有）

POST /project { fromWorld, toWorld, structureRef } // 跨世界结构投影
→ WorldMapping 候选边（需人工/共识确认后才固化）
```

### 2.5 认知循环（高层组合）

```ts
POST /cycle            // 跑一轮 Observe→…→Act 的增量（内部编排上述各端）
POST /cycle/schedule   // 配置空闲期 reflection 策略
```

## 3. 事件 schema 示例

```jsonc
// 一次"现实观测"
{
  "schemaVersion": "0.1",
  "realm": "real",
  "worldId": "world:real",
  "subject": "urn:alaya:person:user:garden",
  "type": "preference.declared",
  "payload": { "topic": "coffee", "value": "hand_drip" },
  "observedAt": "2025-09-05T02:30:00+08:00",
  "attributedBy": "urn:alaya:agent:assistant"
}

// 一次"虚构世界 canon 事件"
{
  "schemaVersion": "0.1",
  "realm": "fiction",
  "worldId": "world:one-piece",
  "subject": "urn:alaya:character:luffy",
  "type": "plot.reveal",
  "payload": { "chapter": 1120, "event": "..." },
  "observedAt": "剧情内时间",          // valid time 用剧情时间
  "attributedBy": "urn:alaya:source:manga:ch1120"
}
```

## 4. 实现分层建议

```
api/         接口层（OpenAPI/ts 类型）
engine/      三台机器 + 蒸馏 + recall（05 的引擎实现）
store/       基岩(append+hash) / 声称层 / 派生索引
domain/      模型与 schema（04/03/02 的类型）
```

## 5. 兼容性与演进

- 所有对象带 `schemaVersion`；新版本读取器兼容旧版本；
- 对外优先对齐既有协议（如把部分数据面暴露为 MCP tool），内核不依赖宿主框架；
- 数据可整体导出为 JSON/Markdown，换库/换宿主零锁定。
