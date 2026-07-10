package packages

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
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

func TestVerifyBrewCaskRequiresReceiptAndArtifact(t *testing.T) {
	runner := fakeOutputRunner{responses: map[string]struct {
		out string
		err error
	}{
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

func TestVerifyCLIRequiresSuccessfulVersionCommand(t *testing.T) {
	versionErr := errors.New("version failed")
	runner := fakeOutputRunner{responses: map[string]struct {
		out string
		err error
	}{
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

func TestVerifyBrewFormulaRequiresNonemptyReceipt(t *testing.T) {
	runner := fakeOutputRunner{responses: map[string]struct {
		out string
		err error
	}{
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

func TestVerifyBrewCaskFailsWhenArtifactIsMissing(t *testing.T) {
	runner := fakeOutputRunner{responses: map[string]struct {
		out string
		err error
	}{
		"brew list --cask --versions zed": {out: "zed 0.190.0\n"},
	}}
	missingApp := filepath.Join(t.TempDir(), "Zed.app")

	got := Verify(context.Background(), runner, nil, VerifySpec{
		Provider: ProviderBrew,
		Kind:     KindCask,
		Token:    "zed",
		AppPath:  missingApp,
		BrewBin:  "brew",
	})

	if got.OK || !errors.Is(got.Err, os.ErrNotExist) || !strings.Contains(got.Detail, missingApp) {
		t.Fatalf("got %#v", got)
	}
}
