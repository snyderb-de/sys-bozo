package packages

import (
	"context"
	"errors"
	"testing"
	"time"
)

type fakeAdapter struct {
	provider   Provider
	gate       <-chan struct{}
	candidates []Candidate
	err        error
}

func TestStartSearchCancellationClosesChannel(t *testing.T) {
	gate := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	events := StartSearch(ctx, SearchRequest{RequestID: 10, Query: "tool"}, []SearchAdapter{
		fakeAdapter{provider: ProviderNix, gate: gate},
	})
	cancel()

	var sawCancelled bool
	for event := range events {
		sawCancelled = sawCancelled || event.Phase == SearchCancelled
	}
	if !sawCancelled {
		t.Fatal("missing cancelled provider event")
	}
}

func TestStartSearchFailedProviderDoesNotStopSuccessfulProvider(t *testing.T) {
	wantErr := errors.New("provider unavailable")
	events := StartSearch(context.Background(), SearchRequest{RequestID: 11, Query: "tool"}, []SearchAdapter{
		fakeAdapter{provider: ProviderNix, err: wantErr},
		fakeAdapter{provider: ProviderAPT, candidates: []Candidate{{Provider: ProviderAPT, ID: "apt-result"}}},
	})

	var sawFailure, sawSuccess bool
	for event := range events {
		sawFailure = sawFailure || event.Provider == ProviderNix && event.Phase == SearchFailed && errors.Is(event.Err, wantErr)
		sawSuccess = sawSuccess || event.Provider == ProviderAPT && event.Phase == SearchDone && len(event.Candidates) == 1
	}
	if !sawFailure || !sawSuccess {
		t.Fatalf("failure=%v success=%v", sawFailure, sawSuccess)
	}
}

type noisyAdapter struct{}

func (noisyAdapter) Provider() Provider { return ProviderDNF }

func (noisyAdapter) Search(_ context.Context, _ string, report PhaseReporter) ([]Candidate, error) {
	for range 10_000 {
		report(SearchQuerying)
	}
	return nil, context.Canceled
}

func TestStartSearchDoesNotBlockSenderWhenConsumerCancels(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	events := StartSearch(ctx, SearchRequest{RequestID: 12, Query: "tool"}, []SearchAdapter{noisyAdapter{}})
	cancel()

	done := make(chan struct{})
	go func() {
		for range events {
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("search sender did not exit after cancellation")
	}
}

func (a fakeAdapter) Provider() Provider { return a.provider }

func (a fakeAdapter) Search(ctx context.Context, _ string, report PhaseReporter) ([]Candidate, error) {
	report(SearchQuerying)
	if a.gate != nil {
		select {
		case <-a.gate:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	report(SearchParsing)
	return a.candidates, a.err
}

func TestStartSearchEmitsProviderResultsImmediatelyAndCloses(t *testing.T) {
	nixGate := make(chan struct{})
	adapters := []SearchAdapter{
		fakeAdapter{provider: ProviderNix, gate: nixGate, candidates: []Candidate{{Provider: ProviderNix, ID: "nix-result"}}},
		fakeAdapter{provider: ProviderDNF, candidates: []Candidate{{Provider: ProviderDNF, ID: "dnf-result"}}},
	}
	events := StartSearch(context.Background(), SearchRequest{RequestID: 9, Query: "tool"}, adapters)
	first := <-events
	for first.Phase != SearchDone {
		first = <-events
	}
	if first.Provider != ProviderDNF || first.Candidates[0].ID != "dnf-result" {
		t.Fatalf("first done=%#v", first)
	}
	close(nixGate)
	var sawNix, sawFinished bool
	for event := range events {
		sawNix = sawNix || event.Provider == ProviderNix && event.Phase == SearchDone
		sawFinished = sawFinished || event.Phase == SearchSessionFinished
	}
	if !sawNix || !sawFinished {
		t.Fatalf("nix=%v finished=%v", sawNix, sawFinished)
	}
}
