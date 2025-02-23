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

package trc_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/scionproto/scion/go/lib/addr"
	"github.com/scionproto/scion/go/lib/scrypto"
	trc "github.com/scionproto/scion/go/lib/scrypto/trc/v2"
	"github.com/scionproto/scion/go/lib/util"
	"github.com/scionproto/scion/go/lib/xtest"
)

// TestCommonUpdate tests shared error cases between regular and sensitive updates.
func TestCommonUpdate(t *testing.T) {
	tests := map[string]struct {
		Modify         func(updated, prev *trc.TRC)
		ExpectedErrMsg error
	}{
		"Trust reset": {
			Modify: func(updated, _ *trc.TRC) {
				*updated = *newBaseTRC(time.Now())
				updated.BaseVersion = updated.Version
			},
			ExpectedErrMsg: trc.ErrBaseNotUpdate,
		},
		"Invariant violation": {
			Modify: func(updated, _ *trc.TRC) {
				updated.Validity.NotAfter = updated.Validity.NotBefore
			},
			ExpectedErrMsg: trc.ErrInvalidValidityPeriod,
		},
		"Wrong ISD": {
			Modify: func(updated, _ *trc.TRC) {
				updated.ISD = updated.ISD + 1
			},
			ExpectedErrMsg: trc.ErrImmutableISD,
		},
		"Wrong Version": {
			Modify: func(updated, prev *trc.TRC) {
				updated.Version = prev.Version + 2
			},
			ExpectedErrMsg: trc.ErrInvalidVersionIncrement,
		},
		"Changed TrustResetAllowed": {
			Modify: func(updated, prev *trc.TRC) {
				*updated.TrustResetAllowedPtr = !prev.TrustResetAllowed()
			},
			ExpectedErrMsg: trc.ErrImmutableTrustResetAllowed,
		},
		"New NotBefore not in Validity": {
			Modify: func(updated, prev *trc.TRC) {
				updated.Validity = &scrypto.Validity{
					NotBefore: util.UnixTime{Time: prev.Validity.NotAfter.Add(time.Hour)},
					NotAfter:  util.UnixTime{Time: prev.Validity.NotAfter.Add(8760 * time.Hour)},
				}
			},
			ExpectedErrMsg: trc.ErrNotInsidePreviousValidityPeriod,
		},
		"Changed BaseVersion": {
			Modify: func(updated, prev *trc.TRC) {
				prev.Version = 5
				updated.Version = 6
				updated.BaseVersion = 2
			},
			ExpectedErrMsg: trc.ErrImmutableBaseVersion,
		},
	}
	for name, test := range tests {
		run := func(t *testing.T, trcs func(time.Time) (*trc.TRC, *trc.TRC), ut trc.UpdateType) {
			updated, prev := trcs(time.Now())
			test.Modify(updated, prev)
			v := trc.UpdateValidator{
				Prev: prev,
				Next: updated,
			}
			info, err := v.Validate()
			xtest.AssertErrorsIs(t, err, test.ExpectedErrMsg)
			if test.ExpectedErrMsg == nil {
				assert.Equal(t, ut, info.Type)
			}
		}
		t.Run(name+" (regular)", func(t *testing.T) {
			run(t, newRegularUpdate, trc.RegularUpdate)
		})
		sensitiveUpdate := func(now time.Time) (*trc.TRC, *trc.TRC) {
			updated, prev := newSensitiveUpdate(now)
			*updated.VotingQuorumPtr -= 1
			return updated, prev
		}
		t.Run(name+" (sensitive)", func(t *testing.T) {
			run(t, sensitiveUpdate, trc.SensitiveUpdate)
		})
	}
}

func TestSensitiveUpdate(t *testing.T) {
	tests := map[string]struct {
		Modify         func(updated, prev *trc.TRC)
		Info           trc.UpdateInfo
		ExpectedErrMsg error
	}{
		// Valid updates

		"Votes not cast by all": {
			Modify: func(updated, prev *trc.TRC) {
				*prev.VotingQuorumPtr -= 1
				delete(updated.Votes, a110)
			},
			Info: trc.UpdateInfo{
				Type: trc.SensitiveUpdate,
			},
		},
		"Decreased VotingQuorum": {
			Modify: func(updated, _ *trc.TRC) {
				*updated.VotingQuorumPtr -= 1
			},
			Info: trc.UpdateInfo{
				Type: trc.SensitiveUpdate,
			},
		},
		"Add new Issuing and Voting AS": {
			Modify: func(updated, _ *trc.TRC) {
				*updated.VotingQuorumPtr += 1
				updated.PrimaryASes[a190] = trc.PrimaryAS{
					Attributes: trc.Attributes{trc.Issuing, trc.Voting},
					Keys: map[trc.KeyType]scrypto.KeyMeta{
						trc.OnlineKey: {
							KeyVersion: 1,
							Algorithm:  scrypto.Ed25519,
							Key:        []byte{0, 190, 1},
						},
						trc.OfflineKey: {
							KeyVersion: 1,
							Algorithm:  scrypto.Ed25519,
							Key:        []byte{1, 190, 1},
						},
						trc.IssuingKey: {
							KeyVersion: 1,
							Algorithm:  scrypto.Ed25519,
							Key:        []byte{2, 190, 1},
						},
					},
				}
				updated.ProofOfPossession[a190] = []trc.KeyType{
					trc.OnlineKey, trc.OfflineKey, trc.IssuingKey}
			},
			Info: trc.UpdateInfo{
				Type: trc.SensitiveUpdate,
				KeyChanges: &trc.KeyChanges{
					Fresh: map[trc.KeyType]trc.ASToKeyMeta{
						trc.OnlineKey: {
							a190: {
								KeyVersion: 1,
								Algorithm:  scrypto.Ed25519,
								Key:        []byte{0, 190, 1},
							},
						},
						trc.OfflineKey: {
							a190: {
								KeyVersion: 1,
								Algorithm:  scrypto.Ed25519,
								Key:        []byte{1, 190, 1},
							},
						},
						trc.IssuingKey: {
							a190: {
								KeyVersion: 1,
								Algorithm:  scrypto.Ed25519,
								Key:        []byte{2, 190, 1},
							},
						},
					},
				},
				AttributeChanges: trc.AttributeChanges{
					a190: map[trc.Attribute]trc.AttributeChange{
						trc.Voting:  trc.AttributeAdded,
						trc.Issuing: trc.AttributeAdded,
					},
				},
			},
		},
		"Remove Voting AS": {
			Modify: func(updated, _ *trc.TRC) {
				*updated.VotingQuorumPtr -= 1
				delete(updated.PrimaryASes, a140)
			},
			Info: trc.UpdateInfo{
				Type: trc.SensitiveUpdate,
				AttributeChanges: trc.AttributeChanges{
					a140: map[trc.Attribute]trc.AttributeChange{
						trc.Voting: trc.AttributeRemoved,
					},
				},
			},
		},
		"Promote AS to Issuing": {
			Modify: func(updated, _ *trc.TRC) {
				primary := updated.PrimaryASes[a150]
				primary.Attributes = append(primary.Attributes, trc.Issuing)
				primary.Keys = map[trc.KeyType]scrypto.KeyMeta{
					trc.IssuingKey: {
						KeyVersion: 1,
						Algorithm:  scrypto.Ed25519,
						Key:        []byte{2, 150, 1},
					},
				}
				updated.PrimaryASes[a150] = primary
				updated.ProofOfPossession[a150] = []trc.KeyType{trc.IssuingKey}
			},
			Info: trc.UpdateInfo{
				Type: trc.SensitiveUpdate,
				KeyChanges: &trc.KeyChanges{
					Fresh: map[trc.KeyType]trc.ASToKeyMeta{
						trc.IssuingKey: {
							a150: {
								KeyVersion: 1,
								Algorithm:  scrypto.Ed25519,
								Key:        []byte{2, 150, 1},
							},
						},
					},
				},
			},
		},
		"Promote AS to Voting": {
			Modify: func(updated, _ *trc.TRC) {
				primary := updated.PrimaryASes[a130]
				primary.Attributes = trc.Attributes{trc.Issuing, trc.Core, trc.Voting}
				primary.Keys[trc.OnlineKey] = scrypto.KeyMeta{
					KeyVersion: 1,
					Algorithm:  scrypto.Ed25519,
					Key:        []byte{0, 130, 1},
				}
				primary.Keys[trc.OfflineKey] = scrypto.KeyMeta{
					KeyVersion: 1,
					Algorithm:  scrypto.Ed25519,
					Key:        []byte{1, 130, 1},
				}
				updated.PrimaryASes[a130] = primary
				updated.ProofOfPossession[a130] = []trc.KeyType{trc.OfflineKey, trc.OnlineKey}
			},
			Info: trc.UpdateInfo{
				Type: trc.SensitiveUpdate,
				KeyChanges: &trc.KeyChanges{
					Fresh: map[trc.KeyType]trc.ASToKeyMeta{
						trc.OnlineKey: {
							a130: {
								KeyVersion: 1,
								Algorithm:  scrypto.Ed25519,
								Key:        []byte{0, 130, 1},
							},
						},
						trc.OfflineKey: {
							a130: {
								KeyVersion: 1,
								Algorithm:  scrypto.Ed25519,
								Key:        []byte{1, 130, 1},
							},
						},
					},
				},
				AttributeChanges: trc.AttributeChanges{
					a130: map[trc.Attribute]trc.AttributeChange{
						trc.Voting: trc.AttributeAdded,
					},
				},
			},
		},
		"Demote AS from Voting": {
			Modify: func(updated, _ *trc.TRC) {
				*updated.VotingQuorumPtr -= 1
				primary := updated.PrimaryASes[a110]
				primary.Attributes = trc.Attributes{trc.Issuing, trc.Core}
				delete(primary.Keys, trc.OnlineKey)
				delete(primary.Keys, trc.OfflineKey)
				updated.PrimaryASes[a110] = primary
			},
			Info: trc.UpdateInfo{
				Type: trc.SensitiveUpdate,
				AttributeChanges: trc.AttributeChanges{
					a110: map[trc.Attribute]trc.AttributeChange{
						trc.Voting: trc.AttributeRemoved,
					},
				},
			},
		},
		"Demote AS from Issuing": {
			Modify: func(updated, _ *trc.TRC) {
				primary := updated.PrimaryASes[a110]
				primary.Attributes = trc.Attributes{trc.Voting, trc.Core}
				delete(primary.Keys, trc.IssuingKey)
				updated.PrimaryASes[a110] = primary
			},
			Info: trc.UpdateInfo{
				Type: trc.SensitiveUpdate,
				AttributeChanges: trc.AttributeChanges{
					a110: map[trc.Attribute]trc.AttributeChange{
						trc.Issuing: trc.AttributeRemoved,
					},
				},
			},
		},
		"Update offline key": {
			Modify: func(updated, _ *trc.TRC) {
				updated.PrimaryASes[a110].Keys[trc.OfflineKey] = scrypto.KeyMeta{
					KeyVersion: 2,
					Algorithm:  scrypto.Ed25519,
					Key:        []byte{1, 110, 2},
				}
				updated.ProofOfPossession[a110] = []trc.KeyType{trc.OfflineKey}
			},
			Info: trc.UpdateInfo{
				Type: trc.SensitiveUpdate,
				KeyChanges: &trc.KeyChanges{
					Modified: map[trc.KeyType]trc.ASToKeyMeta{
						trc.OfflineKey: {
							a110: {
								KeyVersion: 2,
								Algorithm:  scrypto.Ed25519,
								Key:        []byte{1, 110, 2},
							},
						},
					},
				},
			},
		},

		// Invalid updates

		"VotingQuorum zero": {
			Modify: func(updated, _ *trc.TRC) {
				*updated.VotingQuorumPtr = 0
			},
			ExpectedErrMsg: trc.ErrZeroVotingQuorum,
		},
		"VotingQuorum larger than voting ASes": {
			Modify: func(updated, _ *trc.TRC) {
				*updated.VotingQuorumPtr = uint8(updated.PrimaryASes.Count(trc.Voting) + 1)
			},
			ExpectedErrMsg: trc.ErrVotingQuorumTooLarge,
		},
		"Underflow voting quorum": {
			Modify: func(updated, _ *trc.TRC) {
				delete(updated.PrimaryASes, a140)
			},
			ExpectedErrMsg: trc.ErrVotingQuorumTooLarge,
		},
		"Vote quorum too small": {
			Modify: func(updated, _ *trc.TRC) {
				// Make sure this is a sensitive update
				*updated.VotingQuorumPtr = 1
				delete(updated.Votes, a120)
				delete(updated.Votes, a140)
			},
			ExpectedErrMsg: trc.ErrQuorumUnmet,
		},
		"New Voting AS that does not sign with offline key": {
			Modify: func(updated, _ *trc.TRC) {
				updated.PrimaryASes[a190] = trc.PrimaryAS{
					Attributes: trc.Attributes{trc.Voting, trc.Core},
					Keys: map[trc.KeyType]scrypto.KeyMeta{
						trc.OnlineKey: {
							KeyVersion: 1,
							Algorithm:  scrypto.Ed25519,
							Key:        []byte{0, 190, 1},
						},
						trc.OfflineKey: {
							KeyVersion: 1,
							Algorithm:  scrypto.Ed25519,
							Key:        []byte{1, 190, 1},
						},
					},
				}
				updated.ProofOfPossession[a190] = []trc.KeyType{trc.OnlineKey}
			},
			ExpectedErrMsg: trc.ErrMissingProofOfPossession,
		},
		"New Voting AS that does not sign with online key": {
			Modify: func(updated, _ *trc.TRC) {
				updated.PrimaryASes[a190] = trc.PrimaryAS{
					Attributes: trc.Attributes{trc.Voting, trc.Core},
					Keys: map[trc.KeyType]scrypto.KeyMeta{
						trc.OnlineKey: {
							KeyVersion: 1,
							Algorithm:  scrypto.Ed25519,
							Key:        []byte{0, 190, 1},
						},
						trc.OfflineKey: {
							KeyVersion: 1,
							Algorithm:  scrypto.Ed25519,
							Key:        []byte{1, 190, 1},
						},
					},
				}
				updated.ProofOfPossession[a190] = []trc.KeyType{trc.OnlineKey}
			},
			ExpectedErrMsg: trc.ErrMissingProofOfPossession,
		},
		"Promoted Issuing AS has no key": {
			Modify: func(updated, _ *trc.TRC) {
				primary := updated.PrimaryASes[a150]
				primary.Attributes = append(primary.Attributes, trc.Issuing)
				updated.PrimaryASes[a150] = primary
			},
			ExpectedErrMsg: trc.ErrMissingKey,
		},
		"Demoted AS keeps offline key": {
			Modify: func(updated, _ *trc.TRC) {
				*updated.VotingQuorumPtr -= 1
				primary := updated.PrimaryASes[a110]
				primary.Attributes = trc.Attributes{trc.Issuing, trc.Core}
				updated.PrimaryASes[a110] = primary
			},
			ExpectedErrMsg: trc.ErrUnexpectedKey,
		},
		"Unexpected proof of possession": {
			Modify: func(updated, _ *trc.TRC) {
				*updated.VotingQuorumPtr -= 1
				updated.ProofOfPossession[a110] = []trc.KeyType{trc.OnlineKey}
			},
			ExpectedErrMsg: trc.ErrUnexpectedProofOfPossession,
		},
		"Update offline key without proof of possession": {
			Modify: func(updated, _ *trc.TRC) {
				updated.PrimaryASes[a110].Keys[trc.OfflineKey] = scrypto.KeyMeta{
					KeyVersion: 2,
					Algorithm:  scrypto.Ed25519,
					Key:        []byte{1, 110, 2},
				}
			},
			ExpectedErrMsg: trc.ErrMissingProofOfPossession,
		},
		"Increase offline key version without modification": {
			Modify: func(updated, _ *trc.TRC) {
				meta := updated.PrimaryASes[a110].Keys[trc.OfflineKey]
				meta.KeyVersion = 2
				updated.PrimaryASes[a110].Keys[trc.OfflineKey] = meta
				updated.ProofOfPossession[a110] = append(updated.ProofOfPossession[a110],
					trc.OfflineKey)
			},
			ExpectedErrMsg: trc.ErrInvalidKeyVersion,
		},
		"Modify offline key without increasing version": {
			Modify: func(updated, _ *trc.TRC) {
				meta := updated.PrimaryASes[a110].Keys[trc.OfflineKey]
				meta.KeyVersion = 2
				updated.PrimaryASes[a110].Keys[trc.OfflineKey] = meta
				updated.ProofOfPossession[a110] = append(updated.ProofOfPossession[a110],
					trc.OfflineKey)
			},
			ExpectedErrMsg: trc.ErrInvalidKeyVersion,
		},
		"Increase offline key version by 2": {
			Modify: func(updated, _ *trc.TRC) {
				meta := updated.PrimaryASes[a110].Keys[trc.OfflineKey]
				meta.KeyVersion += 2
				meta.Key = []byte{1, 110, uint8(meta.KeyVersion)}
				updated.PrimaryASes[a110].Keys[trc.OfflineKey] = meta
				updated.ProofOfPossession[a110] = append(updated.ProofOfPossession[a110],
					trc.OfflineKey)
			},
			ExpectedErrMsg: trc.ErrInvalidKeyVersion,
		},
		"Signature from non-Voting AS": {
			Modify: func(updated, _ *trc.TRC) {
				*updated.VotingQuorumPtr -= 1
				updated.Votes[a130] = trc.Vote{
					KeyType:    trc.IssuingKey,
					KeyVersion: 1,
				}
			},
			ExpectedErrMsg: trc.ErrNoVotingRight,
		},
		"Signature from unknown AS": {
			Modify: func(updated, _ *trc.TRC) {
				updated.PrimaryASes[a190] = trc.PrimaryAS{
					Attributes: trc.Attributes{trc.Core},
				}
				updated.Votes[a190] = trc.Vote{
					KeyType:    trc.OnlineKey,
					KeyVersion: 1,
				}
			},
			ExpectedErrMsg: trc.ErrUnexpectedVote,
		},
		"Wrong KeyType on Vote": {
			Modify: func(updated, _ *trc.TRC) {
				*updated.VotingQuorumPtr -= 1
				updated.Votes[a110] = trc.Vote{
					KeyType:    trc.OnlineKey,
					KeyVersion: 1,
				}
			},
			ExpectedErrMsg: trc.ErrWrongVotingKeyType,
		},
		"Wrong KeyVersion": {
			Modify: func(updated, _ *trc.TRC) {
				*updated.VotingQuorumPtr -= 1
				updated.Votes[a110] = trc.Vote{
					KeyType:    trc.OfflineKey,
					KeyVersion: 10,
				}
			},
			ExpectedErrMsg: trc.ErrWrongVotingKeyVersion,
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			updated, prev := newSensitiveUpdate(time.Now())
			test.Modify(updated, prev)
			v := trc.UpdateValidator{
				Prev: prev,
				Next: updated,
			}
			info, err := v.Validate()
			xtest.AssertErrorsIs(t, err, test.ExpectedErrMsg)
			if test.ExpectedErrMsg == nil {
				assert.Equal(t, trc.SensitiveUpdate, info.Type)
				initKeyChanges(&test.Info)
				assert.Equal(t, test.Info.KeyChanges, info.KeyChanges)
				//assert.Equal(t, test.Info, info)
			}
		})
	}
}

func TestRegularUpdate(t *testing.T) {
	tests := map[string]struct {
		Modify         func(updated, prev *trc.TRC)
		Info           trc.UpdateInfo
		ExpectedErrMsg error
	}{
		// Valid updates

		"No modification": {
			Modify: func(_, _ *trc.TRC) {},
			Info: trc.UpdateInfo{
				Type: trc.RegularUpdate,
			},
		},
		"Update Description": {
			Modify: func(updated, _ *trc.TRC) {
				updated.Description = "This is the updated description"
			},
			Info: trc.UpdateInfo{
				Type: trc.RegularUpdate,
			},
		},
		"Update issuing key": {
			Modify: func(updated, _ *trc.TRC) {
				updated.PrimaryASes[a110].Keys[trc.IssuingKey] = scrypto.KeyMeta{
					KeyVersion: 2,
					Algorithm:  scrypto.Ed25519,
					Key:        []byte{2, 110, 2},
				}
				updated.ProofOfPossession[a110] = []trc.KeyType{trc.IssuingKey}
			},
			Info: trc.UpdateInfo{
				Type: trc.RegularUpdate,
				KeyChanges: &trc.KeyChanges{
					Modified: map[trc.KeyType]trc.ASToKeyMeta{
						trc.IssuingKey: {
							a110: {
								KeyVersion: 2,
								Algorithm:  scrypto.Ed25519,
								Key:        []byte{2, 110, 2},
							},
						},
					},
				},
			},
		},
		"Update online key": {
			Modify: func(updated, _ *trc.TRC) {
				updated.PrimaryASes[a110].Keys[trc.OnlineKey] = scrypto.KeyMeta{
					KeyVersion: 2,
					Algorithm:  scrypto.Ed25519,
					Key:        []byte{0, 110, 2},
				}
				updated.ProofOfPossession[a110] = []trc.KeyType{trc.OnlineKey}
				updated.Votes[a110] = trc.Vote{
					KeyType:    trc.OfflineKey,
					KeyVersion: 1,
				}
			},
			Info: trc.UpdateInfo{
				Type: trc.RegularUpdate,
				KeyChanges: &trc.KeyChanges{
					Modified: map[trc.KeyType]trc.ASToKeyMeta{
						trc.OnlineKey: {
							a110: {
								KeyVersion: 2,
								Algorithm:  scrypto.Ed25519,
								Key:        []byte{0, 110, 2},
							},
						},
					},
				},
			},
		},

		// Invalid updates

		"Signature from previous non-Primary AS": {
			Modify: func(updated, _ *trc.TRC) {
				updated.Votes[a190] = trc.Vote{
					KeyType:    trc.OnlineKey,
					KeyVersion: 1,
				}
			},
			ExpectedErrMsg: trc.ErrUnexpectedVote,
		},
		"Signature from non-Voting AS": {
			Modify: func(updated, prev *trc.TRC) {
				updated.Votes[a130] = trc.Vote{
					KeyType:    trc.OnlineKey,
					KeyVersion: 1,
				}
			},
			ExpectedErrMsg: trc.ErrNoVotingRight,
		},
		"Wrong KeyType": {
			Modify: func(updated, _ *trc.TRC) {
				updated.Votes[a110] = trc.Vote{
					KeyType:    trc.OfflineKey,
					KeyVersion: 1,
				}
			},
			ExpectedErrMsg: trc.ErrWrongVotingKeyType,
		},
		"Wrong KeyVersion": {
			Modify: func(updated, _ *trc.TRC) {
				updated.Votes[a110] = trc.Vote{
					KeyType:    trc.OnlineKey,
					KeyVersion: 10,
				}
			},
			ExpectedErrMsg: trc.ErrWrongVotingKeyVersion,
		},
		"Signature Quorum too small": {
			Modify: func(updated, _ *trc.TRC) {
				delete(updated.Votes, a140)
			},
			ExpectedErrMsg: trc.ErrQuorumUnmet,
		},
		"Missing proof of possession": {
			Modify: func(updated, _ *trc.TRC) {
				updated.PrimaryASes[a110].Keys[trc.OnlineKey] = scrypto.KeyMeta{
					KeyVersion: 2,
					Algorithm:  scrypto.Ed25519,
					Key:        []byte{0, 110, 2},
				}
				updated.Votes[a110] = trc.Vote{
					KeyType:    trc.OfflineKey,
					KeyVersion: 1,
				}
			},
			ExpectedErrMsg: trc.ErrMissingProofOfPossession,
		},
		"Unexpected proof of possession": {
			Modify: func(updated, _ *trc.TRC) {
				updated.ProofOfPossession[a110] = []trc.KeyType{trc.IssuingKey}
			},
			ExpectedErrMsg: trc.ErrUnexpectedProofOfPossession,
		},
		"Update online key with online vote": {
			Modify: func(updated, _ *trc.TRC) {
				updated.PrimaryASes[a110].Keys[trc.OnlineKey] = scrypto.KeyMeta{
					KeyVersion: 2,
					Algorithm:  scrypto.Ed25519,
					Key:        []byte{0, 110, 2},
				}
				updated.ProofOfPossession[a110] = []trc.KeyType{trc.OnlineKey}
			},
			ExpectedErrMsg: trc.ErrWrongVotingKeyType,
		},
		"Update online key without any vote": {
			Modify: func(updated, prev *trc.TRC) {
				*prev.VotingQuorumPtr = 2
				*updated.VotingQuorumPtr = 2
				updated.PrimaryASes[a110].Keys[trc.OnlineKey] = scrypto.KeyMeta{
					KeyVersion: 2,
					Algorithm:  scrypto.Ed25519,
					Key:        []byte{0, 110, 2},
				}
				updated.ProofOfPossession[a110] = []trc.KeyType{trc.OnlineKey}
				delete(updated.Votes, a110)
			},
			ExpectedErrMsg: trc.ErrMissingVote,
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			updated, prev := newRegularUpdate(time.Now())
			test.Modify(updated, prev)
			v := trc.UpdateValidator{
				Prev: prev,
				Next: updated,
			}
			info, err := v.Validate()
			xtest.AssertErrorsIs(t, err, test.ExpectedErrMsg)
			if test.ExpectedErrMsg == nil {
				assert.Equal(t, trc.RegularUpdate, info.Type)
				initKeyChanges(&test.Info)
				assert.Equal(t, test.Info.KeyChanges, info.KeyChanges)
				//assert.Equal(t, test.Info, info)
			}
		})
	}
}

func initKeyChanges(info *trc.UpdateInfo) {
	if info.KeyChanges == nil {
		info.KeyChanges = &trc.KeyChanges{}
	}
	initModType := func(m *map[trc.KeyType]trc.ASToKeyMeta) {
		if *m == nil {
			*m = make(map[trc.KeyType]trc.ASToKeyMeta)
		}
		for _, keyType := range []trc.KeyType{trc.OnlineKey, trc.OfflineKey, trc.IssuingKey} {
			if _, ok := (*m)[keyType]; !ok {
				(*m)[keyType] = make(trc.ASToKeyMeta)
			}
		}
	}
	initModType(&info.KeyChanges.Fresh)
	initModType(&info.KeyChanges.Modified)
}

func newRegularUpdate(now time.Time) (*trc.TRC, *trc.TRC) {
	t := newBaseTRC(now.Add(time.Hour))
	t.Version = 2
	t.GracePeriod = &trc.Period{Duration: 6 * time.Hour}
	t.Votes = map[addr.AS]trc.Vote{
		a110: {KeyType: trc.OnlineKey, KeyVersion: 1},
		a120: {KeyType: trc.OnlineKey, KeyVersion: 1},
		a140: {KeyType: trc.OnlineKey, KeyVersion: 1},
	}
	t.ProofOfPossession = make(map[addr.AS][]trc.KeyType)
	return t, newBaseTRC(now)
}

// newSensitive creates an update that is signed with the offline keys.
// The caller has to add the sensitive change.
func newSensitiveUpdate(now time.Time) (*trc.TRC, *trc.TRC) {
	t, _ := newRegularUpdate(now.Add(time.Hour))
	t.Version = 3
	t.GracePeriod = &trc.Period{Duration: 6 * time.Hour}
	t.Votes = map[addr.AS]trc.Vote{
		a110: {KeyType: trc.OfflineKey, KeyVersion: 1},
		a120: {KeyType: trc.OfflineKey, KeyVersion: 1},
		a140: {KeyType: trc.OfflineKey, KeyVersion: 1},
	}
	t.ProofOfPossession = make(map[addr.AS][]trc.KeyType)
	prev, _ := newRegularUpdate(now)
	return t, prev
}
