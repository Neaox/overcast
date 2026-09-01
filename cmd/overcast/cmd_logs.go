package main

// cmd_logs.go — `overcast logs [name]`. Prints a background instance's log
// output: for the native backend, the daemon.log file the registry record
// points at (instances.go, cmd_start.go's startNative); for docker, a
// straight `docker logs` against the recorded container id. --follow keeps
// streaming new output until interrupted, with the same UX for both
// backends despite the different underlying mechanism.

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

func newLogsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "logs [name]",
		Short: "Show a background overcast instance's logs",
		Args:  cobra.MaximumNArgs(1),
		ValidArgsFunction: func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
			if len(args) > 0 {
				return nil, cobra.ShellCompDirectiveNoFileComp
			}
			return completeInstanceNames(cmd, args, toComplete)
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			name := instanceNameArg(args)
			follow, _ := cmd.Flags().GetBool("follow")
			tail, _ := cmd.Flags().GetInt("tail")

			rec, err := loadInstance(name)
			if err != nil {
				return unknownInstanceError(name)
			}

			if rec.Backend == "docker" {
				return streamDockerLogs(cmd, rec, tail, follow)
			}
			return streamNativeLogs(cmd, rec, tail, follow)
		},
	}
	cmd.Flags().BoolP("follow", "f", false, "keep streaming new log output until interrupted")
	cmd.Flags().IntP("tail", "n", 100, "number of lines to show from the end of the log")
	return cmd
}

// unknownInstanceError reports that name has no registry record, listing
// every currently known instance name so the caller doesn't have to run
// `overcast status` first just to find out what exists.
func unknownInstanceError(name string) error {
	recs, err := listInstances()
	if err != nil || len(recs) == 0 {
		return fmt.Errorf("no instance named %q — no instances are registered (run `overcast start` first)", name)
	}
	names := make([]string, len(recs))
	for i, r := range recs {
		names[i] = r.Name
	}
	return fmt.Errorf("no instance named %q — known instances: %s", name, strings.Join(names, ", "))
}

// followPollInterval is how often followLogFile re-stats the log file for
// appended bytes. A var, not a const, so tests can shrink it instead of
// sleeping a full interval per assertion.
var followPollInterval = 500 * time.Millisecond

// streamNativeLogs is newLogsCmd's native-backend path: print the tail of
// rec.LogFile, then (with --follow) keep polling it for appended bytes.
func streamNativeLogs(cmd *cobra.Command, rec instanceRecord, tail int, follow bool) error {
	if rec.LogFile == "" {
		return fmt.Errorf("instance %q has no recorded log file", rec.Name)
	}
	out := cmd.OutOrStdout()
	lines, size, err := tailFile(rec.LogFile, tail)
	if err != nil {
		return fmt.Errorf("read log file %s: %w", rec.LogFile, err)
	}
	for _, l := range lines {
		fmt.Fprintln(out, l)
	}
	if !follow {
		return nil
	}
	return followLogFile(cmd.Context(), out, rec.LogFile, size)
}

// tailFile returns the last n lines of the file at path (n <= 0 means the
// whole file), plus the file's size at the moment it was read — the offset
// followLogFile starts polling from, so --follow never re-prints anything
// tailFile already printed.
func tailFile(path string, n int) (lines []string, size int64, err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, 0, err
	}
	size = int64(len(data))
	text := strings.TrimRight(string(data), "\n")
	if text == "" {
		return nil, size, nil
	}
	all := strings.Split(text, "\n")
	if n > 0 && len(all) > n {
		all = all[len(all)-n:]
	}
	return all, size, nil
}

// followLogFile polls path every followPollInterval for bytes appended after
// offset, writing them to out, until ctx is done (Ctrl+C, via
// cmd.Context()). If the file is now shorter than offset — truncated, or
// rotated to a fresh file created at the same path — it re-seeks to the
// start rather than erroring: a shorter file at the same path is
// unambiguously new content, never a partial write in progress (a partial
// write only ever grows a file).
func followLogFile(ctx context.Context, out io.Writer, path string, offset int64) error {
	ticker := time.NewTicker(followPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
		info, err := os.Stat(path)
		if err != nil {
			// Log file momentarily missing (mid-rotation) or the instance
			// was stopped/removed out from under us — keep polling;
			// ctx.Done above is how this ever ends.
			continue
		}
		if info.Size() < offset {
			offset = 0
		}
		if info.Size() == offset {
			continue
		}
		appended, newOffset, err := readAppended(path, offset, info.Size())
		if err != nil {
			continue
		}
		if len(appended) > 0 {
			_, _ = out.Write(appended)
		}
		offset = newOffset
	}
}

// readAppended reads path's bytes in [offset, upTo) — the region
// followLogFile just observed as newly written.
func readAppended(path string, offset, upTo int64) ([]byte, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, offset, err
	}
	defer f.Close() //nolint:errcheck // read-only handle; nothing meaningful to do with a close error here
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return nil, offset, err
	}
	buf := make([]byte, upTo-offset)
	n, err := io.ReadFull(f, buf)
	if err != nil && err != io.ErrUnexpectedEOF && err != io.EOF {
		return nil, offset, err
	}
	return buf[:n], offset + int64(n), nil
}

// streamDockerLogs is newLogsCmd's docker-backend path: `docker logs` run
// directly via os/exec (not the dockerRun seam — see docker_backend.go's
// file comment: dockerRun is for short request/response calls, this is a
// long-lived stream) with stdio inherited from this process, so output
// streams live and --follow's Ctrl+C is delivered by cmd.Context()
// cancellation rather than anything handled in this function.
func streamDockerLogs(cmd *cobra.Command, rec instanceRecord, tail int, follow bool) error {
	if rec.ContainerID == "" {
		return fmt.Errorf("instance %q has no recorded container id", rec.Name)
	}
	args := dockerLogsArgs(rec.ContainerID, tail, follow)
	c := exec.CommandContext(cmd.Context(), "docker", args...) //nolint:gosec // "docker" resolves via PATH; args come entirely from dockerLogsArgs, built from validated flag values and our own registry record, never passthrough.
	c.Stdout = cmd.OutOrStdout()
	c.Stderr = cmd.ErrOrStderr()
	if err := c.Run(); err != nil {
		if cmd.Context().Err() != nil {
			// Interrupted mid-stream (Ctrl+C during --follow): a clean stop,
			// not a failure — the process was killed on purpose.
			return nil
		}
		return fmt.Errorf("docker logs: %w", err)
	}
	return nil
}

// dockerLogsArgs assembles `docker logs`'s argv — split out from
// streamDockerLogs so the argument assembly is unit-testable without an
// os/exec call, the same split cmd_aws.go's completeAWSArgs/awsCompLine use
// for the same reason.
func dockerLogsArgs(containerID string, tail int, follow bool) []string {
	args := []string{"logs", "--tail", strconv.Itoa(tail)}
	if follow {
		args = append(args, "-f")
	}
	return append(args, containerID)
}
