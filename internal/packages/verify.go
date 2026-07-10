package packages

import (
	"context"
	"fmt"
	"os"
	"strings"
)

func Verify(ctx context.Context, runner OutputRunner, lookup PathLookup, spec VerifySpec) VerifyResult {
	switch spec.Provider {
	case ProviderNix:
		if spec.Kind != KindPackage {
			return verificationFailure("unsupported Nix package kind %q", spec.Kind)
		}
		if spec.Executable == "" {
			return verificationFailure("verification requires an executable for provider %q", spec.Provider)
		}
		return verifyExecutable(ctx, runner, lookup, spec)
	case ProviderBrew:
		switch spec.Kind {
		case KindFormula:
			if spec.Executable != "" {
				return verifyExecutable(ctx, runner, lookup, spec)
			}
			return verifyBrewReceipt(ctx, runner, spec, KindFormula)
		case KindCask:
			return verifyBrewCask(ctx, runner, lookup, spec)
		default:
			return verificationFailure("unsupported Brew package kind %q", spec.Kind)
		}
	default:
		return verificationFailure("unsupported package provider %q", spec.Provider)
	}
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
