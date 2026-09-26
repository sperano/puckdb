// Package newsevent extracts validated player news events from stored
// article versions with an LLM, and keeps their lifecycle.
//
// The model only interprets the articles it is given. Its output is decoded
// strictly (unknown fields fail), every event must name a player the news
// resolver supplied and cite verbatim quotes of the article, and a factual
// value (a length, a date, a destination) the quotes do not state is cleared
// back to unknown. Events are then reconciled deterministically with the
// events already on record: nothing a model says overwrites earlier
// provenance, and nothing here ranks or penalizes a player.
package newsevent

import (
	"time"

	"github.com/sperano/puckdb/internal/news"
)

// Type is what happened to a player.
type Type string

const (
	TypeInjury        Type = "injury"
	TypeSuspension    Type = "suspension"
	TypeReinstatement Type = "reinstatement"
	TypeTrade         Type = "trade"
	TypeRoleChange    Type = "role_change"
)

var validTypes = map[Type]bool{
	TypeInjury: true, TypeSuspension: true, TypeReinstatement: true, TypeTrade: true, TypeRoleChange: true,
}

// ReportStatus is how firmly the article reports the event: confidence in
// the report, never in its fantasy impact.
type ReportStatus string

const (
	// StatusConfirmed: announced by the league, the team or the player, or
	// reported as fact on their authority.
	StatusConfirmed ReportStatus = "confirmed"
	// StatusReported: attributed to a reporter or unnamed sources, not
	// announced.
	StatusReported ReportStatus = "reported"
	// StatusRumor: speculation ("could", "is being considered", "linked to").
	StatusRumor ReportStatus = "rumor"
	// StatusDenied: the article says the event did not happen or corrects
	// an earlier report of it. A denial is never stored as an event; it
	// retracts or contradicts the events it denies.
	StatusDenied ReportStatus = "denied"
)

var validStatuses = map[ReportStatus]bool{
	StatusConfirmed: true, StatusReported: true, StatusRumor: true, StatusDenied: true,
}

// rank orders statuses by how firmly they report an event.
func (s ReportStatus) rank() int {
	switch s {
	case StatusConfirmed:
		return 3
	case StatusReported:
		return 2
	case StatusRumor:
		return 1
	default:
		return 0
	}
}

// DurationKind is how long the article says the event lasts. Unknown is the
// default and the only honest value when the article states no length.
type DurationKind string

const (
	DurationUnknown      DurationKind = "unknown"
	DurationIndefinite   DurationKind = "indefinite"
	DurationGames        DurationKind = "games"
	DurationDays         DurationKind = "days"
	DurationUntilDate    DurationKind = "until_date"
	DurationDayToDay     DurationKind = "day_to_day"
	DurationWeekToWeek   DurationKind = "week_to_week"
	DurationMonthToMonth DurationKind = "month_to_month"
	DurationSeason       DurationKind = "season"
)

var validDurationKinds = map[DurationKind]bool{
	DurationUnknown: true, DurationIndefinite: true, DurationGames: true, DurationDays: true, DurationUntilDate: true,
	DurationDayToDay: true, DurationWeekToWeek: true, DurationMonthToMonth: true, DurationSeason: true,
}

// ChangeField is what a trade or role change moves.
type ChangeField string

const (
	ChangeNone ChangeField = ""
	// ChangeTeam: the player's NHL team (trades, waiver claims).
	ChangeTeam ChangeField = "team"
	// ChangeLeague: the league the player plays in (assigned to the AHL,
	// recalled, loaned to Europe).
	ChangeLeague ChangeField = "league"
	// ChangeRosterStatus: a roster designation (injured reserve, waivers,
	// non-roster).
	ChangeRosterStatus ChangeField = "roster_status"
	// ChangeRole: a stated role (starting goaltender, captain, top power
	// play unit).
	ChangeRole ChangeField = "role"
)

var validChangeFields = map[ChangeField]bool{
	ChangeTeam: true, ChangeLeague: true, ChangeRosterStatus: true, ChangeRole: true,
}

// Lifecycle is where a stored event stands.
type Lifecycle string

const (
	// LifecycleActive: the latest word on record.
	LifecycleActive Lifecycle = "active"
	// LifecycleSuperseded: a later report gave other details; the newer
	// event replaced this one.
	LifecycleSuperseded Lifecycle = "superseded"
	// LifecycleRetracted: corrected or denied with authority.
	LifecycleRetracted Lifecycle = "retracted"
	// LifecycleResolved: ended by a reinstatement.
	LifecycleResolved Lifecycle = "resolved"
)

// Quote is a verbatim excerpt of a supplied document.
type Quote struct {
	Doc  string `json:"doc"`
	Text string `json:"quote"`
}

// Duration is the length the article states, with the quote stating it.
type Duration struct {
	Kind  DurationKind `json:"kind"`
	Games int          `json:"games,omitempty"`
	Days  int          `json:"days,omitempty"`
	// Until is the stated end date (DurationUntilDate only).
	Until time.Time `json:"until,omitzero"`
	Quote string    `json:"quote,omitempty"`
}

// Change is the move a trade or role change states.
type Change struct {
	Field ChangeField `json:"field"`
	From  string      `json:"from,omitempty"`
	To    string      `json:"to,omitempty"`
	Quote string      `json:"quote,omitempty"`
}

// Event is one validated event: every value in it is stated by its evidence.
type Event struct {
	Player       news.Identity `json:"player"`
	Type         Type          `json:"type"`
	Status       ReportStatus  `json:"reportStatus"`
	Attribution  string        `json:"attribution,omitempty"`
	EffectiveOn  time.Time     `json:"effectiveFrom,omitzero"`
	Duration     Duration      `json:"duration"`
	Change       Change        `json:"change,omitzero"`
	Evidence     []Quote       `json:"evidence"`
	NeedsReview  bool          `json:"needsReview,omitempty"`
	ReviewReason string        `json:"reviewReason,omitempty"`
}

// sameClaim reports whether two events state the same facts about the same
// player. Evidence, attribution and review flags are not facts.
func (e Event) sameClaim(o Event) bool {
	return samePlayer(e.Player, o.Player) && e.Type == o.Type && e.Status == o.Status &&
		e.EffectiveOn.Equal(o.EffectiveOn) && e.Duration.Kind == o.Duration.Kind &&
		e.Duration.Games == o.Duration.Games && e.Duration.Days == o.Duration.Days &&
		e.Duration.Until.Equal(o.Duration.Until) && e.Change.Field == o.Change.Field &&
		sameText(e.Change.From, o.Change.From) && sameText(e.Change.To, o.Change.To)
}

// sameSubject reports whether two events are about the same thing and may
// therefore confirm, update or contradict each other: same player and type,
// and for trades and role changes the same field.
func (e Event) sameSubject(o Event) bool {
	return samePlayer(e.Player, o.Player) && e.Type == o.Type && (!e.Type.keyedByChange() || e.Change.Field == o.Change.Field)
}

// keyedByChange reports whether events of the type are told apart by what
// they move: a league assignment and a new role are two role changes, while
// an injury placed on injured reserve is still the same injury.
func (t Type) keyedByChange() bool {
	return t == TypeTrade || t == TypeRoleChange
}

// samePlayer reports whether two identities share an NHL or a Yahoo ID: an
// event first known by one ID matches a report that knows both.
func samePlayer(a, b news.Identity) bool {
	return (a.NHLPlayerID != 0 && a.NHLPlayerID == b.NHLPlayerID) ||
		(a.YahooPlayerID != 0 && a.YahooPlayerID == b.YahooPlayerID)
}

func sameText(a, b string) bool {
	return normalize(a) == normalize(b)
}
