package packages

import (
	"bytes"

	"github.com/snyderb-de/sys-bozo/internal/fileedit"
)

var ErrStaleFile = fileedit.ErrStaleFile

func Apply(proposal Proposal) (AppliedEdit, error) {
	applied, err := fileedit.Apply(toFileEditProposal(proposal))
	return finishApply(proposal.Target, applied, err)
}

func finishApply(target Target, applied fileedit.AppliedEdit, err error) (AppliedEdit, error) {
	return AppliedEdit{
		Target:     target,
		Path:       applied.Path,
		Before:     bytes.Clone(applied.Before),
		After:      bytes.Clone(applied.After),
		BeforeHash: applied.BeforeHash,
		AfterHash:  applied.AfterHash,
	}, err
}

func ProposeRevert(applied AppliedEdit) (Proposal, error) {
	revert, err := fileedit.ProposeRevert(fileedit.AppliedEdit{
		Path: applied.Path, Before: bytes.Clone(applied.Before), After: bytes.Clone(applied.After),
		BeforeHash: applied.BeforeHash, AfterHash: applied.AfterHash,
	})
	if err != nil {
		return Proposal{}, err
	}
	target := applied.Target
	if target.Path == "" {
		target.Path = applied.Path
	}
	return fromFileEditProposal(target, revert), nil
}

func ProposeReplacement(target Target, original, proposed []byte) Proposal {
	return fromFileEditProposal(target, fileedit.ProposeReplacement(target.Path, original, proposed))
}

func toFileEditProposal(proposal Proposal) fileedit.Proposal {
	return fileedit.Proposal{
		Path: proposal.Target.Path, Original: bytes.Clone(proposal.Original), Proposed: bytes.Clone(proposal.Proposed),
		OriginalHash: proposal.OriginalHash, ProposedHash: proposal.ProposedHash, Diff: proposal.Diff,
	}
}

func fromFileEditProposal(target Target, proposal fileedit.Proposal) Proposal {
	return Proposal{
		Target: target, Original: bytes.Clone(proposal.Original), Proposed: bytes.Clone(proposal.Proposed),
		OriginalHash: proposal.OriginalHash, ProposedHash: proposal.ProposedHash, Diff: proposal.Diff,
	}
}
