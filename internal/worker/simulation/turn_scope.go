package simulation

import "github.com/jackc/pgx/v5/pgtype"

// TurnScope identifies one agent's turn in a pool: which pool and agent,
// the simulated day, and the pool and agent configuration the workflow
// loaded once at start (or after a ContinueAsNew). The configs live on
// the input rather than as Activities-struct fields because one activity
// worker may host several pools at once.
//
// DraftPickInput, BuildManageRosterContextInput and ManageRosterInput
// embed it without a JSON tag, so encoding/json flattens its fields into
// theirs: the payload keeps the top-level pool_id / agent_id / sim_date /
// pool_config / agent_config keys of the inputs that listed these fields
// one by one, and activities scheduled before the embedding decode
// unchanged.
//
// PickTeamNameInput does not embed it: naming a team happens before the
// draft and needs neither a day nor the pool configuration (only its
// team count), so it carries the narrower pool/agent identity itself.
type TurnScope struct {
	PoolID      int32       `json:"pool_id"`
	AgentID     int32       `json:"agent_id"`
	SimDate     pgtype.Date `json:"sim_date"`
	PoolConfig  PoolConfig  `json:"pool_config"`
	AgentConfig AgentConfig `json:"agent_config"`
}

// turnScope is the scope of agentID's turn on simDate.
func (s simState) turnScope(agentID int32, simDate pgtype.Date) TurnScope {
	return TurnScope{
		PoolID:      s.PoolID,
		AgentID:     agentID,
		SimDate:     simDate,
		PoolConfig:  s.PoolConfig,
		AgentConfig: s.AgentConfigByID[agentID],
	}
}
