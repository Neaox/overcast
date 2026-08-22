//go:build dev

package ec2

// no_hardcoded_account_id_dev_test.go — regression guard for #1276.
//
// #1276 found two different hardcoded ownerId literals spread across EC2
// operations — "123456789012" in legacy RunInstances/DescribeInstances and
// typed RunInstances, "000000000000" in DescribeSecurityGroups — neither
// derived from cfg.AccountID, which every one of those sites could already
// reach. Fixing the four call sites is not enough on its own to keep this
// fixed: nothing stops a new operation from being added with its own fresh
// literal. This test parses every non-test source file in the package and
// fails if either value ever reappears as a Go string literal, so the next
// hardcoded account ID fails CI instead of waiting for another wire-byte
// golden audit to notice.
//
// AMI owner IDs (handler_images.go: "137112412989" Amazon, "099720109477"
// Canonical, "801119661308" Debian) are real, distinct AWS publisher account
// numbers unrelated to the emulator's own configured account, and are
// deliberately not among the values checked here.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

func TestNoHardcodedAccountID_dev(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not determine this file's own path via runtime.Caller")
	}
	dir := filepath.Dir(thisFile)

	// The two literals #1276 found. Every EC2 response that carries an
	// account identifier (ownerId/OwnerId/requesterId) must derive it from
	// cfg.AccountID instead.
	banned := map[string]string{
		"123456789012": "legacy/typed RunInstances' and DescribeInstances' old literal",
		"000000000000": "DescribeSecurityGroups' old literal (also config's own default account, " +
			"which is exactly why a *literal* copy of it here is the trap — use h.cfg.AccountID)",
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir %s: %v", dir, err)
	}

	fset := token.NewFileSet()
	found := 0
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		path := filepath.Join(dir, name)
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			val, err := strconv.Unquote(lit.Value)
			if err != nil {
				return true
			}
			if why, bad := banned[val]; bad {
				pos := fset.Position(lit.Pos())
				t.Errorf("%s:%d: hardcoded account ID literal %q found (%s) — use h.cfg.AccountID instead (#1276)",
					name, pos.Line, val, why)
				found++
			}
			return true
		})
	}
	if found == 0 {
		t.Logf("scanned %s: no hardcoded account ID literals", dir)
	}
}
