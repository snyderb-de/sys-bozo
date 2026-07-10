package packages

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestVerifyCLIRequiresResolvedExecutable(t *testing.T) {
	lookup := func(name string) (string, error) {
		if name == "yazi" {
			return "/nix/store/test/bin/yazi", nil
		}
		return "", exec.ErrNotFound
	}

	got := Verify(context.Background(), fakeOutputRunner{}, lookup, VerifySpec{
		Provider:   ProviderNix,
		Kind:       KindPackage,
		Token:      "yazi",
		Executable: "yazi",
	})

	if !got.OK || got.Path != "/nix/store/test/bin/yazi" {
		t.Fatalf("got %#v", got)
	}
}

func TestVerifyNixWithoutTrustedExecutableUsesDirectProfileReference(t *testing.T) {
	calls := []string{}
	runner := fakeOutputRunner{responses: map[string]fakeOutputResponse{
		"nix-store -q --references /tmp/profile": {out: "/nix/store/aaa-bash-5.2\n/nix/store/bbb-ripgrep-14.1.1\n"},
	}, calls: &calls}

	got := Verify(context.Background(), runner, nil, VerifySpec{
		Provider: ProviderNix, Kind: KindPackage, Token: "ripgrep",
		PName: "ripgrep", Version: "14.1.1", NixStoreBin: "nix-store", ProfilePath: "/tmp/profile",
	})

	if !got.OK || !strings.Contains(got.Detail, "direct profile reference") {
		t.Fatalf("got %#v", got)
	}
	if want := []string{"nix-store -q --references /tmp/profile"}; !slices.Equal(calls, want) {
		t.Fatalf("calls=%#v want %#v", calls, want)
	}
}

func TestVerifyNixNestedAttrDoesNotGuessPnameExecutable(t *testing.T) {
	calls := []string{}
	lookupCalls := 0
	runner := fakeOutputRunner{responses: map[string]fakeOutputResponse{
		"nix-store -q --references /tmp/profile": {out: "/nix/store/bbb-requests-2.32.4\n"},
	}, calls: &calls}

	got := Verify(context.Background(), runner, func(string) (string, error) {
		lookupCalls++
		return "/wrong", nil
	}, VerifySpec{
		Provider: ProviderNix, Kind: KindPackage, Token: "python313Packages.requests",
		PName: "requests", Version: "2.32.4", NixStoreBin: "nix-store", ProfilePath: "/tmp/profile",
	})

	if !got.OK || lookupCalls != 0 || len(calls) != 1 {
		t.Fatalf("got=%#v lookupCalls=%d calls=%#v", got, lookupCalls, calls)
	}
}

func TestVerifyNixWithoutProviderEvidenceFailsClosed(t *testing.T) {
	for _, spec := range []VerifySpec{
		{Provider: ProviderNix, Kind: KindPackage, PName: "ripgrep", Version: "14.1.1", ProfilePath: "/tmp/profile"},
		{Provider: ProviderNix, Kind: KindPackage, PName: "ripgrep", Version: "14.1.1", NixStoreBin: "nix-store"},
		{Provider: ProviderNix, Kind: KindPackage, NixStoreBin: "nix-store", ProfilePath: "/tmp/profile"},
	} {
		got := Verify(context.Background(), fakeOutputRunner{}, nil, spec)
		if got.OK || got.Err == nil || !strings.Contains(got.Detail, "evidence") {
			t.Fatalf("spec=%#v got=%#v", spec, got)
		}
	}
}

func TestVerifyBrewFormulaWithTrustedExecutableRequiresReceiptAndExecutable(t *testing.T) {
	calls := []string{}
	runner := fakeOutputRunner{responses: map[string]fakeOutputResponse{
		"brew list --formula --versions ripgrep": {out: "ripgrep 14.1.1\n"},
		"/test/bin/rg --version":                 {out: "ripgrep 14.1.1\n"},
	}, calls: &calls}
	got := Verify(context.Background(), runner, func(name string) (string, error) {
		if name != "rg" {
			t.Fatalf("lookup=%q", name)
		}
		return "/test/bin/rg", nil
	}, VerifySpec{
		Provider: ProviderBrew, Kind: KindFormula, Token: "ripgrep", BrewBin: "brew",
		Executable: "rg", VersionArgs: []string{"--version"},
	})
	if !got.OK {
		t.Fatalf("got %#v", got)
	}
	want := []string{"brew list --formula --versions ripgrep", "/test/bin/rg --version"}
	if !slices.Equal(calls, want) {
		t.Fatalf("calls=%#v want %#v", calls, want)
	}
}

func TestVerifyBrewCaskRequiresReceiptAndArtifact(t *testing.T) {
	runner := fakeOutputRunner{responses: map[string]fakeOutputResponse{
		"brew list --cask --versions zed": {out: "zed 0.190.0\n"},
	}}
	lookup := func(string) (string, error) { return "", exec.ErrNotFound }
	dir := t.TempDir()
	app := filepath.Join(dir, "Zed.app")
	if err := os.Mkdir(app, 0o755); err != nil {
		t.Fatal(err)
	}

	got := Verify(context.Background(), runner, lookup, VerifySpec{
		Provider: ProviderBrew,
		Kind:     KindCask,
		Token:    "zed",
		AppPath:  app,
		BrewBin:  "brew",
	})

	if !got.OK {
		t.Fatalf("got %#v", got)
	}
}

func TestVerifyBrewCaskWithExecutableRequiresReceipt(t *testing.T) {
	calls := []string{}
	runner := fakeOutputRunner{
		responses: map[string]fakeOutputResponse{
			"/test/bin/zed --version": {out: "Zed 0.190.0\n"},
		},
		calls: &calls,
	}
	app := filepath.Join(t.TempDir(), "Zed.app")
	if err := os.Mkdir(app, 0o755); err != nil {
		t.Fatal(err)
	}

	got := Verify(context.Background(), runner, func(string) (string, error) {
		return "/test/bin/zed", nil
	}, VerifySpec{
		Provider:    ProviderBrew,
		Kind:        KindCask,
		Token:       "zed",
		Executable:  "zed",
		VersionArgs: []string{"--version"},
		AppPath:     app,
		BrewBin:     "brew",
	})

	if got.OK || got.Err == nil || !strings.Contains(got.Detail, "receipt") {
		t.Fatalf("got %#v; calls=%#v", got, calls)
	}
	if want := []string{"brew list --cask --versions zed"}; !slices.Equal(calls, want) {
		t.Fatalf("calls=%#v want %#v", calls, want)
	}
}

func TestVerifyBrewCaskWithExecutableRequiresEveryConfiguredCheck(t *testing.T) {
	calls := []string{}
	runner := fakeOutputRunner{
		responses: map[string]fakeOutputResponse{
			"brew list --cask --versions zed": {out: "zed 0.190.0\n"},
			"/test/bin/zed --version":         {out: "Zed 0.190.0\n"},
		},
		calls: &calls,
	}
	app := filepath.Join(t.TempDir(), "Zed.app")
	if err := os.Mkdir(app, 0o755); err != nil {
		t.Fatal(err)
	}

	got := Verify(context.Background(), runner, func(string) (string, error) {
		return "/test/bin/zed", nil
	}, VerifySpec{
		Provider:    ProviderBrew,
		Kind:        KindCask,
		Token:       "zed",
		Executable:  "zed",
		VersionArgs: []string{"--version"},
		AppPath:     app,
		BrewBin:     "brew",
	})

	if !got.OK || got.Path != "/test/bin/zed" {
		t.Fatalf("got %#v; calls=%#v", got, calls)
	}
	want := []string{
		"brew list --cask --versions zed",
		"/test/bin/zed --version",
	}
	if !slices.Equal(calls, want) {
		t.Fatalf("calls=%#v want %#v", calls, want)
	}
}

func TestVerifyCLIRequiresSuccessfulVersionCommand(t *testing.T) {
	versionErr := errors.New("version failed")
	runner := fakeOutputRunner{responses: map[string]fakeOutputResponse{
		"/test/bin/yazi --version": {err: versionErr},
	}}
	lookup := func(string) (string, error) { return "/test/bin/yazi", nil }

	got := Verify(context.Background(), runner, lookup, VerifySpec{
		Provider:    ProviderNix,
		Kind:        KindPackage,
		Token:       "yazi",
		Executable:  "yazi",
		VersionArgs: []string{"--version"},
	})

	if got.OK || got.Path != "" || !errors.Is(got.Err, versionErr) || !strings.Contains(got.Detail, "version") {
		t.Fatalf("got %#v", got)
	}
}

func TestVerifyCLIRejectsEmptyResolvedPath(t *testing.T) {
	lookup := func(string) (string, error) { return "", nil }

	got := Verify(context.Background(), fakeOutputRunner{}, lookup, VerifySpec{
		Provider:   ProviderNix,
		Kind:       KindPackage,
		Token:      "yazi",
		Executable: "yazi",
	})

	if got.OK || got.Err == nil || !strings.Contains(got.Detail, "could not be resolved") {
		t.Fatalf("got %#v", got)
	}
}

func TestVerifyRejectsVersionArgsWithoutExecutableBeforeOtherChecks(t *testing.T) {
	for _, spec := range []VerifySpec{
		{
			Provider:    ProviderBrew,
			Kind:        KindFormula,
			Token:       "yazi",
			VersionArgs: []string{"--version"},
			BrewBin:     "brew",
		},
		{
			Provider:    ProviderBrew,
			Kind:        KindCask,
			Token:       "zed",
			VersionArgs: []string{"--version"},
			BrewBin:     "brew",
		},
	} {
		t.Run(string(spec.Kind), func(t *testing.T) {
			calls := []string{}
			runner := fakeOutputRunner{
				responses: map[string]fakeOutputResponse{
					"brew list --formula --versions yazi": {out: "yazi 25.5.31\n"},
					"brew list --cask --versions zed":     {out: "zed 0.190.0\n"},
				},
				calls: &calls,
			}
			lookupCalls := 0

			got := Verify(context.Background(), runner, func(string) (string, error) {
				lookupCalls++
				return "/test/bin/tool", nil
			}, spec)

			if got.OK || got.Err == nil || !strings.Contains(got.Detail, "version") {
				t.Fatalf("got %#v", got)
			}
			if len(calls) != 0 || lookupCalls != 0 {
				t.Fatalf("calls=%#v lookupCalls=%d want no side effects", calls, lookupCalls)
			}
		})
	}
}

func TestVerifyRejectsIncoherentProviderKind(t *testing.T) {
	for _, spec := range []VerifySpec{
		{Provider: ProviderNix, Kind: KindCask, Executable: "zed"},
		{Provider: ProviderBrew, Kind: KindPackage, Executable: "yazi"},
	} {
		t.Run(string(spec.Provider)+"_"+string(spec.Kind), func(t *testing.T) {
			got := Verify(context.Background(), fakeOutputRunner{}, func(string) (string, error) {
				return "/test/bin/tool", nil
			}, spec)

			if got.OK || got.Err == nil || !strings.Contains(got.Detail, "unsupported") {
				t.Fatalf("got %#v", got)
			}
		})
	}
}

func TestVerifyBrewFormulaRequiresNonemptyReceipt(t *testing.T) {
	runner := fakeOutputRunner{responses: map[string]fakeOutputResponse{
		"brew list --formula --versions yazi": {},
	}}

	got := Verify(context.Background(), runner, nil, VerifySpec{
		Provider: ProviderBrew,
		Kind:     KindFormula,
		Token:    "yazi",
		BrewBin:  "brew",
	})

	if got.OK || got.Err == nil || !strings.Contains(got.Detail, "receipt") {
		t.Fatalf("got %#v", got)
	}
}

func TestVerifyBrewFormulaAcceptsNonemptyReceipt(t *testing.T) {
	calls := []string{}
	runner := fakeOutputRunner{
		responses: map[string]fakeOutputResponse{
			"brew list --formula --versions yazi": {out: "yazi 25.5.31\n"},
		},
		calls: &calls,
	}

	got := Verify(context.Background(), runner, nil, VerifySpec{
		Provider: ProviderBrew,
		Kind:     KindFormula,
		Token:    "yazi",
		BrewBin:  "brew",
	})

	if !got.OK || got.Err != nil {
		t.Fatalf("got %#v", got)
	}
	if want := []string{"brew list --formula --versions yazi"}; !slices.Equal(calls, want) {
		t.Fatalf("calls=%#v want %#v", calls, want)
	}
}

func TestVerifyBrewCaskFailsWhenArtifactIsMissing(t *testing.T) {
	calls := []string{}
	runner := fakeOutputRunner{responses: map[string]fakeOutputResponse{
		"brew list --cask --versions zed": {out: "zed 0.190.0\n"},
		"/test/bin/zed --version":         {out: "Zed 0.190.0\n"},
	}, calls: &calls}
	missingApp := filepath.Join(t.TempDir(), "Zed.app")

	got := Verify(context.Background(), runner, func(string) (string, error) {
		return "/test/bin/zed", nil
	}, VerifySpec{
		Provider:    ProviderBrew,
		Kind:        KindCask,
		Token:       "zed",
		Executable:  "zed",
		VersionArgs: []string{"--version"},
		AppPath:     missingApp,
		BrewBin:     "brew",
	})

	if got.OK || !errors.Is(got.Err, os.ErrNotExist) || !strings.Contains(got.Detail, missingApp) {
		t.Fatalf("got %#v; calls=%#v", got, calls)
	}
	if want := []string{"brew list --cask --versions zed"}; !slices.Equal(calls, want) {
		t.Fatalf("calls=%#v want %#v", calls, want)
	}
}
