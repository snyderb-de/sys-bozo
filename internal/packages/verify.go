package packages

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"
)

func Verify(ctx context.Context, runner OutputRunner, lookup PathLookup, spec VerifySpec) VerifyResult {
	if len(spec.VersionArgs) > 0 && spec.Executable == "" {
		return verificationFailure("version arguments require an executable")
	}

	switch spec.Provider {
	case ProviderNix:
		if spec.Kind != KindPackage {
			return verificationFailure("unsupported Nix package kind %q", spec.Kind)
		}
		provider := verifyNixGenerationClosure(ctx, runner, spec)
		if !provider.OK || spec.Executable == "" {
			return provider
		}
		executable := verifyExecutable(ctx, runner, lookup, spec)
		if executable.OK {
			executable.Detail = provider.Detail + "; " + executable.Detail
		}
		return executable
	case ProviderBrew:
		switch spec.Kind {
		case KindFormula:
			receipt := verifyBrewReceipt(ctx, runner, spec, KindFormula)
			if !receipt.OK || spec.Executable == "" {
				return receipt
			}
			executable := verifyExecutable(ctx, runner, lookup, spec)
			if executable.OK {
				executable.Detail = receipt.Detail + "; " + executable.Detail
			}
			return executable
		case KindCask:
			return verifyBrewCask(ctx, runner, lookup, spec)
		default:
			return verificationFailure("unsupported Brew package kind %q", spec.Kind)
		}
	default:
		return verificationFailure("unsupported package provider %q", spec.Provider)
	}
}

var nixToken = regexp.MustCompile(`^[A-Za-z0-9_+.-]+$`)

func verifyNixGenerationClosure(ctx context.Context, runner OutputRunner, spec VerifySpec) VerifyResult {
	if spec.NixStoreBin == "" || spec.NixBin == "" || spec.HomeManagerBin == "" || spec.Repo == "" || spec.System == "" || spec.NixInput == "" || spec.Attr == "" {
		return verificationFailure("Nix applied evidence requires nix-store, nix, home-manager, repo, system, input, and attr")
	}
	if runner == nil {
		return verificationFailure("Nix provider evidence requires a command runner")
	}
	if !nixToken.MatchString(spec.System) || !nixToken.MatchString(spec.NixInput) {
		return verificationFailure("invalid Nix system or input token")
	}
	for _, part := range strings.Split(spec.Attr, ".") {
		if !nixToken.MatchString(part) {
			return verificationFailure("invalid Nix attribute token")
		}
	}
	generations, err := runner.Output(ctx, spec.HomeManagerBin, "generations")
	if err != nil {
		return VerifyResult{Detail: "Home Manager generations could not be queried", Err: err}
	}
	generation := ""
	for _, line := range strings.Split(string(generations), "\n") {
		if at := strings.Index(line, "-> "); at >= 0 {
			candidate := strings.TrimSpace(line[at+3:])
			if strings.HasPrefix(candidate, "/nix/store/") && strings.HasSuffix(candidate, "-home-manager-generation") {
				generation = candidate
				break
			}
		}
	}
	if generation == "" {
		return verificationFailure("Home Manager newest generation store path is missing or malformed")
	}
	expr := fmt.Sprintf(`(builtins.getFlake %q).inputs.%s.legacyPackages.%s.%s.outPath`, spec.Repo, spec.NixInput, spec.System, spec.Attr)
	evaluated, err := runner.Output(ctx, spec.NixBin, "eval", "--raw", "--impure", "--expr", expr)
	if err != nil {
		return VerifyResult{Detail: "pinned Nix package outPath could not be evaluated", Err: err}
	}
	outPath := strings.TrimSpace(string(evaluated))
	if !strings.HasPrefix(outPath, "/nix/store/") {
		return verificationFailure("evaluated Nix outPath is malformed")
	}
	closure, err := runner.Output(ctx, spec.NixStoreBin, "-q", "--requisites", generation)
	if err != nil {
		return VerifyResult{Detail: "Home Manager generation closure could not be queried", Err: err}
	}
	for _, line := range strings.Split(string(closure), "\n") {
		if strings.TrimSpace(line) == outPath {
			return VerifyResult{OK: true, Path: outPath, Detail: "exact pinned Nix package outPath verified in applied Home Manager generation"}
		}
	}
	return verificationFailure("exact pinned Nix package outPath is absent from applied Home Manager generation")
}

func verifyBrewCask(ctx context.Context, runner OutputRunner, lookup PathLookup, spec VerifySpec) VerifyResult {
	result := verifyBrewReceipt(ctx, runner, spec, KindCask)
	if !result.OK {
		return result
	}
	if spec.AppPath != "" {
		if _, err := os.Stat(spec.AppPath); err != nil {
			return VerifyResult{
				Detail: fmt.Sprintf("Brew cask artifact %q is unavailable", spec.AppPath),
				Err:    err,
			}
		}
		result.Detail = fmt.Sprintf("Brew cask receipt and artifact %q verified", spec.AppPath)
	}
	if spec.Executable == "" {
		return result
	}

	executableResult := verifyExecutable(ctx, runner, lookup, spec)
	if !executableResult.OK {
		return executableResult
	}
	executableResult.Detail = result.Detail + "; " + executableResult.Detail
	return executableResult
}

func verifyExecutable(ctx context.Context, runner OutputRunner, lookup PathLookup, spec VerifySpec) VerifyResult {
	if lookup == nil {
		return verificationFailure("executable verification requires a path lookup")
	}
	path, err := lookup(spec.Executable)
	if err != nil {
		return VerifyResult{
			Detail: fmt.Sprintf("executable %q could not be resolved", spec.Executable),
			Err:    err,
		}
	}
	if path == "" {
		return verificationFailure("executable %q could not be resolved to a non-empty path", spec.Executable)
	}
	if len(spec.VersionArgs) == 0 {
		return VerifyResult{
			OK:     true,
			Path:   path,
			Detail: fmt.Sprintf("executable %q resolved to %q", spec.Executable, path),
		}
	}
	if runner == nil {
		return verificationFailure("executable version verification requires a command runner")
	}
	if _, err := runner.Output(ctx, path, spec.VersionArgs...); err != nil {
		return VerifyResult{
			Detail: fmt.Sprintf("version command for executable %q failed", spec.Executable),
			Err:    err,
		}
	}
	return VerifyResult{
		OK:     true,
		Path:   path,
		Detail: fmt.Sprintf("executable %q resolved to %q and its version command succeeded", spec.Executable, path),
	}
}

func verifyBrewReceipt(ctx context.Context, runner OutputRunner, spec VerifySpec, kind Kind) VerifyResult {
	if spec.BrewBin == "" {
		return verificationFailure("Brew verification requires a brew executable")
	}
	if spec.Token == "" {
		return verificationFailure("Brew verification requires a package token")
	}
	if runner == nil {
		return verificationFailure("Brew verification requires a command runner")
	}
	output, err := runner.Output(ctx, spec.BrewBin, "list", "--"+string(kind), "--versions", spec.Token)
	if err != nil {
		return VerifyResult{
			Detail: fmt.Sprintf("Brew %s receipt for %q could not be verified", kind, spec.Token),
			Err:    err,
		}
	}
	if strings.TrimSpace(string(output)) == "" {
		return verificationFailure("Brew %s receipt for %q was not found", kind, spec.Token)
	}
	return VerifyResult{
		OK:     true,
		Detail: fmt.Sprintf("Brew %s receipt for %q verified", kind, spec.Token),
	}
}

func verificationFailure(format string, args ...any) VerifyResult {
	err := fmt.Errorf(format, args...)
	return VerifyResult{Detail: err.Error(), Err: err}
}
