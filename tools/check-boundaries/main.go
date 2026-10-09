// Command check-boundaries validates production dependencies without imposing CI policy.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

func main() {
	command := exec.Command("go", "list", "-json", "./internal/...")
	output, err := command.StdoutPipe()
	if err != nil {
		panic(err)
	}
	command.Stderr = os.Stderr
	if err := command.Start(); err != nil {
		panic(err)
	}
	allowed := map[string][]string{
		"review": {}, "ingestion": {"review"}, "reviewdata": {"review", "ingestion"},
		"session": {"review"}, "contextservice": {"review", "reviewdata", "session", "ingestion"},
		"reviewservice":  {"review", "reviewdata", "session", "contextservice", "ingestion"},
		"ingestionqueue": {"review", "ingestion"},
		"reviewstore":    {"review", "reviewdata", "ingestion", "ingestionqueue", "processlock"},
		"submission":     {"ingestion", "processlock"},
		"httpapi":        {"review", "reviewdata", "session", "contextservice", "reviewservice", "ingestion", "processmetrics"},
		"mcpapi":         {"review", "contextservice", "reviewservice", "ingestion"},
	}
	const prefix = "github.com/flexdinesh/servediff/internal/"
	decoder := json.NewDecoder(output)
	failed := false
	for {
		var pkg struct {
			ImportPath string
			Imports    []string
		}
		err := decoder.Decode(&pkg)
		if err == io.EOF {
			break
		}
		if err != nil {
			panic(err)
		}
		name := strings.TrimPrefix(pkg.ImportPath, prefix)
		permitted, checked := allowed[name]
		if !checked {
			continue
		}
		for _, dependency := range pkg.Imports {
			if dependency == "os/exec" {
				fmt.Fprintf(os.Stderr, "%s must not execute producers\n", name)
				failed = true
			}
			if !strings.HasPrefix(dependency, prefix) {
				continue
			}
			dependency = strings.TrimPrefix(dependency, prefix)
			ok := false
			for _, candidate := range permitted {
				ok = ok || dependency == candidate
			}
			if !ok {
				fmt.Fprintf(os.Stderr, "forbidden dependency: %s -> %s\n", name, dependency)
				failed = true
			}
		}
	}
	if err := command.Wait(); err != nil {
		panic(err)
	}
	if failed {
		os.Exit(1)
	}
	fmt.Println("Architecture boundaries passed")
}
