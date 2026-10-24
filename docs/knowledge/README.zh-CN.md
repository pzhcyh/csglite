# CSG Lite 个人知识库

把资料保存为可追溯的 Wiki，通过记忆管家整理、原生对话使用，并向 CSGClaw 提供受控检索和待审稿回流。界面沿用 CSG Lite 的靛紫、白、灰配色、系统字体和原生导航，保留文件树、Markdown、双向链接与图谱。

本版基于 OpenCSGs/csglite 官方源码，基线提交为 `1c197214ef3ebc6aa463df9ab5f9d47275a844b2`，属于个人版开发交付，**不是官方发行版**。普通代码沿用 Apache-2.0，`ee/` 保留原有许可。

## 从这里开始

- [使用与交付说明](DELIVERY.zh-CN.md)：五分钟体验、记忆管家、备份恢复、功能边界。
- [CSGClaw 接入指南](CSGCLAW.zh-CN.md)：创建凭证、配置连接器、绑定 Agent、验证回流。
- [产品与接口分工](INTEGRATION.zh-CN.md)：知识、模型、对话、管家和 CSGClaw 如何协作。

打开 **http://localhost:11437/knowledge**。首次使用为空库，不扫描用户目录。可以新建空库，或主动载入“青松阅读计划”虚构示例；演示和验收只使用虚构样本。

## 本版能力

| 状态 | 能力 |
| --- | --- |
| 已实现，可直接体验 | UTF-8 Markdown/TXT 导入、原件保留、Wiki 编辑、双链/图谱、提案审阅、版本记录与恢复、规则式健康检查、离线分享 ZIP、完整 JSON 备份与恢复 |
| 已实现，需要配置可用模型 | 记忆管家整理队列；原生 Chat 关联知识、引用回答并回流待审稿 |
| 已实现，需要配置客户端并验证兼容性 | MCP 知识检索、分页阅读、待审稿提交；按库发放、到期、撤销连接凭证 |
| 后续范围 | PDF/DOCX/OCR、目录自动同步、向量/增量索引、TB 级容量、长期个人画像、远程公开分享、企业成员权限、自动向 CSGClaw 派发任务与进度同步 |

模型整理和任务回流始终遵循：**保留原件 → 生成待审提案 → 人工接受 → 更新 Wiki、版本和日志**。引用和版本校验用于追溯，不替代事实审查。这里的“记忆管家”负责知识整理与维护，不会自动收集所有聊天或建立终身个人画像。

## 构建和启动

需要 Node.js 22.12+、npm、Go 1.24.2+。在项目根目录运行：

```sh
./scripts/run-knowledge-preview.sh
```

脚本安装前端依赖、构建前端、同步静态资源、构建 Go 程序，并启动 **127.0.0.1:11437**。此独立开发入口使用与默认 11435 服务不同的端口及存储目录。已有 11437 预览时应先停止它，再启动一份新预览；不要另开前端开发服务器。

启动已经构建好的程序：

```sh
./bin/csglite-knowledge-preview \
  --storage "$(pwd)/local/knowledge-preview" \
  --listen 127.0.0.1:11437
```

`--storage` 必须为绝对路径，监听地址必须为 loopback IP。预览程序为当前进程设置独立的 `CSGHUB_LITE_HOME`，配置、会话、模型元数据和知识库均保存在选定目录；不会读取或复制正式实例的凭据。它复用原生 Go 服务，跳过普通 CLI 的自动运行时更新及服务 PID 操作，并关闭集群自动加入。

标准 `csghub-lite` 构建也包含 `/knowledge` 页面与 API。未设置 `CSGHUB_LITE_HOME` 时，默认路径仍为 `~/.csghub-lite`。独立预览需要单独配置模型，不能以正式实例中已有模型或凭据推断预览已经可推理。

## 文件与数据边界

每个库位于 `<storage>/knowledge/<library-id>/`：

```text
raw/                     不覆盖的 UTF-8 原件
wiki/                    正式 Wiki 的 Markdown 投影
index.md                 Wiki 目录
log.md                   操作记录
SCHEMA.md                文件与引用约定
library.config.json      模型和访问规则的投影
.knowledge/state.json    原件、Wiki、版本、提案和整理任务的规范状态
```

请通过知识库页面/API 编辑。Markdown 是规范状态的可读投影；外部编辑器改写文件不会自动同步入库。资料用于模型上下文，未进行模型训练。连接凭证的哈希单独存储，不进入知识库导出、备份或模型上下文。

## 开发验证

在项目根目录运行：

```sh
npm --prefix web test
npm --prefix web run build
cp -R web/dist/. internal/server/static/
go test ./internal/config ./internal/knowledge ./internal/server ./cmd/knowledge-preview
go build -o bin/csglite-knowledge-preview ./cmd/knowledge-preview
```

自动化测试覆盖来源撤回、版本冲突、引用范围、连接隔离、备份恢复和任务取消。真实模型与 CSGClaw 还需在目标环境按[使用说明](DELIVERY.zh-CN.md)及[接入指南](CSGCLAW.zh-CN.md)验证。引用校验不替代事实审查；模型可能补出原文没有的信息，需经人工审阅后入库。
