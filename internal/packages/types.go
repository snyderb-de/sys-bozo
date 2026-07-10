package packages

import (
	"context"
	"errors"
)

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

type Target struct {
	Path        string
	Assignment  string
	Quoted      bool
	ApplyAction string
}

type Section = string

type Proposal struct {
	Target       Target
	Original     []byte
	Proposed     []byte
	OriginalHash [32]byte
	ProposedHash [32]byte
	Diff         string
}

type AppliedEdit struct {
	Path       string
	Before     []byte
	After      []byte
	BeforeHash [32]byte
	AfterHash  [32]byte
}

var (
	ErrAlreadyDeclared   = errors.New("package already declared")
	ErrAmbiguousTarget   = errors.New("declaration target is missing or ambiguous")
	ErrUnsupportedTarget = errors.New("package target is unsupported")
	ErrSectionNotFound   = errors.New("declaration section not found")
	ErrStaleFile         = errors.New("declaration file changed after review")
)

type Candidate struct {
	Provider Provider
	Kind     Kind
	ID       string
	Name     string
	Version  string
	// Executable is populated only from trusted provider metadata. It must never
	// be inferred from ID or Name.
	Executable  string
	Description string
}

type SearchResult struct {
	Candidates []Candidate
	Selected   int
	NixErr     error
	BrewErr    error
}

type VerifySpec struct {
	Provider    Provider
	Kind        Kind
	Token       string
	PName       string
	Version     string
	Executable  string
	VersionArgs []string
	AppPath     string
	BrewBin     string
	NixStoreBin string
	ProfilePath string
}

type VerifyResult struct {
	OK     bool
	Path   string
	Detail string
	Err    error
}

type PathLookup func(string) (string, error)

type OutputRunner interface {
	Output(context.Context, string, ...string) ([]byte, error)
}
