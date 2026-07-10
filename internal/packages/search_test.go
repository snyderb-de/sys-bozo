package packages

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeOutputRunner struct {
	responses map[string]struct {
		out string
		err error
	}
}

func (f fakeOutputRunner) Output(_ context.Context, name string, args ...string) ([]byte, error) {
	key := name + " " + strings.Join(args, " ")
	response := f.responses[key]
	return []byte(response.out), response.err
}

func TestSearchCombinesProvidersAndDefaultsToNix(t *testing.T) {
	runner := fakeOutputRunner{responses: map[string]struct {
		out string
		err error
	}{
		"nix search --json nixpkgs lazydocker": {out: `{"legacyPackages.aarch64-darwin.lazydocker":{"pname":"lazydocker","version":"0.24.1","description":"Docker TUI"}}`},
		"brew search --formula lazydocker":     {out: "lazydocker\n"},
		"brew search --cask lazydocker":        {out: ""},
	}}

	got := Search(context.Background(), runner, "nix", "brew", "lazydocker")

	if len(got.Candidates) != 2 || got.Candidates[0].Provider != ProviderNix {
		t.Fatalf("got %#v", got)
	}
	if got.Selected != 0 {
		t.Fatalf("selected=%d want Nix index 0", got.Selected)
	}
}

func TestSearchKeepsBrewWhenNixFails(t *testing.T) {
	runner := fakeOutputRunner{responses: map[string]struct {
		out string
		err error
	}{
		"nix search --json nixpkgs yazi": {err: errors.New("nix unavailable")},
		"brew search --formula yazi":     {out: "yazi\n"},
		"brew search --cask yazi":        {out: ""},
	}}

	got := Search(context.Background(), runner, "nix", "brew", "yazi")

	if got.NixErr == nil || len(got.Candidates) != 1 || got.Candidates[0].Provider != ProviderBrew {
		t.Fatalf("got %#v", got)
	}
}

func TestSearchSortsNixAttributesAndParsesMetadata(t *testing.T) {
	runner := fakeOutputRunner{responses: map[string]struct {
		out string
		err error
	}{
		"nix search --json nixpkgs tool": {out: `{
			"legacyPackages.aarch64-darwin.zed":{"pname":"zed-editor","version":"0.204.4","description":"Code editor"},
			"legacyPackages.aarch64-darwin.alpha":{"pname":"alpha","version":"1.2.3","description":"First tool"}
		}`},
		"brew search --formula tool": {out: ""},
		"brew search --cask tool":    {out: ""},
	}}

	got := Search(context.Background(), runner, "nix", "brew", "tool")

	want := []Candidate{
		{Provider: ProviderNix, Kind: KindPackage, ID: "alpha", Name: "alpha", Version: "1.2.3", Description: "First tool"},
		{Provider: ProviderNix, Kind: KindPackage, ID: "zed", Name: "zed-editor", Version: "0.204.4", Description: "Code editor"},
	}
	if len(got.Candidates) != len(want) {
		t.Fatalf("got %#v want %#v", got.Candidates, want)
	}
	for i := range want {
		if got.Candidates[i] != want[i] {
			t.Fatalf("candidate[%d]=%#v want %#v", i, got.Candidates[i], want[i])
		}
	}
}

func TestSearchParsesBrewFormulaeAndCasks(t *testing.T) {
	runner := fakeOutputRunner{responses: map[string]struct {
		out string
		err error
	}{
		"nix search --json nixpkgs ghost": {out: `{}`},
		"brew search --formula ghost":     {out: "ghostscript\nghostty => ghostty@tip\n\n"},
		"brew search --cask ghost":        {out: "ghostty\n"},
	}}

	got := Search(context.Background(), runner, "nix", "brew", "ghost")

	want := []Candidate{
		{Provider: ProviderBrew, Kind: KindFormula, ID: "ghostscript", Name: "ghostscript"},
		{Provider: ProviderBrew, Kind: KindCask, ID: "ghostty", Name: "ghostty"},
	}
	if len(got.Candidates) != len(want) {
		t.Fatalf("got %#v want %#v", got.Candidates, want)
	}
	for i := range want {
		if got.Candidates[i] != want[i] {
			t.Fatalf("candidate[%d]=%#v want %#v", i, got.Candidates[i], want[i])
		}
	}
}

func TestSearchDiscardsFailedFormulaOutputAndKeepsCask(t *testing.T) {
	formulaErr := errors.New("brew formula search unavailable")
	runner := fakeOutputRunner{responses: map[string]struct {
		out string
		err error
	}{
		"nix search --json nixpkgs ghostty": {out: `{"legacyPackages.aarch64-darwin.ghostty":{"pname":"ghostty","version":"1.1.3","description":"Terminal emulator"}}`},
		"brew search --formula ghostty":     {out: "failed-formula-output\n", err: formulaErr},
		"brew search --cask ghostty":        {out: "ghostty\n"},
	}}

	got := Search(context.Background(), runner, "nix", "brew", "ghostty")

	if !errors.Is(got.BrewErr, formulaErr) {
		t.Fatalf("BrewErr=%v want %v", got.BrewErr, formulaErr)
	}
	if got.NixErr != nil {
		t.Fatalf("NixErr=%v want nil", got.NixErr)
	}
	if len(got.Candidates) != 2 || got.Candidates[0].Provider != ProviderNix || got.Candidates[1].Kind != KindCask {
		t.Fatalf("got %#v", got)
	}
}

func TestSearchDiscardsFailedCaskOutputAndKeepsFormula(t *testing.T) {
	caskErr := errors.New("brew cask search unavailable")
	runner := fakeOutputRunner{responses: map[string]struct {
		out string
		err error
	}{
		"nix search --json nixpkgs yazi": {out: `{}`},
		"brew search --formula yazi":     {out: "yazi\n"},
		"brew search --cask yazi":        {out: "failed-cask-output\n", err: caskErr},
	}}

	got := Search(context.Background(), runner, "nix", "brew", "yazi")

	if !errors.Is(got.BrewErr, caskErr) {
		t.Fatalf("BrewErr=%v want %v", got.BrewErr, caskErr)
	}
	want := Candidate{Provider: ProviderBrew, Kind: KindFormula, ID: "yazi", Name: "yazi"}
	if len(got.Candidates) != 1 || got.Candidates[0] != want {
		t.Fatalf("candidates=%#v want %#v", got.Candidates, []Candidate{want})
	}
}

func TestSearchDiscardsAllBrewOutputWhenBothSearchesFail(t *testing.T) {
	formulaErr := errors.New("brew formula search unavailable")
	caskErr := errors.New("brew cask search unavailable")
	runner := fakeOutputRunner{responses: map[string]struct {
		out string
		err error
	}{
		"nix search --json nixpkgs tool": {out: `{}`},
		"brew search --formula tool":     {out: "failed-formula-output\n", err: formulaErr},
		"brew search --cask tool":        {out: "failed-cask-output\n", err: caskErr},
	}}

	got := Search(context.Background(), runner, "nix", "brew", "tool")

	if !errors.Is(got.BrewErr, formulaErr) || !errors.Is(got.BrewErr, caskErr) {
		t.Fatalf("BrewErr=%v want both %v and %v", got.BrewErr, formulaErr, caskErr)
	}
	if len(got.Candidates) != 0 {
		t.Fatalf("candidates=%#v want none", got.Candidates)
	}
}

type gatedOutputRunner struct {
	mu      sync.Mutex
	started int
	ready   chan struct{}
}

func (r *gatedOutputRunner) Output(ctx context.Context, _ string, args ...string) ([]byte, error) {
	if len(args) >= 2 && args[1] == "--cask" {
		return nil, nil
	}

	r.mu.Lock()
	r.started++
	if r.started == 2 {
		close(r.ready)
	}
	r.mu.Unlock()

	select {
	case <-r.ready:
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	if len(args) >= 2 && args[1] == "--json" {
		return []byte(`{"legacyPackages.aarch64-darwin.yazi":{"pname":"yazi"}}`), nil
	}
	return []byte("yazi\n"), nil
}

func TestSearchRunsProvidersConcurrently(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	runner := &gatedOutputRunner{ready: make(chan struct{})}

	got := Search(ctx, runner, "nix", "brew", "yazi")

	if got.NixErr != nil || got.BrewErr != nil {
		t.Fatalf("NixErr=%v BrewErr=%v", got.NixErr, got.BrewErr)
	}
	if len(got.Candidates) != 2 {
		t.Fatalf("candidates=%#v", got.Candidates)
	}
}
