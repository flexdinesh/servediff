package main

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"

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

func writeStartup(writer io.Writer, input loadedInput, url string) {
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
	fmt.Fprintf(writer, "  %s  %s\n", color.paint(color.dim, "serving directory:"), input.directory)
	fmt.Fprintf(writer, "  %s               %s\n", color.paint(color.dim, "mode:"), input.mode)
	fmt.Fprintf(writer, "  %s                %s\n", color.paint(color.dim, "url:"), color.paint(color.green, url))
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
	fmt.Fprintf(writer, "  %s\n", color.paint(color.dim, "ctrl-c to stop."))
}

func writePortBusy(writer io.Writer, port, next int) {
	color := terminalColors(writer)
	message := fmt.Sprintf("port %d is busy; trying %d", port, next)
	fmt.Fprintf(writer, "  %s\n", color.paint(color.yellow, message))
}

func plural(count int, singular, plural string) string {
	label := plural
	if count == 1 {
		label = singular
	}
	return fmt.Sprintf("%d %s", count, label)
}

func isYes(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "y", "yes":
		return true
	default:
		return false
	}
}
