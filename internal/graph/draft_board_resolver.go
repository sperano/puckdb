package graph

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/sperano/puckdb/internal/draft"
	"github.com/sperano/puckdb/internal/draftboard"
	"github.com/sperano/puckdb/internal/draftrank"
	"github.com/sperano/puckdb/internal/draftsession"
	"github.com/sperano/puckdb/internal/draftwatch"
	"github.com/sperano/puckdb/internal/graph/model"
)

const (
	defaultDraftBoardPageSize = 100
	maxDraftBoardPageSize     = 200
	defaultDraftEventPageSize = 50
	maxDraftEventPageSize     = 200
)

var errDraftBoardNotConfigured = errors.New("maurice draft board is not available")

func (r *Resolver) draftBoardService() (*draftboard.Service, error) {
	if r.DraftBoard == nil {
		return nil, errDraftBoardNotConfigured
	}
	return r.DraftBoard, nil
}

func (r *Resolver) mauriceDraftBoard(ctx context.Context, input model.MauriceDraftBoardInput) (*model.MauriceDraftBoard, error) {
	service, err := r.draftBoardService()
	if err != nil {
		return nil, err
	}
	request := draftboard.Request{League: input.League, Season: seasonOrCurrent(input.Season)}
	if input.Scenario != nil {
		request.Scenario = draftrank.Scenario(strings.ToLower(input.Scenario.String()))
	}
	board, err := service.Board(ctx, request)
	if err != nil {
		return nil, err
	}
	return gqlDraftBoard(service, board, input), nil
}

func (r *Resolver) mauriceDraftEvents(ctx context.Context, league string, season *int, after int64, limit *int) ([]*model.MauriceDraftSessionEvent, error) {
	service, err := r.draftBoardService()
	if err != nil {
		return nil, err
	}
	if after < 0 {
		return nil, fmt.Errorf("afterStateVersion cannot be negative")
	}
	events, err := service.Events(ctx, league, seasonOrCurrent(season), uint64(after), draftEventPageSize(limit))
	if err != nil {
		return nil, err
	}
	result := make([]*model.MauriceDraftSessionEvent, len(events))
	for index, event := range events {
		result[index] = &model.MauriceDraftSessionEvent{
			StateVersion: checkedGraphQLVersion(event.StateVersion), Kind: event.Kind, CreatedAt: event.CreatedAt,
		}
	}
	return result, nil
}

func draftEventPageSize(requested *int) int {
	if requested == nil || *requested <= 0 {
		return defaultDraftEventPageSize
	}
	return min(*requested, maxDraftEventPageSize)
}

func (r *Resolver) startMauriceDraftWatch(ctx context.Context, input model.MauriceDraftSessionRefInput) (*model.MauriceDraftWatchStatus, error) {
	service, err := r.draftBoardService()
	if err != nil {
		return nil, err
	}
	status, err := service.StartWatch(ctx, input.League, seasonOrCurrent(input.Season))
	return gqlDraftWatchStatus(status, err), err
}

func (r *Resolver) stopMauriceDraftWatch(ctx context.Context, input model.MauriceDraftSessionRefInput) (*model.MauriceDraftWatchStatus, error) {
	service, err := r.draftBoardService()
	if err != nil {
		return nil, err
	}
	status, err := service.StopWatch(ctx, input.League, seasonOrCurrent(input.Season))
	return gqlDraftWatchStatus(status, err), err
}

func (r *Resolver) refreshMauriceDraftBoard(ctx context.Context, input model.MauriceDraftSessionRefInput) (*model.MauriceDraftSyncStatus, error) {
	service, err := r.draftBoardService()
	if err != nil {
		return nil, err
	}
	session, _, err := service.Refresh(ctx, input.League, seasonOrCurrent(input.Season))
	if err != nil {
		return nil, err
	}
	return gqlDraftSessionSync(service, session), nil
}

func (r *Resolver) applyMauriceDraftPick(ctx context.Context, input model.MauriceDraftManualPickInput) (*model.MauriceDraftSyncStatus, error) {
	service, expected, err := r.manualActionContext(input.League, input.Season, input.ExpectedStateVersion, input.ClientMutationID)
	if err != nil {
		return nil, err
	}
	identity, err := service.ResolveIdentity(ctx, input.League, seasonOrCurrent(input.Season))
	if err != nil {
		return nil, err
	}
	operation, err := manualPickOperation(identity, input)
	if err != nil {
		return nil, err
	}
	result, err := service.ApplyManual(ctx, input.League, seasonOrCurrent(input.Season), operation, expected, time.Now().UTC())
	if err != nil {
		return nil, err
	}
	return gqlDraftSessionSync(service, result.Session), nil
}

func manualPickOperation(identity draftwatch.Identity, input model.MauriceDraftManualPickInput) (draftsession.ManualOperation, error) {
	teamID, err := draftwatch.ParseTeamKey(input.TeamKey, identity)
	if err != nil {
		return draftsession.ManualOperation{}, err
	}
	playerID, err := draftwatch.ParsePlayerKey(input.PlayerKey, identity)
	if err != nil {
		return draftsession.ManualOperation{}, err
	}
	key := draftsession.PickKey{Round: input.Round, Pick: input.Pick}
	pick := &draftsession.Pick{Key: key, TeamID: teamID, PlayerID: playerID}
	if input.Cost != nil {
		if *input.Cost < 0 {
			return draftsession.ManualOperation{}, fmt.Errorf("draft cost cannot be negative")
		}
		pick.Cost = new(*input.Cost)
	}
	kind := draftsession.ManualAdd
	if input.Action == model.MauriceDraftManualActionCorrect {
		kind = draftsession.ManualCorrect
	}
	return draftsession.ManualOperation{Kind: kind, Key: key, Pick: pick}, nil
}

func (r *Resolver) undoMauriceDraftPick(ctx context.Context, input model.MauriceDraftSlotInput) (*model.MauriceDraftSyncStatus, error) {
	service, expected, err := r.manualActionContext(input.League, input.Season, input.ExpectedStateVersion, input.ClientMutationID)
	if err != nil {
		return nil, err
	}
	key := draftsession.PickKey{Round: input.Round, Pick: input.Pick}
	operation := draftsession.ManualOperation{Kind: draftsession.ManualUndo, Key: key}
	result, err := service.ApplyManual(ctx, input.League, seasonOrCurrent(input.Season), operation, expected, time.Now().UTC())
	if err != nil {
		return nil, err
	}
	return gqlDraftSessionSync(service, result.Session), nil
}

func (r *Resolver) resolveMauriceDraftConflict(ctx context.Context, input model.MauriceDraftResolveInput) (*model.MauriceDraftSyncStatus, error) {
	service, expected, err := r.manualActionContext(input.League, input.Season, input.ExpectedStateVersion, input.ClientMutationID)
	if err != nil {
		return nil, err
	}
	choice := draftsession.KeepManual
	if input.Choice == model.MauriceDraftConflictChoiceAcceptUpstream {
		choice = draftsession.AcceptUpstream
	}
	key := draftsession.PickKey{Round: input.Round, Pick: input.Pick}
	result, err := service.ResolveConflict(ctx, input.League, seasonOrCurrent(input.Season), key, choice, expected, time.Now().UTC())
	if err != nil {
		return nil, err
	}
	return gqlDraftSessionSync(service, result.Session), nil
}

func (r *Resolver) manualActionContext(league string, season *int, expected int64, mutationID string) (*draftboard.Service, uint64, error) {
	service, err := r.draftBoardService()
	if err != nil {
		return nil, 0, err
	}
	if strings.TrimSpace(league) == "" || seasonOrCurrent(season) <= 0 || strings.TrimSpace(mutationID) == "" {
		return nil, 0, fmt.Errorf("league and clientMutationId are required")
	}
	if expected < 0 {
		return nil, 0, fmt.Errorf("expectedStateVersion cannot be negative")
	}
	return service, uint64(expected), nil
}

func (r *Resolver) setMauriceDraftShortlist(ctx context.Context, input model.MauriceDraftShortlistInput) (*model.MauriceDraftSyncStatus, error) {
	service, err := r.draftBoardService()
	if err != nil {
		return nil, err
	}
	if input.ExpectedStateVersion < 0 {
		return nil, fmt.Errorf("expectedStateVersion cannot be negative")
	}
	season := seasonOrCurrent(input.Season)
	if err := service.SetShortlist(ctx, input.League, season, input.PlayerKey, uint64(input.ExpectedStateVersion), input.Selected); err != nil {
		return nil, err
	}
	board, err := service.Board(ctx, draftboard.Request{League: input.League, Season: season})
	if err != nil {
		return nil, err
	}
	return gqlDraftBoardSync(service, board), nil
}

func gqlDraftBoard(service *draftboard.Service, board draftboard.Board, input model.MauriceDraftBoardInput) *model.MauriceDraftBoard {
	all := slices.Clone(board.Available)
	sort.SliceStable(all, func(left, right int) bool { return all[left].SelectedRank < all[right].SelectedRank })
	shortlist := filterDraftPlayers(all, nil, "", true)
	filtered := filterDraftPlayers(all, input.Positions, valueOr(input.Search, ""), false)
	offset, limit, page := draftPlayerPage(filtered, input.Offset, input.Limit)
	return &model.MauriceDraftBoard{
		League: gqlLeague(board.League), Scenario: gqlScenario(board.Scenario), Snapshot: gqlSnapshot(&board.Snapshot),
		Sync: gqlDraftBoardSync(service, board), Turn: gqlDraftTurn(board), Roster: gqlDraftRoster(board),
		History: gqlDraftHistory(board), Available: gqlDraftPlayers(page), Shortlist: gqlDraftPlayers(shortlist),
		Recommendations: gqlDraftRecommendations(board), Warnings: nonNilStrings(board.Session.Warnings),
		TotalAvailable: len(filtered), Offset: offset, Limit: limit,
	}
}

func filterDraftPlayers(players []draftboard.Player, positions []string, search string, shortlistOnly bool) []draftboard.Player {
	positionSet := make(map[string]bool, len(positions))
	for _, position := range positions {
		positionSet[strings.ToUpper(strings.TrimSpace(position))] = true
	}
	words := strings.Fields(strings.ToLower(search))
	result := make([]draftboard.Player, 0, len(players))
	for _, player := range players {
		if shortlistOnly && !player.IsShortlisted {
			continue
		}
		if len(positionSet) > 0 && !slices.ContainsFunc(player.Positions, func(position string) bool { return positionSet[position] }) {
			continue
		}
		haystack := strings.ToLower(player.Name + " " + player.Team + " " + player.PlayerKey)
		if slices.ContainsFunc(words, func(word string) bool { return !strings.Contains(haystack, word) }) {
			continue
		}
		result = append(result, player)
	}
	return result
}

func draftPlayerPage(players []draftboard.Player, requestedOffset, requestedLimit *int) (int, int, []draftboard.Player) {
	offset := max(valueOr(requestedOffset, 0), 0)
	limit := valueOr(requestedLimit, defaultDraftBoardPageSize)
	if limit <= 0 {
		limit = defaultDraftBoardPageSize
	}
	limit = min(limit, maxDraftBoardPageSize)
	if offset >= len(players) {
		return offset, limit, []draftboard.Player{}
	}
	return offset, limit, players[offset:min(offset+limit, len(players))]
}

func gqlDraftPlayers(players []draftboard.Player) []*model.MauriceDraftPlayer {
	result := make([]*model.MauriceDraftPlayer, 0, len(players))
	for _, player := range players {
		result = append(result, gqlDraftPlayer(player))
	}
	return result
}

func gqlDraftPlayer(player draftboard.Player) *model.MauriceDraftPlayer {
	result := &model.MauriceDraftPlayer{
		PlayerKey: player.PlayerKey, YahooPlayerID: player.YahooPlayerID, Name: player.Name, Team: player.Team,
		EligiblePositions: nonNilStrings(player.Positions), Status: optionalString(player.Status), InjuryNote: optionalString(player.InjuryNote),
		BaselineRank: player.BaselineRank, ScenarioRank: player.SelectedRank,
		BaselineValue: player.BaselineValue, ScenarioValue: player.SelectedValue,
		NewsDelta: player.SelectedValue - player.BaselineValue, Shortlisted: player.IsShortlisted,
		RecommendationReasons: []string{}, News: make([]*model.MauriceDraftNewsReason, 0, len(player.News)),
	}
	if candidate := player.Recommendation; candidate != nil {
		result.ValueRank, result.RosterFitRank = new(candidate.ValueRank), new(candidate.RosterFitRank)
		result.AssignedSlot = optionalString(candidate.Reasons.AssignedSlot)
		result.RecommendationReasons = nonNilStrings(candidate.Explanations)
		result.NewsDelta = candidate.Reasons.NewsDelta
	}
	for _, reason := range player.News {
		reportedAt := reason.LatestEvidenceAt
		if reportedAt.IsZero() {
			reportedAt = reason.EffectiveFrom
		}
		result.News = append(result.News, &model.MauriceDraftNewsReason{
			Detail: reason.Detail, ReportedAt: optionalTime(reportedAt), Status: reason.Status,
			Scenarios: gqlScenarios(reason.Scenarios),
		})
	}
	return result
}

func gqlDraftHistory(board draftboard.Board) []*model.MauriceDraftPick {
	result := make([]*model.MauriceDraftPick, 0, len(board.Picks))
	for _, pick := range board.Picks {
		teamName := fmt.Sprintf("Yahoo team %d", pick.TeamID)
		if pick.TeamID == board.TeamID {
			teamName = board.TeamName
		}
		playerKey := pick.PlayerKey
		if playerKey == "" {
			playerKey = fmt.Sprintf("%d.p.%d", board.Identity.GameKey, pick.PlayerID)
		}
		result = append(result, &model.MauriceDraftPick{
			Round: pick.Round, Pick: pick.Pick, TeamID: pick.TeamID, TeamName: teamName,
			PlayerID: pick.PlayerID, PlayerKey: playerKey, PlayerName: pick.Name,
			Source:   model.MauriceDraftEntrySource(strings.ToUpper(string(pick.Source))),
			Conflict: pick.Conflict, Undone: pick.Undone, Cost: cloneOptionalInt(pick.Cost),
		})
	}
	return result
}

func gqlDraftRoster(board draftboard.Board) *model.MauriceDraftRoster {
	players := make(map[int]draft.RosterPlayer, len(board.Roster.Players))
	keys := make(map[int]string, len(board.Available)+len(board.Picks))
	for _, player := range board.Roster.Players {
		players[player.YahooPlayerID] = player
	}
	for _, pick := range board.Picks {
		keys[pick.PlayerID] = pick.PlayerKey
	}
	assignments := make([]*model.MauriceDraftRosterAssignment, 0, len(board.Roster.Assignments))
	for _, assignment := range board.Roster.Assignments {
		player := players[assignment.YahooPlayerID]
		assignments = append(assignments, &model.MauriceDraftRosterAssignment{
			Slot: assignment.Slot, Index: assignment.Index, PlayerID: assignment.YahooPlayerID,
			PlayerKey: keys[assignment.YahooPlayerID], PlayerName: player.Name,
		})
	}
	open := make([]*model.MauriceDraftOpenSlot, 0, len(board.Roster.OpenSlots))
	for slot, count := range board.Roster.OpenSlots {
		open = append(open, &model.MauriceDraftOpenSlot{Slot: slot, Count: count})
	}
	sort.Slice(open, func(left, right int) bool { return open[left].Slot < open[right].Slot })
	return &model.MauriceDraftRoster{TeamID: board.TeamID, TeamName: board.TeamName,
		Feasible: board.Roster.Feasible, Assignments: assignments, OpenSlots: open, Warnings: nonNilStrings(board.Roster.Warnings)}
}

func gqlDraftRecommendations(board draftboard.Board) *model.MauriceDraftRecommendations {
	result := &model.MauriceDraftRecommendations{Issues: []string{}, RankingAsOf: optionalTime(board.Snapshot.AsOf)}
	if board.Recommendation == nil {
		return result
	}
	recommendation := board.Recommendation.Result
	result.GeneratedAt = optionalTime(recommendation.GeneratedAt)
	result.Issues = nonNilStrings(recommendation.Issues)
	if recommendation.BestValue != nil {
		result.BestValuePlayerKey = optionalString(recommendation.BestValue.PlayerKey)
		result.BestValue = gqlRecommendedPlayer(board.Available, recommendation.BestValue.PlayerKey)
	}
	if recommendation.BestRosterFit != nil {
		result.BestRosterFitPlayerKey = optionalString(recommendation.BestRosterFit.PlayerKey)
		result.BestRosterFit = gqlRecommendedPlayer(board.Available, recommendation.BestRosterFit.PlayerKey)
	}
	return result
}

func gqlRecommendedPlayer(players []draftboard.Player, key string) *model.MauriceDraftPlayer {
	for _, player := range players {
		if player.PlayerKey == key {
			return gqlDraftPlayer(player)
		}
	}
	return nil
}

func gqlDraftTurn(board draftboard.Board) *model.MauriceDraftTurn {
	result := &model.MauriceDraftTurn{DraftType: board.DraftFormat, PickTimeSeconds: cloneOptionalInt(board.PickClockSecs)}
	if board.CurrentPick != nil {
		result.CurrentRound, result.CurrentPick = new(board.CurrentPick.Key.Round), new(board.CurrentPick.Key.Pick)
		result.PicksUntilNextTurn = cloneOptionalInt(board.PicksUntilTurn)
		result.OrderKnown = !board.TurnEstimated
		result.TimingLabel = "Turns use the verified chronological draft order."
		if board.TurnEstimated {
			result.TimingLabel = "Turn estimate uses imported team positions and standard snake rotation; traded-pick ownership is unknown."
		}
		return result
	}
	result.CurrentRound, result.CurrentPick = nextContiguousPick(board)
	result.TimingLabel = "Turn estimate unavailable: verified pick order was not supplied."
	if board.PickClockSecs != nil {
		result.TimingLabel += fmt.Sprintf(" Yahoo's configured limit is %d seconds; no live countdown is available.", *board.PickClockSecs)
	}
	return result
}

func nextContiguousPick(board draftboard.Board) (*int, *int) {
	if strings.EqualFold(board.DraftFormat, "auction") || board.League.NumTeams <= 0 || !board.Session.RecommendationsSafe {
		return nil, nil
	}
	for index, pick := range board.Picks {
		if pick.Pick != index+1 {
			return nil, nil
		}
	}
	nextPick := len(board.Picks) + 1
	nextRound := (nextPick-1)/board.League.NumTeams + 1
	return &nextRound, &nextPick
}

func gqlDraftBoardSync(service *draftboard.Service, board draftboard.Board) *model.MauriceDraftSyncStatus {
	watch, _ := service.WatchStatus(board.Identity.LeagueKey)
	return gqlDraftSync(board.Session, watch)
}

func gqlDraftSessionSync(service *draftboard.Service, session draftwatch.Session) *model.MauriceDraftSyncStatus {
	watch, _ := service.WatchStatus(session.Identity.LeagueKey)
	return gqlDraftSync(service.Status(session), watch)
}

func gqlDraftSync(status draftboard.Status, watch draftboard.WatchStatus) *model.MauriceDraftSyncStatus {
	connection := model.MauriceDraftConnectionFresh
	switch {
	case status.LastSuccessAt == nil:
		connection = model.MauriceDraftConnectionNeverSynced
	case status.LastError != "":
		connection = model.MauriceDraftConnectionError
	case status.Stale:
		connection = model.MauriceDraftConnectionStale
	case !status.RecommendationsSafe:
		connection = model.MauriceDraftConnectionIncomplete
	}
	return &model.MauriceDraftSyncStatus{
		Connection: connection, DraftStatus: status.DraftStatus,
		RecommendationsSafe: status.RecommendationsSafe, Complete: status.Complete,
		StateVersion: checkedGraphQLVersion(status.Version), SyncVersion: checkedGraphQLVersion(status.SyncVersion),
		LastPollAt: status.LastPollAt, LastSuccessAt: status.LastSuccessAt,
		LastAuthoritativeAt: status.LastAuthoritativeAt, LastError: optionalString(status.LastError),
		Watch: gqlDraftWatchStatus(watch, nil),
	}
}

func gqlDraftWatchStatus(status draftboard.WatchStatus, watchErr error) *model.MauriceDraftWatchStatus {
	state := model.MauriceDraftWatchStateStopped
	if status.Running {
		state = model.MauriceDraftWatchStateRunning
	} else if status.LastError != "" || watchErr != nil {
		state = model.MauriceDraftWatchStateFailed
	}
	lastError := status.LastError
	if watchErr != nil {
		lastError = watchErr.Error()
	}
	return &model.MauriceDraftWatchStatus{
		State: state, StartedAt: optionalTime(status.StartedAt), StoppedAt: status.StoppedAt, LastError: optionalString(lastError),
	}
}

func checkedGraphQLVersion(version uint64) int64 {
	const maxGraphQLVersion = uint64(^uint64(0) >> 1)
	if version > maxGraphQLVersion {
		panic("draft version exceeds GraphQL Int64")
	}
	return int64(version)
}

func cloneOptionalInt(value *int) *int {
	if value == nil {
		return nil
	}
	return new(*value)
}
