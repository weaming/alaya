# Alaya

藏万法之识，成一切世界。

Alaya（阿赖耶识 / 藏识）是一套面向 Agent 与人群的**多元世界记忆系统**设计：以不可变基岩保存一切发生过的痕迹（种子），以 scope 化共识支撑各世界（器世间）的成立，并在其上完成从数据到事实、从事实到知识的计算与推理。

- 设计文档在 [`docs/`](docs/)：从 `01-overview.md` 开始读。

## M0 记忆底座（已实现）

Go 单二进制，对外提供 MCP 工具。只做设计里最不该变的那一层——
**数据契约与最小读写路径**，其余全部留作可重建的派生层。

```bash
make install     # 安装到 $GOPATH/bin
alaya mcp        # 以 stdio 启动 MCP server
alaya verify     # 校验事件日志的完整性（内容哈希 + 链哈希）
```

数据存放在 `~/.alaya/`（`ALAYA_HOME` 可覆盖）：

- `events.jsonl` —— 唯一真源：append-only、内容寻址、per-scope 哈希链
- `INDEX.md` —— 人类可读的当前视图；派生物，`alaya rebuild` 随时重建

**不做**（在此版本中有意留空）：LLM 蒸馏、向量检索、共识机、跨世界桥、状态推断。
数据格式已为它们预留字段，日后加入无需迁移历史记录。

测试：`go test ./...`。设计依据见 `docs/12-academic-evidence-on-agent-memory.md`。
