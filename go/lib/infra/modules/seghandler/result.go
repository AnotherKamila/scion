// Copyright 2019 Anapaya Systems
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//   http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package seghandler

import (
	"golang.org/x/xerrors"

	"github.com/scionproto/scion/go/lib/ctrl/path_mgmt"
	"github.com/scionproto/scion/go/lib/infra/modules/segverifier"
	"github.com/scionproto/scion/go/lib/serrors"
)

// Stats provides statistics about handling segments.
type Stats struct {
	// segDB contains stats about segment insertions/updates.
	segDB           SegStats
	segVerifyErrors int
	// VerifiedSegs contains all segments that were successfully verified.
	VerifiedSegs []*SegWithHP
	// StoredRevs contains all revocations that were verified and stored.
	StoredRevs []*path_mgmt.SignedRevInfo
	// VerifiedRevs contains all revocations that were verified.
	VerifiedRevs []*path_mgmt.SignedRevInfo
	revErrors    int
}

// SegsInserted returns the amount of inserted segments.
func (s Stats) SegsInserted() int {
	return len(s.segDB.InsertedSegs)
}

// SegsUpdated returns the amount of updated segments.
func (s Stats) SegsUpdated() int {
	return len(s.segDB.UpdatedSegs)
}

// SegVerifyErrors returns the amount of segment verification errors.
func (s Stats) SegVerifyErrors() int {
	return s.segVerifyErrors
}

// RevStored returns the amount of stored revocations.
func (s Stats) RevStored() int {
	return len(s.StoredRevs)
}

// RevDBErrs returns the amount of db errors for storing revocations.
func (s Stats) RevDBErrs() int {
	return len(s.StoredRevs) - len(s.VerifiedRevs)
}

// RevVerifyErrors returns the amount of verification errors for revocations.
func (s Stats) RevVerifyErrors() int {
	return s.revErrors
}

func (s *Stats) addStoredSegs(segs SegStats) {
	s.segDB.InsertedSegs = append(s.segDB.InsertedSegs, segs.InsertedSegs...)
	s.segDB.UpdatedSegs = append(s.segDB.UpdatedSegs, segs.UpdatedSegs...)
}

func (s *Stats) verificationErrs(errors []error) {
	for _, err := range errors {
		if xerrors.Is(err, segverifier.ErrRevocation) {
			s.revErrors++
		}
		if xerrors.Is(err, segverifier.ErrSegment) {
			s.segVerifyErrors++
		}
	}
}

// ProcessedResult is the result of handling a segment reply.
type ProcessedResult struct {
	early      chan int
	full       chan struct{}
	stats      Stats
	revs       []*path_mgmt.SignedRevInfo
	err        error
	verifyErrs serrors.List
}

// EarlyTriggerProcessed returns a channel that will contain the number of
// successfully stored segments once it is done processing the early trigger.
func (r *ProcessedResult) EarlyTriggerProcessed() <-chan int {
	return r.early
}

// FullReplyProcessed returns a channel that will be closed once the full reply
// has been processed.
func (r *ProcessedResult) FullReplyProcessed() <-chan struct{} {
	return r.full
}

// Stats provides insights about storage and verification of segments.
func (r *ProcessedResult) Stats() Stats {
	return r.stats
}

// Err indicates the error that happened when storing the segments. This should
// only be accessed after FullReplyProcessed channel has been closed.
func (r *ProcessedResult) Err() error {
	return r.err
}

// VerificationErrors returns the list of verification errors that happened.
func (r *ProcessedResult) VerificationErrors() serrors.List {
	return r.verifyErrs
}
