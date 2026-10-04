package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"time"

	"github.com/flexdinesh/servediff/internal/contextservice"
	"github.com/flexdinesh/servediff/internal/controlapi"
	buildversion "github.com/flexdinesh/servediff/internal/version"
)

type colors struct {
	bold    string
	cyan    string
	green   string
	yellow  string
	red     string
	magenta string
	dim     string
	reset   string
}

func terminalColors(writer io.Writer) colors {
	file, ok := writer.(*os.File)
	_, noColor := os.LookupEnv("NO_COLOR")
	if !ok || noColor || os.Getenv("TERM") == "dumb" {
		return colors{}
	}
	stat, err := file.Stat()
	if err != nil || stat.Mode()&os.ModeCharDevice == 0 {
		return colors{}
	}
	return colors{
		bold: "\x1b[1m", cyan: "\x1b[36m", green: "\x1b[32m",
		yellow: "\x1b[33m", red: "\x1b[31m", magenta: "\x1b[35m",
		dim: "\x1b[2m", reset: "\x1b[0m",
	}
}

func (color colors) paint(style, value string) string {
	if style == "" {
		return value
	}
	return style + value + color.reset
}

func displayVersion() string {
	return buildversion.Number()
}

func writeStartup(writer io.Writer, input loadedInput, urls ...string) {
	color := terminalColors(writer)
	files, additions, deletions, binaries := 0, 0, 0, 0
	for _, file := range input.snapshot.Files {
		files++
		additions += file.Additions
		deletions += file.Deletions
		if file.Binary {
			binaries++
		}
	}
	fmt.Fprintf(writer, "  %s\n", color.paint(color.bold+color.cyan, "servediff "+displayVersion()))
	if input.directory != "" {
		fmt.Fprintf(writer, "  %s  %s\n", color.paint(color.dim, "collected from:  "), input.directory)
	}
	fmt.Fprintf(writer, "  %s               %s\n", color.paint(color.dim, "mode:"), input.mode)
	for _, url := range urls {
		fmt.Fprintf(writer, "  %s                %s\n", color.paint(color.dim, "url:"), color.paint(color.green, url))
	}
	if input.captureID != "" {
		fmt.Fprintf(writer, "  %s         %s\n", color.paint(color.dim, "capture ID:"), input.captureID)
	}
	if input.contextID != "" {
		fmt.Fprintf(writer, "  context ID:         %s\n", input.contextID)
	}
	if input.mcpURL != "" {
		fmt.Fprintf(writer, "  MCP:                %s\n", input.mcpURL)
	}
	fmt.Fprintln(writer, "  ")
	elapsed := max(input.processed.Round(time.Millisecond).Milliseconds(), 1)
	fmt.Fprintf(writer, "  %s %s\n", color.paint(color.dim, "diff statistics"), color.paint(color.magenta, fmt.Sprintf("%dms", elapsed)))
	fmt.Fprintf(writer, "    %s\n", plural(files, "file changed", "files changed"))
	fmt.Fprintf(writer, "    %s\n", color.paint(color.green, plural(additions, "addition", "additions")))
	fmt.Fprintf(writer, "    %s\n", color.paint(color.red, plural(deletions, "deletion", "deletions")))
	if binaries > 0 {
		fmt.Fprintf(writer, "    %s\n", plural(binaries, "binary file", "binary files"))
	}
	fmt.Fprintln(writer, "  ")
	guidance := "ctrl-c to stop."
	if input.remote {
		guidance = "snapshot submitted to remote server."
	} else if input.submitted {
		guidance = "servediff service stop to stop the daemon."
	}
	fmt.Fprintf(writer, "  %s\n", color.paint(color.dim, guidance))
}

func writeSubmission(writer io.Writer, submission contextservice.Submission, elapsed time.Duration, url, mcpURL string, additionalURLs ...string) {
	input := submissionInput(submission, elapsed)
	input.contextID, input.mcpURL, input.submitted = submission.Context.ID, mcpURL, true
	writeStartup(writer, input, append([]string{url}, additionalURLs...)...)
}

func writeServiceStatus(writer io.Writer, status controlapi.Status, asJSON bool) {
	if asJSON {
		_ = json.NewEncoder(writer).Encode(status)
		return
	}
	fmt.Fprintf(writer, "  servediff service %s\n", status.State)
	if status.State != "running" {
		return
	}
	fmt.Fprintf(writer, "  url:                %s\n", status.URL)
	fmt.Fprintf(writer, "  listen:             %s\n", net.JoinHostPort(status.Settings.Host, strconv.Itoa(status.Settings.Port)))
	fmt.Fprintf(writer, "  version:            %s\n", status.Version)
	fmt.Fprintf(writer, "  protocol:           %d\n", status.ProtocolVersion)
	fmt.Fprintf(writer, "  PID:                %d\n", status.PID)
	fmt.Fprintf(writer, "  state:              %s\n", status.Settings.State)
	fmt.Fprintf(writer, "  contexts:           %d legacy worktrees, %d snapshots\n", status.Worktrees, status.Captures)
}

func plural(count int, singular, plural string) string {
	label := plural
	if count == 1 {
		label = singular
	}
	return fmt.Sprintf("%d %s", count, label)
}
