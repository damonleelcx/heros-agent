package cliagent

import (
	"context"
	"encoding/json"
	"fmt"
	ansi_strip "github.com/heros-foreal/agentd/internal/promptlayer/embedded_defaults/tools/_global/ansi-strip"
	approval "github.com/heros-foreal/agentd/internal/promptlayer/embedded_defaults/tools/_global/approval"
	binary_extensions "github.com/heros-foreal/agentd/internal/promptlayer/embedded_defaults/tools/_global/binary-extensions"
	browser_camofox "github.com/heros-foreal/agentd/internal/promptlayer/embedded_defaults/tools/_global/browser-camofox"
	browser_camofox_state "github.com/heros-foreal/agentd/internal/promptlayer/embedded_defaults/tools/_global/browser-camofox-state"
	browser_providers_base "github.com/heros-foreal/agentd/internal/promptlayer/embedded_defaults/tools/_global/browser-providers-base"
	browser_providers_browser_use "github.com/heros-foreal/agentd/internal/promptlayer/embedded_defaults/tools/_global/browser-providers-browser-use"
	browser_providers_browserbase "github.com/heros-foreal/agentd/internal/promptlayer/embedded_defaults/tools/_global/browser-providers-browserbase"
	browser_providers_firecrawl "github.com/heros-foreal/agentd/internal/promptlayer/embedded_defaults/tools/_global/browser-providers-firecrawl"
	browser_tool "github.com/heros-foreal/agentd/internal/promptlayer/embedded_defaults/tools/_global/browser-tool"
	budget_config "github.com/heros-foreal/agentd/internal/promptlayer/embedded_defaults/tools/_global/budget-config"
	checkpoint_manager "github.com/heros-foreal/agentd/internal/promptlayer/embedded_defaults/tools/_global/checkpoint-manager"
	clarify_tool "github.com/heros-foreal/agentd/internal/promptlayer/embedded_defaults/tools/_global/clarify-tool"
	code_execution_tool "github.com/heros-foreal/agentd/internal/promptlayer/embedded_defaults/tools/_global/code-execution-tool"
	credential_files "github.com/heros-foreal/agentd/internal/promptlayer/embedded_defaults/tools/_global/credential-files"
	cronjob_tools "github.com/heros-foreal/agentd/internal/promptlayer/embedded_defaults/tools/_global/cronjob-tools"
	debug_helpers "github.com/heros-foreal/agentd/internal/promptlayer/embedded_defaults/tools/_global/debug-helpers"
	delegate_tool "github.com/heros-foreal/agentd/internal/promptlayer/embedded_defaults/tools/_global/delegate-tool"
	echo_safe "github.com/heros-foreal/agentd/internal/promptlayer/embedded_defaults/tools/_global/echo-safe"
	env_passthrough "github.com/heros-foreal/agentd/internal/promptlayer/embedded_defaults/tools/_global/env-passthrough"
	environments_base "github.com/heros-foreal/agentd/internal/promptlayer/embedded_defaults/tools/_global/environments-base"
	environments_daytona "github.com/heros-foreal/agentd/internal/promptlayer/embedded_defaults/tools/_global/environments-daytona"
	environments_docker "github.com/heros-foreal/agentd/internal/promptlayer/embedded_defaults/tools/_global/environments-docker"
	environments_file_sync "github.com/heros-foreal/agentd/internal/promptlayer/embedded_defaults/tools/_global/environments-file-sync"
	environments_local "github.com/heros-foreal/agentd/internal/promptlayer/embedded_defaults/tools/_global/environments-local"
	environments_managed_modal "github.com/heros-foreal/agentd/internal/promptlayer/embedded_defaults/tools/_global/environments-managed-modal"
	environments_modal "github.com/heros-foreal/agentd/internal/promptlayer/embedded_defaults/tools/_global/environments-modal"
	environments_modal_utils "github.com/heros-foreal/agentd/internal/promptlayer/embedded_defaults/tools/_global/environments-modal-utils"
	environments_singularity "github.com/heros-foreal/agentd/internal/promptlayer/embedded_defaults/tools/_global/environments-singularity"
	environments_ssh "github.com/heros-foreal/agentd/internal/promptlayer/embedded_defaults/tools/_global/environments-ssh"
	evolution_reminder "github.com/heros-foreal/agentd/internal/promptlayer/embedded_defaults/tools/_global/evolution-reminder"
	file_operations "github.com/heros-foreal/agentd/internal/promptlayer/embedded_defaults/tools/_global/file-operations"
	file_tools "github.com/heros-foreal/agentd/internal/promptlayer/embedded_defaults/tools/_global/file-tools"
	fuzzy_match "github.com/heros-foreal/agentd/internal/promptlayer/embedded_defaults/tools/_global/fuzzy-match"
	homeassistant_tool "github.com/heros-foreal/agentd/internal/promptlayer/embedded_defaults/tools/_global/homeassistant-tool"
	image_generation_tool "github.com/heros-foreal/agentd/internal/promptlayer/embedded_defaults/tools/_global/image-generation-tool"
	interrupt "github.com/heros-foreal/agentd/internal/promptlayer/embedded_defaults/tools/_global/interrupt"
	managed_tool_gateway "github.com/heros-foreal/agentd/internal/promptlayer/embedded_defaults/tools/_global/managed-tool-gateway"
	mcp_oauth "github.com/heros-foreal/agentd/internal/promptlayer/embedded_defaults/tools/_global/mcp-oauth"
	mcp_tool "github.com/heros-foreal/agentd/internal/promptlayer/embedded_defaults/tools/_global/mcp-tool"
	memory_tool "github.com/heros-foreal/agentd/internal/promptlayer/embedded_defaults/tools/_global/memory-tool"
	mixture_of_agents_tool "github.com/heros-foreal/agentd/internal/promptlayer/embedded_defaults/tools/_global/mixture-of-agents-tool"
	neutts_synth "github.com/heros-foreal/agentd/internal/promptlayer/embedded_defaults/tools/_global/neutts-synth"
	openrouter_client "github.com/heros-foreal/agentd/internal/promptlayer/embedded_defaults/tools/_global/openrouter-client"
	osv_check "github.com/heros-foreal/agentd/internal/promptlayer/embedded_defaults/tools/_global/osv-check"
	patch_parser "github.com/heros-foreal/agentd/internal/promptlayer/embedded_defaults/tools/_global/patch-parser"
	path_security "github.com/heros-foreal/agentd/internal/promptlayer/embedded_defaults/tools/_global/path-security"
	process_registry "github.com/heros-foreal/agentd/internal/promptlayer/embedded_defaults/tools/_global/process-registry"
	registry "github.com/heros-foreal/agentd/internal/promptlayer/embedded_defaults/tools/_global/registry"
	rl_training_tool "github.com/heros-foreal/agentd/internal/promptlayer/embedded_defaults/tools/_global/rl-training-tool"
	send_message_tool "github.com/heros-foreal/agentd/internal/promptlayer/embedded_defaults/tools/_global/send-message-tool"
	session_search_tool "github.com/heros-foreal/agentd/internal/promptlayer/embedded_defaults/tools/_global/session-search-tool"
	skill_manager_tool "github.com/heros-foreal/agentd/internal/promptlayer/embedded_defaults/tools/_global/skill-manager-tool"
	skills_guard "github.com/heros-foreal/agentd/internal/promptlayer/embedded_defaults/tools/_global/skills-guard"
	skills_hub "github.com/heros-foreal/agentd/internal/promptlayer/embedded_defaults/tools/_global/skills-hub"
	skills_sync "github.com/heros-foreal/agentd/internal/promptlayer/embedded_defaults/tools/_global/skills-sync"
	skills_tool "github.com/heros-foreal/agentd/internal/promptlayer/embedded_defaults/tools/_global/skills-tool"
	terminal_tool "github.com/heros-foreal/agentd/internal/promptlayer/embedded_defaults/tools/_global/terminal-tool"
	tirith_security "github.com/heros-foreal/agentd/internal/promptlayer/embedded_defaults/tools/_global/tirith-security"
	todo_tool "github.com/heros-foreal/agentd/internal/promptlayer/embedded_defaults/tools/_global/todo-tool"
	tool_backend_helpers "github.com/heros-foreal/agentd/internal/promptlayer/embedded_defaults/tools/_global/tool-backend-helpers"
	tool_result_storage "github.com/heros-foreal/agentd/internal/promptlayer/embedded_defaults/tools/_global/tool-result-storage"
	transcription_tools "github.com/heros-foreal/agentd/internal/promptlayer/embedded_defaults/tools/_global/transcription-tools"
	tts_tool "github.com/heros-foreal/agentd/internal/promptlayer/embedded_defaults/tools/_global/tts-tool"
	url_safety "github.com/heros-foreal/agentd/internal/promptlayer/embedded_defaults/tools/_global/url-safety"
	vision_tools "github.com/heros-foreal/agentd/internal/promptlayer/embedded_defaults/tools/_global/vision-tools"
	voice_mode "github.com/heros-foreal/agentd/internal/promptlayer/embedded_defaults/tools/_global/voice-mode"
	web_tools "github.com/heros-foreal/agentd/internal/promptlayer/embedded_defaults/tools/_global/web-tools"
	website_policy "github.com/heros-foreal/agentd/internal/promptlayer/embedded_defaults/tools/_global/website-policy"
	"strings"
	"time"
)

type moduleExecutor func(args map[string]any) (map[string]any, error)

var extensionToolModules = map[string]moduleExecutor{
	"ansi-strip":                    func(args map[string]any) (map[string]any, error) { return ansi_strip.Execute(args) },
	"approval":                      func(args map[string]any) (map[string]any, error) { return approval.Execute(args) },
	"binary-extensions":             func(args map[string]any) (map[string]any, error) { return binary_extensions.Execute(args) },
	"browser-camofox":               func(args map[string]any) (map[string]any, error) { return browser_camofox.Execute(args) },
	"browser-camofox-state":         func(args map[string]any) (map[string]any, error) { return browser_camofox_state.Execute(args) },
	"browser-providers-base":        func(args map[string]any) (map[string]any, error) { return browser_providers_base.Execute(args) },
	"browser-providers-browser-use": func(args map[string]any) (map[string]any, error) { return browser_providers_browser_use.Execute(args) },
	"browser-providers-browserbase": func(args map[string]any) (map[string]any, error) { return browser_providers_browserbase.Execute(args) },
	"browser-providers-firecrawl":   func(args map[string]any) (map[string]any, error) { return browser_providers_firecrawl.Execute(args) },
	"browser-tool":                  func(args map[string]any) (map[string]any, error) { return browser_tool.Execute(args) },
	"budget-config":                 func(args map[string]any) (map[string]any, error) { return budget_config.Execute(args) },
	"checkpoint-manager":            func(args map[string]any) (map[string]any, error) { return checkpoint_manager.Execute(args) },
	"clarify-tool":                  func(args map[string]any) (map[string]any, error) { return clarify_tool.Execute(args) },
	"code-execution-tool":           func(args map[string]any) (map[string]any, error) { return code_execution_tool.Execute(args) },
	"credential-files":              func(args map[string]any) (map[string]any, error) { return credential_files.Execute(args) },
	"cronjob-tools":                 func(args map[string]any) (map[string]any, error) { return cronjob_tools.Execute(args) },
	"debug-helpers":                 func(args map[string]any) (map[string]any, error) { return debug_helpers.Execute(args) },
	"delegate-tool":                 func(args map[string]any) (map[string]any, error) { return delegate_tool.Execute(args) },
	"echo-safe":                     func(args map[string]any) (map[string]any, error) { return echo_safe.Execute(args) },
	"env-passthrough":               func(args map[string]any) (map[string]any, error) { return env_passthrough.Execute(args) },
	"environments-base":             func(args map[string]any) (map[string]any, error) { return environments_base.Execute(args) },
	"environments-daytona":          func(args map[string]any) (map[string]any, error) { return environments_daytona.Execute(args) },
	"environments-docker":           func(args map[string]any) (map[string]any, error) { return environments_docker.Execute(args) },
	"environments-file-sync":        func(args map[string]any) (map[string]any, error) { return environments_file_sync.Execute(args) },
	"environments-local":            func(args map[string]any) (map[string]any, error) { return environments_local.Execute(args) },
	"environments-managed-modal":    func(args map[string]any) (map[string]any, error) { return environments_managed_modal.Execute(args) },
	"environments-modal":            func(args map[string]any) (map[string]any, error) { return environments_modal.Execute(args) },
	"environments-modal-utils":      func(args map[string]any) (map[string]any, error) { return environments_modal_utils.Execute(args) },
	"environments-singularity":      func(args map[string]any) (map[string]any, error) { return environments_singularity.Execute(args) },
	"environments-ssh":              func(args map[string]any) (map[string]any, error) { return environments_ssh.Execute(args) },
	"evolution-reminder":            func(args map[string]any) (map[string]any, error) { return evolution_reminder.Execute(args) },
	"file-operations":               func(args map[string]any) (map[string]any, error) { return file_operations.Execute(args) },
	"file-tools":                    func(args map[string]any) (map[string]any, error) { return file_tools.Execute(args) },
	"fuzzy-match":                   func(args map[string]any) (map[string]any, error) { return fuzzy_match.Execute(args) },
	"homeassistant-tool":            func(args map[string]any) (map[string]any, error) { return homeassistant_tool.Execute(args) },
	"image-generation-tool":         func(args map[string]any) (map[string]any, error) { return image_generation_tool.Execute(args) },
	"interrupt":                     func(args map[string]any) (map[string]any, error) { return interrupt.Execute(args) },
	"managed-tool-gateway":          func(args map[string]any) (map[string]any, error) { return managed_tool_gateway.Execute(args) },
	"mcp-oauth":                     func(args map[string]any) (map[string]any, error) { return mcp_oauth.Execute(args) },
	"mcp-tool":                      func(args map[string]any) (map[string]any, error) { return mcp_tool.Execute(args) },
	"memory-tool":                   func(args map[string]any) (map[string]any, error) { return memory_tool.Execute(args) },
	"mixture-of-agents-tool":        func(args map[string]any) (map[string]any, error) { return mixture_of_agents_tool.Execute(args) },
	"neutts-synth":                  func(args map[string]any) (map[string]any, error) { return neutts_synth.Execute(args) },
	"openrouter-client":             func(args map[string]any) (map[string]any, error) { return openrouter_client.Execute(args) },
	"osv-check":                     func(args map[string]any) (map[string]any, error) { return osv_check.Execute(args) },
	"patch-parser":                  func(args map[string]any) (map[string]any, error) { return patch_parser.Execute(args) },
	"path-security":                 func(args map[string]any) (map[string]any, error) { return path_security.Execute(args) },
	"process-registry":              func(args map[string]any) (map[string]any, error) { return process_registry.Execute(args) },
	"registry":                      func(args map[string]any) (map[string]any, error) { return registry.Execute(args) },
	"rl-training-tool":              func(args map[string]any) (map[string]any, error) { return rl_training_tool.Execute(args) },
	"send-message-tool":             func(args map[string]any) (map[string]any, error) { return send_message_tool.Execute(args) },
	"session-search-tool":           func(args map[string]any) (map[string]any, error) { return session_search_tool.Execute(args) },
	"skill-manager-tool":            func(args map[string]any) (map[string]any, error) { return skill_manager_tool.Execute(args) },
	"skills-guard":                  func(args map[string]any) (map[string]any, error) { return skills_guard.Execute(args) },
	"skills-hub":                    func(args map[string]any) (map[string]any, error) { return skills_hub.Execute(args) },
	"skills-sync":                   func(args map[string]any) (map[string]any, error) { return skills_sync.Execute(args) },
	"skills-tool":                   func(args map[string]any) (map[string]any, error) { return skills_tool.Execute(args) },
	"terminal-tool":                 func(args map[string]any) (map[string]any, error) { return terminal_tool.Execute(args) },
	"tirith-security":               func(args map[string]any) (map[string]any, error) { return tirith_security.Execute(args) },
	"todo-tool":                     func(args map[string]any) (map[string]any, error) { return todo_tool.Execute(args) },
	"tool-backend-helpers":          func(args map[string]any) (map[string]any, error) { return tool_backend_helpers.Execute(args) },
	"tool-result-storage":           func(args map[string]any) (map[string]any, error) { return tool_result_storage.Execute(args) },
	"transcription-tools":           func(args map[string]any) (map[string]any, error) { return transcription_tools.Execute(args) },
	"tts-tool":                      func(args map[string]any) (map[string]any, error) { return tts_tool.Execute(args) },
	"url-safety":                    func(args map[string]any) (map[string]any, error) { return url_safety.Execute(args) },
	"vision-tools":                  func(args map[string]any) (map[string]any, error) { return vision_tools.Execute(args) },
	"voice-mode":                    func(args map[string]any) (map[string]any, error) { return voice_mode.Execute(args) },
	"web-tools":                     func(args map[string]any) (map[string]any, error) { return web_tools.Execute(args) },
	"website-policy":                func(args map[string]any) (map[string]any, error) { return website_policy.Execute(args) },
}

func (s *Session) runImportedCatalogTool(ctx context.Context, toolID string, args map[string]any) (string, error) {
	if args == nil {
		args = map[string]any{}
	}
	id := strings.TrimSpace(strings.ToLower(toolID))
	args["_workdir"] = s.WorkDir
	args["_session_id"] = s.SessionID
	if s.Agentd != nil {
		args["_agentd_url"] = s.Agentd.BaseURL
		args["_api_key"] = s.Agentd.APIKey
	}
	exec, ok := extensionToolModules[id]
	if !ok {
		return extStub(id, args)
	}
	res, err := exec(args)
	if err != nil {
		return "", err
	}
	res = normalizeToolResponse(id, args, res)
	b, _ := json.Marshal(res)
	return string(b), nil
}

func normalizeToolResponse(toolID string, args map[string]any, res map[string]any) map[string]any {
	if res == nil {
		res = map[string]any{}
	}
	res["tool_id"] = toolID
	status := toString(res["status"])
	if status == "" {
		status = "ok"
	}
	res["status"] = status
	action := toString(res["action"])
	if action == "" {
		action = toString(args["action"])
		if strings.TrimSpace(action) == "" {
			action = "run"
		}
		res["action"] = action
	}
	audit, _ := res["audit"].(map[string]any)
	if audit == nil {
		audit = map[string]any{}
	}
	audit["timestamp"] = safeStringOrDefault(audit["timestamp"], time.Now().UTC().Format(time.RFC3339))
	audit["session_id"] = safeStringOrDefault(audit["session_id"], toString(args["_session_id"]))
	audit["workdir"] = safeStringOrDefault(audit["workdir"], toString(args["_workdir"]))
	audit["action"] = action
	res["audit"] = audit
	if status == ToolStatusError {
		code := ParseErrorCode(toString(res["error_code"]))
		if !IsAllowedToolErrorCode(code) {
			code = ErrorCodeUnknownError
		}
		res["error_code"] = string(code)
		if toString(res["error"]) == "" {
			res["error"] = "tool returned error status without message"
		}
	}
	return res
}

func toString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	if v == nil {
		return ""
	}
	return fmt.Sprint(v)
}

func safeStringOrDefault(v any, def string) string {
	s := toString(v)
	if strings.TrimSpace(s) == "" {
		return def
	}
	return s
}
