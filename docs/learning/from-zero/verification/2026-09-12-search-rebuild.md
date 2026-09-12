# 搜索维护重建验证

本记录新增的是搜索投影维护能力，不改变原有任务功能的验收口径。MySQL 仍是事实来源，维护窗口内暂停 Search 与 Canal；不是双索引无停机切换。

## 已有证据

- `results/search-demo-618eeaa8313b4125ad11249989520abe.json`：第一次重建后，真实任务创建、执行到成功及搜索版本收敛通过。
- `results/search-demo-8f0e7fb8a4754f069fb1b015e73e33f7.json`：保持其他服务运行，只重建搜索并重新启动 Search，后续任务同步通过。
- `results/search-rebuild-5285326f43b54f11a062e82f69743d3d.json`：同时制造未完成标记和缺失 ES 索引；正常启动拒绝未完成标记；维护重建后 MySQL 22 条任务与 ES 22 个文档数量一致；全部 11 张业务表 EXTENDED 校验和前后一致。
- `.cache/search-rebuild-20260912/retention-floor.txt`：真实 Kafka 截断一次性测试 topic；没有快照边界的严格消费者拒绝缺失历史，有明确边界的消费者从已被快照覆盖的历史之后重放，普通实体消费者行为保留。
- `.cache/search-rebuild-20260912/search-integration.jsonl`：搜索包的真实 ES、MySQL、Kafka 集成测试及单元测试通过。包含消费组有成员时拒绝重置、CDC 在快照期间继续增长时拒绝完成、相同版本冲突、租户分页、一致性快照。
- `.cache/search-rebuild-20260912/unit.txt`、`vet.txt`：根工程普通测试及 vet 通过。普通测试不代表依赖类 SKIP 已执行，真实依赖的证据看上一项。

## 实现顺序与原因

1. 检查并只停止当前学习目录拥有的 Search 进程，核对 PID、路径和启动时间；其他进程保留。
2. 原子写入 `complete=false`，再停止并移除 Canal 容器，清理其专用卷中固定的 `meshops` 位点目录。不能删除整套 Compose 数据卷。
3. 等待搜索消费组为空，记录专用单分区 CDC topic 的排他末尾 `cdc_start`。保留 topic 中的旧消息，不删除业务事件。
4. 只重建固定任务索引。MySQL 短暂全局读锁内读取 binlog 坐标并建立一致性读视图，解锁后读取任务并写 ES。
5. 再次检查消费组为空、CDC 末尾未改变，提交新的消费起点。写入完成标记及新 ES UUID，然后启动 Canal。
6. Search 绑定新 UUID；消费组位点缺失或被重置到快照之前时，以 `cdc_start` 为最早重放边界。边界之后的历史缺失仍拒绝继续。

维护开始后失败，保持搜索停止且标记未完成。修复依赖后重新运行 `scripts/rebuild-search.ps1`，允许清理半成品索引后重新获取快照，不复用旧快照坐标冒充本次完成。

## 使用入口

在自己的学习工程根目录运行 `./scripts/rebuild-search.ps1`。若 Search 原本正在运行，脚本完成后重新启动它；原本没有启动时，运行 `./scripts/start-search.ps1`。再执行 `./scripts/demo-search.ps1` 验证新增任务同步。

`./scripts/test-search-rebuild.ps1` 是明确制造索引丢失的完整性演练，要求先停止课程应用；它核对 11 张业务表校验和及任务数量。不要把该演练当作每次启动命令。

## 尚未覆盖

后续已完成新的学习目录 Z09→Z10 复验及本次统一审查，见 [课程验收](2026-09-12-search-course.md) 和 [复核范围](2026-09-12-integrated-review.md)。Kafka topic 被外部删除重建、MySQL 实例被替换等身份变化不在当前完整自动检测承诺内，不能把维护能力描述为自动检测所有数据丢失。普通恢复不打印密码、不删除 MySQL/Redis/原事件 topic，不声明生产高可用。
