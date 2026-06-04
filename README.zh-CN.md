# llmlore

**LLM 时代的工具 / 教程资源库** —— 一个开源、手动触发的命令行工具 + 一份持续更新的开放数据集。它把 GitHub 上"教人用 LLM / AI"的优质仓库收集、筛选、打标后,沉淀成结构化数据,并渲染成一个本地仪表盘供浏览。

定位:**一份开放数据集 + 一个查看 / 刷新引擎**。数据本体是 `data/repos.json`,网页是它的只读视图。

> 说明:本工具的命令行与界面文案均为英文;本文档用中文是为方便中文读者。English docs: [README.md](README.md)。

## 特性

- **零门槛**:预生成数据开源在仓库里,装完直接看,不强制配 LLM key。
- **会筛选**:用模型判定"是不是冲着教人用 AI 去的",滤掉框架 / 模型本体 / 论文复现。
- **结构化**:主题 × 类型双轴标签 + 一句话摘要 + 活跃度 + star 趋势,比 awesome-list 更系统。
- **可切换取向**:历史高星(经典稳定)/ 近期上升(trending 新秀)。
- **整理你自己的 star**:把关注过的一堆仓库重新归纳成可按主题浏览的视图(纯本地)。

## 安装

```bash
# Homebrew(csthink 共享 tap)
brew install csthink/tap/llmlore

# 或从源码
go install github.com/csthink/llmlore/cmd/llmlore@latest
```

装完先 `llmlore pull` 拉一份预生成数据,再 `llmlore` 直接看仪表盘(零配置)。

## 快速开始

```bash
# 下载预生成的开放数据,然后打开仪表盘(零配置)
llmlore pull
llmlore                 # 渲染 + 起本地服务 + 自动开浏览器;Ctrl+C 停止

# 仅查看本地已有数据
llmlore serve --port 7777

# 自配 key 拉取 / 重新生成最新数据
export LLMLORE_LLM_PROVIDER=anthropic
export LLMLORE_LLM_API_KEY=...      # 不配则降级为启发式筛选
llmlore update --mode historical    # 或 trending / both

# 整理自己 star 过的仓库(纯本地,不进任何仓库)
llmlore stars sync --user <login>      # 拉某用户的公开 star;或
export LLMLORE_GITHUB_TOKEN=...         # 设 token 拉自己的(含私有)star
llmlore stars sync                     # 设了 token 后这样拉自己的
llmlore stars organize
llmlore stars view
```

## 工作原理

```
Collector(search / trending) → Classifier(收/不收·LLM 或启发式)
→ Enricher(摘要·标签·活跃度·快照) → Store(repos.json) → Renderer(HTML + 本地服务)
```

## 配置(环境变量)

| 变量 | 含义 |
|---|---|
| `LLMLORE_LLM_PROVIDER` / `LLMLORE_LLM_API_KEY` | LLM 提供方与 key(不配则走启发式) |
| `LLMLORE_GITHUB_TOKEN` | GitHub token(提限额 / 拉私有 star) |
| `LLMLORE_PORT` | 本地服务端口(默认 7777) |
| `LLMLORE_EXCLUDE_STARRED` | discover 时排除你已 star 的 |

**如何获取 `LLMLORE_GITHUB_TOKEN`:** 在 <https://github.com/settings/tokens>
(Settings → Developer settings → Personal access tokens)创建一个 Personal
Access Token。只拉**公开** star 的话,**不需要任何 scope**(token 仅用于提限额);
要拉**自己的私有** star,用 classic token 并勾选 **`repo`** scope。然后
`export LLMLORE_GITHUB_TOKEN=<token>`。token 从环境变量读取、仅驻内存,
绝不写入磁盘 / 日志 / argv。详见
[GitHub 官方 token 文档](https://docs.github.com/en/authentication/keeping-your-account-and-data-secure/managing-your-personal-access-tokens)。

详见 `docs/spec.md`。

## 数据

`data/repos.json` 是开放数据集,由仓库的 GitHub Action 定期跑全流程更新。即使不装工具,直接看这份数据也有价值。`llmlore update` 可用你自己的 key 在本地重新生成。

## 隐私

`my-stars` 数据(你关注过的仓库)**只存本地** `~/.local/share/llmlore/`,与共享数据物理分离,**永不进开源仓库**。工具只列出"可取关候选",不会替你执行任何账号操作。

## 开发

- 语言:Go,单文件二进制。
- 开发流程:使用 Claude Code,单会话顺序做 T0–T8、自验 DoD、守红线、按需第二双眼睛(不强制独立评审),详见 `docs/workflow.md`。
- 设计与契约:`docs/proposal.md` / `docs/design.md` / `docs/spec.md` / `docs/tasks.md`;项目宪法见根目录 `CLAUDE.md`。

## 路线图

T0 脚手架 → T1 数据模型/配置 → T2 采集 → T3 分类 → T4 存储/富化 → T5 渲染/服务 → T6 discover 接线 → T7 my-stars → T8 分发与开放数据。详见 `docs/tasks.md`。

## 许可证

MIT,见 [LICENSE](LICENSE)。
