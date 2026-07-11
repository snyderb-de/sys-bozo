package packages

import (
	"reflect"
	"testing"
)

func TestDetectProviderSpecsUsesExactlyOneNativeManager(t *testing.T) {
	tests := []struct {
		name string
		host HostCapabilities
		want []ProviderSpec
	}{
		{"mac", HostCapabilities{OS: "darwin", NixBin: "nix", BrewBin: "brew"}, []ProviderSpec{
			{Provider: ProviderNix, Label: "NIX", Command: "nix", Enabled: true},
			{Provider: ProviderBrew, Label: "HOMEBREW", Command: "brew", Enabled: true},
		}},
		{"fedora", HostCapabilities{OS: "linux", OSID: "fedora", NixBin: "nix", DnfBin: "dnf5"}, []ProviderSpec{
			{Provider: ProviderNix, Label: "NIX", Command: "nix", Enabled: true},
			{Provider: ProviderDNF, Label: "DNF", Command: "dnf5", Enabled: true},
		}},
		{"ubuntu missing apt", HostCapabilities{OS: "linux", OSID: "ubuntu", NixBin: "nix"}, []ProviderSpec{
			{Provider: ProviderNix, Label: "NIX", Command: "nix", Enabled: true},
			{Provider: ProviderAPT, Label: "APT", DisabledReason: "apt-cache is not installed"},
		}},
		{"arch unsupported", HostCapabilities{OS: "linux", OSID: "arch", NixBin: "nix"}, []ProviderSpec{
			{Provider: ProviderNix, Label: "NIX", Command: "nix", Enabled: true},
			{Provider: ProviderNativeUnsupported, Label: "PACMAN", DisabledReason: "pacman search is not supported yet"},
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := DetectProviderSpecs(tt.host); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got %#v want %#v", got, tt.want)
			}
		})
	}
}
