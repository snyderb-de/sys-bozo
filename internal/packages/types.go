package packages

import "context"

type Provider string

const (
	ProviderNix  Provider = "nix"
	ProviderBrew Provider = "brew"
)

type Kind string

const (
	KindPackage Kind = "package"
	KindFormula Kind = "formula"
	KindCask    Kind = "cask"
)

type Scope string

const (
	ScopeShared   Scope = "shared"
	ScopePlatform Scope = "platform"
	ScopeHost     Scope = "host"
)

type Candidate struct {
	Provider    Provider
	Kind        Kind
	ID          string
	Name        string
	Version     string
	Description string
}

type SearchResult struct {
	Candidates []Candidate
	Selected   int
	NixErr     error
	BrewErr    error
}

type OutputRunner interface {
	Output(context.Context, string, ...string) ([]byte, error)
}
