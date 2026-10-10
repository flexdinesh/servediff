package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestPackageBoundaries(t *testing.T) {
	tests := []struct {
		name    string
		pkg     string
		imports []string
		want    string
	}{
		{name: "unknown module cannot silently pass", pkg: "internal/newservice", want: "unclassified package: internal/newservice"},
		{name: "new command needs a role", pkg: "cmd/other", want: "unclassified package: cmd/other"},
		{name: "nested service needs its own role", pkg: "internal/reviewservice/adapter", want: "unclassified package: internal/reviewservice/adapter"},
		{name: "domain cannot import transport", pkg: "internal/reviewservice", imports: []string{modulePrefix + "internal/httpapi"}, want: "forbidden dependency: internal/reviewservice -> internal/httpapi"},
		{name: "domain cannot import concrete storage", pkg: "internal/contextservice", imports: []string{modulePrefix + "internal/reviewstore"}, want: "forbidden dependency: internal/contextservice -> internal/reviewstore"},
		{name: "submission cannot import hook scheduling", pkg: "internal/submission", imports: []string{modulePrefix + "internal/hooks"}, want: "forbidden dependency: internal/submission -> internal/hooks"},
		{name: "nested import cannot inherit permission", pkg: "internal/reviewservice", imports: []string{modulePrefix + "internal/review/helpers"}, want: "forbidden dependency: internal/reviewservice -> internal/review/helpers"},
		{name: "domain cannot import generated transport contract", pkg: "internal/review", imports: []string{modulePrefix + "packages/api"}, want: "forbidden dependency: internal/review -> packages/api"},
		{name: "local root composes storage and producers", pkg: "cmd/diffx", imports: []string{modulePrefix + "internal/reviewstore", modulePrefix + "internal/collector", modulePrefix + "internal/daemon"}},
		{name: "remote root composes storage and server", pkg: "cmd/diffx-server", imports: []string{modulePrefix + "internal/reviewstore", modulePrefix + "internal/remoteserver"}},
		{name: "remote composition wires worker", pkg: "internal/remoteserver", imports: []string{modulePrefix + "internal/ingestionqueue", modulePrefix + "internal/serverapp", modulePrefix + "internal/reviewstore"}},
		{name: "service uses shared contracts", pkg: "internal/reviewservice", imports: []string{modulePrefix + "internal/review", modulePrefix + "internal/reviewdata", modulePrefix + "internal/contextservice"}},
		{name: "transport can project generated contract", pkg: "internal/httpapi", imports: []string{modulePrefix + "packages/api", modulePrefix + "internal/session"}},
		{name: "standard and external libraries unaffected", pkg: "internal/review", imports: []string{"context", "encoding/json", "example.com/library"}},
		{name: "shared reads cannot execute producers", pkg: "internal/reviewservice", imports: []string{"os/exec"}, want: "internal/reviewservice must not execute processes"},
		{name: "command delegates process execution", pkg: "cmd/diffx", imports: []string{"os/exec"}, want: "cmd/diffx must not execute processes"},
		{name: "storage cannot execute producers", pkg: "internal/reviewstore", imports: []string{"os/exec"}, want: "internal/reviewstore must not execute processes"},
		{name: "git adapter executes git", pkg: "internal/diffsource", imports: []string{"os/exec"}},
		{name: "hook scheduler executes finite producer", pkg: "internal/hooks", imports: []string{"os/exec"}},
		{name: "lifecycle inspects processes", pkg: "internal/daemon", imports: []string{"os/exec"}},
		{name: "browser launches platform opener", pkg: "internal/browser", imports: []string{"os/exec"}},
		{name: "service cannot bypass atomic storage contract", pkg: "internal/contextservice", imports: []string{"database/sql"}, want: "internal/contextservice must not access SQL storage directly"},
		{name: "composition cannot query sqlite directly", pkg: "internal/remoteserver", imports: []string{"modernc.org/sqlite"}, want: "internal/remoteserver must not access SQL storage directly"},
		{name: "sqlite subpackages preserve storage boundary", pkg: "internal/review", imports: []string{"modernc.org/sqlite/lib"}, want: "internal/review must not access SQL storage directly"},
		{name: "storage owns sql and sqlite", pkg: "internal/reviewstore", imports: []string{"database/sql", "modernc.org/sqlite"}},
		{name: "production cannot use test helpers", pkg: "internal/contextservice", imports: []string{modulePrefix + "internal/testsupport"}, want: "production dependency on test helpers: internal/contextservice -> internal/testsupport"},
		{name: "command cannot use nested test helpers", pkg: "cmd/diffx", imports: []string{modulePrefix + "internal/testsupport/fixtures"}, want: "production dependency on test helpers: cmd/diffx -> internal/testsupport/fixtures"},
		{name: "test helper can compose real boundary", pkg: "internal/testsupport", imports: []string{modulePrefix + "internal/reviewstore", modulePrefix + "internal/serverapp", "testing", "net/http/httptest"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := checkPackage(listedPackage{ImportPath: modulePrefix + test.pkg, Imports: test.imports})
			if test.want == "" {
				if len(got) != 0 {
					t.Fatalf("permitted dependencies rejected: %v", got)
				}
			} else if len(got) != 1 || !strings.Contains(got[0], test.want) {
				t.Fatalf("violations = %v; want one containing %q", got, test.want)
			}
		})
	}
}

func TestCheckPackagesReportsAllViolations(t *testing.T) {
	var input, diagnostics bytes.Buffer
	encoder := json.NewEncoder(&input)
	for _, pkg := range []listedPackage{
		{ImportPath: modulePrefix + "internal/review"},
		{ImportPath: modulePrefix + "internal/newservice"},
		{ImportPath: modulePrefix + "internal/reviewservice", Imports: []string{"os/exec", modulePrefix + "internal/reviewstore"}},
	} {
		if err := encoder.Encode(pkg); err != nil {
			t.Fatal(err)
		}
	}
	passed, err := checkPackages(&input, &diagnostics)
	if err != nil || passed {
		t.Fatalf("passed = %v, error = %v; want rejected stream without decoding error", passed, err)
	}
	for _, want := range []string{
		"unclassified package: internal/newservice",
		"internal/reviewservice must not execute processes",
		"forbidden dependency: internal/reviewservice -> internal/reviewstore",
	} {
		if !strings.Contains(diagnostics.String(), want) {
			t.Errorf("diagnostics missing %q: %s", want, &diagnostics)
		}
	}
}

func TestCheckPackagesRejectsMalformedInventory(t *testing.T) {
	passed, err := checkPackages(strings.NewReader("{"), &bytes.Buffer{})
	if passed || err == nil {
		t.Fatalf("passed = %v, error = %v; want decoding failure", passed, err)
	}
}
