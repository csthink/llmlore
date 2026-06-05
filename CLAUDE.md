# CLAUDE.md — llmlore 项目宪法

> Claude Code 每个会话都会读取本文件。这里只放**每次都必须遵守的常驻约束**,细节见 `docs/`。
> 设计文档是唯一事实来源(source of truth):**代码服从文档,不服从记忆、不服从直觉。**

## 你的角色

你是本项目的 **generator**。按 `docs/tasks.md` 顺序实现 T0–T8。每个任务的收尾(强制):

1. 自测:`go build ./...` / `go vet ./...` / `go test ./...`。
2. 对照 `docs/tasks.md` 该任务的 DoD 逐条自验,并在 `.relay/Tn.status.md` 里说明如何满足。
3. 过了就 `git commit` 并 push。**Commit messages must be in English**,形如 `Tn: <short summary>`(如 `T2: implement repo collector`)。
4. 在下方"当前进度"勾选 Tn,直接进入下一个任务。

不一口气连做多个任务(一次一个,做完收尾再进下一个),但**不需要等独立评审**——自验 DoD 通过即可推进。若某个任务我(maintainer)想要第二双眼睛,会另行单独发起,不阻塞你。

详细流程见 `docs/workflow.md`。

## 三条红线(违反即返工,不可协商)

1. **范围红线**:v1 = 手动触发的 discover(采集 → 分类 → 打标 → `repos.json` → HTML 仪表盘)+ 去重/快照 + brew/源码安装 + GitHub Action 更新开放数据 + my-stars 归纳。out-of-scope 一律**不实现**:常驻服务 / SaaS、账号写操作(star/unstar/follow)、移动端、团队协作。(数据模型可为未来来源**预留**,但本期不写实现。)
2. **安全·隐私红线**:API key **绝不**落盘 / 进日志 / 进命令行参数(argv)/ 进 shell history / 进任何持久化的环境变量;配置里只存 provider 设置与引用,**绝不存 key**。`my-stars` 数据只落 `~/.local/share/llmlore/`,**永不进仓库**,与 `data/repos.json` 物理分离。**绝不**执行任何账号写操作;`stars stale` 只列候选、不替用户取关。
3. **界面语言红线**:工具一切面向用户的输出(CLI 提示 / help / 报错 / 日志 / HTML 仪表盘文案)**一律英文**,不出现中文、单条字符串不中英混排。源码 / 标识符 / 注释 / commit / 分支名均英文。(验收:`docs/spec.md` AC-9。)

## 核心约定(实现时反复用到)

- **事实源**:`data/repos.json` 是唯一事实源,HTML 是只读视图、可重生成。schema 见 `docs/spec.md §3`。
- **双轴标签**:type(`tutorial`/`example`/`template`/`guide`)× topic(`llm`/`agent`/`rag`/`multimodal`/`ai-coding`/…),受控词表见 `docs/spec.md §4`。
- **分层降级**:无 LLM key → 启发式筛选(`classified_by=heuristic`);有 key → LLM 判定 + 一句话摘要 + 标签。
- **取向可切换**:`--mode historical|trending|both`,默认 `historical`;trending 走 goquery、对页面结构容错。
- **快照**:每轮把 star 数 + 时间戳追加进 `star_snapshots`,从第一次运行就记、不覆盖历史。

## 工作纪律

- **一文件一 owner**:每个任务"主要负责的文件"见 `docs/tasks.md` 的 owned files,尽量不并发写同一文件。
- **不擅改设计文档**:开发期 `docs/*.md` 冻结。需要改 spec/design → 写一份变更提案(`docs/PROPOSAL-NNN-<topic>.md`),交维护者拍板,**不得单方面改**。
- **DoD 即验收点**:动手前先复述该任务的 DoD,实现后逐条对照,在 `.relay/Tn.status.md` 里说明如何满足。
- **错误可读**:所有面向用户的错误是人类可读消息(英文),不是裸 panic / stack trace。
- **质量门**:提交前 `go build` / `go vet` / `go test` 应通过(无 error)。
- **任务即提交**:每个任务完成后,先 commit(再写/更新 status)。**未 commit 不算任务完成。**
- **Commit 语言与形式**:一律英文,`<type|Tn>: <summary>`(如 `T2: implement collector`、`docs: simplify workflow`)。一个任务对应一个独立 commit,作为该任务的评审快照。
- **只提交本任务的改动**:commit 前先 `git status` 看清待提交清单,**只 `git add` 属于当前任务 Tn 的文件**,不要用 `git add -A` / `git add .` 把工作区里其它未提交的改动(如 maintainer 正在编辑的文档)一起带走。若发现工作区有不属于本任务的改动,**停下来问我**,不要擅自提交或还原。

## 发版(release · 维护期)

> 开发期 T0–T8 已全部完成,项目处发版/维护期。**完整手册见 `docs/RELEASE.md`**,这里只放常驻约束。

- **发版 = 维护者动作**:**未经 maintainer 明确确认,不得自行打 tag / 发版。**
- **怎么发**:`git tag vX.Y.Z && git push origin vX.Y.Z` 即触发全自动(GoReleaser 构建 darwin+linux × amd64/arm64 → 发 Release → 推 `Formula/llmlore.rb` 到共享 tap `csthink/homebrew-tap`)。**版本号来自 tag,无需手改任何版本文件。**
- **不要擅动**(动了会断发布链,改前先读 `docs/PROPOSAL-003`):`release.yml` 里 goreleaser-action 的 `version: "v2.15.4"` pin、`.goreleaser.yml` 的 `brews[].directory: Formula` 与 `repository.name: homebrew-tap`、secret `HOMEBREW_TAP_GITHUB_TOKEN`(与 llmkeys 的 `HOMEBREW_TAP_TOKEN` **不同名**)。
- **版本号纪律**:绝不用不同二进制重打同一已存在 tag;面向用户的变化 → bump 新版本再发。

## 关键文件导航

| 路径 | 作用 |
|---|---|
| `docs/proposal.md` | 动机、范围、锁定决策 |
| `docs/spec.md` | 行为契约(MUST/SHOULD 是验收点) |
| `docs/design.md` | Go 架构、模块划分、数据流 |
| `docs/tasks.md` | T0–T8 拆解 + 每任务 owned files + DoD |
| `docs/workflow.md` | 开发流程、status / review 格式 |
| `docs/RELEASE.md` | **发版 runbook**(如何切版本、护栏、发版后验证) |
| `docs/PROPOSAL-003-shared-homebrew-tap.md` | 共享 tap 决策(发版护栏的由来) |
| `~/.config/llmlore/config.toml` | 用户配置:github 设置 + 自定义 llm provider(**不含 key**) |
| `data/repos.json` | 开放数据集(提交进仓库) |
| `~/.local/share/llmlore/my-stars.json` | 个人 star 数据(本地,**永不提交**) |
| `.relay/Tn.status.md` | 你写的任务状态报告(DoD 自验 / 评审输入) |

## 当前进度

<!-- 每完成一个任务更新这里,方便跨会话快速定位 -->
- [x] T0 项目骨架
- [x] T1 数据模型 + 配置
- [x] T2 采集(search + trending)
- [x] T3 分类(heuristic + LLM)
- [x] T4 存储 + 富化
- [x] T5 渲染 + 本地服务
- [x] T6 discover 接线 + pull + 内嵌兜底
- [x] T7 my-stars
- [x] T8 分发 + 开放数据更新 + README 回填
- [x] T9 config init 模板 + 启动时占位符检查(维护期增量)
- [x] T10 单一组合仪表盘(全网/我的/交叉 tab,PROPOSAL-005)(维护期增量)
