package fileedit

import "errors"

type Proposal struct {
	Path         string
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

type PreparedEdit struct {
	state *exchangeApplyState
}

var ErrStaleFile = errors.New("file changed after review")
