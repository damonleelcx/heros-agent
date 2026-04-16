# Heros — 统一 TODO（技术 + 商业 + Memory/Observability）

本文是唯一待办入口（技术、商业、memory / observability 待办均在此维护）。

## 1) 当前实现（已具备）

1. **本机节点：`agentd` / `heros`**  
   - 本地长期进程，磁盘为权威：`skills/`、`tools/`、`memory/`、`system/prompt.md`  
   - SQLite 索引与审批队列；可选 Neo4j/Qdrant/NATS  
   - 支持提案 -> 人工审批 -> 落盘（skills/tools/memory/harness）

2. **部署形态**  
   - 现状是二进制运行 + 配置文件  
   - 尚未内建完整 systemd/launchd/Windows Service 安装与自动升级闭环

3. **集体能力（当前为挂钩位）**  
   - 可将 proposal / approved-mutation 通过 HTTP 上送至 `collectived`  
   - 可通过 NATS 广播事件  
   - 尚未形成组织级技能/记忆自动双向同步成品

## 2) 商业目标对照（游戏 / 电商 / 通用企业）

| 目标能力 | 当前状态 |
|------|------|
| 每位员工本机装 agent 并日常使用 | **可行**（单机/单租户能力） |
| 管理员集体节点汇总全员知识 | **部分可行**（ingest + 事件） |
| 跨机器同步技能（版本/冲突/审批） | **未完成** |
| 跨机器同步记忆（隐私/租户/脱敏/分发） | **未完成** |
| 跨机器进度同步（里程碑/任务） | **未完成** |

一句话：当前是“可联邦的本地控制面 + 集体扩展点”，不是现成的组织级自动同步套件。

## 3) 统一待办（可拆 issue）

### A. 安装与运维产品化

- [ ] 提供 systemd / launchd / Windows Service 单元与安装脚本。
- [ ] 提供升级与回滚路径（版本检测、平滑迁移、失败回退）。
- [ ] 增加开箱即用运维文档（启动、重启、日志、备份、恢复）。

### B. 集体技能同步（push/pull 闭环）

- [ ] 定义技能同步协议：版本号、租户、签名、冲突策略、幂等键。
- [ ] 落地集体侧技能存储与 API（查询、差异、批量分发）。
- [ ] 工作站侧实现 pull/push worker 与自动重建索引流程。
- [ ] 明确“提案信号”和“已批准制品”两条通道的治理边界。

### C. 集体记忆同步（隐私可控）

- [ ] 定义记忆上收范围（租户/会话/实体）与脱敏策略。
- [ ] 设计本地 SQLite/Qdrant 与集体存储的分层职责。
- [ ] 实现记忆制品的版本、TTL、删除、回收与审计。
- [ ] 提供跨节点下发策略（全量/增量、优先级、失败重试）。

### D. Agent 间通信与 Inbox

- [ ] 定义 inbox message schema：`message_id`, `tenant_id`, `payload_type`, `payload_version`, `signature`, `created_at`, `expire_at`。
- [ ] 定义消费状态机：`received -> verified -> applied -> acked`，失败进入 `retry/dead-letter`。
- [ ] payload 支持 `skill`, `tool`, `memory-link-batch`, `memory-entity`。
- [ ] 增加签名校验、幂等消费、ACK/重试、审计日志。

### E. Memory 目录树标准化

目标目录：

```text
<data_dir>/
  memory/
    <tenant>/
      sessions/
        <session-id>/
          meta.json
          turns.jsonl
          links.json
      entities/
        <entity-id>.md
      indexes/
        sessions.index.json
        entities.index.json
```

- [ ] 定义 `links.json` schema（`source`, `target`, `rel`, `created_at`, `confidence`, `provenance`）。
- [ ] 定义 `sessions.index.json` / `entities.index.json` schema（版本号 + checksum + 生成时间）。
- [ ] 增加 `memory schema version` 与迁移策略。
- [ ] 增加启动期目录自检（缺目录自动修复 + 只读告警）。
- [ ] 增加损坏文件恢复策略（坏行跳过、隔离 quarantine、继续服务）。

### F. Memory 文件互链（File-to-file Linking）

- [ ] 统一 link 类型：`REFERS_TO`, `DERIVED_FROM`, `SUMMARIZES`, `DECIDES`, `BLOCKS`, `SUPERSEDES`。
- [ ] 每条链接带 `provenance`（来源工具调用/审批/会话）。
- [ ] 链接同时写入磁盘 `links.json`、数据库 `graph_edges`、Neo4j（若启用）。
- [ ] 在 `turns.jsonl` 保留 `link_ids`，支持回放与审计。
- [ ] 新增 API：`POST /api/memory/links`、`GET /api/memory/links?session_id=...`。
- [ ] 新增 CLI：`heros_memory_link`、`heros_memory_links_list`。
- [ ] 增加冲突策略（重复链接幂等、反向关系共存并带权重/时间）。
- [ ] 周期检查孤儿节点，并支持按 `provenance` 批量回滚。

### G. Logging / Tracing / Metrics / SLO

- [ ] 统一结构化日志字段：`ts`, `level`, `node_id`, `tenant_id`, `session_id`, `trace_id`, `span_id`, `event`, `status`, `latency_ms`, `error_code`。
- [ ] 日志覆盖关键事件：`proposal_submitted`, `proposal_approved`, `mutation_applied`, `memory_link_created`, `inbox_message_received`, `inbox_message_applied`。
- [ ] 落地敏感信息脱敏与日志采样策略。
- [ ] 引入 OpenTelemetry tracing（HTTP handler、memory retrieve、proposal apply、fleet 分发）。
- [ ] 在 agentd/collective/NATS worker 透传 `traceparent`，日志可跳转 `trace_id`。
- [ ] 指标补齐：请求量、错误率、P95/P99、审批时延、同步时延、队列积压、重试/死信。
- [ ] 增加 memory 质量指标（链接数、孤儿节点数、命中率、空结果率）。
- [ ] 定义 SLO 与告警分级（P1/P2/P3），配套 runbook（排查/回滚/恢复验证）。

### H. 商业化路线（对外口径）

- [ ] **Phase 1（可试点）**：每岗位/项目独立 `agentd`，统一技能包分发；默认不跨人共享原始记忆。
- [ ] **Phase 2（平台能力）**：在提案 + NATS 之上补齐集体服务（skills/memory 的 push/pull、租户、版本、审批）。
- [ ] **行业化（游戏/电商）**：沉淀行业技能模板、工具集成、记忆结构；平台层保持通用。

## 4) 验收标准（Definition of Done）

- [ ] 文档与 schema 完整：memory 目录、互链、迁移策略可执行。
- [ ] 互链能力可创建/查询/追踪/回滚，且磁盘与图索引一致。
- [ ] 日志 + trace + metrics 可在统一 dashboard 关联排障。
- [ ] inbox 下发链路通过故障演练（签名失败、重复消息、部分成功、重试耗尽）。
- [ ] 组织级同步链路具备端到端演示（提案 -> 审批 -> 集体 -> 多节点落地）。

---

维护规则：新增待办统一写入本文，不再新增平行 TODO 文件。
