# Heros TODO — Memory 结构与可观测性

本文是专题待办，聚焦三件事：
1) memory 目录树结构标准化；
2) memory 文件之间可互链、可追踪；
3) logging / tracing / observability 落地。

关联文档：
- [TODO.md](TODO.md)
- [AGENT_LAYOUT.md](AGENT_LAYOUT.md)
- [MEMORY-VAULT.md](MEMORY-VAULT.md)
- [ARCHITECTURE.md](ARCHITECTURE.md)

---

## A. Memory 文件夹树（目标结构）

> 目标：本地磁盘可读、可审计、可恢复；与 SQLite/Qdrant/Neo4j 索引协同，但磁盘结构可独立理解。

```text
<data_dir>/
  memory/
    <tenant>/
      sessions/
        <session-id>/
          meta.json
          turns.jsonl
          links.json              # 新增：本 session 的显式链接关系
      entities/
        <entity-id>.md            # 可选：长期实体页（客户、项目、组件）
      indexes/
        sessions.index.json       # 新增：会话级索引快照（可重建）
        entities.index.json       # 新增：实体级索引快照（可重建）
```

### A1 待办（结构）

- [ ] 定义 `links.json` schema（`source`, `target`, `rel`, `created_at`, `confidence`, `provenance`）。
- [ ] 定义 `sessions.index.json` / `entities.index.json` schema（版本号 + checksum + 生成时间）。
- [ ] 增加 `memory schema version` 字段并支持迁移。
- [ ] 增加启动期目录自检（缺目录自动修复 + 只读告警）。
- [ ] 增加损坏文件恢复策略（坏行跳过、隔离到 quarantine、继续服务）。

---

## B. 文件互链（File-to-file Linking）

> 目标：任意 memory 文件可链接到其他文件，并可追踪来源，支持人类阅读与机器检索。

### B1 链接模型（建议）

- [ ] 统一 link 类型：`REFERS_TO`, `DERIVED_FROM`, `SUMMARIZES`, `DECIDES`, `BLOCKS`, `SUPERSEDES`。
- [ ] 每条链接都带 `provenance`（来自哪次工具调用/审批/会话）。
- [ ] 链接同时写入：
  - 磁盘：`links.json`
  - 数据库：`graph_edges`
  - Neo4j（若启用）
- [ ] `turns.jsonl` 中保留 `link_ids`，便于回放与审计。

### B2 链接操作能力

- [ ] 新增 API：`POST /api/memory/links`（创建/更新链接）。
- [ ] 新增 API：`GET /api/memory/links?session_id=...`（读取链接）。
- [ ] 新增 CLI 工具：`heros_memory_link`、`heros_memory_links_list`。
- [ ] 新增冲突策略：重复 link 幂等；相反关系可共存但标注权重和时间。

### B3 链接质量

- [ ] 周期任务检查孤儿节点（没有入边/出边且长期未访问）。
- [ ] 为高价值链接增加人工审批入口（可选）。
- [ ] 链接误差回滚：支持按 `provenance` 批量撤销。

---

## C. Logging / Tracing / Observability

> 目标：任何一次“提案、审批、文件改动、检索、分发”都可追踪。

### C1 Logging（结构化日志）

- [ ] 全链路统一字段：`ts`, `level`, `node_id`, `tenant_id`, `session_id`, `trace_id`, `span_id`, `event`, `status`, `latency_ms`, `error_code`。
- [ ] 关键事件日志化：`proposal_submitted`, `proposal_approved`, `mutation_applied`, `memory_link_created`, `inbox_message_received`, `inbox_message_applied`。
- [ ] 敏感信息脱敏策略：token、密钥、PII 字段统一 redaction。
- [ ] 提供日志采样策略（debug 全量，info 抽样，error 全量）。

### C2 Tracing（分布式追踪）

- [ ] 引入 OpenTelemetry tracing（HTTP handler、memory retrieve、proposal apply、fleet 分发）。
- [ ] 在 agentd/collective/NATS worker 之间透传 `traceparent`。
- [ ] 每个 mutation 链接到 trace（日志里可直接跳转 trace_id）。
- [ ] 慢查询与慢操作阈值告警（例如 >500ms retrieve，>2s apply）。

### C3 Metrics（指标）

- [ ] 指标补齐：请求量、错误率、P95/P99、审批时延、同步时延、队列积压、重试次数、死信数量。
- [ ] memory 质量指标：链接数、孤儿节点数、检索命中率、空结果率。
- [ ] 业务指标：批准率、回滚率、跨节点下发成功率。
- [ ] Prometheus 指标命名规范与 dashboard（Grafana）模板。

### C4 SLO / 告警

- [ ] 定义 SLO：可用性、延迟、同步成功率、审批闭环时长。
- [ ] 告警分级：P1（数据损坏/全局失败）、P2（高错误率）、P3（性能退化）。
- [ ] 运行手册：告警 -> 排查路径 -> 回滚策略 -> 恢复验证。

---

## D. 与 Admin Board / Inbox 的联动

> 对齐“个人 agent 接收 admin board 下发 skill/tool/memory 包”的需求。

- [ ] 定义 inbox message schema（`message_id`, `tenant_id`, `payload_type`, `payload_version`, `signature`, `created_at`, `expire_at`）。
- [ ] inbox 消费状态机：`received -> verified -> applied -> acked`，失败进入 `retry/dead-letter`。
- [ ] 下发 payload 支持 `skill`, `tool`, `memory-link-batch`, `memory-entity`。
- [ ] 关联 tracing：每条 inbox message 必须可追踪到上游审批记录与操作人。

---

## E. 验收标准（Definition of Done）

- [ ] memory 目录树与 schema 文档化完成，且有迁移策略。
- [ ] 文件互链可创建、查询、追踪、回滚；磁盘与图索引一致。
- [ ] 日志、trace、metrics 三件套可在一个 dashboard 上关联排障。
- [ ] 对 inbox 下发链路做故障演练（签名失败、重复消息、部分成功、重试耗尽）。
- [ ] 文档互链完整：本文件 <-> `TODO.md` <-> `AGENT_LAYOUT.md` <-> `MEMORY-VAULT.md`。

