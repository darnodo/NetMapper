package api

import (
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// T059, FR-015: the package reaches for nothing that opens a session to a device, resolves a secret
// reference or knows a vendor.
//
// Only direct imports are checked, deliberately. internal/store's audit.go uses transport.Ref, and
// transport imports secret, so both are linked into this package through store without the api ever
// calling them; a check over every dependency would fail for that reason and prove nothing. The
// boundary is held by the code path and by the netmapper_api role, which cannot read credential_set at
// all. This test stops the api from importing them itself.
func TestNoDirectImportOfDeviceReach(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	forbidden := []string{"internal/transport", "internal/secret", "internal/collector", "internal/pack"}
	checked := 0
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), f, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		checked++
		for _, imp := range parsed.Imports {
			path, _ := strconv.Unquote(imp.Path.Value)
			for _, bad := range forbidden {
				if strings.HasPrefix(path, "github.com/darnodo/NetMapper/"+bad) {
					t.Errorf("%s imports %s", f, path)
				}
			}
		}
	}
	if checked == 0 {
		t.Fatal("no source file checked")
	}
}
