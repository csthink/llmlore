# llmlore

**A library of tools & tutorials for the LLM era** — an open-source, manually
triggered CLI plus a continuously updated open dataset. It collects, filters,
and tags high-quality GitHub repositories that *teach you how to use LLMs / AI*,
distills them into structured data, and renders a local dashboard for browsing.

In short: **an open dataset + a viewer / refresh engine**. The data itself lives
in `data/repos.json`; the web page is a read-only view of it.

> 中文文档见 [README.zh-CN.md](README.zh-CN.md)。

## Features

- **Zero setup** — pre-generated data ships in the repo; install and browse right
  away, no LLM key required.
- **It filters** — a model decides whether a repo is really *about teaching AI*,
  dropping frameworks, model weights, and paper reproductions.
- **Structured** — a topic × type two-axis tagging scheme plus a one-line summary,
  activity, and star trend; more systematic than an awesome-list.
- **Switchable orientation** — historical high-star (established classics) or
  recent risers (trending newcomers).
- **Organize your own stars** — re-group the repos you've starred into a
  topic-browsable view (entirely local).

## Install

```bash
# Homebrew (shared csthink tap)
brew install csthink/tap/llmlore

# or from source
go install github.com/csthink/llmlore/cmd/llmlore@latest
```

After installing, run `llmlore pull` to fetch the pre-generated data, then
`llmlore` to open the dashboard (zero config).

## Quick start

```bash
# Download the pre-generated open dataset, then open the dashboard (zero config)
llmlore pull
llmlore                 # render + serve locally + open your browser; Ctrl+C to stop

# Just view the data already on disk
llmlore serve --port 7777

# Bring your own key to fetch / regenerate the latest data
export LLMLORE_LLM_PROVIDER=anthropic
export LLMLORE_LLM_API_KEY=...      # without it, falls back to heuristic filtering
llmlore update --mode historical    # or trending / both

# Organize the repos you've starred (local-only, never committed anywhere)
llmlore stars sync --user <login>      # public stars of any user; OR set
export LLMLORE_GITHUB_TOKEN=...         # a token to sync your own (incl. private) stars
llmlore stars sync                     # then this fetches your own stars
llmlore stars organize
llmlore stars view
```

## How it works

```
Collector (search / trending) → Classifier (keep/drop · LLM or heuristic)
→ Enricher (summary · tags · activity · snapshots) → Store (repos.json) → Renderer (HTML + local server)
```

## Configuration (environment variables)

| Variable | Meaning |
|---|---|
| `LLMLORE_LLM_PROVIDER` / `LLMLORE_LLM_API_KEY` | LLM provider and key (without it, runs heuristic) |
| `LLMLORE_GITHUB_TOKEN` | GitHub token (raise rate limits / read private stars) |
| `LLMLORE_PORT` | Local server port (default 7777) |
| `LLMLORE_EXCLUDE_STARRED` | Exclude repos you've already starred during discover |

**Optional config file.** Non-secret settings (`provider`, `base_url`, `model`,
port, discover options) can also live in `~/.config/llmlore/config.toml`. Scaffold
one with:

```bash
llmlore config init     # writes a commented template; --force to overwrite
```

Then edit it and set `provider` to a real name to enable LLM features. Your **API
key is never stored in the file** — always export `LLMLORE_LLM_API_KEY`.
Environment variables override the file. An untouched template stays in heuristic
mode and prints a one-line reminder when you start the server. llmlore speaks the
OpenAI-compatible `/chat/completions` API, so point `base_url` at OpenAI, DeepSeek,
SiliconFlow, a local Ollama/vLLM, etc.

**Getting a `LLMLORE_GITHUB_TOKEN`:** create a Personal Access Token at
<https://github.com/settings/tokens> (Settings → Developer settings → Personal
access tokens). For public stars only, a token with **no scopes** is enough (it
just raises rate limits); to sync your **own private** stars, use a classic
token with the **`repo`** scope. Then `export LLMLORE_GITHUB_TOKEN=<token>`.
The token is read from the environment, kept in memory only, and never written
to disk, logs, or argv. See
[GitHub's token docs](https://docs.github.com/en/authentication/keeping-your-account-and-data-secure/managing-your-personal-access-tokens).

See `docs/spec.md` for details.

## Data

`data/repos.json` is the open dataset, refreshed on a schedule by the repo's
GitHub Action running the full pipeline. The data is useful on its own even
without the tool. `llmlore update` regenerates it locally with your own key.

## Privacy

Your `my-stars` data (the repos you've starred) is stored **locally only** in
`~/.local/share/llmlore/`, physically separate from the shared data, and is
**never committed to the open repository**. The tool only lists *unstar
candidates* — it never performs any account action on your behalf.

## Development

- Language: Go, single-file binary.
- Workflow: built with Claude Code, implementing T0–T8 sequentially in one
  session — self-verifying each task's DoD, holding the red lines, with an
  optional second pair of eyes (independent review not mandatory). See
  `docs/workflow.md`.
- Design & contracts: `docs/proposal.md` / `docs/design.md` / `docs/spec.md` /
  `docs/tasks.md`; the project constitution is `CLAUDE.md` at the repo root.

## Roadmap

T0 scaffolding → T1 data model/config → T2 collection → T3 classification →
T4 store/enrich → T5 render/serve → T6 discover wiring → T7 my-stars →
T8 distribution & open data → T9 config init & placeholder check. See `docs/tasks.md`.

## License

MIT — see [LICENSE](LICENSE).
