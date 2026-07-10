// Package fileedit owns neutral reviewed file-replacement metadata for flows
// that must not mutate a target until explicit confirmation.
package fileedit

import (
	"bytes"
	"crypto/sha256"

	"github.com/snyderb-de/sys-bozo/internal/packages"
)

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

var ErrStaleFile = packages.ErrStaleFile

func ProposeReplacement(path string, original, proposed []byte) Proposal {
	wrapped := packages.ProposeReplacement(packages.Target{Path: path}, original, proposed)
	return Proposal{
		Path: path, Original: bytes.Clone(wrapped.Original), Proposed: bytes.Clone(wrapped.Proposed),
		OriginalHash: wrapped.OriginalHash, ProposedHash: wrapped.ProposedHash, Diff: wrapped.Diff,
	}
}

func Apply(proposal Proposal) (AppliedEdit, error) {
	applied, err := packages.Apply(packages.Proposal{
		Target: packages.Target{Path: proposal.Path}, Original: bytes.Clone(proposal.Original), Proposed: bytes.Clone(proposal.Proposed),
		OriginalHash: proposal.OriginalHash, ProposedHash: proposal.ProposedHash, Diff: proposal.Diff,
	})
	if err != nil {
		return AppliedEdit{}, err
	}
	return AppliedEdit{
		Path: applied.Path, Before: bytes.Clone(applied.Before), After: bytes.Clone(applied.After),
		BeforeHash: applied.BeforeHash, AfterHash: applied.AfterHash,
	}, nil
}

func Valid(proposal Proposal) bool {
	return proposal.Path != "" && proposal.OriginalHash == sha256.Sum256(proposal.Original) && proposal.ProposedHash == sha256.Sum256(proposal.Proposed)
}
