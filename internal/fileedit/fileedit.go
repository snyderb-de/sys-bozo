// Package fileedit owns neutral reviewed file-replacement metadata and the
// hardened single-file compare-and-swap engine used by confirmed edit flows.
package fileedit

import "crypto/sha256"

func Valid(proposal Proposal) bool {
	return proposal.Path != "" && proposal.OriginalHash == sha256.Sum256(proposal.Original) && proposal.ProposedHash == sha256.Sum256(proposal.Proposed)
}
