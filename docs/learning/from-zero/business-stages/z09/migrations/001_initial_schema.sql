-- ============================================================================
-- 多源实体实时协同与可靠任务调度平台 - 数据库Schema
-- 项目：MeshOps
-- 版本：V1.0
-- 日期：2026-08-25
-- ============================================================================

-- 字符集说明：使用 utf8mb4 支持完整Unicode（包括emoji）
-- 引擎说明：InnoDB支持事务、外键和崩溃恢复

-- ============================================================================
-- 1. 集成方管理
-- ============================================================================

CREATE TABLE integration_sources (
  id BIGINT AUTO_INCREMENT PRIMARY KEY,
  source_id VARCHAR(64) NOT NULL UNIQUE COMMENT '数据源唯一标识',
  tenant_id VARCHAR(64) NOT NULL COMMENT '所属租户',

  source_name VARCHAR(128) NOT NULL COMMENT '数据源名称',
  source_type ENUM('gateway', 'simulator', 'integration') NOT NULL COMMENT '来源类型',

  -- 认证信息（存储加密后的凭证或密钥ID）
  auth_method ENUM('api_key', 'jwt', 'mtls') NOT NULL DEFAULT 'api_key',
  credentials_hash VARCHAR(256) NOT NULL COMMENT '凭证哈希',

  -- 权限范围
  allowed_entity_types JSON COMMENT '允许上报的实体类型列表',
  rate_limit_per_second INT NOT NULL DEFAULT 100 COMMENT '每秒速率限制',

  -- 状态
  status ENUM('active', 'suspended', 'deleted') NOT NULL DEFAULT 'active',

  created_at TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
  updated_at TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),

  INDEX idx_tenant (tenant_id, status),
  INDEX idx_source_type (source_type, status)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='集成方/数据源管理';

-- ============================================================================
-- 2. 实体元数据
-- ============================================================================

CREATE TABLE entities (
  id BIGINT AUTO_INCREMENT PRIMARY KEY,
  tenant_id VARCHAR(64) NOT NULL COMMENT '租户ID',
  entity_id VARCHAR(128) NOT NULL COMMENT '实体业务ID',

  entity_type VARCHAR(64) NOT NULL COMMENT '实体类型：drone/vehicle/sensor/person',
  entity_name VARCHAR(256) COMMENT '实体名称',

  -- 最新状态摘要（可选，主要依赖Redis）
  last_seen_at TIMESTAMP(6) COMMENT '最后上报时间',
  last_source_id VARCHAR(64) COMMENT '最后上报来源',
  current_status VARCHAR(32) COMMENT '当前状态：online/offline/flying/idle',

  -- 生命周期
  registered_at TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT '注册时间',
  deactivated_at TIMESTAMP(6) COMMENT '停用时间',

  created_at TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
  updated_at TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),

  UNIQUE KEY uk_tenant_entity (tenant_id, entity_id),
  INDEX idx_type (tenant_id, entity_type, current_status),
  INDEX idx_last_seen (tenant_id, last_seen_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='实体元数据';

-- ============================================================================
-- 3. 历史抽样（轨迹回放）
-- ============================================================================

CREATE TABLE entity_history_samples (
  id BIGINT AUTO_INCREMENT,
  tenant_id VARCHAR(64) NOT NULL COMMENT '租户ID',
  entity_id VARCHAR(128) NOT NULL COMMENT '实体ID',

  occurred_at TIMESTAMP(6) NOT NULL COMMENT '事件发生时刻',
  sampled_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT '抽样写入时刻',

  event_id VARCHAR(128) COMMENT '原始事件ID',
  source_id VARCHAR(64) COMMENT '数据来源',
  entity_version BIGINT COMMENT '实体版本号',

  -- 完整快照（JSON存储）
  snapshot JSON NOT NULL COMMENT '实体完整状态快照',

  -- 抽样原因
  sample_reason ENUM('periodic', 'position', 'velocity', 'state_change', 'task_event')
    NOT NULL COMMENT '抽样触发原因',

  PRIMARY KEY (id, sampled_at),
  INDEX idx_entity_time (tenant_id, entity_id, occurred_at),
  INDEX idx_sampled_at (sampled_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='实体历史抽样'
PARTITION BY RANGE COLUMNS (sampled_at) (
  PARTITION p_initial VALUES LESS THAN ('2026-09-01 00:00:00'),
  PARTITION p_future VALUES LESS THAN MAXVALUE
);

-- ============================================================================
-- 4. 任务管理
-- ============================================================================

CREATE TABLE tasks (
  id BIGINT AUTO_INCREMENT PRIMARY KEY,
  tenant_id VARCHAR(64) NOT NULL COMMENT '租户ID',
  task_id VARCHAR(128) NOT NULL UNIQUE COMMENT '任务业务ID（UUID）',
  idempotency_key VARCHAR(256) NOT NULL COMMENT '租户内幂等键（客户端生成）',

  -- 任务基本信息
  task_type VARCHAR(64) NOT NULL COMMENT '任务类型：move_to/patrol/monitor/return_home',
  target_entity_id VARCHAR(128) NOT NULL COMMENT '目标实体ID',
  payload JSON NOT NULL COMMENT '任务参数（JSON）',
  priority INT NOT NULL DEFAULT 5 COMMENT '优先级 [1-10]',

  -- 状态管理
  status ENUM(
    'CREATED',           -- 已创建
    'DISPATCH_PENDING',  -- 待下发
    'DISPATCHED',        -- 已下发
    'ACKED',             -- 已确认
    'EXECUTING',         -- 执行中
    'SUCCEEDED',         -- 成功
    'FAILED',            -- 失败
    'CANCELLED',         -- 已取消
    'TIMED_OUT',         -- 超时
    'REJECTED'           -- 被拒绝
  ) NOT NULL DEFAULT 'CREATED' COMMENT '任务状态',
  status_version INT NOT NULL DEFAULT 0 COMMENT '状态版本（乐观锁）',

  -- 时间信息
  created_at TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
  updated_at TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
  deadline TIMESTAMP(6) COMMENT '任务截止时间',

  -- 审计信息
  created_by VARCHAR(128) NOT NULL COMMENT '创建者',
  cancelled_reason TEXT COMMENT '取消原因',
  failure_reason TEXT COMMENT '失败原因',

  UNIQUE KEY uk_tenant_idempotency (tenant_id, idempotency_key),
  INDEX idx_tenant_status (tenant_id, status, created_at),
  INDEX idx_entity (tenant_id, target_entity_id, created_at),
  INDEX idx_updated_at (updated_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='任务主表';

-- ============================================================================
-- 5. 任务状态历史（审计）
-- ============================================================================

CREATE TABLE task_status_history (
  id BIGINT AUTO_INCREMENT PRIMARY KEY,
  tenant_id VARCHAR(64) NOT NULL,
  task_id VARCHAR(128) NOT NULL,

  from_status VARCHAR(32) NOT NULL COMMENT '原状态',
  to_status VARCHAR(32) NOT NULL COMMENT '新状态',
  status_version INT NOT NULL COMMENT '状态版本',

  changed_at TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
  changed_by VARCHAR(128) NOT NULL COMMENT '变更者：system/user:{id}/executor:{id}',
  reason TEXT COMMENT '变更原因',

  INDEX idx_task (task_id, changed_at),
  INDEX idx_tenant_time (tenant_id, changed_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='任务状态变更历史';

-- ============================================================================
-- 6. Transactional Outbox
-- ============================================================================

CREATE TABLE outbox_events (
  id BIGINT AUTO_INCREMENT PRIMARY KEY,

  -- 事件基本信息
  event_type VARCHAR(64) NOT NULL COMMENT '事件类型：task.created/task.cancelled等',
  aggregate_type VARCHAR(64) NOT NULL COMMENT '聚合类型：task/entity',
  aggregate_id VARCHAR(128) NOT NULL COMMENT '聚合ID（task_id/entity_id）',

  -- 事件内容
  payload JSON NOT NULL COMMENT '事件payload（Protobuf JSON）',

  -- 投递状态
  published_at TIMESTAMP(6) NULL COMMENT '发布成功时间（NULL表示未发布）',
  retry_count INT NOT NULL DEFAULT 0 COMMENT '重试次数',
  last_error TEXT COMMENT '最后一次错误信息',

  created_at TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),

  INDEX idx_unpublished (published_at, created_at),
  INDEX idx_aggregate (aggregate_type, aggregate_id, created_at),
  INDEX idx_cleanup (published_at, created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='Transactional Outbox事件表';

-- ============================================================================
-- 7. 消费者去重表（可选）
-- ============================================================================

CREATE TABLE consumer_dedup (
  id BIGINT AUTO_INCREMENT,

  consumer_group VARCHAR(128) NOT NULL COMMENT 'Consumer Group名称',
  idempotency_key VARCHAR(256) NOT NULL COMMENT '幂等键（event_id/task_id+attempt等）',

  processed_at TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),

  PRIMARY KEY (id, consumer_group),
  UNIQUE KEY uk_consumer_key (consumer_group, idempotency_key),
  INDEX idx_processed_at (processed_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='消费者幂等去重'
PARTITION BY KEY (consumer_group) PARTITIONS 8;

-- 定期清理策略：保留7天内的去重记录
-- DELETE FROM consumer_dedup WHERE processed_at < NOW() - INTERVAL 7 DAY;

-- ============================================================================
-- 8. 任务分发记录（可选，用于监控）
-- ============================================================================

CREATE TABLE task_dispatches (
  id BIGINT AUTO_INCREMENT PRIMARY KEY,

  task_id VARCHAR(128) NOT NULL,
  dispatch_id VARCHAR(256) NOT NULL UNIQUE COMMENT 'task_id + attempt',
  attempt INT NOT NULL COMMENT '尝试次数',

  executor_id VARCHAR(128) COMMENT '执行器ID',
  dispatched_at TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
  acked_at TIMESTAMP(6) COMMENT 'ACK时间',
  completed_at TIMESTAMP(6) COMMENT '完成时间',

  status ENUM('dispatched', 'acked', 'executing', 'succeeded', 'failed', 'timeout')
    NOT NULL DEFAULT 'dispatched',

  INDEX idx_task (task_id, attempt),
  INDEX idx_executor (executor_id, dispatched_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='任务下发记录';

-- ============================================================================
-- 初始化数据
-- ============================================================================

-- 测试租户
INSERT INTO integration_sources (
  source_id, tenant_id, source_name, source_type,
  auth_method, credentials_hash, rate_limit_per_second
) VALUES
  ('gateway_demo_001', 'demo_tenant', 'Demo Gateway 1', 'gateway',
   'api_key', SHA2('demo_key_1', 256), 100),
  ('gateway_test_001', 'test_tenant', 'Test Gateway 1', 'gateway',
   'api_key', SHA2('test_key_1', 256), 50);

-- ============================================================================
-- 维护脚本
-- ============================================================================

-- 清理已发布的Outbox事件（保留24小时）
-- 建议通过定时任务每小时执行一次
DELIMITER //
CREATE EVENT IF NOT EXISTS cleanup_outbox_events
ON SCHEDULE EVERY 1 HOUR
DO
BEGIN
  DELETE FROM outbox_events
  WHERE published_at IS NOT NULL
    AND published_at < NOW() - INTERVAL 24 HOUR
  LIMIT 10000;
END //
DELIMITER ;

-- 清理过期的去重记录（保留7天）
DELIMITER //
CREATE EVENT IF NOT EXISTS cleanup_consumer_dedup
ON SCHEDULE EVERY 6 HOUR
DO
BEGIN
  DELETE FROM consumer_dedup
  WHERE processed_at < NOW() - INTERVAL 7 DAY
  LIMIT 10000;
END //
DELIMITER ;
