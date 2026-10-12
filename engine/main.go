// Command cel-lens-core is cel-lens core, a command-line
// program of cel-lens.
//
// Usage:
//
//	cel-lens-core [flags] [command]
//
// The commands are:
//
//	lsp [--stdio] [--clientProcessId=<pid>]
//		Serve CEL language support (diagnostics, hover, completion) for
//		Kubernetes YAML files as a Language Server Protocol server on
//		stdin and stdout. Diagnostics from the server go to stderr. The
//		flags are those VS Code's language client passes; stdio is the
//		only transport.
//
//	check [--format text|json] [--kube-version <1.x>] <paths...>
//		Check the CEL in Kubernetes YAML files and folders (folders are
//		searched for .yaml and .yml files) and print each problem, as the
//		API server of the chosen Kubernetes version would.
//
// The flags are:
//
//	--version
//		Print the version, the Kubernetes version results match by
//		default and the range supported, and exit.
//	-h, --help
//		Print the help and exit.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/JairJosafath/cel-lens/engine/internal/lsp"
	"github.com/JairJosafath/cel-lens/engine/internal/procwatch"
	"github.com/JairJosafath/cel-lens/engine/internal/scan"
	"github.com/JairJosafath/cel-lens/engine/internal/stdio"
	"github.com/JairJosafath/cel-lens/engine/pkg/check"
	"github.com/JairJosafath/cel-lens/engine/pkg/kubeversion"
)

// name is the program's name in messages and help, the same as its binary's.
const name = "cel-lens-core"

// version is the release this binary was built from. make build and make dist
// set it with -ldflags "-X main.version=..."; a plain go build or go install
// leaves it empty and buildVersion reads the build info instead.
var version string

// buildVersion returns stamped when set, else the main module version that
// read reports (the tag go install fetched), else "(devel)".
func buildVersion(stamped string, read func() (*debug.BuildInfo, bool)) string {
	if stamped != "" {
		return stamped
	}
	if info, ok := read(); ok && info.Main.Version != "" {
		return info.Main.Version
	}
	return "(devel)"
}

// Exit codes, as shells and CI read them.
const (
	exitOK    = 0 // success, including --help and --version
	exitFail  = 1 // the command ran and failed
	exitUsage = 2 // the command line was wrong

	exitInterrupted = 130 // the run was cancelled, as by Ctrl-C: 128 + SIGINT
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	code := run(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}

// run is the whole program behind main: it parses args, reads stdin, writes
// results to stdout and diagnostics to stderr, and returns the exit code. It
// never exits or touches global state, so tests call it directly. Cancelling
// ctx stops a running command.
func run(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet(name, flag.ContinueOnError)
	flags.SetOutput(io.Discard) // run reports parse errors itself
	showVersion := flags.Bool("version", false, "print the version and exit")

	err := flags.Parse(args)
	switch {
	case errors.Is(err, flag.ErrHelp):
		usage(stdout, flags)
		return exitOK
	case err != nil:
		fmt.Fprintf(stderr, "%s: %v\nRun '%s --help' for usage.\n", name, err, name)
		return exitUsage
	case flags.Arg(0) == "check" && !*showVersion:
		return runCheck(ctx, flags.Args()[1:], stdout, stderr)
	case flags.Arg(0) == "lsp" && !*showVersion:
		return runLSP(ctx, flags.Args()[1:], stdin, stdout, stderr, lspEnv{alive: procwatch.Alive, interval: time.Second})
	case flags.NArg() > 0:
		fmt.Fprintf(stderr, "%s: unexpected argument %q\nRun '%s --help' for usage.\n", name, flags.Arg(flags.NArg()-1), name)
		return exitUsage
	case *showVersion:
		fmt.Fprintf(stdout, "%s %s (%s)\n", name, buildVersion(version, debug.ReadBuildInfo), kubeversion.Summary())
		return exitOK
	}
	usage(stdout, flags)
	return exitOK
}

const checkHelp = `Usage: %s check [--format text|json] [--kube-version <1.x>] <paths...>

Check the CEL in Kubernetes YAML files and folders, the way the API server
does. By default the results match Kubernetes %s; --kube-version picks the
version your cluster runs (supported: %s). Folders are searched for .yaml and .yml files;
the CustomResourceDefinitions among them type the params of policies. Each problem
is printed as file:line:col: severity: message. Exit 0: no errors; 1: errors
found; 2: usage or read error; 130: interrupted.

Flags may come before, between or after the paths.

Example:
  %[1]s check deploy/ crd.yaml

Flags:
`

// runCheck runs the check command on its arguments, after the word check.
func runCheck(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet(name+" check", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	format := flags.String("format", string(scan.Text), "output format: text or json")
	kube := flags.String("kube-version", kubeversion.Default().String(), "Kubernetes version to match: "+kubeversion.Range())
	paths, err := parseInterspersed(flags, args)
	switch {
	case errors.Is(err, flag.ErrHelp):
		fmt.Fprintf(stdout, checkHelp, name, kubeversion.Default(), kubeversion.Range())
		flags.SetOutput(stdout)
		flags.PrintDefaults()
		return exitOK
	case err != nil:
		fmt.Fprintf(stderr, "%s check: %v\nRun '%s check --help' for usage.\n", name, err, name)
		return exitUsage
	case len(paths) == 0:
		fmt.Fprintf(stderr, "%s check: no paths given\nRun '%s check --help' for usage.\n", name, name)
		return exitUsage
	}
	kv, err := kubeversion.Parse(*kube)
	if err != nil {
		fmt.Fprintf(stderr, "%s check: --kube-version: %v\nRun '%s check --help' for usage.\n", name, err, name)
		return exitUsage
	}
	found, err := scan.Run(ctx, paths, scan.Format(*format), stdout, check.WithVersion(kv))
	switch {
	case errors.Is(err, scan.ErrFormat):
		fmt.Fprintf(stderr, "%s check: %v\nRun '%s check --help' for usage.\n", name, err, name)
		return exitUsage
	case errors.Is(err, context.Canceled):
		fmt.Fprintf(stderr, "%s check: interrupted\n", name)
		return exitInterrupted
	case err != nil:
		for _, line := range errorLines(err) {
			fmt.Fprintf(stderr, "%s check: %s\n", name, line)
		}
		return exitUsage
	case found:
		return exitFail
	}
	return exitOK
}

// parseInterspersed parses flags that may come before, between and after the
// paths, which flag.FlagSet.Parse alone does not allow. It returns the paths in
// order. After a "--" every argument is a path.
func parseInterspersed(flags *flag.FlagSet, args []string) ([]string, error) {
	var paths []string
	for {
		if err := flags.Parse(args); err != nil {
			return nil, err
		}
		rest := flags.Args()
		consumed := args[:len(args)-len(rest)]
		if len(consumed) > 0 && consumed[len(consumed)-1] == "--" {
			return append(paths, rest...), nil
		}
		if len(rest) == 0 {
			return paths, nil
		}
		paths = append(paths, rest[0])
		args = rest[1:]
	}
}

// errorLines splits err, which may join several errors, into one message per
// error, leaving out broken YAML, which is already in the results, so each line of output can carry the program prefix.
func errorLines(err error) []string {
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		var lines []string
		for _, e := range joined.Unwrap() {
			lines = append(lines, errorLines(e)...)
		}
		return lines
	}
	if errors.Is(err, scan.ErrSyntax) {
		return nil
	}
	return []string{err.Error()}
}

// usage writes the help text to w.
func usage(w io.Writer, flags *flag.FlagSet) {
	fmt.Fprintf(w, "Usage: %s [flags] [command]\n\nCommands:\n  lsp\tserve CEL language support as a language server on stdin and stdout\n  check\tcheck the CEL in YAML files and folders\n\nFlags:\n", name)
	flags.SetOutput(w)
	flags.PrintDefaults()
}

const lspHelp = `Usage: %s lsp [--stdio] [--clientProcessId=<pid>]

Serve CEL language support as a language server on stdin and stdout. These are
the flags a language client passes when it launches the server.

Flags:
  --stdio
        talk over stdin and stdout (the default and only transport)
  --clientProcessId=<pid>
        exit when the process with this ID, the editor, is gone; a missing or
        invalid value is ignored and reported
  --socket, --pipe, --node-ipc
        not supported: the server fails with exit code 2
`

// lspEnv is what the lsp command takes from its surroundings.
type lspEnv struct {
	alive    func(pid int) bool // reports whether a process exists
	interval time.Duration      // how often to ask alive
	serve    serveFunc          // runs the server; nil runs lsp.Server
}

// serveFunc serves the language protocol on in and out until it ends or ctx is
// cancelled.
type serveFunc func(ctx context.Context, in io.Reader, out io.Writer) error

// lspArgs is the parsed command line of lsp.
type lspArgs struct {
	help        bool
	clientPID   int      // 0 when absent or invalid
	pidProblems []string // why each --clientProcessId was invalid, in order
	unsupported string   // the first transport flag other than stdio
	bad         string   // the first argument lsp does not know
}

// parseLSP reads the arguments after the word lsp. It accepts the flags with
// one or two dashes, and a value after = or as the next argument.
func parseLSP(args []string) lspArgs {
	var a lspArgs
	for i := 0; i < len(args); i++ {
		arg := args[i]
		flagName, value, hasValue := strings.Cut(strings.TrimLeft(arg, "-"), "=")
		if !strings.HasPrefix(arg, "-") {
			flagName = ""
		}
		switch flagName {
		case "stdio":
			if hasValue && a.bad == "" {
				a.bad = arg // --stdio takes no value
			}
		case "h", "help":
			a.help = true
		case "socket", "pipe", "node-ipc":
			if a.unsupported == "" {
				a.unsupported = "--" + flagName
			}
			if flagName != "node-ipc" && !hasValue && i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				i++ // the port or name that goes with it
			}
		case "clientProcessId":
			if !hasValue && i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				i++
				value, hasValue = args[i], true
			}
			pid, err := strconv.Atoi(value)
			switch {
			case !hasValue:
				a.pidProblems = append(a.pidProblems, "no value")
			case err != nil || pid <= 0:
				a.pidProblems = append(a.pidProblems, fmt.Sprintf("invalid value %q", value))
			default:
				a.clientPID = pid
			}
		default:
			if a.bad == "" {
				a.bad = arg
			}
		}
	}
	return a
}

// runLSP runs the lsp command on its arguments, after the word lsp.
func runLSP(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer, env lspEnv) int {
	a := parseLSP(args)
	switch {
	case a.help:
		fmt.Fprintf(stdout, lspHelp, name)
		return exitOK
	case a.bad != "":
		fmt.Fprintf(stderr, "%s lsp: unexpected argument %q\nRun '%s lsp --help' for usage.\n", name, a.bad, name)
		return exitUsage
	case a.unsupported != "":
		fmt.Fprintf(stderr, "%s lsp: %s is not supported: only stdio is supported; use --stdio or no flag\n", name, a.unsupported)
		return exitUsage
	}
	for _, p := range a.pidProblems {
		fmt.Fprintf(stderr, "%s lsp: ignoring --clientProcessId: %s\n", name, p)
	}

	serve := env.serve
	if serve == nil {
		serve = (&lsp.Server{Version: buildVersion(version, debug.ReadBuildInfo)}).Serve
	}
	if f, ok := stdin.(*os.File); ok {
		var restore func()
		stdin, restore = stdio.Pollable(f)
		defer restore() // runs after Serve has returned: the parent's stdin is as it was
	}

	ctx, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	in := stdin
	if c, ok := stdio.Cancellable(ctx, stdin); ok {
		in = c
		defer c.Close() // after Serve has returned, and before the stdin is restored
	}
	defer func() {
		cancel()
		wg.Wait()
	}()
	done := make(chan error, 1)
	wg.Go(func() { done <- serve(ctx, in, stdout) })

	var gone chan bool // nil blocks forever when there is no client process to watch
	if a.clientPID > 0 {
		gone = make(chan bool, 1)
		wg.Go(func() {
			gone <- procwatch.Wait(ctx, env.interval, func() bool { return env.alive(a.clientPID) })
		})
	}
	select {
	case err := <-done:
		if err != nil {
			fmt.Fprintf(stderr, "%s: lsp: %v\n", name, err)
			return exitFail
		}
		return exitOK
	case <-gone:
		fmt.Fprintf(stderr, "%s: lsp: client process %d is gone, exiting\n", name, a.clientPID)
		return exitOK // the deferred cancel releases Serve and wg.Wait waits for it
	}
}
