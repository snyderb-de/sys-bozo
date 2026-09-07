package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// Completion/error tests write run history. Give the whole suite a disposable
// home so tests without their own t.Setenv cannot pollute the operator's history.
func TestMain(m *testing.M) { os.Exit(runIsolatedTUITests(m)) }

func runIsolatedTUITests(m *testing.M) int {
	home, err := os.MkdirTemp("", "sys-bozo-tui-tests-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer os.RemoveAll(home)
	for key, value := range map[string]string{
		"HOME":            home,
		"XDG_CONFIG_HOME": filepath.Join(home, ".config"),
		"XDG_STATE_HOME":  filepath.Join(home, ".local", "state"),
		"XDG_DATA_HOME":   filepath.Join(home, ".local", "share"),
		"XDG_CACHE_HOME":  filepath.Join(home, ".cache"),
		"DOTFILES_REPO":   filepath.Join(home, "dotfiles"),
		"SYS_BOZO_REPO":   filepath.Join(home, "sys-bozo"),
	} {
		if err := os.Setenv(key, value); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
	}
	return m.Run()
}
