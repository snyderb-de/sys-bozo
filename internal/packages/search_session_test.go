package packages

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"
	"time"
)

type fakeAdapter struct {
	provider   Provider
	gate       <-chan struct{}
	candidates []Candidate
	err        error
}

type brewMaxPhaseAdapter struct {
	reported chan<- struct{}
}

func (brewMaxPhaseAdapter) Provider() Provider { return ProviderBrew }

func (a brewMaxPhaseAdapter) Search(_ context.Context, _ string, report PhaseReporter) ([]Candidate, error) {
	report(SearchQuerying)
	report(SearchParsing)
	report(SearchQuerying)
	report(SearchParsing)
	close(a.reported)
	return []Candidate{{Provider: ProviderBrew, ID: "brew-result"}}, nil
}

func TestStartSearchDeliversTerminalAfterSlowConsumerAndBrewMaxPhases(t *testing.T) {
	reported := make(chan struct{})
	events := StartSearch(context.Background(), SearchRequest{RequestID: 13, Query: "tool"}, []SearchAdapter{
		brewMaxPhaseAdapter{reported: reported},
	})

	// Let starting plus Homebrew's four command phases fill the four-slot buffer.
	// Reading one event releases the final phase, then this consumer pauses again.
	time.Sleep(10 * time.Millisecond)
	<-events
	<-reported
	time.Sleep(10 * time.Millisecond)

	var terminal *SearchEvent
	for event := range events {
		if event.Phase == SearchDone {
			event := event
			terminal = &event
		}
	}
	if terminal == nil || len(terminal.Candidates) != 1 || terminal.Candidates[0].ID != "brew-result" {
		t.Fatalf("terminal=%#v", terminal)
	}
}

type outputBearingError struct {
	output string
}

func (e *outputBearingError) Error() string { return "provider failed: " + e.output }

func TestStartSearchSanitizesProviderErrors(t *testing.T) {
	secret := "SECRET\n" + strings.Repeat("stderr", 200)
	errorsToTest := []error{
		&exec.ExitError{Stderr: []byte(secret)},
		&outputBearingError{output: secret},
	}
	for _, providerErr := range errorsToTest {
		events := StartSearch(context.Background(), SearchRequest{RequestID: 14, Query: "tool"}, []SearchAdapter{
			fakeAdapter{provider: ProviderDNF, err: providerErr},
		})
		var sawFailed bool
		for event := range events {
			if event.Phase != SearchFailed {
				continue
			}
			sawFailed = true
			if event.Err == nil {
				t.Fatal("failed event has nil error")
			}
			if strings.Contains(event.Err.Error(), "SECRET") || strings.ContainsAny(event.Err.Error(), "\r\n\t") {
				t.Fatalf("unsafe event error %q", event.Err)
			}
			if len(event.Err.Error()) > 64 {
				t.Fatalf("event error length=%d", len(event.Err.Error()))
			}
			var exitErr *exec.ExitError
			var outputErr *outputBearingError
			if errors.As(event.Err, &exitErr) || errors.As(event.Err, &outputErr) {
				t.Fatalf("raw provider error recoverable from %#v", event.Err)
			}
		}
		if !sawFailed {
			t.Fatal("missing failed provider event")
		}
	}
}

func TestStartSearchPreservesContextErrorClassification(t *testing.T) {
	tests := []struct {
		name  string
		err   error
		phase SearchPhase
	}{
		{name: "cancelled", err: errors.New("wrapped: " + context.Canceled.Error()), phase: SearchFailed},
		{name: "context cancelled", err: errors.Join(errors.New("provider detail"), context.Canceled), phase: SearchCancelled},
		{name: "deadline", err: errors.Join(errors.New("provider detail"), context.DeadlineExceeded), phase: SearchTimedOut},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			events := StartSearch(context.Background(), SearchRequest{RequestID: 15, Query: "tool"}, []SearchAdapter{
				fakeAdapter{provider: ProviderAPT, err: tt.err},
			})
			for event := range events {
				if event.Phase != tt.phase {
					continue
				}
				if tt.phase == SearchCancelled && !errors.Is(event.Err, context.Canceled) {
					t.Fatalf("Err=%v does not match context.Canceled", event.Err)
				}
				if tt.phase == SearchTimedOut && !errors.Is(event.Err, context.DeadlineExceeded) {
					t.Fatalf("Err=%v does not match context.DeadlineExceeded", event.Err)
				}
				return
			}
			t.Fatalf("missing terminal phase %s", tt.phase)
		})
	}
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
		sawFailure = sawFailure || event.Provider == ProviderNix && event.Phase == SearchFailed && event.Err != nil && !errors.Is(event.Err, wantErr)
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
