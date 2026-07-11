package packages

import (
	"context"
	"errors"
	"sync"
	"time"
)

const SearchSessionFinished SearchPhase = "session-finished"

type SearchRequest struct {
	RequestID uint64
	Query     string
}

type SearchEvent struct {
	RequestID  uint64
	Provider   Provider
	Phase      SearchPhase
	Candidates []Candidate
	Err        error
	At         time.Time
}

func StartSearch(ctx context.Context, request SearchRequest, adapters []SearchAdapter) <-chan SearchEvent {
	bufferSize := len(adapters) * 4
	if bufferSize < 4 {
		bufferSize = 4
	}
	events := make(chan SearchEvent, bufferSize)

	emit := func(event SearchEvent) {
		select {
		case events <- event:
		case <-ctx.Done():
		}
	}
	emitTerminal := func(event SearchEvent) {
		select {
		case events <- event:
		default:
		}
	}

	var adaptersDone sync.WaitGroup
	adaptersDone.Add(len(adapters))
	for _, adapter := range adapters {
		adapter := adapter
		go func() {
			defer adaptersDone.Done()
			provider := adapter.Provider()
			emit(newSearchEvent(request.RequestID, provider, SearchStarting, nil, nil))
			candidates, err := adapter.Search(ctx, request.Query, func(phase SearchPhase) {
				emit(newSearchEvent(request.RequestID, provider, phase, nil, nil))
			})

			phase := SearchDone
			switch {
			case errors.Is(err, context.Canceled):
				phase = SearchCancelled
			case errors.Is(err, context.DeadlineExceeded):
				phase = SearchTimedOut
			case err != nil:
				phase = SearchFailed
			}
			emitTerminal(newSearchEvent(request.RequestID, provider, phase, candidates, err))
		}()
	}

	go func() {
		adaptersDone.Wait()
		emitTerminal(newSearchEvent(request.RequestID, "", SearchSessionFinished, nil, nil))
		close(events)
	}()

	return events
}

func newSearchEvent(requestID uint64, provider Provider, phase SearchPhase, candidates []Candidate, err error) SearchEvent {
	return SearchEvent{
		RequestID:  requestID,
		Provider:   provider,
		Phase:      phase,
		Candidates: append([]Candidate(nil), candidates...),
		Err:        err,
		At:         time.Now(),
	}
}
