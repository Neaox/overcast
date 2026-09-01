package main

// cmd_logs_test.go — tests for `overcast logs`. The native path is exercised
// against real temp files (tailFile/followLogFile do real file I/O by
// design); the docker path only ever asserts on dockerLogsArgs's argv —
// docker_backend_test.go covers streamDockerLogs's sibling functions — since
// this repo's convention is that no test here may require a real docker
// daemon (see docker_backend.go's file comment).

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
)

// logsTestRoot builds a minimal root command carrying the same persistent
// --endpoint flag main.go registers, with newLogsCmd() attached — mirrors
// startTestRoot/stopTestRoot in the sibling test files.
func logsTestRoot() (*cobra.Command, *bytes.Buffer) {
	root := &cobra.Command{Use: "overcast", SilenceUsage: true, SilenceErrors: true}
	root.PersistentFlags().String("endpoint", "http://localhost:4566", "overcast daemon base URL")
	root.AddCommand(newLogsCmd())
	buf := &bytes.Buffer{}
	root.SetOut(buf)
	root.SetErr(buf)
	return root, buf
}

func TestLogsCommandShape(t *testing.T) {
	cmd := newLogsCmd()
	if cmd.Args == nil {
		t.Fatal("newLogsCmd: Args is nil")
	}
	if err := cmd.Args(cmd, []string{"a", "b"}); err == nil {
		t.Error("newLogsCmd: Args accepted two positional arguments, want at most one")
	}
	if err := cmd.Args(cmd, []string{"only-one"}); err != nil {
		t.Errorf("newLogsCmd: Args rejected a single positional argument: %v", err)
	}
	if cmd.ValidArgsFunction == nil {
		t.Fatal("newLogsCmd: ValidArgsFunction is nil")
	}
	if f := cmd.Flags().Lookup("follow"); f == nil || f.Shorthand != "f" {
		t.Error("newLogsCmd: missing -f/--follow")
	}
	if f := cmd.Flags().Lookup("tail"); f == nil || f.Shorthand != "n" || f.DefValue != "100" {
		t.Errorf("newLogsCmd: -n/--tail wrong or missing default, got %+v", f)
	}
}

func TestLogsCmd_UnknownInstanceListsKnownNames(t *testing.T) {
	withTestInstancesDir(t)
	if err := saveInstance(instanceRecord{Name: "alpha", Backend: "native", PID: os.Getpid()}); err != nil {
		t.Fatalf("saveInstance: %v", err)
	}
	if err := saveInstance(instanceRecord{Name: "beta", Backend: "native", PID: os.Getpid()}); err != nil {
		t.Fatalf("saveInstance: %v", err)
	}

	root, _ := logsTestRoot()
	root.SetArgs([]string{"logs", "nope"})
	err := root.Execute()
	if err == nil {
		t.Fatal("logs of an unknown instance succeeded, want an error")
	}
	if !strings.Contains(err.Error(), "alpha") || !strings.Contains(err.Error(), "beta") {
		t.Errorf("error %q does not list both known instance names", err)
	}
}

func TestLogsCmd_UnknownInstanceNoInstancesRegistered(t *testing.T) {
	withTestInstancesDir(t)
	root, _ := logsTestRoot()
	root.SetArgs([]string{"logs", "nope"})
	err := root.Execute()
	if err == nil {
		t.Fatal("logs of an unknown instance succeeded, want an error")
	}
	if !strings.Contains(err.Error(), "overcast start") {
		t.Errorf("error %q does not point at `overcast start`", err)
	}
}

func TestLogsCmd_DefaultsNameToDefault(t *testing.T) {
	withTestInstancesDir(t)
	dir := t.TempDir()
	logPath := filepath.Join(dir, "daemon.log")
	if err := os.WriteFile(logPath, []byte("line one\nline two\n"), 0644); err != nil {
		t.Fatalf("write log file: %v", err)
	}
	if err := saveInstance(instanceRecord{Name: "default", Backend: "native", LogFile: logPath}); err != nil {
		t.Fatalf("saveInstance: %v", err)
	}

	root, buf := logsTestRoot()
	root.SetArgs([]string{"logs"}) // no positional name given
	if err := root.Execute(); err != nil {
		t.Fatalf("logs: %v", err)
	}
	if !strings.Contains(buf.String(), "line one") {
		t.Errorf("output %q does not contain the log content", buf.String())
	}
}

func TestLogsCmd_NativeNoLogFileRecorded(t *testing.T) {
	withTestInstancesDir(t)
	if err := saveInstance(instanceRecord{Name: "nolog", Backend: "native"}); err != nil {
		t.Fatalf("saveInstance: %v", err)
	}
	root, _ := logsTestRoot()
	root.SetArgs([]string{"logs", "nolog"})
	if err := root.Execute(); err == nil {
		t.Fatal("logs of an instance with no recorded log file succeeded, want an error")
	}
}

func TestTailFile_ReturnsLastNLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "log")
	content := "one\ntwo\nthree\nfour\nfive\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	lines, size, err := tailFile(path, 2)
	if err != nil {
		t.Fatalf("tailFile: %v", err)
	}
	want := []string{"four", "five"}
	if len(lines) != len(want) || lines[0] != want[0] || lines[1] != want[1] {
		t.Errorf("tailFile(n=2) = %v, want %v", lines, want)
	}
	if size != int64(len(content)) {
		t.Errorf("size = %d, want %d", size, len(content))
	}
}

func TestTailFile_NFewerLinesThanFileReturnsWholeFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "log")
	if err := os.WriteFile(path, []byte("only\ntwo\n"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	lines, _, err := tailFile(path, 100)
	if err != nil {
		t.Fatalf("tailFile: %v", err)
	}
	if len(lines) != 2 {
		t.Errorf("tailFile(n=100) on a 2-line file = %v, want both lines", lines)
	}
}

func TestTailFile_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "log")
	if err := os.WriteFile(path, nil, 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	lines, size, err := tailFile(path, 10)
	if err != nil {
		t.Fatalf("tailFile: %v", err)
	}
	if len(lines) != 0 || size != 0 {
		t.Errorf("tailFile(empty) = (%v, %d), want (nil, 0)", lines, size)
	}
}

func TestTailFile_MissingFile(t *testing.T) {
	if _, _, err := tailFile(filepath.Join(t.TempDir(), "nope"), 10); err == nil {
		t.Fatal("tailFile on a missing file succeeded, want an error")
	}
}

// TestFollowLogFile_StreamsAppendedBytes writes to a file after
// followLogFile has started watching it and confirms the appended content
// arrives, then cancels the context and confirms followLogFile returns.
func TestFollowLogFile_StreamsAppendedBytes(t *testing.T) {
	prev := followPollInterval
	followPollInterval = 20 * time.Millisecond
	t.Cleanup(func() { followPollInterval = prev })

	dir := t.TempDir()
	path := filepath.Join(dir, "log")
	if err := os.WriteFile(path, []byte("initial\n"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	var out bytes.Buffer
	done := make(chan error, 1)
	go func() { done <- followLogFile(ctx, &out, path, int64(len("initial\n"))) }()

	// Give the poll loop a moment to start, then append.
	time.Sleep(50 * time.Millisecond)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		t.Fatalf("open for append: %v", err)
	}
	if _, err := f.WriteString("appended line\n"); err != nil {
		t.Fatalf("append: %v", err)
	}
	f.Close()

	deadline := time.After(2 * time.Second)
	for {
		if strings.Contains(out.String(), "appended line") {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("followLogFile did not pick up appended content within the deadline; got %q", out.String())
		case <-time.After(10 * time.Millisecond):
		}
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("followLogFile returned %v after cancellation, want nil", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("followLogFile did not return after its context was cancelled")
	}
}

// TestFollowLogFile_TruncationResetsOffset covers log rotation: a file that
// is now shorter than the offset followLogFile was tracking must be read
// from its new start, not treated as an error or as "nothing new".
func TestFollowLogFile_TruncationResetsOffset(t *testing.T) {
	prev := followPollInterval
	followPollInterval = 20 * time.Millisecond
	t.Cleanup(func() { followPollInterval = prev })

	dir := t.TempDir()
	path := filepath.Join(dir, "log")
	longContent := "this was a long line that will be truncated away\n"
	if err := os.WriteFile(path, []byte(longContent), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var out bytes.Buffer
	go func() { _ = followLogFile(ctx, &out, path, int64(len(longContent))) }()

	time.Sleep(50 * time.Millisecond)
	// Simulate rotation: replace with a short, fresh file at the same path.
	if err := os.WriteFile(path, []byte("fresh\n"), 0644); err != nil {
		t.Fatalf("rewrite (simulated rotation): %v", err)
	}

	deadline := time.After(2 * time.Second)
	for {
		if strings.Contains(out.String(), "fresh") {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("followLogFile did not recover after truncation; got %q", out.String())
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func TestDockerLogsArgs_UsedByStreamDockerLogs_MissingContainerID(t *testing.T) {
	root, _ := logsTestRoot()
	withTestInstancesDir(t)
	if err := saveInstance(instanceRecord{Name: "nocid", Backend: "docker"}); err != nil {
		t.Fatalf("saveInstance: %v", err)
	}
	root.SetArgs([]string{"logs", "nocid"})
	if err := root.Execute(); err == nil {
		t.Fatal("logs of a docker instance with no recorded container id succeeded, want an error")
	}
}
