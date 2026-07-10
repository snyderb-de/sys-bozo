package history

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"time"
)

type Status string

const (
	StatusSuccess   Status = "success"
	StatusFailure   Status = "failure"
	StatusCancelled Status = "cancelled"
)

type Entry struct {
	Ts     time.Time `json:"ts"`
	Action string    `json:"action"`
	Secs   float64   `json:"secs"`
	OK     bool      `json:"ok"`
	Status Status    `json:"status,omitempty"`
}

func (e Entry) EffectiveStatus() Status {
	if e.Status != "" {
		return e.Status
	}
	if e.OK {
		return StatusSuccess
	}
	return StatusFailure
}

func Append(e Entry) {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	dir := filepath.Join(home, ".local", "state", "sys-bozo")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return
	}
	f, err := os.OpenFile(filepath.Join(dir, "history.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()
	_ = json.NewEncoder(f).Encode(e)
}

func Read(limit int) []Entry {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	f, err := os.Open(filepath.Join(home, ".local", "state", "sys-bozo", "history.jsonl"))
	if err != nil {
		return nil
	}
	defer f.Close()

	var entries []Entry
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var entry Entry
		if json.Unmarshal(scanner.Bytes(), &entry) == nil {
			entries = append(entries, entry)
		}
	}
	slices.Reverse(entries)
	if limit > 0 && len(entries) > limit {
		entries = entries[:limit]
	}
	return entries
}
