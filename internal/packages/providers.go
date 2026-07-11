package packages

import "strings"

func DetectProviderSpecs(host HostCapabilities) []ProviderSpec {
	var specs []ProviderSpec
	if host.NixBin != "" {
		specs = append(specs, ProviderSpec{Provider: ProviderNix, Label: "NIX", Command: host.NixBin, Enabled: true})
	}

	native := ProviderSpec{}
	switch {
	case host.OS == "darwin":
		native = availableSpec(ProviderBrew, "HOMEBREW", host.BrewBin, "Homebrew is not installed")
	case host.OS == "linux" && host.OSID == "fedora":
		native = availableSpec(ProviderDNF, "DNF", host.DnfBin, "dnf/dnf5 is not installed")
	case host.OS == "linux" && (host.OSID == "debian" || host.OSID == "ubuntu"):
		native = availableSpec(ProviderAPT, "APT", host.AptCacheBin, "apt-cache is not installed")
	case host.OS == "linux" && (host.OSID == "arch" || host.OSID == "manjaro"):
		native = ProviderSpec{Provider: ProviderNativeUnsupported, Label: "PACMAN", DisabledReason: "pacman search is not supported yet"}
	case host.OS == "linux" && strings.Contains(host.OSID, "suse"):
		native = ProviderSpec{Provider: ProviderNativeUnsupported, Label: "ZYPPER", DisabledReason: "zypper search is not supported yet"}
	case host.OS == "linux" && host.OSID == "alpine":
		native = ProviderSpec{Provider: ProviderNativeUnsupported, Label: "APK", DisabledReason: "apk search is not supported yet"}
	case host.OS == "linux":
		native = ProviderSpec{Provider: ProviderNativeUnsupported, Label: "NATIVE", DisabledReason: "native search is not supported for " + valueOr(host.OSID, "this Linux distribution")}
	}
	if native.Label != "" {
		specs = append(specs, native)
	}
	return specs
}

func availableSpec(provider Provider, label, command, disabledReason string) ProviderSpec {
	spec := ProviderSpec{Provider: provider, Label: label, Command: command}
	if command == "" {
		spec.DisabledReason = disabledReason
		return spec
	}
	spec.Enabled = true
	return spec
}

func valueOr(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
