package discovery

import (
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Loader walks a repository as untrusted text and yields one Package at a time so memory is bounded by
// a single package's AST, not the whole repo (§3.1 / NFR3). It NEVER executes or `go list`s the repo.
type Loader struct {
	Root       string
	ModulePath string // from the target's go.mod `module` line ("" if absent)
	// SkipTests excludes *_test.go from discovery (default true — test files are not production nodes).
	SkipTests bool
}

// NewLoader reads the target module path from go.mod (parsed as text, never executed).
func NewLoader(root string) (*Loader, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	return &Loader{Root: abs, ModulePath: readModulePath(filepath.Join(abs, "go.mod")), SkipTests: true}, nil
}

func readModulePath(goModPath string) string {
	b, err := os.ReadFile(goModPath)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "module ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "module "))
		}
	}
	return ""
}

// skipDir reports directories excluded from the walk. Symlinks are handled separately (never followed).
func skipDir(name string) bool {
	switch name {
	case "vendor", "testdata", "node_modules", ".git":
		return true
	}
	// Hidden and tool dirs (".foo", "_foo") — Go itself ignores these for package resolution.
	return len(name) > 1 && (name[0] == '.' || name[0] == '_')
}

// ForEachPackage streams packages to fn in a deterministic (sorted-by-dir) order, one at a time. It
// returns loader-level diagnostics (parse errors, skipped symlinks). fn returning an error stops the walk.
func (l *Loader) ForEachPackage(fn func(*Package) error) ([]Diagnostic, error) {
	var diags []Diagnostic
	filesByDir := map[string][]string{}
	var dirOrder []string

	walkErr := filepath.WalkDir(l.Root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			diags = append(diags, Diagnostic{Code: CodeWalkError, Severity: SeverityWarn, File: l.rel(path), Message: err.Error()})
			return nil // skip-and-report; keep walking (I7)
		}
		if d.IsDir() {
			if path != l.Root && skipDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		// Never follow symlinks — this is the symlink-cycle safety (I7, doc 08 F5). WalkDir does not
		// descend into symlinked dirs; here we also refuse symlinked files.
		if d.Type()&fs.ModeSymlink != 0 {
			diags = append(diags, Diagnostic{Code: CodeSymlinkCycleSkipped, Severity: SeverityWarn, File: l.rel(path), Message: "symlink skipped (not followed)"})
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		if l.SkipTests && strings.HasSuffix(path, "_test.go") {
			return nil
		}
		dir := filepath.Dir(path)
		if _, ok := filesByDir[dir]; !ok {
			dirOrder = append(dirOrder, dir)
		}
		filesByDir[dir] = append(filesByDir[dir], path)
		return nil
	})
	if walkErr != nil {
		return diags, walkErr
	}

	sort.Strings(dirOrder)
	for _, dir := range dirOrder {
		files := filesByDir[dir]
		sort.Strings(files)
		pkg, pdiags := l.parsePackage(dir, files)
		diags = append(diags, pdiags...)
		if pkg != nil && len(pkg.Files) > 0 {
			if err := fn(pkg); err != nil {
				return diags, err
			}
		}
	}
	return diags, nil
}

func (l *Loader) rel(p string) string {
	r, err := filepath.Rel(l.Root, p)
	if err != nil {
		return p
	}
	return r
}

// pkgPathFor derives the module-relative package path for a directory (module + "/" + relDir).
func (l *Loader) pkgPathFor(dir string) string {
	rel := l.rel(dir)
	if rel == "." {
		if l.ModulePath != "" {
			return l.ModulePath
		}
		return "."
	}
	rel = filepath.ToSlash(rel)
	if l.ModulePath != "" {
		return l.ModulePath + "/" + rel
	}
	return rel
}

// parsePackage parses every file in one directory (AST + text only). A parse error on one file is a
// per-file diagnostic; other files still parse (skip-and-report, I7 / doc 08 F1).
func (l *Loader) parsePackage(dir string, files []string) (*Package, []Diagnostic) {
	var diags []Diagnostic
	fset := token.NewFileSet()
	pkgPath := l.pkgPathFor(dir)
	pkg := &Package{Dir: l.rel(dir), PkgPath: pkgPath}

	for _, f := range files {
		src, err := os.ReadFile(f)
		if err != nil {
			diags = append(diags, Diagnostic{Code: CodeWalkError, Severity: SeverityWarn, File: l.rel(f), Message: err.Error()})
			continue
		}
		astFile, err := parser.ParseFile(fset, f, src, parser.SkipObjectResolution)
		if err != nil {
			diags = append(diags, Diagnostic{Code: CodeParseError, Severity: SeverityError, File: l.rel(f), Message: err.Error()})
			continue // other files in this package still discovered
		}
		pf := &ParsedFile{
			RelPath:     l.rel(f),
			PkgPath:     pkgPath,
			Fset:        fset,
			AST:         astFile,
			Imports:     map[string]string{},
			importPaths: map[string]bool{},
		}
		buildImportMap(pf)
		pkg.Files = append(pkg.Files, pf)
	}
	return pkg, diags
}

// buildImportMap fills a file's local-name -> import-path map. Within one file Go forbids two imports
// with the same local name without an alias, so this map is unambiguous WITHOUT type-checking (doc 04).
// For a non-aliased import the local name is the last path segment (a version suffix like "/v2" or
// ".v3" is stripped). This is correct for in-house wrappers and langchaingo's `llms`; SDKs whose package
// name differs from the path tail (e.g. anthropic-sdk-go) are matched by import-presence, not local name.
func buildImportMap(pf *ParsedFile) {
	for _, imp := range pf.AST.Imports {
		path, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			continue
		}
		pf.importPaths[path] = true
		var local string
		switch {
		case imp.Name != nil && imp.Name.Name == "_":
			continue // blank import binds no name
		case imp.Name != nil && imp.Name.Name == ".":
			continue // dot import — not resolvable to a local name; import-presence still recorded
		case imp.Name != nil:
			local = imp.Name.Name
		default:
			local = lastPathSegment(path)
		}
		if local != "" {
			pf.Imports[local] = path
		}
	}
}

func lastPathSegment(path string) string {
	seg := path
	if i := strings.LastIndex(seg, "/"); i >= 0 {
		seg = seg[i+1:]
	}
	// Strip a major-version suffix: "yaml.v3" -> "yaml".
	if i := strings.LastIndex(seg, "."); i >= 0 {
		if v := seg[i+1:]; len(v) >= 2 && v[0] == 'v' && isDigits(v[1:]) {
			seg = seg[:i]
		}
	}
	return seg
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
