package simulation

// checkAcquisitionCapacity is the one per-slot capacity policy for
// every acquisition: add_player, claim_player at filing, and waiver
// resolution on the process date. An acquired player always lands in
// BN, so the prospective roster (current placements, minus dropID when
// it is rostered, plus one BN player) must keep BN within its limit.
//
// Only BN is counted. Total roster capacity is deliberately not
// consulted: IR or active-slot vacancies cannot hold a bench add, so a
// drop from an active slot frees nothing for it. A roster whose BN is
// already over its limit (e.g. the limit was lowered) needs as many BN
// drops as it takes to get back under.
//
// Returns ErrRosterFullNeedsDrop when no drop was named and
// ErrDropLeavesBenchFull when the named drop does not make room. A
// dropID that is not on the roster frees nothing; validators reject it
// earlier (validateDropOwned), waiver resolution treats it as a drop
// that vanished since filing. No BN entry in Limits means no BN cap.
func checkAcquisitionCapacity(r RosterState, dropID *int64) error {
	limit, ok := r.Limits[SlotBN]
	if !ok {
		return nil
	}
	bench := 0
	for id, s := range r.Placements {
		if s == SlotBN && (dropID == nil || id != *dropID) {
			bench++
		}
	}
	if bench+1 <= limit {
		return nil
	}
	if dropID == nil {
		return ErrRosterFullNeedsDrop
	}
	return ErrDropLeavesBenchFull
}
