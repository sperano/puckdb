// Package newsadjust turns validated player news events into versioned,
// explainable adjustments of a baseline projection snapshot. It produces one
// adjusted snapshot per assumption scenario (conservative, base, optimistic)
// so league rankings can be recomputed from adjusted inputs and compared
// with the baseline. Nothing here reads live state: the same baseline,
// event versions, overrides, policy and as-of time give the same result.
package newsadjust

import (
	"fmt"
	"slices"
	"time"

	"github.com/sperano/puckdb/internal/news"
)

// EventType is what a validated event says happened to a player.
type EventType string

const (
	// EventSuspension: league or team suspension.
	EventSuspension EventType = "suspension"
	// EventInjury: injury or illness.
	EventInjury EventType = "injury"
	// EventAbsence: leave of absence or another non-injury unavailability.
	EventAbsence EventType = "absence"
	// EventReturn: reinstatement, activation or clearance to play. It closes
	// earlier availability events instead of adding an effect of its own.
	EventReturn EventType = "return"
	// EventTrade: the player changed teams.
	EventTrade EventType = "trade"
	// EventRoleChange: ice time, power-play or goalie workload role changed.
	EventRoleChange EventType = "role_change"
)

// ReportStatus separates a confirmed report from a rumor. It says nothing
// about fantasy impact.
type ReportStatus string

const (
	StatusConfirmed ReportStatus = "confirmed"
	// StatusReported: attributed to a reporter or unnamed sources, not
	// announced. It counts like a confirmed report, which is preferred as
	// the primary report of an incident.
	StatusReported ReportStatus = "reported"
	StatusRumor    ReportStatus = "rumor"
)

// firmness orders statuses by how firmly they report an event.
func (s ReportStatus) firmness() int {
	switch s {
	case StatusConfirmed:
		return 2
	case StatusReported:
		return 1
	default:
		return 0
	}
}

// Lifecycle is the durable state of an event version.
type Lifecycle string

const (
	LifecycleActive     Lifecycle = "active"
	LifecycleSuperseded Lifecycle = "superseded"
	LifecycleRetracted  Lifecycle = "retracted"
	// LifecycleResolved: the event ended; EffectiveUntil says when.
	LifecycleResolved Lifecycle = "resolved"
)

// DurationKind says how much the sources say about an absence's length.
type DurationKind string

const (
	// DurationGames: the sources attribute an exact number of games.
	DurationGames DurationKind = "games"
	// DurationUntil: the sources give a return date (EffectiveUntil).
	DurationUntil        DurationKind = "until"
	DurationDayToDay     DurationKind = "day_to_day"
	DurationWeekToWeek   DurationKind = "week_to_week"
	DurationMonthToMonth DurationKind = "month_to_month"
	DurationIndefinite   DurationKind = "indefinite"
	// DurationSeason: the sources say the player is out for the season.
	DurationSeason DurationKind = "season"
	// DurationUnknown: the sources say nothing about duration.
	DurationUnknown DurationKind = "unknown"
)

// Direction is the sign of a reported role change.
type Direction string

const (
	DirectionNone Direction = ""
	DirectionUp   Direction = "up"
	DirectionDown Direction = "down"
)

// GoalieRole is a reported goalie workload role.
type GoalieRole string

const (
	GoalieRoleNone    GoalieRole = ""
	GoalieRoleStarter GoalieRole = "starter"
	GoalieRoleTandem  GoalieRole = "tandem"
	GoalieRoleBackup  GoalieRole = "backup"
)

// Duration is the attributed length of an availability event. Games is set
// only for DurationGames; no other kind carries a number.
type Duration struct {
	Kind  DurationKind `json:"kind"`
	Games int          `json:"games,omitempty"`
}

// RoleChange is a reported change of team or role. Directions and roles are
// what the evidence says; their numeric effect comes from the Policy.
type RoleChange struct {
	TeamID     *int64     `json:"team_id,omitempty"`
	IceTime    Direction  `json:"ice_time,omitempty"`
	PowerPlay  Direction  `json:"power_play,omitempty"`
	GoalieRole GoalieRole `json:"goalie_role,omitempty"`
}

func (r *RoleChange) empty() bool {
	return r == nil || r.TeamID == nil && r.IceTime == DirectionNone &&
		r.PowerPlay == DirectionNone && r.GoalieRole == GoalieRoleNone
}

// EvidenceRef points at the stored article version behind an event.
type EvidenceRef struct {
	VersionID   int64     `json:"version_id"`
	Publisher   string    `json:"publisher"`
	Kind        news.Kind `json:"kind"`
	URL         string    `json:"url,omitempty"`
	ReportedAt  time.Time `json:"reported_at"`
	RetrievedAt time.Time `json:"retrieved_at"`
	Quote       string    `json:"quote,omitempty"`
}

// Event is one version of a validated news event, the contract between
// event extraction and adjustment. ID is stable across versions; Version
// increases with every recorded revision. RecordedAt is when PuckDB learned
// this version and decides what a replay at a historical as-of time sees.
// Hold, when set, says why the event may only alert: it is routed to
// review, its extractor has not passed evaluation, or it has no numeric
// mapping. A held event never changes a projection or closes another.
type Event struct {
	ID             string        `json:"id"`
	Version        int           `json:"version"`
	IncidentID     int64         `json:"incident_id,omitempty"`
	PlayerKey      string        `json:"player_key"`
	Type           EventType     `json:"type"`
	Status         ReportStatus  `json:"status"`
	Lifecycle      Lifecycle     `json:"lifecycle"`
	ReportedAt     time.Time     `json:"reported_at"`
	RecordedAt     time.Time     `json:"recorded_at"`
	EffectiveFrom  time.Time     `json:"effective_from"`
	EffectiveUntil time.Time     `json:"effective_until,omitzero"`
	Duration       Duration      `json:"duration"`
	Role           *RoleChange   `json:"role,omitempty"`
	Supersedes     []string      `json:"supersedes,omitempty"`
	Evidence       []EvidenceRef `json:"evidence"`
	Hold           string        `json:"hold,omitempty"`
}

// affectsAvailability reports whether the event removes games.
func (e Event) affectsAvailability() bool {
	return e.Type == EventSuspension || e.Type == EventInjury || e.Type == EventAbsence
}

// start is when the event takes effect.
func (e Event) start() time.Time {
	if e.EffectiveFrom.IsZero() {
		return e.ReportedAt
	}
	return e.EffectiveFrom
}

// authority ranks the event's best evidence: official before structured
// before reporting. Lower is more authoritative.
func (e Event) authority() int {
	best := len(authorityOrder)
	for _, ref := range e.Evidence {
		if rank := slices.Index(authorityOrder, ref.Kind); rank >= 0 && rank < best {
			best = rank
		}
	}
	return best
}

var authorityOrder = []news.Kind{news.KindOfficial, news.KindStructured, news.KindReporting}

// latestEvidence is the newest retrieval time of the event's evidence.
func (e Event) latestEvidence() time.Time {
	var latest time.Time
	for _, ref := range e.Evidence {
		if ref.RetrievedAt.After(latest) {
			latest = ref.RetrievedAt
		}
	}
	return latest
}

var (
	eventTypes     = []EventType{EventSuspension, EventInjury, EventAbsence, EventReturn, EventTrade, EventRoleChange}
	reportStatuses = []ReportStatus{StatusConfirmed, StatusReported, StatusRumor}
	lifecycles     = []Lifecycle{LifecycleActive, LifecycleSuperseded, LifecycleRetracted, LifecycleResolved}
	durationKinds  = []DurationKind{
		DurationGames, DurationUntil, DurationDayToDay, DurationWeekToWeek,
		DurationMonthToMonth, DurationIndefinite, DurationSeason, DurationUnknown,
	}
	directions  = []Direction{DirectionNone, DirectionUp, DirectionDown}
	goalieRoles = []GoalieRole{GoalieRoleNone, GoalieRoleStarter, GoalieRoleTandem, GoalieRoleBackup}
)

// Validate rejects an event that could not have come from validated
// extraction: unknown enums, missing identity or evidence, numbers the
// duration kind does not allow, or an impossible chronology. A held event
// only alerts, so only its identity is checked.
func (e Event) Validate() error {
	if err := e.validateIdentity(); err != nil {
		return fmt.Errorf("event %q v%d: %w", e.ID, e.Version, err)
	}
	if e.Hold != "" {
		return nil
	}
	if err := e.validateEffect(); err != nil {
		return fmt.Errorf("event %q v%d: %w", e.ID, e.Version, err)
	}
	return nil
}

func (e Event) validateIdentity() error {
	switch {
	case e.ID == "" || e.Version <= 0:
		return fmt.Errorf("ID and positive version are required")
	case e.PlayerKey == "":
		return fmt.Errorf("player key is required")
	case !slices.Contains(eventTypes, e.Type):
		return fmt.Errorf("unknown type %q", e.Type)
	case !slices.Contains(reportStatuses, e.Status):
		return fmt.Errorf("unknown status %q", e.Status)
	case !slices.Contains(lifecycles, e.Lifecycle):
		return fmt.Errorf("unknown lifecycle %q", e.Lifecycle)
	case e.ReportedAt.IsZero() || e.RecordedAt.IsZero():
		return fmt.Errorf("reported and recorded times are required")
	case len(e.Evidence) == 0:
		return fmt.Errorf("at least one evidence reference is required")
	case slices.Contains(e.Supersedes, e.ID):
		return fmt.Errorf("an event cannot supersede itself")
	}
	for _, ref := range e.Evidence {
		if ref.VersionID <= 0 || !slices.Contains(authorityOrder, ref.Kind) {
			return fmt.Errorf("evidence needs an article version and a known source kind")
		}
	}
	return nil
}

func (e Event) validateEffect() error {
	// A resolved event may end before it took effect: the return was
	// reported first.
	if !e.EffectiveUntil.IsZero() && !e.EffectiveUntil.After(e.start()) && e.Lifecycle != LifecycleResolved {
		return fmt.Errorf("effective until must follow the effective start")
	}
	if e.Lifecycle == LifecycleResolved && e.EffectiveUntil.IsZero() {
		return fmt.Errorf("a resolved event needs its end time")
	}
	switch {
	case e.affectsAvailability():
		return e.validateDuration()
	case e.Type == EventTrade && (e.Role == nil || e.Role.TeamID == nil):
		return fmt.Errorf("a trade needs the new team")
	case e.Type == EventRoleChange && e.Role.empty():
		return fmt.Errorf("a role change needs at least one changed role")
	}
	if e.Role != nil && (!slices.Contains(directions, e.Role.IceTime) ||
		!slices.Contains(directions, e.Role.PowerPlay) || !slices.Contains(goalieRoles, e.Role.GoalieRole)) {
		return fmt.Errorf("unknown role value")
	}
	return nil
}

func (e Event) validateDuration() error {
	d := e.Duration
	switch {
	case !slices.Contains(durationKinds, d.Kind):
		return fmt.Errorf("unknown duration kind %q", d.Kind)
	case d.Kind == DurationGames && d.Games <= 0:
		return fmt.Errorf("a games duration needs a positive attributed game count")
	case d.Kind != DurationGames && d.Games != 0:
		return fmt.Errorf("duration %q cannot carry a game count", d.Kind)
	case d.Kind == DurationUntil && e.EffectiveUntil.IsZero():
		return fmt.Errorf("an until duration needs the return time")
	}
	return nil
}

// normalized returns the event with every time in UTC, so identical
// instants hash identically whatever location they were loaded in.
func (e Event) normalized() Event {
	e.ReportedAt, e.RecordedAt = utc(e.ReportedAt), utc(e.RecordedAt)
	e.EffectiveFrom, e.EffectiveUntil = utc(e.EffectiveFrom), utc(e.EffectiveUntil)
	e.Supersedes = slices.Clone(e.Supersedes)
	e.Evidence = slices.Clone(e.Evidence)
	for i := range e.Evidence {
		e.Evidence[i].ReportedAt = utc(e.Evidence[i].ReportedAt)
		e.Evidence[i].RetrievedAt = utc(e.Evidence[i].RetrievedAt)
	}
	return e
}

func utc(t time.Time) time.Time {
	if t.IsZero() {
		return time.Time{}
	}
	return t.UTC()
}
