# External Skills/Tools Import

This repo includes a bulk importer that can bring external skills and tool metadata into Heros.

## What is included

- **Skills:** markdown files from `skills/` into  
  `internal/promptlayer/embedded_defaults/skills/_global/custom/`
- **Tools folder entries:** generated in  
  `internal/promptlayer/embedded_defaults/tools/_global/<tool-id>/` with:
  - `tool.yaml` (catalog metadata)
  - `tool.go` (Go implementation marker)
- **Tool runtime handlers:** implemented in Go under  
  `internal/cliagent/tools_runtime.go`

## Run importer

```bash
python scripts/import_external_assets.py --source tmp/source-agent --clean
```

`--clean` removes previous custom external content before writing new files.

## Runtime note

At chat time, extension capabilities are invoked through the Go-backed OpenAI tool **`heros_extension_tool`** (see skill `runtime-tools`). There is **no** bundled Python runtime in the Heros binary.

## Policy

- Tools are **Go-first** in this repository.
- New tool behavior should be added in Go code (with tests), not as script wrappers.
