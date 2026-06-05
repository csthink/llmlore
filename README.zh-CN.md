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
llmlore                 # 打开组合仪表盘(Catalog / My stars / Cross);Ctrl+C 停止

# 同一个组合仪表盘,显式命令 + 自定义端口
llmlore view --port 7777

# 自配 key 拉取 / 重新生成最新数据。
# llmlore 走 OpenAI 兼容的 /chat/completions 接口;`provider` 只是一个启用 LLM 的
# 非空标识。把 base_url 指向任意兼容端点即可(OpenAI、DeepSeek、SiliconFlow、本地
# Ollama/vLLM 等)。
export LLMLORE_LLM_PROVIDER=openai
export LLMLORE_LLM_API_KEY=...      # 不配则降级为启发式筛选
llmlore update --mode historical    # 或 trending / both

# 整理自己 star 过的仓库(纯本地,不进任何仓库)
llmlore stars sync --user <login>      # 拉某用户的公开 star;或
export LLMLORE_GITHUB_TOKEN=...         # 设 token 拉自己的(含私有)star
llmlore stars sync                     # 设了 token 后这样拉自己的
llmlore stars organize
llmlore view                           # 个人 star 会出现在 My stars / Cross tab
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

**可选配置文件。** 非密设置(`provider`、`base_url`、`model`、端口、discover 选项)
也可以放在 `~/.config/llmlore/config.toml`。用下面这条命令生成模板:

```bash
llmlore config init     # 写出一份带注释的模板;--force 可覆盖
```

然后编辑它、把 `provider` 改成真实名字即可启用 LLM 功能。**key 绝不写进文件** ——
始终用 `export LLMLORE_LLM_API_KEY` 提供。环境变量优先于文件。未改动的模板会保持启发式
模式,并在你起服务时打印一行提醒。llmlore 走 OpenAI 兼容的 `/chat/completions` 接口,
把 `base_url` 指向 OpenAI、DeepSeek、SiliconFlow、本地 Ollama/vLLM 等即可。

### 如何获取 `LLMLORE_GITHUB_TOKEN`

token 是**可选**的。不配也能用 `llmlore stars sync --user <login>` 拉任意用户的**公开** star
——token 只是抬高限额、并让你能同步**自己的私有** star。

请用 **personal access token(classic)**,不要用 fine-grained:fine-grained token 只能访问你
自己拥有的仓库,因此那些你 star 了但归属**他人 / 组织**的私有仓库不会被同步。在 **Settings →
Developer settings → Personal access tokens → Tokens (classic) → Generate new token (classic)**
创建:

![创建 personal access token(classic)](docs/images/github-token-classic.png)

- **Note** —— 起个好记的名字,如 `llmlore`(同截图)。
- **Expiration** —— 自定。截图用的是自定义的约 1 年期、省得频繁续期;想更安全可设更短。
  无论如何都会过期,到期前记得续期。
- **Select scopes**:
  - 只要**公开 star + 抬限额**:可以**不勾任何 scope**,光是认证就能抬高限额。
  - 还要同步**自己的私有** star:勾选 **`repo`**(Full control of private repositories)。
    无需其他 scope;llmlore 只读取你的 star 列表,绝不对你的账号做任何写操作。

点 **Generate token (classic)**,复制后:

```bash
export LLMLORE_GITHUB_TOKEN=<token>
```

token 从环境变量读取、仅驻内存,绝不写入磁盘 / 日志 / argv。详见
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

T0 脚手架 → T1 数据模型/配置 → T2 采集 → T3 分类 → T4 存储/富化 → T5 渲染/服务 → T6 discover 接线 → T7 my-stars → T8 分发与开放数据 → T9 config init 与占位符检查。详见 `docs/tasks.md`。

## 许可证

MIT,见 [LICENSE](LICENSE)。
