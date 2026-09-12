# ADR-0005：使用 Kafka 作为可靠事件主干

- 状态：已实施；验证范围见文末当前入口
- 日期：2026-08-25

## 背景

本平台需要处理边缘网络恢复后的补报突发、消费者停机续传、最新快照重建以及任务可靠下发。Redis Pub/Sub没有持久化、消费位点和ACK，无法承担这些职责。Redis Streams能够覆盖小规模可靠队列，但同时维护Streams和Kafka会形成两套消息语义，也不利于后续增加历史、统计和异常检测消费者。

## 决策

使用Kafka作为状态事件和任务领域事件的统一可靠主干：

- `entity-state-events.v1`：Key为`tenant_id:entity_id`（多租户隔离 + 同实体有序），供状态投影、历史抽样和后续分析消费者使用。
- `task-events.v1`：Key为`tenant_id:task_id`，由MySQL Transactional Outbox发布。
- 任务重试使用 MySQL next_attempt_at 与有界 worker；Kafka 不提供原生延迟计时，超过上限进入DLQ。
- Redis Pub/Sub 仅是多实例通知的条件式候选，当前未启用；当前单实例订阅由实体服务维护。
- 不同时引入Redis Streams、RabbitMQ或NATS。

## 语义

- Kafka生产成功表示事件达到配置的持久化ACK条件。
- 消费采用至少一次语义；重复是正常情况，消费者必须幂等。
- 同一Key在同一分区内有序（`tenant_id:entity_id`保证同租户同实体有序），不承诺跨分区全局有序。
- Consumer Offset只能在业务处理达到安全点后提交。
- Kafka保留期内可重放，不等于永久历史存储。
- 不宣称MySQL与Kafka跨系统”恰好一次”。任务通过Outbox解决数据库提交与事件发布之间的原子性缺口。

## Transactional Outbox

任务服务在同一个MySQL事务中写入业务记录、审计记录和Outbox记录。Outbox投递器读取未发布记录并写Kafka，成功后标记已发布。投递器在发送成功、标记失败时可能重复发送，因此任务分发器仍必须通过业务唯一键幂等。

## 为什么不选其他方案

### 只使用Redis Pub/Sub

无法断线补发、重试、回放和观察消费积压，不满足任务可靠性与快照重建需求。

### Redis Streams

适合较轻量的可靠队列，但本项目还需要按Key分区、多个独立消费者组、较长保留和后续流式扩展。选用Kafka后不再并存Streams。

### 所有请求同步写MySQL

高频状态逐条写入会造成写放大，无法自然吸收补报突发，也不利于新增下游消费者。

## 代价

- 本地部署、监控、分区规划和故障排查更复杂。
- 必须学习并验证重平衡、Offset、Lag、重复消费和分区热点。
- Kafka不替代MySQL、Redis或历史查询存储。

## 验证要求

1. Broker不可用时Ingest不得返回虚假成功。
2. 状态投影器处理后崩溃、Offset未提交时，重复消费不破坏快照。
3. 清空Redis后，只有窗口覆盖全部最新事件和墓碑才能完整重建；否则需来源全量补报或检查点。
4. Consumer Group重平衡期间无不可解释的数据丢失。
5. Outbox投递器重复发送不会导致重复任务执行。
6. 保存Consumer Lag、吞吐、端到端P99和故障恢复时间的原始证据。

## 当前落地范围（2026-09-13）

根工程已实现六类模拟来源、状态与 inspect 闭环、任务搜索及 HTTP/SSE/Vue 成品。单实体仍使用单权威来源，任务采用稳定 execution_key。2026-09-05 的两类来源骨架阶段已结束，具体范围与证据见 [当前工程](../../README.md) 和 [验证边界](../production-readiness-checklist.md)。
