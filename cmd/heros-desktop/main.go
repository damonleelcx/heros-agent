package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/storage"
	"fyne.io/fyne/v2/widget"
	"github.com/google/uuid"
	"github.com/heros-foreal/agentd/internal/cliagent"
	"github.com/heros-foreal/agentd/internal/config"
	"github.com/heros-foreal/agentd/internal/installpath"
	"github.com/heros-foreal/agentd/internal/launch"
)

type uiStreamWriter struct {
	writeFn func(string)
	mu      sync.Mutex
	pending string
}

func (w *uiStreamWriter) Write(p []byte) (int, error) {
	if w == nil || w.writeFn == nil || len(p) == 0 {
		return len(p), nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.pending += string(p)
	for {
		i := strings.IndexByte(w.pending, '\n')
		if i < 0 {
			break
		}
		line := strings.TrimRight(w.pending[:i], "\r")
		w.pending = w.pending[i+1:]
		if rendered, ok := renderHarnessEventLine(line); ok {
			if strings.TrimSpace(rendered) != "" {
				w.writeFn(rendered + "\n")
			}
			continue
		}
		w.writeFn(line + "\n")
	}
	// Do not wait for newline for regular assistant text; stream it live.
	// Keep buffering only potential harness_event JSON lines until newline.
	if strings.TrimSpace(w.pending) != "" && !strings.HasPrefix(strings.TrimSpace(w.pending), cliagent.HarnessEventPrefix) {
		w.writeFn(w.pending)
		w.pending = ""
	}
	return len(p), nil
}

func renderHarnessEventLine(line string) (string, bool) {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, cliagent.HarnessEventPrefix) {
		return "", false
	}
	raw := strings.TrimSpace(strings.TrimPrefix(line, cliagent.HarnessEventPrefix))
	var ev cliagent.HarnessEvent
	if err := json.Unmarshal([]byte(raw), &ev); err != nil {
		return "", false
	}
	phase := strings.TrimPrefix(strings.TrimSpace(ev.Phase), "harness_")
	switch phase {
	case "assistant":
		// Hide generic assistant runtime chatter in desktop UI.
		return "", true
	case "tool":
		name := strings.TrimSpace(ev.ToolName)
		if name == "" {
			name = "tool"
		}
		if ev.Stage == "start" {
			return fmt.Sprintf("[exec] running %s", name), true
		}
		if ev.Stage == "end" {
			if ev.DurationMS > 0 {
				return fmt.Sprintf("[exec] done %s status=%s (%dms)", name, strings.TrimSpace(ev.Status), ev.DurationMS), true
			}
			return fmt.Sprintf("[exec] done %s status=%s", name, strings.TrimSpace(ev.Status)), true
		}
	case "leader", "specialist", "feedback", "todo", "verify", "critic", "refine", "harness":
		return cliagent.FormatHarnessProgressLine(cliagent.HarnessEventToProgressEvent(ev)), true
	}
	return fmt.Sprintf("[harness] %s %s", phase, ev.Stage), true
}

// Set at link time by release builds, e.g. -ldflags "-X main.version=v1.2.3"
var version = "dev"

func firstNonEmptyEnv(keys ...string) string {
	for _, k := range keys {
		if v := strings.TrimSpace(os.Getenv(k)); v != "" {
			return v
		}
	}
	return ""
}

func treeIDToAbsPath(workdir, id string) string {
	if id == "." || strings.TrimSpace(id) == "" {
		return workdir
	}
	return filepath.Join(workdir, id)
}

func listTreeChildren(workdir, id string) []string {
	abs := treeIDToAbsPath(workdir, id)
	entries, err := os.ReadDir(abs)
	if err != nil {
		return nil
	}
	dirs := make([]string, 0, len(entries))
	files := make([]string, 0, len(entries))
	for _, e := range entries {
		name := e.Name()
		if name == "" {
			continue
		}
		child := name
		if id != "." {
			child = filepath.Join(id, name)
		}
		if e.IsDir() {
			dirs = append(dirs, child)
		} else {
			files = append(files, child)
		}
	}
	sort.Slice(dirs, func(i, j int) bool {
		return strings.ToLower(dirs[i]) < strings.ToLower(dirs[j])
	})
	sort.Slice(files, func(i, j int) bool {
		return strings.ToLower(files[i]) < strings.ToLower(files[j])
	})
	return append(dirs, files...)
}

type gitChange struct {
	Kind string
	Path string
}

func collectGitChanges(ctx context.Context, workdir string) ([]gitChange, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", workdir, "status", "--porcelain")
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if strings.Contains(strings.ToLower(msg), "not a git repository") {
			return nil, nil
		}
		return nil, fmt.Errorf("git status failed: %s", msg)
	}
	var changes []gitChange
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" || len(line) < 4 {
			continue
		}
		status := line[:2]
		path := strings.TrimSpace(line[3:])
		if strings.Contains(path, " -> ") {
			parts := strings.Split(path, " -> ")
			path = strings.TrimSpace(parts[len(parts)-1])
		}
		kind := "other"
		switch {
		case status == "??" || strings.Contains(status, "A"):
			kind = "created"
		case strings.Contains(status, "D"):
			kind = "deleted"
		case strings.Contains(status, "R"):
			kind = "renamed"
		case strings.Contains(status, "M"):
			kind = "updated"
		}
		changes = append(changes, gitChange{Kind: kind, Path: path})
	}
	return changes, nil
}

func renderGitChanges(workdir string, changes []gitChange) string {
	if len(changes) == 0 {
		return fmt.Sprintf("Git changes (%s)\n\nclean working tree or not a git repository.", workdir)
	}
	var created, updated, deleted, renamed, other int
	for _, c := range changes {
		switch c.Kind {
		case "created":
			created++
		case "updated":
			updated++
		case "deleted":
			deleted++
		case "renamed":
			renamed++
		default:
			other++
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Git changes (%s)\n", workdir)
	fmt.Fprintf(&b, "created=%d updated=%d deleted=%d renamed=%d other=%d\n\n", created, updated, deleted, renamed, other)
	for _, c := range changes {
		code := "?"
		switch c.Kind {
		case "created":
			code = "A"
		case "updated":
			code = "M"
		case "deleted":
			code = "D"
		case "renamed":
			code = "R"
		}
		fmt.Fprintf(&b, "[%s] %s\n", code, c.Path)
	}
	return b.String()
}

func main() {
	for _, a := range os.Args[1:] {
		switch a {
		case "--version", "-V":
			fmt.Println(version)
			return
		}
	}

	cfgPath := flag.String("config", "", "override path to config.json")
	apiKey := flag.String("api-key", "", "X-API-Key when agentd auth_mode=required")
	openaiBase := flag.String("openai-base", "", "override LLM API base")
	openaiKeyFlag := flag.String("openai-api-key", "", "override LLM bearer token")
	model := flag.String("model", "", "override chat model")
	workdir := flag.String("workdir", "", "workspace for heros_shell")
	sessionID := flag.String("session", "", "episodic memory session id")
	targetTenant := flag.String("target-tenant", "", "default target_tenant for heros_submit_proposal")
	agentShell := flag.Bool("agent-shell", false, "expose heros_agent_shell on agentd host")
	noSessionLog := flag.Bool("no-session-log", false, "do not auto-append each turn to episodic memory")
	addPath := flag.Bool("add-path", false, "add this binary directory (or Go bin when applicable) to user PATH and exit")
	showVer := flag.Bool("version", false, "print version and exit")
	deskTheme := flag.String("theme", "", "desktop UI palette: light|dark (overrides HEROS_DESKTOP_THEME; default dark)")
	flag.Parse()

	if *showVer {
		fmt.Println(version)
		return
	}
	if *addPath {
		dir, err := installpath.AddPathTargetDir()
		if err != nil {
			log.Fatalf("add-path: %v", err)
		}
		if err := installpath.AddUserPATH(dir); err != nil {
			log.Fatalf("add-path: %v", err)
		}
		_, _ = fmt.Fprintf(os.Stderr, "heros-desktop: added %q to your user PATH.\n", dir)
		_, _ = fmt.Fprintln(os.Stderr, "heros-desktop: open a new terminal, then run: heros-desktop")
		return
	}

	cfg, cfgSrc, err := config.LoadAuto(*cfgPath)
	if err != nil {
		log.Fatal(err)
	}

	def := config.Default()
	openaiBaseStr := strings.TrimSpace(*openaiBase)
	if openaiBaseStr == "" {
		openaiBaseStr = strings.TrimSpace(os.Getenv("OPENAI_BASE_URL"))
	}
	if openaiBaseStr == "" {
		openaiBaseStr = strings.TrimSpace(cfg.OpenAIBaseURL)
	}
	if openaiBaseStr == "" {
		openaiBaseStr = def.OpenAIBaseURL
	}

	modelStr := strings.TrimSpace(*model)
	if modelStr == "" {
		modelStr = firstNonEmptyEnv("OPENAI_MODEL", "HEROS_MODEL")
	}
	if modelStr == "" {
		modelStr = strings.TrimSpace(cfg.OpenAIModel)
	}
	if modelStr == "" {
		modelStr = def.OpenAIModel
	}

	openaiKey := strings.TrimSpace(*openaiKeyFlag)
	if openaiKey == "" {
		openaiKey = strings.TrimSpace(os.Getenv("OPENAI_API_KEY"))
	}
	if openaiKey == "" {
		openaiKey = strings.TrimSpace(cfg.OpenAIAPIKey)
	}
	if openaiKey == "" {
		log.Fatal("missing LLM API key; set OPENAI_API_KEY, configure openai_api_key in config.json, or pass -openai-api-key")
	}

	wd := strings.TrimSpace(*workdir)
	if wd != "" {
		absWD, err := filepath.Abs(wd)
		if err != nil {
			log.Fatalf("workdir: %v", err)
		}
		wd = absWD
	} else {
		if envWD := firstNonEmptyEnv("HEROS_WORKDIR", "INIT_CWD"); envWD != "" {
			absWD, err := filepath.Abs(envWD)
			if err != nil {
				log.Fatalf("workdir from env: %v", err)
			}
			wd = absWD
		} else if savedWD, _ := config.GetCLIWorkdir(); strings.TrimSpace(savedWD) != "" {
			absWD, err := filepath.Abs(strings.TrimSpace(savedWD))
			if err == nil {
				if st, serr := os.Stat(absWD); serr == nil && st.IsDir() {
					wd = absWD
				}
			}
		}
		if wd == "" {
			cwd, err := os.Getwd()
			if err != nil {
				log.Fatalf("getwd: %v", err)
			}
			wd = cwd
		}
	}

	sid := strings.TrimSpace(*sessionID)
	if sid == "" {
		sid = uuid.NewString()
	}

	srv, err := launch.StartAgentd(context.Background(), cfg)
	if err != nil {
		log.Fatalf("start agentd: %v", err)
	}

	waitCtx, waitCancel := context.WithTimeout(context.Background(), 45*time.Second)
	err = launch.WaitReady(waitCtx, srv.AgentdBaseURL())
	waitCancel()
	if err != nil {
		log.Fatalf("agent not ready at %s: %v", srv.AgentdBaseURL(), err)
	}

	sess := &cliagent.Session{
		Agentd: &cliagent.AgentdClient{
			BaseURL:    srv.AgentdBaseURL(),
			APIKey:     *apiKey,
			HTTPClient: cliagent.DefaultHTTPClient(),
		},
		OpenAIBase:               openaiBaseStr,
		OpenAIKey:                openaiKey,
		Model:                    modelStr,
		SessionID:                sid,
		DataDir:                  cfg.DataDir,
		WorkDir:                  wd,
		AgentShell:               *agentShell,
		Stream:                   true,
		UseReadline:              false,
		TargetTenant:             *targetTenant,
		LogTurnsToEpisodic:       !*noSessionLog,
		AutoInjectMemory:         true,
		AutoInjectTopK:           3,
		AutoConsolidateEvery:     6,
		AutoConsolidateThreshold: 0.45,
	}

	ctx := context.Background()
	if err := sess.PrimeSystem(ctx); err != nil {
		log.Fatalf("prime system: %v", err)
	}

	ui := app.NewWithID("com.heros.desktop")
	filePrefs := loadDesktopFilePrefs()
	mergedUI := mergeHerosDesktopForUI(cfg.HerosDesktop, filePrefs)
	themeOpts := CelestialOpts{}
	accentFromEnv := false
	if acRaw := strings.TrimSpace(os.Getenv("HEROS_DESKTOP_ACCENT")); acRaw != "" {
		accentFromEnv = true
		if c, ok := ParseHexRGB(acRaw); ok {
			cp := c
			themeOpts.Accent = &cp
		} else {
			log.Printf("heros-desktop: ignoring invalid HEROS_DESKTOP_ACCENT=%q (use #RGB, #RRGGBB, or RRGGBB)", acRaw)
		}
	} else if c, ok := ParseHexRGB(mergedUI.Accent); ok {
		cp := c
		themeOpts.Accent = &cp
	}
	tChoice := strings.TrimSpace(strings.ToLower(*deskTheme))
	if tChoice == "" {
		tChoice = strings.TrimSpace(strings.ToLower(os.Getenv("HEROS_DESKTOP_THEME")))
	}
	if tChoice == "" {
		tChoice = normalizeAppearanceKey(mergedUI.Appearance)
	}
	switch tChoice {
	case "light", "day", "paper":
		themeOpts.Light = true
	case "dark", "night", "":
		themeOpts.Light = false
	default:
		log.Printf("heros-desktop: unknown theme %q — use light or dark (falling back to dark)", tChoice)
		themeOpts.Light = false
	}
	cfgHerosPath, herr := config.ResolveHerosConfigWritePath(cfgSrc)
	if herr != nil {
		log.Printf("heros-desktop: config path for heros_desktop: %v", herr)
	}
	ui.Settings().SetTheme(NewCelestialTheme(themeOpts))

	w := ui.NewWindow("Heros — local agent studio")
	w.Resize(fyne.NewSize(1080, 820))
	w.CenterOnScreen()

	cfgNote := "defaults"
	if cfgSrc != "" {
		cfgNote = cfgSrc
	}
	statusLine := func(currentWorkdir, state string) string {
		return fmt.Sprintf("config=%s | agent=%s | model=%s | workdir=%s%s", cfgNote, srv.AgentdBaseURL(), modelStr, currentWorkdir, state)
	}
	status := widget.NewLabel(statusLine(wd, " | ready"))
	activity := widget.NewActivity()
	activity.Hide()
	statusRow := container.NewHBox(activity, status)

	outputMD := "## Welcome to Heros Desktop\n\n" +
		"Everything here runs **locally**: skills, tools, memory, and long harness runs stay on your machine.\n\n" +
		"> **Try this:** choose a workspace folder on the left, describe what you want to build or fix, then hit **Send**. " +
		"Git changes refresh after each assistant turn.\n\n" +
		"*Tip: **Ctrl+Enter** (Windows/Linux) or **⌘+Enter** (macOS) sends from the prompt.*\n\n" +
		"*Look & feel:* **Appearance** syncs to `heros_desktop` in the agent `config.json` (discovered e.g. next to the repo, `HEROS_CONFIG`, or `~/.heros-agent/config.json`), and to `~/.heros-desktop.json` as a mirror. Per-run overrides: `-theme`, then `HEROS_DESKTOP_*` env.\n\n"
	output := widget.NewRichTextFromMarkdown(outputMD)
	output.Wrapping = fyne.TextWrapWord
	outputBox := container.NewVScroll(output)
	var outputMu sync.Mutex
	pendingRender := ""
	renderScheduled := false
	flushRender := func() {
		outputMu.Lock()
		if pendingRender == "" {
			renderScheduled = false
			outputMu.Unlock()
			return
		}
		outputMD += pendingRender
		pendingRender = ""
		renderScheduled = false
		cur := outputMD
		outputMu.Unlock()
		output.ParseMarkdown(cur)
		outputBox.ScrollToBottom()
	}

	input := widget.NewMultiLineEntry()
	input.SetMinRowsVisible(6)
	input.Wrapping = fyne.TextWrapWord
	input.SetPlaceHolder("Ask in plain language — e.g. “add tests for the parser”, “explain this repo”, or “run a harness plan for …”")

	var (
		sessMu sync.Mutex
		busy   bool
	)
	getWorkdir := func() string {
		sessMu.Lock()
		defer sessMu.Unlock()
		return sess.WorkDir
	}

	appendOutput := func(text string) {
		outputMu.Lock()
		outputMD += text
		cur := outputMD
		outputMu.Unlock()
		output.ParseMarkdown(cur)
		outputBox.ScrollToBottom()
	}
	appendOutputAsync := func(text string) {
		outputMu.Lock()
		pendingRender += text
		if renderScheduled {
			outputMu.Unlock()
			return
		}
		renderScheduled = true
		outputMu.Unlock()
		go func() {
			time.Sleep(80 * time.Millisecond)
			fyne.Do(flushRender)
		}()
	}

	setBusy := func(v bool, msg string) {
		busy = v
		if v {
			input.Disable()
			activity.Show()
			activity.Start()
		} else {
			input.Enable()
			activity.Stop()
			activity.Hide()
		}
		status.SetText(msg)
	}
	setBusyAsync := func(v bool, msg string) {
		fyne.Do(func() {
			busy = v
			if v {
				input.Disable()
				activity.Show()
				activity.Start()
			} else {
				input.Enable()
				activity.Stop()
				activity.Hide()
			}
			status.SetText(msg)
		})
	}

	selectedPath := widget.NewLabel("Nothing selected yet — tap a file or folder in the tree.")
	selectedPath.Wrapping = fyne.TextWrapWord
	explorerTree := widget.NewTree(
		func(uid string) []string {
			return listTreeChildren(getWorkdir(), uid)
		},
		func(uid string) bool {
			if uid == "." {
				return true
			}
			st, err := os.Stat(treeIDToAbsPath(getWorkdir(), uid))
			return err == nil && st.IsDir()
		},
		func(branch bool) fyne.CanvasObject {
			return widget.NewLabel("template")
		},
		func(uid string, branch bool, obj fyne.CanvasObject) {
			label := obj.(*widget.Label)
			if uid == "." {
				label.SetText("[D] " + getWorkdir())
				return
			}
			name := filepath.Base(uid)
			if branch {
				label.SetText("[D] " + name)
			} else {
				label.SetText("[F] " + name)
			}
		},
	)
	explorerTree.Root = "."
	explorerTree.OnSelected = func(uid string) {
		selectedPath.SetText(treeIDToAbsPath(getWorkdir(), uid))
	}
	explorerScroll := container.NewVScroll(explorerTree)
	explorerFoot := container.NewVBox(
		widget.NewLabelWithStyle("Current selection", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		selectedPath,
	)
	explorerInner := container.NewBorder(nil, explorerFoot, nil, nil, explorerScroll)
	explorerCard := widget.NewCard("Workspace", "Browse folders and files in your workdir", explorerInner)

	gitChanges := widget.NewTextGrid()
	gitChanges.SetText("Loading git changes…")
	gitScroll := container.NewVScroll(gitChanges)
	gitCard := widget.NewCard("Git", "Created, modified, and removed paths — refreshes after each reply", gitScroll)

	refreshWorkspaceViews := func() {
		go func() {
			current := getWorkdir()
			changes, err := collectGitChanges(ctx, current)
			text := renderGitChanges(current, changes)
			if err != nil {
				text = fmt.Sprintf("Git changes (%s)\n\nerror: %v", current, err)
			}
			fyne.Do(func() {
				explorerTree.Refresh()
				gitChanges.SetText(text)
			})
		}()
	}
	refreshWorkspaceViews()

	var runUserMessage func()
	runUserMessage = func() {
		if busy {
			return
		}
		user := strings.TrimSpace(input.Text)
		if user == "" {
			return
		}
		input.SetText("")
		appendOutput("\n### You\n" + user + "\n\n### Assistant\n")
		setBusy(true, "✦ Assistant is thinking…")
		go func(prompt string) {
			streamOut := &uiStreamWriter{writeFn: appendOutputAsync}
			sessMu.Lock()
			err := sess.RunUserTurn(ctx, prompt, streamOut)
			sessMu.Unlock()
			if err != nil {
				appendOutputAsync("**Error:** " + err.Error() + "\n")
				setBusyAsync(false, statusLine(sess.WorkDir, " | ready"))
				return
			}
			fyne.Do(flushRender)
			appendOutputAsync("\n")
			setBusyAsync(false, statusLine(sess.WorkDir, " | ready"))
			refreshWorkspaceViews()
		}(user)
	}
	send := widget.NewButton("Send", runUserMessage)
	send.Importance = widget.HighImportance

	refresh := widget.NewButton("Refresh context", func() {
		if busy {
			return
		}
		setBusy(true, "✦ Syncing skills & tools…")
		go func() {
			sessMu.Lock()
			err := sess.RefreshContext(ctx)
			sessMu.Unlock()
			if err != nil {
				appendOutputAsync("\n[refresh error] " + err.Error() + "\n")
			} else {
				appendOutputAsync("\n*Catalog refreshed — skills and tools are up to date.*\n")
			}
			setBusyAsync(false, statusLine(sess.WorkDir, " | ready"))
			refreshWorkspaceViews()
		}()
	})

	browseWorkdir := widget.NewButton("Workspace folder…", func() {
		if busy {
			return
		}
		fd := dialog.NewFolderOpen(func(list fyne.ListableURI, ferr error) {
			if ferr != nil {
				dialog.ShowError(ferr, w)
				return
			}
			if list == nil {
				return
			}
			selected := strings.TrimSpace(filepath.Clean(filepath.FromSlash(list.Path())))
			if selected == "" {
				return
			}
			absWD, err := filepath.Abs(selected)
			if err != nil {
				dialog.ShowError(err, w)
				return
			}
			st, err := os.Stat(absWD)
			if err != nil {
				dialog.ShowError(err, w)
				return
			}
			if !st.IsDir() {
				dialog.ShowInformation("Invalid Folder", "Please select a directory.", w)
				return
			}
			setBusy(true, "✦ Opening your workspace…")
			go func(newWD string) {
				sessMu.Lock()
				sess.WorkDir = newWD
				saveErr := config.SaveCLIWorkdir(newWD)
				refreshErr := sess.RefreshContext(ctx)
				sessMu.Unlock()
				appendOutputAsync(fmt.Sprintf("\n[workdir] switched to %s\n", newWD))
				if saveErr != nil {
					appendOutputAsync(fmt.Sprintf("[workdir warning] could not persist default workspace: %v\n", saveErr))
				}
				if refreshErr != nil {
					appendOutputAsync(fmt.Sprintf("[workdir warning] context refresh failed: %v\n", refreshErr))
				}
				setBusyAsync(false, statusLine(newWD, " | ready"))
				refreshWorkspaceViews()
			}(absWD)
		}, w)
		fd.SetTitleText("Select Workspace Folder")
		if start, err := storage.ListerForURI(storage.NewFileURI(sess.WorkDir)); err == nil {
			fd.SetLocation(start)
		}
		fd.Show()
	})

	clear := widget.NewButton("Clear chat", func() {
		outputMu.Lock()
		pendingRender = ""
		outputMD = ""
		renderScheduled = false
		outputMu.Unlock()
		output.ParseMarkdown("")
	})
	copyOutput := widget.NewButton("Copy chat", func() {
		outputMu.Lock()
		cur := outputMD + pendingRender
		outputMu.Unlock()
		w.Clipboard().SetContent(cur)
	})

	refreshFiles := widget.NewButton("Refresh tree", refreshWorkspaceViews)
	for _, b := range []*widget.Button{refresh, browseWorkdir, refreshFiles, copyOutput, clear} {
		b.Importance = widget.LowImportance
	}
	controls := container.NewHBox(layout.NewSpacer(), refresh, browseWorkdir, refreshFiles, copyOutput, clear, send)

	chatSplit := container.NewVSplit(outputBox, input)
	chatSplit.SetOffset(0.62)
	chatCard := widget.NewCard("Conversation", "Streaming replies, tools, and harness progress land here", chatSplit)
	chatPane := container.NewBorder(statusRow, controls, nil, nil, chatCard)

	leftPane := container.NewVSplit(explorerCard, gitCard)
	leftPane.SetOffset(0.62)
	mainPane := container.NewHSplit(leftPane, chatPane)
	mainPane.SetOffset(0.34)

	title := widget.NewLabelWithStyle("Heros Desktop", fyne.TextAlignCenter, fyne.TextStyle{Bold: true})
	subtitle := widget.NewLabelWithStyle("A small studio for your local agent — calm, focused, and yours.", fyne.TextAlignCenter, fyne.TextStyle{Italic: true})
	themeSwitcher := widget.NewSelect([]string{"Dark", "Light"}, func(choice string) {
		themeOpts.Light = (choice == "Light")
		ui.Settings().SetTheme(NewCelestialTheme(themeOpts))
		cp := config.HerosDesktopPrefs{Appearance: appearanceForThemeOpts(themeOpts.Light)}
		if !accentFromEnv && themeOpts.Accent != nil {
			cp.Accent = formatHexAccent(*themeOpts.Accent)
		}
		if strings.TrimSpace(cfgHerosPath) != "" {
			if err := config.SaveHerosDesktopToConfigFile(cfgHerosPath, cp); err != nil {
				log.Printf("heros-desktop: save heros_desktop in config: %v", err)
			}
		}
		mir := desktopFilePrefs{Appearance: strings.TrimSpace(cp.Appearance)}
		if a := strings.TrimSpace(cp.Accent); a != "" {
			mir.Accent = a
		} else {
			mir.Accent = strings.TrimSpace(mergedUI.Accent)
		}
		if err := saveDesktopFilePrefs(mir); err != nil {
			log.Printf("heros-desktop: save ~/.heros-desktop.json: %v", err)
		}
	})
	if themeOpts.Light {
		themeSwitcher.SetSelected("Light")
	} else {
		themeSwitcher.SetSelected("Dark")
	}
	appearance := widget.NewLabelWithStyle("Appearance", fyne.TextAlignCenter, fyne.TextStyle{})
	themeRow := container.NewHBox(layout.NewSpacer(), appearance, themeSwitcher, layout.NewSpacer())
	header := container.NewVBox(title, subtitle, themeRow)
	content := container.NewBorder(container.NewPadded(header), nil, nil, nil, mainPane)
	w.SetContent(content)

	sendShortcut := func(_ fyne.Shortcut) { runUserMessage() }
	w.Canvas().AddShortcut(&desktop.CustomShortcut{
		KeyName:  fyne.KeyReturn,
		Modifier: fyne.KeyModifierControl,
	}, sendShortcut)
	// macOS habit: Cmd+Return
	w.Canvas().AddShortcut(&desktop.CustomShortcut{
		KeyName:  fyne.KeyReturn,
		Modifier: fyne.KeyModifierSuper,
	}, sendShortcut)

	ui.Lifecycle().SetOnStopped(func() {
		shCtx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
		defer cancel()
		if err := srv.Shutdown(shCtx); err != nil {
			log.Printf("shutdown: %v", err)
		}
	})

	w.ShowAndRun()
}
