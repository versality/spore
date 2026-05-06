package lints

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
)

// TmuxSocketTest flags `exec.Command("tmux", ...)` calls in *_test.go
// files that do not pass a `-L <socket>` flag. Without -L, tmux uses
// the default socket path; combined with an inherited TMUX env from
// the operator's running tmux, tests attach to the host server and
// pollute the operator's session list with sessions like
// "spore/<project>/<slug>". The companion TestMain helpers under
// internal/task/, internal/fleet/, and cmd/spore/ set TMUX_TMPDIR to
// a per-process temp dir and unset TMUX; tests must thread `-L
// <socket>` through every direct tmux invocation so the lint can
// catch a regression at edit time rather than at session-leak time.
type TmuxSocketTest struct{}

func (TmuxSocketTest) Name() string { return "tmux-socket-test" }

func (TmuxSocketTest) Run(root string) ([]Issue, error) {
	files, err := listFiles(root, map[string]bool{".go": true})
	if err != nil {
		return nil, err
	}
	var issues []Issue
	for _, rel := range files {
		if !strings.HasSuffix(rel, "_test.go") {
			continue
		}
		path := filepath.Join(root, rel)
		body, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		issues = append(issues, scanTmuxSocketTest(rel, body)...)
	}
	return issues, nil
}

// scanTmuxSocketTest walks src as a Go file and reports each
// exec.Command("tmux", ...) call whose argument list does not include
// a "-L" string literal. Uses a parser AST rather than a regexp so
// commented-out examples and string-internal "tmux" mentions do not
// trip false positives.
func scanTmuxSocketTest(rel string, src []byte) []Issue {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, rel, src, parser.SkipObjectResolution)
	if err != nil {
		return nil
	}
	var issues []Issue
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		ident, ok := sel.X.(*ast.Ident)
		if !ok || ident.Name != "exec" || sel.Sel.Name != "Command" {
			return true
		}
		if len(call.Args) == 0 {
			return true
		}
		first, ok := stringLit(call.Args[0])
		if !ok || first != "tmux" {
			return true
		}
		hasL := false
		for _, arg := range call.Args[1:] {
			s, ok := stringLit(arg)
			if !ok {
				continue
			}
			if s == "-L" {
				hasL = true
				break
			}
		}
		if !hasL {
			pos := fset.Position(call.Pos())
			issues = append(issues, Issue{
				Path:    rel,
				Line:    pos.Line,
				Message: `exec.Command("tmux", ...) without "-L <socket>"; tests must isolate from the operator's tmux server`,
			})
		}
		return true
	})
	return issues
}

func stringLit(e ast.Expr) (string, bool) {
	bl, ok := e.(*ast.BasicLit)
	if !ok || bl.Kind != token.STRING {
		return "", false
	}
	if len(bl.Value) < 2 {
		return "", false
	}
	first := bl.Value[0]
	last := bl.Value[len(bl.Value)-1]
	if (first != '"' && first != '`') || first != last {
		return "", false
	}
	return bl.Value[1 : len(bl.Value)-1], true
}
