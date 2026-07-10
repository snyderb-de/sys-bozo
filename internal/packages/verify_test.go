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

	got := Verify(context.Background(), fakeOutputRunner{responses: map[string]fakeOutputResponse{"brew list --formula --versions yazi": {out: "yazi 1\n"}}}, lookup, VerifySpec{
		Provider: ProviderBrew,
		Kind:     KindFormula, BrewBin: "brew",
		Token:      "yazi",
		Executable: "yazi",
	})

	if !got.OK || got.Path != "/nix/store/test/bin/yazi" {
		t.Fatalf("got %#v", got)
	}
}

func TestVerifyNixUsesPinnedAttrAndActualGenerationClosure(t *testing.T) {
	calls := []string{}
	runner := fakeOutputRunner{responses: map[string]fakeOutputResponse{
		"home-manager generations": {out: "2026-07-10 -> /nix/store/gen-home-manager-generation\n"},
		`nix eval --raw --impure --expr (builtins.getAttr "requests" (builtins.getAttr "python313Packages" (builtins.getAttr "aarch64-darwin" (builtins.getAttr "nixpkgs" (builtins.getFlake "/repo").inputs).legacyPackages))).outPath`: {out: "/nix/store/exact-requests\n"},
		"nix-store -q --requisites /nix/store/gen-home-manager-generation": {out: "/nix/store/registry-drift-requests\n/nix/store/exact-requests\n"},
	}, calls: &calls}

	got := Verify(context.Background(), runner, nil, VerifySpec{
		Provider: ProviderNix, Kind: KindPackage, Token: "python313Packages.requests",
		NixStoreBin: "nix-store", NixBin: "nix", HomeManagerBin: "home-manager", Repo: "/repo", System: "aarch64-darwin", NixInput: "nixpkgs", Attr: "python313Packages.requests",
	})

	if !got.OK || got.Path != "/nix/store/exact-requests" {
		t.Fatalf("got %#v", got)
	}
	if len(calls) != 3 {
		t.Fatalf("calls=%#v", calls)
	}
	for _, call := range calls {
		if strings.Contains(call, "/etc/profiles") || strings.Contains(call, ".nix-profile") {
			t.Fatalf("profile inference: %s", call)
		}
	}
}

func TestVerifyNixMissingOrMalformedGenerationFails(t *testing.T) {
	base := VerifySpec{Provider: ProviderNix, Kind: KindPackage, NixStoreBin: "nix-store", NixBin: "nix", HomeManagerBin: "home-manager", Repo: "/repo", System: "x86_64-linux", NixInput: "nixpkgs", Attr: "hello"}
	for _, output := range []string{"", "1 -> /tmp/not-store\n"} {
		r := fakeOutputRunner{responses: map[string]fakeOutputResponse{"home-manager generations": {out: output}}}
		got := Verify(context.Background(), r, nil, base)
		if got.OK || !strings.Contains(got.Detail, "generation") {
			t.Fatalf("got=%#v", got)
		}
	}
}

func TestVerifyNixLinuxGenerationRequiresExactEvaluatedStorePath(t *testing.T) {
	spec := VerifySpec{Provider: ProviderNix, Kind: KindPackage, NixStoreBin: "nix-store", NixBin: "nix", HomeManagerBin: "home-manager", Repo: "/repo", System: "x86_64-linux", NixInput: "nixpkgsUnstable", Attr: "hello"}
	r := fakeOutputRunner{responses: map[string]fakeOutputResponse{
		"home-manager generations": {out: "current -> /nix/store/linux-home-manager-generation\n"},
		`nix eval --raw --impure --expr (builtins.getAttr "hello" (builtins.getAttr "x86_64-linux" (builtins.getAttr "nixpkgsUnstable" (builtins.getFlake "/repo").inputs).legacyPackages)).outPath`: {out: "/nix/store/pinned-hello\n"},
		"nix-store -q --requisites /nix/store/linux-home-manager-generation": {out: "/nix/store/registry-drift-hello\n"},
	}}
	got := Verify(context.Background(), r, nil, spec)
	if got.OK || !strings.Contains(got.Detail, "absent") {
		t.Fatalf("got=%#v", got)
	}
}

func TestNixAttrExpressionQuotesEveryComponent(t *testing.T) {
	expr, err := nixAttrExpression(`/repo path`, `nixpkgsUnstable`, `x86_64-linux`, `1password.c++.quote"name`)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`builtins.getFlake "/repo path"`, `builtins.getAttr "nixpkgsUnstable"`, `builtins.getAttr "x86_64-linux"`, `builtins.getAttr "1password"`, `builtins.getAttr "c++"`, `builtins.getAttr "quote\"name"`} {
		if !strings.Contains(expr, want) {
			t.Fatalf("missing %q: %s", want, expr)
		}
	}
	for _, bad := range []string{"", `.hello`, `hello.`, `hello..world`, "hello.\nworld"} {
		if _, err := nixAttrExpression(`/repo`, `nixpkgs`, `x86_64-linux`, bad); err == nil {
			t.Fatalf("accepted %q", bad)
		}
	}
}

func TestVerifyNixWithoutProviderEvidenceFailsClosed(t *testing.T) {
	for _, spec := range []VerifySpec{
		{Provider: ProviderNix, Kind: KindPackage},
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

	runner.responses["brew list --formula --versions yazi"] = fakeOutputResponse{out: "yazi 1\n"}
	got := Verify(context.Background(), runner, lookup, VerifySpec{
		Provider: ProviderBrew, BrewBin: "brew",
		Kind:        KindFormula,
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

	got := Verify(context.Background(), fakeOutputRunner{responses: map[string]fakeOutputResponse{"brew list --formula --versions yazi": {out: "yazi 1\n"}}}, lookup, VerifySpec{
		Provider: ProviderBrew, BrewBin: "brew",
		Kind:       KindFormula,
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
