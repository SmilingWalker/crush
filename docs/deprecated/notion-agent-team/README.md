# Notion AgentTeam Materials

This directory mirrors the AgentTeam planning materials from Notion so the
`crush` repository has a local, reviewable source set.

Source parent page: `多智能体架构设计（AgentTeam）`

## Files

| File | Notion page | Purpose |
| --- | --- | --- |
| `00-index.md` | `0` | Overall migration report and document map. |
| `01-current-state.md` | `01. Crush 当前架构深挖` | Current Crush architecture facts and gaps. |
| `02-target-architecture.md` | `02. AgentTeam 目标架构` | Target package, DB, service, API, event, workspace design. |
| `03-runtime-protocol.md` | `03. 运行时与协作协议` | TeamRunner, MateRunner, mailbox, task, permission, shutdown protocol. |
| `04-a2a-alternatives.md` | `04. A2A 与替代方案评估` | Why A2A is a later gateway, not the first internal protocol. |
| `05-roadmap-tests.md` | `05. 多期实施路线与测试策略` | Phase 0-9 implementation and testing plan. |
| `06-design-review.md` | `06. 辩证设计评审` | Dialectical review from multiple architecture perspectives. |

## Relationship To `docs/agent-team-mode`

The Notion plan is the broader original migration blueprint. The existing
`docs/deprecated/agent-team-mode` directory is a safer staged implementation proposal
that adds earlier read-only, ActorContext, sensitive-file, MCP, budget, and
risk controls.

The official merged recommendation lives in `../../agent-team-mode`.
