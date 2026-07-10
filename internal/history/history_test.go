package history

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestReadReturnsNewestEntriesFirst(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	Append(Entry{Ts: time.Unix(1, 0), Action: "hms", OK: true})
	Append(Entry{Ts: time.Unix(2, 0), Action: "brew", OK: false})

	got := Read(1)
	if len(got) != 1 || got[0].Action != "brew" {
		t.Fatalf("got %#v", got)
	}
}

func TestReadIgnoresMalformedLinesAndKeepsLegacyEntries(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".local", "state", "sys-bozo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	data := []byte("not-json\n{\"ts\":\"1970-01-01T00:00:01Z\",\"action\":\"legacy\",\"secs\":1,\"ok\":true}\n")
	if err := os.WriteFile(filepath.Join(dir, "history.jsonl"), data, 0o644); err != nil {
		t.Fatal(err)
	}

	got := Read(0)
	if len(got) != 1 || got[0].Action != "legacy" || !got[0].OK || got[0].Status != "" || got[0].EffectiveStatus() != StatusSuccess {
		t.Fatalf("got %#v", got)
	}
}
