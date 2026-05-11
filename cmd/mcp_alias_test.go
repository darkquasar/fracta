package cmd

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// captureStderr swaps os.Stderr for a pipe for the duration of fn and returns
// what was written. Restores os.Stderr on return.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stderr = w

	done := make(chan struct{})
	var buf bytes.Buffer
	go func() {
		_, _ = io.Copy(&buf, r)
		close(done)
	}()

	fn()

	_ = w.Close()
	<-done
	os.Stderr = orig
	return buf.String()
}

func TestDeprecatedAliasEmitsWarningAndReachesRunner(t *testing.T) {
	cases := []struct {
		name        string
		aliasVerb   string // value of cmd.Use (Cobra uses the first word as Name())
		newPath     string
		wantOldHint string // substring expected for the old path
		wantNewHint string // substring expected for the new path
	}{
		{
			name:        "login",
			aliasVerb:   "login <server>",
			newPath:     "login",
			wantOldHint: "'fracta mcp login' is deprecated",
			wantNewHint: "use 'fracta config mcp auth login'",
		},
		{
			name:        "logout",
			aliasVerb:   "logout <server>",
			newPath:     "logout",
			wantOldHint: "'fracta mcp logout' is deprecated",
			wantNewHint: "use 'fracta config mcp auth logout'",
		},
		{
			// The hyphenated 'auth-status' on the alias should map to the
			// renamed 'status' verb on the new path.
			name:        "auth-status -> status",
			aliasVerb:   "auth-status [server]",
			newPath:     "status",
			wantOldHint: "'fracta mcp auth-status' is deprecated",
			wantNewHint: "use 'fracta config mcp auth status'",
		},
		{
			name:        "export",
			aliasVerb:   "export <server>",
			newPath:     "export",
			wantOldHint: "'fracta mcp export' is deprecated",
			wantNewHint: "use 'fracta config mcp auth export'",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reached := false
			cmd := &cobra.Command{Use: tc.aliasVerb}
			wrapped := deprecatedAlias(tc.newPath, func(*cobra.Command, []string) error {
				reached = true
				return nil
			})

			stderr := captureStderr(t, func() {
				if err := wrapped(cmd, nil); err != nil {
					t.Fatalf("wrapped runner returned error: %v", err)
				}
			})

			if !reached {
				t.Fatalf("inner runner was not reached")
			}
			if !strings.Contains(stderr, tc.wantOldHint) {
				t.Errorf("stderr missing %q\nstderr=%q", tc.wantOldHint, stderr)
			}
			if !strings.Contains(stderr, tc.wantNewHint) {
				t.Errorf("stderr missing %q\nstderr=%q", tc.wantNewHint, stderr)
			}
		})
	}
}

func TestMcpAliasCommandTreeStructure(t *testing.T) {
	// The top-level 'mcp' alias must expose all four deprecated verbs and
	// must be registered on the root command. Pure tree-structure check —
	// no runners invoked.
	var aliasCmd *cobra.Command
	for _, c := range rootCmd.Commands() {
		if c.Name() == "mcp" {
			aliasCmd = c
			break
		}
	}
	if aliasCmd == nil {
		t.Fatalf("'mcp' alias not registered on rootCmd")
	}
	got := map[string]bool{}
	for _, c := range aliasCmd.Commands() {
		got[c.Name()] = true
	}
	for _, want := range []string{"login", "logout", "auth-status", "export"} {
		if !got[want] {
			t.Errorf("alias missing verb %q (have: %v)", want, got)
		}
	}
}
