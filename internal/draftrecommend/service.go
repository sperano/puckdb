package draftrecommend

import (
	"errors"
	"fmt"
	"slices"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/sperano/puckdb/internal/draft"
	"github.com/sperano/puckdb/internal/draftrank"
	"github.com/sperano/puckdb/internal/draftsession"
)

var (
	ErrInvalidInput = errors.New("invalid draft recommendation input")
	ErrUnsafeRoster = errors.New("draft roster is incomplete or infeasible")
)

const (
	BoardStatusReady      = "ready"
	BoardStatusIncomplete = "incomplete"
	BoardStatusStale      = "stale"
	maxCategoryReasons    = 3
	maxCategoryWeight     = 10.0
)

type playerRef struct {
	player draftrank.Player
	place  draftrank.Placement
}

// Evaluate computes recommendations from one immutable ranking and session
// version. It returns labeled output without candidates for an unsafe board.
func Evaluate(in Input) (Result, error) {
	started := time.Now()
	if err := validateInput(in); err != nil {
		return Result{}, err
	}
	scenario, issues := selectedScenario(in)
	result := newResult(in, scenario, issues)
	if err := annotateBoard(&result, in); err != nil {
		return result, err
	}
	if in.SessionStale {
		result.BoardStatus = BoardStatusStale
		result.Issues = append(result.Issues, "draft board is stale")
	}
	if !in.SessionSafe || !draftsession.SafeToRecommend(in.Session) || in.SessionStale {
		if !in.SessionStale {
			result.BoardStatus = BoardStatusIncomplete
		}
		result.Issues = append(result.Issues, "draft board is incomplete or has unresolved manual conflicts")
		result.LatencyMillis = time.Since(started).Milliseconds()
		return result, nil
	}
	if result.CurrentPick == nil {
		if len(in.Order) > 0 {
			result.LatencyMillis = time.Since(started).Milliseconds()
			return result, nil
		}
		result.Issues = append(result.Issues, "verified chronological pick order is unavailable; turn estimate omitted")
	}
	if err := validateRoster(in); err != nil {
		return result, err
	}
	refs := availablePlayers(in, scenario, &result)
	result.Candidates = evaluateCandidates(in, refs, &result)
	if len(result.Candidates) == 0 {
		result.Issues = append(result.Issues, "no available player is feasible for the current roster")
	}
	rankCandidates(result.Candidates)
	selectRecommendations(&result)
	result.LatencyMillis = time.Since(started).Milliseconds()
	return result, nil
}

func validateInput(in Input) error {
	if in.Ranking == nil || in.OurTeamID <= 0 || in.GeneratedAt.IsZero() {
		return fmt.Errorf("%w: ranking, team ID and generated-at time are required", ErrInvalidInput)
	}
	info := in.Ranking.SnapshotInfo
	if info.ID == uuid.Nil || info.Identity == "" || info.League.LeagueKey == "" || info.League.RulesHash == "" ||
		info.Projection.SnapshotID == uuid.Nil || info.Projection.ModelVersion == "" {
		return fmt.Errorf("%w: ranking, projection and rule versions are required", ErrInvalidInput)
	}
	if in.GeneratedAt.Before(info.AsOf) {
		return fmt.Errorf("%w: generated-at time precedes the ranking snapshot", ErrInvalidInput)
	}
	scenario, _ := selectedScenario(in)
	if in.Ranking.Versions[scenario] == "" {
		return fmt.Errorf("%w: ranking version for scenario %q is required", ErrInvalidInput, scenario)
	}
	if in.Strategy.RiskToleranceSet && (!finite(in.Strategy.RiskTolerance) || in.Strategy.RiskTolerance < 0 || in.Strategy.RiskTolerance > 1) {
		return fmt.Errorf("%w: risk tolerance must be in [0, 1]", ErrInvalidInput)
	}
	categoryIDs := make(map[int]bool, len(in.Ranking.League.Categories))
	for _, category := range in.Ranking.League.Categories {
		categoryIDs[category.StatID] = true
	}
	for id, weight := range in.Strategy.CategoryWeights {
		if !categoryIDs[id] || weight < 0 || weight > maxCategoryWeight || !finite(weight) {
			return fmt.Errorf("%w: invalid category weight for stat %d", ErrInvalidInput, id)
		}
	}
	return nil
}

func selectedScenario(in Input) (draftrank.Scenario, []string) {
	scenario := in.Scenario
	if scenario == "" {
		scenario = draftrank.DefaultScenario
	}
	if in.Ranking.HasScenario(scenario) {
		return scenario, nil
	}
	if in.Ranking.HasScenario(draftrank.ScenarioBaseline) {
		return draftrank.ScenarioBaseline, []string{"requested news scenario unavailable; baseline scenario used"}
	}
	return scenario, []string{"requested scenario is unavailable; no recommendations generated"}
}

func newResult(in Input, scenario draftrank.Scenario, issues []string) Result {
	info := in.Ranking.SnapshotInfo
	result := Result{
		RecommendationVersion: RecommendationVersion, SessionVersion: in.Session.Version,
		RankingSnapshotID: info.ID.String(), RankingIdentity: info.Identity, RankingAsOf: info.AsOf,
		Scenario: scenario, RankingVersion: in.Ranking.Versions[scenario], ProjectionVersion: info.Projection.ModelVersion,
		RulesVersion: info.League.RulesHash, Strategy: cloneStrategy(in.Strategy),
		Availability: cloneAvailability(in.Availability), ADP: cloneADP(in.ADP),
		BoardStatus: BoardStatusReady, Issues: slices.Clone(issues), Candidates: []Candidate{},
		GeneratedAt: in.GeneratedAt.UTC(),
	}
	if info.Adjustment != nil {
		result.NewsVersion = info.Adjustment.PolicyVersion + ":" + info.Adjustment.ID
	}
	for _, issue := range in.RankingIssues {
		result.Issues = append(result.Issues, string(issue.Code)+": "+issue.Message)
		if issue.Code == draftrank.IssueStalePool || issue.Code == draftrank.IssueStaleSnapshot {
			result.BoardStatus = BoardStatusStale
		}
	}
	for _, issue := range info.Unavailable {
		result.Issues = append(result.Issues, string(issue.Code)+": "+issue.Message)
	}
	if info.PoolSize != len(in.Ranking.Players) || len(info.Unavailable) > 0 {
		result.BoardStatus = BoardStatusIncomplete
		result.Issues = append(result.Issues, "ranking pool is incomplete")
	}
	return result
}

func annotateBoard(result *Result, in Input) error {
	if len(in.Order) == 0 {
		result.Issues = append(result.Issues, "verified chronological pick order is missing")
		return nil
	}
	indexByKey := make(map[draftsession.PickKey]int, len(in.Order))
	for i, slot := range in.Order {
		if slot.Key.Round <= 0 || slot.Key.Pick <= 0 || slot.TeamID <= 0 {
			return fmt.Errorf("%w: pick order contains invalid slot", ErrInvalidInput)
		}
		if _, exists := indexByKey[slot.Key]; exists {
			return fmt.Errorf("%w: pick order contains duplicate slot", ErrInvalidInput)
		}
		indexByKey[slot.Key] = i
	}
	board := draftsession.EffectiveBoard(in.Session)
	boardKeys := make(map[draftsession.PickKey]bool, len(board))
	for _, entry := range board {
		if _, exists := indexByKey[entry.Pick.Key]; !exists {
			result.Issues = append(result.Issues, "draft board contains a pick outside the verified order")
			result.BoardStatus = BoardStatusIncomplete
			return nil
		}
		boardKeys[entry.Pick.Key] = true
	}
	currentIndex := len(in.Order)
	for i, slot := range in.Order {
		if !boardKeys[slot.Key] {
			currentIndex = i
			result.CurrentPick = pickSlotCopy(slot)
			break
		}
	}
	if result.CurrentPick == nil {
		result.Issues = append(result.Issues, "draft has no remaining picks in the verified order")
		return nil
	}
	pending := 0
	for i := currentIndex; i < len(in.Order); i++ {
		if boardKeys[in.Order[i].Key] {
			continue
		}
		if in.Order[i].TeamID == in.OurTeamID {
			until := pending
			result.PicksUntilNextTurn = &until
			break
		}
		pending++
	}
	if result.PicksUntilNextTurn == nil {
		result.Issues = append(result.Issues, "our next turn is absent from the supplied draft order")
	}
	return nil
}

func pickSlotCopy(slot PickSlot) *PickSlot { copy := slot; return &copy }

func validateRoster(in Input) error {
	seen := make(map[int]bool, len(in.Roster))
	for _, player := range in.Roster {
		if player.YahooPlayerID <= 0 || seen[player.YahooPlayerID] {
			return fmt.Errorf("%w: roster player IDs must be positive and unique", ErrUnsafeRoster)
		}
		seen[player.YahooPlayerID] = true
		if !player.Resolved() {
			return fmt.Errorf("%w: roster player %d has no base Yahoo eligibility", ErrUnsafeRoster, player.YahooPlayerID)
		}
	}
	if result := draft.CheckRoster(in.Ranking.League.RosterSlots, in.Roster); !result.Feasible() {
		return fmt.Errorf("%w: current roster cannot be allocated to league slots", ErrUnsafeRoster)
	}
	rosterIDs := make(map[int]bool, len(in.Roster))
	for _, player := range in.Roster {
		rosterIDs[player.YahooPlayerID] = true
	}
	for _, entry := range draftsession.EffectiveBoard(in.Session) {
		if entry.Pick.TeamID == in.OurTeamID && !rosterIDs[entry.Pick.PlayerID] {
			return fmt.Errorf("%w: selected player %d is missing from supplied roster", ErrUnsafeRoster, entry.Pick.PlayerID)
		}
	}
	return nil
}

func availablePlayers(in Input, scenario draftrank.Scenario, result *Result) []playerRef {
	draftedIDs, draftedKeys := draftedPlayers(in)
	refs := make([]playerRef, 0, len(in.Ranking.Players))
	missingFullEligibility := false
	for _, player := range in.Ranking.Players {
		if player.YahooPlayerID <= 0 || player.PlayerKey == "" || draftedIDs[player.YahooPlayerID] || draftedKeys[player.PlayerKey] || in.KeeperPlayerKeys[player.PlayerKey] || in.Availability.Unavailable[player.PlayerKey] {
			continue
		}
		if _, unavailable := in.Availability.UnavailablePlayerIDs[player.YahooPlayerID]; unavailable {
			continue
		}
		positions := player.RosterEligiblePositions
		if len(positions) == 0 {
			positions = slices.Clone(player.EligiblePositions)
			player.RosterEligiblePositions = positions
			missingFullEligibility = true
		}
		placement, exists := player.Placements[scenario]
		if !exists {
			result.Issues = append(result.Issues, "ranking player "+player.PlayerKey+" lacks requested scenario placement")
			result.BoardStatus = BoardStatusIncomplete
			continue
		}
		refs = append(refs, playerRef{player: player, place: placement})
	}
	if missingFullEligibility {
		result.Issues = append(result.Issues, "ranking snapshot lacks full Yahoo roster eligibility; reserve-slot feasibility is unavailable")
		result.BoardStatus = BoardStatusIncomplete
	}
	sort.Slice(refs, func(i, j int) bool { return refs[i].player.PlayerKey < refs[j].player.PlayerKey })
	return refs
}

func draftedPlayers(in Input) (map[int]bool, map[string]bool) {
	ids := make(map[int]bool)
	keys := make(map[string]bool)
	for _, player := range in.Roster {
		ids[player.YahooPlayerID] = true
	}
	for _, entry := range draftsession.EffectiveBoard(in.Session) {
		ids[entry.Pick.PlayerID] = true
	}
	for _, player := range in.Ranking.Players {
		if ids[player.YahooPlayerID] {
			keys[player.PlayerKey] = true
		}
	}
	return ids, keys
}

func evaluateCandidates(in Input, refs []playerRef, result *Result) []Candidate {
	feasible := feasiblePlayers(in, refs)
	feasibleRefs := make([]playerRef, len(feasible))
	for i := range feasible {
		feasibleRefs[i] = feasible[i].ref
	}
	candidates := make([]Candidate, 0, len(feasible))
	for _, candidateRef := range feasible {
		candidate := makeCandidate(in, candidateRef.ref, candidateRef.slot, feasibleRefs, candidateRef.startsAdded)
		candidate.WaitRisk = waitRisk(in, candidateRef.ref.player.PlayerKey, result)
		candidate.Explanations = explanations(candidate)
		candidates = append(candidates, candidate)
	}
	return candidates
}

type feasibleRef struct {
	ref         playerRef
	slot        string
	startsAdded int
}

func feasiblePlayers(in Input, refs []playerRef) []feasibleRef {
	before := draft.CheckRoster(in.Ranking.League.RosterSlots, in.Roster)
	beforeStarts := startingAssignments(in.Ranking.League.RosterSlots, before.Assignments)
	feasible := make([]feasibleRef, 0, len(refs))
	for _, ref := range refs {
		hypothetical := append(slices.Clone(in.Roster), draft.RosterPlayer{
			YahooPlayerID: ref.player.YahooPlayerID, Name: ref.player.Name,
			EligiblePositions: slices.Clone(ref.player.RosterEligiblePositions),
		})
		feasibility := draft.CheckRoster(in.Ranking.League.RosterSlots, hypothetical)
		if !feasibility.Feasible() {
			continue
		}
		assigned := assignedSlot(feasibility.Assignments, ref.player.YahooPlayerID)
		if assigned == "" {
			continue
		}
		startsAdded := startingAssignments(in.Ranking.League.RosterSlots, feasibility.Assignments) -
			beforeStarts
		feasible = append(feasible, feasibleRef{ref: ref, slot: assigned, startsAdded: startsAdded})
	}
	return feasible
}

func startingAssignments(slots []draft.RosterSlot, assignments []draft.SlotAssignment) int {
	starting := make(map[string]bool, len(slots))
	for _, slot := range slots {
		starting[slot.Position] = slot.Kind() == draft.SlotKindStarting
	}
	count := 0
	for _, assignment := range assignments {
		if starting[assignment.Slot] {
			count++
		}
	}
	return count
}

func assignedSlot(assignments []draft.SlotAssignment, playerID int) string {
	for _, assignment := range assignments {
		if assignment.YahooPlayerID == playerID {
			return assignment.Slot
		}
	}
	return ""
}

func rankCandidates(candidates []Candidate) {
	sort.Slice(candidates, func(i, j int) bool {
		left, right := candidates[i], candidates[j]
		if left.Reasons.MarginalValue == right.Reasons.MarginalValue {
			return left.PlayerKey < right.PlayerKey
		}
		return left.Reasons.MarginalValue > right.Reasons.MarginalValue
	})
	for i := range candidates {
		candidates[i].ValueRank = i + 1
	}
	fitOrder := slices.Clone(candidates)
	sort.Slice(fitOrder, func(i, j int) bool { return rosterFitLess(fitOrder[i], fitOrder[j]) })
	ranks := make(map[string]int, len(fitOrder))
	for index, candidate := range fitOrder {
		ranks[candidate.PlayerKey] = index + 1
	}
	for i := range candidates {
		candidates[i].RosterFitRank = ranks[candidates[i].PlayerKey]
	}
}

func rosterFitLess(a, b Candidate) bool {
	if a.Reasons.AdditionalStarts != b.Reasons.AdditionalStarts {
		return a.Reasons.AdditionalStarts > b.Reasons.AdditionalStarts
	}
	aKind, bKind := slotPriority(a.Reasons.AssignedSlot), slotPriority(b.Reasons.AssignedSlot)
	if aKind != bKind {
		return aKind < bKind
	}
	if a.Reasons.ScarcityCount != b.Reasons.ScarcityCount {
		return a.Reasons.ScarcityCount < b.Reasons.ScarcityCount
	}
	if a.Reasons.MarginalValue != b.Reasons.MarginalValue {
		return a.Reasons.MarginalValue > b.Reasons.MarginalValue
	}
	return a.PlayerKey < b.PlayerKey
}

func slotPriority(slot string) int {
	switch (draft.RosterSlot{Position: slot}).Kind() {
	case draft.SlotKindStarting:
		return 0
	case draft.SlotKindReserve:
		return 1
	case draft.SlotKindBench:
		return 2
	default:
		return 3
	}
}

func selectRecommendations(result *Result) {
	if len(result.Candidates) == 0 {
		return
	}
	bestValue, bestFit := result.Candidates[0], result.Candidates[0]
	for _, candidate := range result.Candidates {
		if candidate.ValueRank < bestValue.ValueRank {
			bestValue = candidate
		}
		if candidate.RosterFitRank < bestFit.RosterFitRank {
			bestFit = candidate
		}
	}
	result.BestValue = candidateCopy(bestValue)
	result.BestRosterFit = candidateCopy(bestFit)
}

func candidateCopy(candidate Candidate) *Candidate { copy := candidate; return &copy }

func explanations(candidate Candidate) []string {
	reasons := candidate.Reasons
	lines := []string{
		fmt.Sprintf("marginal value %.3f (league %.3f, strategy %.3f, risk %.3f)", candidate.Reasons.MarginalValue,
			candidate.Reasons.LeagueValue, candidate.Reasons.StrategyValue, candidate.Reasons.RiskAdjustment),
		fmt.Sprintf("fits %s; %d available players fit that slot", reasons.AssignedSlot, reasons.ScarcityCount),
	}
	if reasons.TierDrop > 0 {
		lines = append(lines, fmt.Sprintf("position rank %d to next available rank %d; tier %d to %d",
			reasons.PositionRank, reasons.NextPositionRank, reasons.Tier, reasons.NextTier))
	}
	if reasons.NewsDelta != 0 {
		lines = append(lines, fmt.Sprintf("selected news scenario changes value by %.3f", reasons.NewsDelta))
	}
	lines = append(lines, reasons.Tradeoffs...)
	lines = append(lines, candidate.NewsRisk...)
	return lines
}

func cloneStrategy(strategy Strategy) Strategy {
	weights := make(map[int]float64, len(strategy.CategoryWeights))
	for id, weight := range strategy.CategoryWeights {
		weights[id] = weight
	}
	strategy.CategoryWeights = weights
	return strategy
}

func cloneAvailability(source AvailabilitySource) AvailabilitySource {
	source.Unavailable = cloneBoolMap(source.Unavailable)
	source.UnavailableReasons = cloneStringMap(source.UnavailableReasons)
	source.UnavailablePlayerIDs = cloneIntStringMap(source.UnavailablePlayerIDs)
	return source
}

func cloneADP(source ADPSource) ADPSource {
	values := make(map[string]float64, len(source.ByPlayer))
	for key, value := range source.ByPlayer {
		values[key] = value
	}
	source.ByPlayer = values
	return source
}

func cloneBoolMap(source map[string]bool) map[string]bool {
	cloned := make(map[string]bool, len(source))
	for key, value := range source {
		cloned[key] = value
	}
	return cloned
}

func cloneStringMap(source map[string]string) map[string]string {
	cloned := make(map[string]string, len(source))
	for key, value := range source {
		cloned[key] = value
	}
	return cloned
}

func cloneIntStringMap(source map[int]string) map[int]string {
	cloned := make(map[int]string, len(source))
	for key, value := range source {
		cloned[key] = value
	}
	return cloned
}
