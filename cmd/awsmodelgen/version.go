package main

import (
	"fmt"
	"os"
	"strings"
	"time"
)

func updateModelVersion(path, revision, modelDate string) error {
	if strings.TrimSpace(revision) == "" {
		return fmt.Errorf("model revision must not be empty")
	}
	if _, err := time.Parse(time.DateOnly, modelDate); err != nil {
		return fmt.Errorf("model date %q: %w", modelDate, err)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read model version %s: %w", path, err)
	}
	lines := strings.Split(string(contents), "\n")
	revisionFound := replaceVersionField(lines, "revision", revision)
	dateFound := replaceVersionField(lines, "model-date", modelDate)
	if !revisionFound || !dateFound {
		return fmt.Errorf("model version %s must contain revision and model-date fields", path)
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o644); err != nil {
		return fmt.Errorf("write model version %s: %w", path, err)
	}
	return nil
}

func replaceVersionField(lines []string, name, value string) bool {
	prefix := name + "="
	found := false
	for i, line := range lines {
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		if found {
			return false
		}
		lines[i] = prefix + value
		found = true
	}
	return found
}
