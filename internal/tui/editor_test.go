package tui

import (
	"slices"
	"testing"
)

func TestParseEditorArgv(t *testing.T) {
	tests := []struct {
		name, value string
		want        []string
	}{
		{"arguments", "code --wait", []string{"code", "--wait"}},
		{"quoted executable and argument", `"/Applications/Visual Studio Code.app/Contents/MacOS/Electron" --wait "profile name"`, []string{"/Applications/Visual Studio Code.app/Contents/MacOS/Electron", "--wait", "profile name"}},
		{"backslash arguments", `my\ editor --name=profile\ one`, []string{"my editor", "--name=profile one"}},
		{"single quotes", `'/opt/My Editor/bin/edit' '--literal=$HOME'`, []string{"/opt/My Editor/bin/edit", "--literal=$HOME"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseEditorArgv(tt.value)
			if err != nil || !slices.Equal(got, tt.want) {
				t.Fatalf("got=%#v err=%v want=%#v", got, err, tt.want)
			}
		})
	}
}

func TestParseEditorArgvRejectsMalformedInput(t *testing.T) {
	for _, value := range []string{"", `code "unterminated`, `code trailing\`} {
		if got, err := parseEditorArgv(value); err == nil {
			t.Fatalf("value=%q got=%#v", value, got)
		}
	}
}
