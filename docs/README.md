# 文档索引

当前文档只保留运行和后续维护需要的内容。

## 运行维护

- [部署说明](deployment.md): VPS systemd + Mac launchd 双实例、角色切换、健康检查、备份恢复
- [架构方案（2026-11-09）](architecture-v2-2026-11-09.md): 源评估、R1–R10 修复、双实例、事件模型路线与实施状态
- [事件模型](event-model.md): Story/Thread/Decision/Edition 的身份、打分、预算、模式与 API
- [数据源输出格式](source-output-format.md): 各源真实样本（含 jin10）、字段契约与格式限制

## 清理约定

- 运行配置模板只维护 `config.platform.yaml.example`，本地实际配置为 `config.platform.yaml`
- 密钥只维护 `.env.example`，本地实际密钥为 `.env`
- 服务入口只保留 `cmd/platform`
- 部署入口：`Makefile` + `deploy/`（VPS systemd、Mac launchd；Docker 路径已删除）
- `data/`、`bin/`、`web/node_modules/`、`.venv/` 和根目录二进制不提交
- 已完成或失效的历史分析、竞品、宣传和一次性报告不再保留在仓库
