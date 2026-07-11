package packages

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

type PhaseReporter func(SearchPhase)

type SearchAdapter interface {
	Provider() Provider
	Search(context.Context, string, PhaseReporter) ([]Candidate, error)
}

type commandSearchAdapter struct {
	provider Provider
	runner   OutputRunner
	command  string
}

func (a commandSearchAdapter) Provider() Provider { return a.provider }

func (a commandSearchAdapter) Search(ctx context.Context, query string, report PhaseReporter) ([]Candidate, error) {
	var candidates []Candidate
	var err error
	switch a.provider {
	case ProviderNix:
		candidates, err = searchNix(ctx, a.runner, a.command, query, report)
	case ProviderBrew:
		candidates, err = searchBrew(ctx, a.runner, a.command, query, report)
	case ProviderDNF:
		candidates, err = searchDNF(ctx, a.runner, a.command, query, report)
	case ProviderAPT:
		candidates, err = searchAPT(ctx, a.runner, a.command, query, report)
	default:
		err = fmt.Errorf("unsupported search provider %q", a.provider)
	}
	return candidates, safeSearchError(err)
}

func NewSearchAdapters(specs []ProviderSpec, runner OutputRunner) []SearchAdapter {
	adapters := make([]SearchAdapter, 0, len(specs))
	for _, spec := range specs {
		if spec.Enabled {
			adapters = append(adapters, commandSearchAdapter{provider: spec.Provider, runner: runner, command: spec.Command})
		}
	}
	return adapters
}

type ExecRunner struct{}

func (ExecRunner) Output(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).Output()
}

type providerSearchResult struct {
	provider   Provider
	candidates []Candidate
	err        error
}

func Search(ctx context.Context, runner OutputRunner, nixCommand, brewCommand, query string) SearchResult {
	results := make(chan providerSearchResult, 2)

	go func() {
		candidates, err := searchNix(ctx, runner, nixCommand, query, nil)
		results <- providerSearchResult{provider: ProviderNix, candidates: candidates, err: err}
	}()
	go func() {
		candidates, err := searchBrew(ctx, runner, brewCommand, query, nil)
		results <- providerSearchResult{provider: ProviderBrew, candidates: candidates, err: err}
	}()

	var nixResult, brewResult providerSearchResult
	for range 2 {
		result := <-results
		switch result.provider {
		case ProviderNix:
			nixResult = result
		case ProviderBrew:
			brewResult = result
		}
	}

	candidates := make([]Candidate, 0, len(nixResult.candidates)+len(brewResult.candidates))
	candidates = append(candidates, nixResult.candidates...)
	candidates = append(candidates, brewResult.candidates...)

	return SearchResult{
		Candidates: candidates,
		Selected:   0,
		NixErr:     nixResult.err,
		BrewErr:    brewResult.err,
	}
}

type nixPackage struct {
	PName       string `json:"pname"`
	Version     string `json:"version"`
	Description string `json:"description"`
}

func searchNix(ctx context.Context, runner OutputRunner, command, query string, report PhaseReporter) ([]Candidate, error) {
	reportPhase(report, SearchQuerying)
	output, err := runner.Output(ctx, command, "search", "--json", "nixpkgs", query)
	if err != nil {
		return nil, err
	}
	reportPhase(report, SearchParsing)

	packages := make(map[string]nixPackage)
	if err := json.Unmarshal(output, &packages); err != nil {
		return nil, err
	}

	attributes := make([]string, 0, len(packages))
	for attribute := range packages {
		attributes = append(attributes, attribute)
	}
	sort.Strings(attributes)

	candidates := make([]Candidate, 0, len(attributes))
	for _, attribute := range attributes {
		pkg := packages[attribute]
		candidates = append(candidates, Candidate{
			Provider:    ProviderNix,
			Kind:        KindPackage,
			ID:          nixCandidateID(attribute),
			Name:        pkg.PName,
			Version:     pkg.Version,
			Description: pkg.Description,
		})
	}

	return candidates, nil
}

func nixCandidateID(attribute string) string {
	parts := strings.Split(attribute, ".")
	if len(parts) >= 3 && (parts[0] == "packages" || parts[0] == "legacyPackages") && knownNixSystem(parts[1]) {
		return strings.Join(parts[2:], ".")
	}
	return attribute
}

func knownNixSystem(system string) bool {
	switch system {
	case "aarch64-darwin", "x86_64-darwin", "aarch64-linux", "x86_64-linux", "i686-linux", "armv7l-linux", "riscv64-linux", "powerpc64le-linux":
		return true
	default:
		return false
	}
}

func searchBrew(ctx context.Context, runner OutputRunner, command, query string, report PhaseReporter) ([]Candidate, error) {
	reportPhase(report, SearchQuerying)
	formulaOutput, formulaErr := runner.Output(ctx, command, "search", "--formula", query)
	if formulaErr == nil {
		reportPhase(report, SearchParsing)
	}
	reportPhase(report, SearchQuerying)
	caskOutput, caskErr := runner.Output(ctx, command, "search", "--cask", query)
	if caskErr == nil {
		reportPhase(report, SearchParsing)
	}

	var formulae []Candidate
	if formulaErr == nil {
		formulae = brewCandidates(formulaOutput, KindFormula)
	}
	var casks []Candidate
	if caskErr == nil {
		casks = brewCandidates(caskOutput, KindCask)
	}
	candidates := make([]Candidate, 0, len(formulae)+len(casks))
	candidates = append(candidates, formulae...)
	candidates = append(candidates, casks...)

	return candidates, errors.Join(formulaErr, caskErr)
}

func searchDNF(ctx context.Context, runner OutputRunner, command, query string, report PhaseReporter) ([]Candidate, error) {
	reportPhase(report, SearchQuerying)
	output, err := runner.Output(ctx, command, "search", "--all", "--quiet", query)
	if err != nil {
		return nil, err
	}
	reportPhase(report, SearchParsing)
	return nativeCandidates(output, ProviderDNF, " : ", dnfCandidateID), nil
}

func searchAPT(ctx context.Context, runner OutputRunner, command, query string, report PhaseReporter) ([]Candidate, error) {
	reportPhase(report, SearchQuerying)
	output, err := runner.Output(ctx, command, "search", "--names-only", query)
	if err != nil {
		return nil, err
	}
	reportPhase(report, SearchParsing)
	return nativeCandidates(output, ProviderAPT, " - ", func(id string) string { return id }), nil
}

func reportPhase(report PhaseReporter, phase SearchPhase) {
	if report != nil {
		report(phase)
	}
}

func nativeCandidates(output []byte, provider Provider, separator string, normalizeID func(string) string) []Candidate {
	candidates := make([]Candidate, 0)
	for _, line := range strings.Split(string(output), "\n") {
		idAndDescription := strings.SplitN(line, separator, 2)
		if len(idAndDescription) != 2 {
			continue
		}
		rawID := idAndDescription[0]
		if !validCandidateID(rawID) {
			continue
		}
		id := normalizeID(rawID)
		if !validCandidateID(id) {
			continue
		}
		candidates = append(candidates, Candidate{
			Provider:    provider,
			Kind:        KindPackage,
			ID:          id,
			Name:        id,
			Description: boundDescription(strings.TrimSpace(idAndDescription[1])),
		})
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].ID < candidates[j].ID })
	return candidates
}

func dnfCandidateID(id string) string {
	dot := strings.LastIndexByte(id, '.')
	if dot == -1 {
		return id
	}
	architecture := id[dot+1:]
	switch architecture {
	case "aarch64", "armv7hl", "i386", "i486", "i586", "i686", "noarch", "ppc64", "ppc64le", "s390x", "src", "x86_64":
		return id[:dot]
	default:
		return id
	}
}

func validCandidateID(id string) bool {
	return id != "" && strings.IndexFunc(id, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsControl(r)
	}) == -1
}

func boundDescription(description string) string {
	description = strings.ToValidUTF8(description, "\uFFFD")
	if len(description) > 512 {
		end := 512
		for !utf8.ValidString(description[:end]) {
			end--
		}
		return description[:end]
	}
	return description
}

func brewCandidates(output []byte, kind Kind) []Candidate {
	names := make([]string, 0)
	for _, line := range strings.Split(string(output), "\n") {
		name := strings.TrimSpace(line)
		if name == "" || strings.Contains(name, "=>") {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)

	candidates := make([]Candidate, 0, len(names))
	for _, name := range names {
		candidates = append(candidates, Candidate{
			Provider: ProviderBrew,
			Kind:     kind,
			ID:       name,
			Name:     name,
		})
	}
	return candidates
}
