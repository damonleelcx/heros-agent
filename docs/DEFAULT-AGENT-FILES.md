# Default skills, tools, and system prompt (in the repo)

Bundled defaults live under **`internal/promptlayer/embedded_defaults/`** in the same layout as `data_dir`:

```
embedded_defaults/
  system/prompt.md
  skills/_global/<skill-slug>/SKILL.md
  tools/_global/<tool-id>/tool.yaml
```

They are compiled into the **`heros` / `agentd`** binary with `go:embed` and copied into your **`data_dir`** on **first start** only when each target path is still missing (existing installs are not overwritten).

## What ships today

| Kind | Name | Role |
|------|------|------|
| System | `system/prompt.md` | Base assistant instructions + governance |
| Skill | `core-reasoning` | Steps, memory search, when to propose changes |
| Skill | `interaction-learning-loop` | Hermes-style memory save/search + when to evolve |
| Skill | `long-running-work` | Milestones, checkpoints, shell for sustained tasks |
| Skill | `self-evolution-via-proposals` | `heros_submit_proposal` layers and diff formats |
| Skill | `agentskills-packaging` | Small composable skills ([agentskills.io](https://agentskills.io) spirit) |
| Tool | `echo-safe` | Placeholder low-risk catalog entry |
| Tool | `evolution-reminder` | Nudge to use proposals for durable changes |

To change defaults for everyone, **edit the files under `embedded_defaults/`** and rebuild. To customize one machine only, edit files under your **`data_dir`** after seeding.

See also [AGENT_LAYOUT.md](AGENT_LAYOUT.md) for how disk and SQLite indexes relate.
