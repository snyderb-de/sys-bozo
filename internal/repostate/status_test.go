package repostate

import (
	"strings"
	"testing"
)

func TestParsePorcelainV2PreservesExactPathsAndStates(t *testing.T) {
	raw := strings.Join([]string{
		"1 .M N... 100644 100644 100644 abc def configs/a file.toml",
		"2 R. N... 100644 100644 100644 abc def R100 configs/new-name\nline.toml",
		"configs/old-name.toml",
		"? --leading-dash",
		"? 日本語.txt",
	}, "\x00") + "\x00"

	got, err := ParsePorcelainV2([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 4 {
		t.Fatalf("entries=%#v", got)
	}
	if got[0].Path != "configs/a file.toml" || got[0].Index != StateUnmodified || got[0].Worktree != StateModified {
		t.Fatalf("ordinary=%#v", got[0])
	}
	if got[1].Path != "configs/new-name\nline.toml" || got[1].OriginalPath != "configs/old-name.toml" || got[1].Index != StateRenamed {
		t.Fatalf("rename=%#v", got[1])
	}
	if got[2].Path != "--leading-dash" || got[3].Path != "日本語.txt" {
		t.Fatalf("paths=%#v", got)
	}
}
