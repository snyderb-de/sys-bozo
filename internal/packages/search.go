package packages

import (
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"sort"
	"strings"
)

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
		candidates, err := searchNix(ctx, runner, nixCommand, query)
		results <- providerSearchResult{provider: ProviderNix, candidates: candidates, err: err}
	}()
	go func() {
		candidates, err := searchBrew(ctx, runner, brewCommand, query)
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

func searchNix(ctx context.Context, runner OutputRunner, command, query string) ([]Candidate, error) {
	output, err := runner.Output(ctx, command, "search", "--json", "nixpkgs", query)
	if err != nil {
		return nil, err
	}

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
	if len(parts) >= 3 && (parts[0] == "packages" || parts[0] == "legacyPackages") && parts[1] != "" {
		return strings.Join(parts[2:], ".")
	}
	return attribute
}

func searchBrew(ctx context.Context, runner OutputRunner, command, query string) ([]Candidate, error) {
	formulaOutput, formulaErr := runner.Output(ctx, command, "search", "--formula", query)
	caskOutput, caskErr := runner.Output(ctx, command, "search", "--cask", query)

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
