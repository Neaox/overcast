//go:build slim

package main

import (
	"bytes"
	"strings"
	"testing"
)

// TestMCPCmd_Slim_ExplainsAbsence pins the slim-build stub's behaviour: the
// command is still registered (so `overcast --help` and its flags look the
// same across builds — see cmd_mcp_slim.go's doc comment) but running it
// fails with a clear reason instead of doing nothing or erroring with an
// unhelpful "unknown flag".
func TestMCPCmd_Slim_ExplainsAbsence(t *testing.T) {
	cmd := newMCPCmd()
	cmd.SetArgs([]string{"--stdio", "--workspace", "."})
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)

	err := cmd.Execute()
	if err == nil {
		t.Fatal("overcast mcp on a slim build returned nil error, want a clear refusal")
	}
	if !strings.Contains(err.Error(), "slim") {
		t.Errorf("error %q does not explain the slim-build exclusion", err.Error())
	}
}

func TestMCPCmd_Slim_FlagsRegistered(t *testing.T) {
	cmd := newMCPCmd()
	for _, name := range []string{"workspace", "listen", "stdio"} {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("flag %q not registered on the slim stub", name)
		}
	}
}
