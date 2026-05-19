// Package main is the entry point for the Overcast Go SDK compatibility suite.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/Neaox/overcast-compat-go-sdk/internal/clients"
	"github.com/Neaox/overcast-compat-go-sdk/internal/groups"
	"github.com/Neaox/overcast-compat-go-sdk/internal/harness"
	"github.com/Neaox/overcast-compat-go-sdk/internal/registry"
)

const suite = "go-sdk"

func main() {
	endpoint := envOr("OVERCAST_ENDPOINT", "http://localhost:4566")
	region := envOr("OVERCAST_DEFAULT_REGION", "us-east-1")
	runID := envOr("OVERCAST_COMPAT_RUN_ID", makeRunID())
	skipDocker := os.Getenv("OVERCAST_COMPAT_SKIP_DOCKER") == "1"

	c := clients.New(endpoint, region)

	// Assemble all impls, setup, and teardown from service group packages.
	allImpls := registry.ImplMap{}
	allSetup := map[string]func(context.Context, *harness.TestContext) error{}
	allTeardown := map[string]func(context.Context, *harness.TestContext) error{}

	for _, svc := range groups.All(c) {
		for k, v := range svc.Impls {
			allImpls[k] = v
		}
		for k, v := range svc.Setup {
			allSetup[k] = v
		}
		for k, v := range svc.Teardown {
			allTeardown[k] = v
		}
	}

	// Load registry and build groups.
	reg, err := registry.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "go-sdk: failed to load registry: %v\n", err)
		os.Exit(1)
	}

	caps := map[string]bool{}
	if !skipDocker {
		caps["docker"] = true
	}

	registry.ValidateImpls(reg, allImpls, suite)

	allGroups := registry.BuildGroups(reg, allImpls, registry.BuildGroupsOptions{
		Suite:        suite,
		Capabilities: caps,
		Setup:        allSetup,
		Teardown:     allTeardown,
	})

	// Apply filters.
	filterServices := splitFilter(os.Getenv("OVERCAST_COMPAT_SERVICE"))
	filterGroups := splitFilter(os.Getenv("OVERCAST_COMPAT_GROUPS"))
	filterTests := splitFilter(os.Getenv("OVERCAST_COMPAT_TESTS"))

	if len(filterServices) > 0 {
		var filtered []harness.TestGroup
		for _, g := range allGroups {
			if filterServices[g.Service] {
				filtered = append(filtered, g)
			}
		}
		allGroups = filtered
	}
	if len(filterGroups) > 0 {
		var filtered []harness.TestGroup
		for _, g := range allGroups {
			if filterGroups[g.Name] {
				filtered = append(filtered, g)
			}
		}
		allGroups = filtered
	}
	if len(filterTests) > 0 {
		var filtered []harness.TestGroup
		for _, g := range allGroups {
			var tests []harness.TestCase
			for _, tc := range g.Tests {
				if filterTests[tc.Name] {
					tests = append(tests, tc)
				}
			}
			if len(tests) > 0 {
				g.Tests = tests
				filtered = append(filtered, g)
			}
		}
		allGroups = filtered
	}

	// Interactive mode: don't auto-run, wait for commands on stdin.
	if os.Getenv("OVERCAST_COMPAT_INTERACTIVE") == "1" {
		harness.EmitBuilding(suite, "Loading registry and building test groups...")

		totalTests := 0
		for _, g := range allGroups {
			totalTests += len(g.Tests)
		}
		harness.EmitReady(suite, totalTests)

		groupMap := make(map[string]harness.TestGroup)
		for _, g := range allGroups {
			groupMap[g.Name] = g
		}

		cmds := harness.ReadCommands()

		var cancelMu sync.Mutex
		cancels := make(map[string]context.CancelFunc)
		// runningTest tracks the currently executing test for ping/pong.
		var runningTestMu sync.Mutex

		// Handle SIGINT/SIGTERM: cancel all in-flight tests and exit.
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		go func() {
			<-sigCh
			fmt.Fprintf(os.Stderr, "[go-sdk] received signal — shutting down\n")
			cancelMu.Lock()
			funcs := make([]context.CancelFunc, 0, len(cancels))
			for _, cancel := range cancels {
				funcs = append(funcs, cancel)
			}
			cancelMu.Unlock()
			for _, cancel := range funcs {
				cancel()
			}
			os.Exit(0)
		}()
		runningTest := ""

		for cmd := range cmds {
			switch cmd.Command {
			case "run":
				var groupsToRun []harness.TestGroup
				// Empty tests means "run all groups".
				if len(cmd.Tests) == 0 {
					for _, g := range allGroups {
						groupsToRun = append(groupsToRun, g)
					}
				}
				for _, ref := range cmd.Tests {
					g, ok := groupMap[ref.Group]
					if !ok {
						fmt.Fprintf(os.Stderr, "[go-sdk] unknown group: %s\n", ref.Group)
						continue
					}
					if len(ref.Tests) > 0 {
						requested := make(map[string]bool)
						for _, t := range ref.Tests {
							requested[t] = true
						}
						var filtered []harness.TestCase
						for _, tc := range g.Tests {
							if requested[tc.Name] {
								filtered = append(filtered, tc)
							}
						}
						g.Tests = filtered
					}
					groupsToRun = append(groupsToRun, g)
				}

				batchID := cmd.BatchID
				batchStart := time.Now()

				slots := 8
				if v := os.Getenv("OVERCAST_COMPAT_PARALLEL_SLOTS"); v != "" {
					if n, _ := strconv.Atoi(v); n > 0 {
						slots = n
					}
				}
				sem := make(chan struct{}, slots)

				// Dispatch test execution to a background goroutine so the
				// main goroutine keeps reading stdin commands (ping, cancel,
				// shutdown) without blocking on wg.Wait().
				go func() {
					results := make([]harness.GroupResult, len(groupsToRun))
					var wg sync.WaitGroup
					for i, g := range groupsToRun {
						wg.Add(1)
						go func(i int, g harness.TestGroup) {
							defer wg.Done()
							sem <- struct{}{}
							defer func() { <-sem }()

							groupCtx, groupCancel := context.WithTimeout(context.Background(), 5*time.Minute)
							defer groupCancel()

							// Register cancel funcs and running test for ping/pong.
							cancelMu.Lock()
							for _, tc := range g.Tests {
								cancels[g.Name+":"+tc.Name] = groupCancel
							}
							cancelMu.Unlock()
							runningTestMu.Lock()
							for _, tc := range g.Tests {
								if tc.Skip == "" && runningTest == "" {
									runningTest = g.Name + ":" + tc.Name
								}
							}
							runningTestMu.Unlock()

							results[i] = harness.RunGroup(groupCtx, g, harness.NewTestContext(endpoint, region, runID))

							cancelMu.Lock()
							for _, tc := range g.Tests {
								delete(cancels, g.Name+":"+tc.Name)
							}
							cancelMu.Unlock()
						}(i, g)
					}
					wg.Wait()

					runningTestMu.Lock()
					runningTest = ""
					runningTestMu.Unlock()

					var total harness.GroupResult
					for _, r := range results {
						total.Passed += r.Passed
						total.Failed += r.Failed
						total.Skipped += r.Skipped
						total.Unimplemented += r.Unimplemented
						total.Cancelled += r.Cancelled
					}

					harness.EmitBatchComplete(suite, batchID, total, time.Since(batchStart).Milliseconds())
				}()

			case "cancel":
				cancelMu.Lock()
				if cmd.Group != "" && cmd.Test != "" {
					if cancel, ok := cancels[cmd.Group+":"+cmd.Test]; ok {
						cancel()
					}
				} else {
					for _, cancel := range cancels {
						cancel()
					}
				}
				cancelMu.Unlock()

			case "ping":
				runningTestMu.Lock()
				rt := runningTest
				runningTestMu.Unlock()
				harness.EmitPong(suite, rt)

			case "shutdown":
				cancelMu.Lock()
				for _, cancel := range cancels {
					cancel()
				}
				cancelMu.Unlock()
				os.Exit(0)
			}
		}
		os.Exit(0)
	}

	harness.RunSuite(context.Background(), suite, allGroups, endpoint, region, runID)
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func splitFilter(s string) map[string]bool {
	if s == "" {
		return nil
	}
	m := map[string]bool{}
	for _, part := range strings.Split(s, ",") {
		if p := strings.TrimSpace(part); p != "" {
			m[p] = true
		}
	}
	return m
}

func makeRunID() string {
	return fmt.Sprintf("oc-%x", os.Getpid())
}
