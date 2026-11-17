package mauricereport

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// snapshot is the transaction of an overview: every statement of one
// overview reads the same snapshot, so its totals, model rows and user rows
// agree even while turns are being written.
var snapshot = pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}

// Overview aggregates the activity filter includes: the same conversations
// Conversations returns for it, their turns meeting the turn filters, and
// those turns' and conversations' LLM calls (see Activity).
func (s *PGStore) Overview(ctx context.Context, filter ConversationFilter) (Overview, error) {
	q, err := validateFilter(filter)
	if err != nil {
		return Overview{}, err
	}
	var out Overview
	err = pgx.BeginTxFunc(ctx, s.pool, snapshot, func(tx pgx.Tx) error {
		if err := checkUser(ctx, tx, q); err != nil {
			return err
		}
		var row activityRow
		sql, args := totalsSQL(q)
		if err := tx.QueryRow(ctx, sql, args...).Scan(row.targets()...); err != nil {
			return fmt.Errorf("overview totals: %w", err)
		}
		row.finish()
		out = Overview{Users: row.users, ActiveUsers: row.activeUsers, Activity: row.activity}
		sql, args = modelsSQL(q)
		rows, err := tx.Query(ctx, sql, args...)
		if err != nil {
			return fmt.Errorf("overview models: %w", err)
		}
		if out.Models, err = pgx.CollectRows(rows, scanModel); err != nil {
			return fmt.Errorf("overview models: %w", err)
		}
		return nil
	})
	if err != nil {
		return Overview{}, err
	}
	return out, nil
}

// UserActivity lists the users of filter's scope (its instance, or its one
// user), newest first_seen_at first (ties by ID), each with the activity of
// theirs filter includes. Every user of the scope is listed, active or not,
// so the pages do not depend on activity.
func (s *PGStore) UserActivity(ctx context.Context, filter ConversationFilter, page PageRequest) (UserActivityPage, error) {
	q, err := validateFilter(filter)
	if err != nil {
		return UserActivityPage{}, err
	}
	scope, err := q.scope()
	if err != nil {
		return UserActivityPage{}, err
	}
	limit, after, err := pageOf(cursorUserActivity, scope, page)
	if err != nil {
		return UserActivityPage{}, err
	}
	var out UserActivityPage
	err = pgx.BeginTxFunc(ctx, s.pool, snapshot, func(tx pgx.Tx) error {
		if err := checkUser(ctx, tx, q); err != nil {
			return err
		}
		var userID *string
		if q.userID != nil {
			id := q.userID.String()
			userID = &id
		}
		users, err := listUsers(ctx, tx, q.instance, userID, after, limit+1)
		if err != nil {
			return err
		}
		users, more := trimPage(users, limit)
		if out.Users, err = userActivities(ctx, tx, q, users); err != nil {
			return err
		}
		if more {
			last := users[len(users)-1]
			out.NextCursor, err = keyCursor(cursorUserActivity, scope, last.FirstSeenAt, last.ID)
		}
		return err
	})
	if err != nil {
		return UserActivityPage{}, err
	}
	return out, nil
}

// userActivities reads the activity q includes of each of users, in their
// order.
func userActivities(ctx context.Context, tx pgx.Tx, q conversationQuery, users []User) ([]UserActivity, error) {
	if len(users) == 0 {
		return []UserActivity{}, nil
	}
	ids := make([]string, len(users))
	for i, u := range users {
		ids[i] = u.ID
	}
	sql, args := userActivitySQL(q, ids)
	rows, err := tx.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("user activity: %w", err)
	}
	byUser := make(map[string]activityRow, len(users))
	_, err = pgx.CollectRows(rows, func(row pgx.CollectableRow) (struct{}, error) {
		var id string
		var r activityRow
		if err := row.Scan(append([]any{&id}, r.targets()...)...); err != nil {
			return struct{}{}, err
		}
		r.finish()
		byUser[id] = r
		return struct{}{}, nil
	})
	if err != nil {
		return nil, fmt.Errorf("user activity: %w", err)
	}
	out := make([]UserActivity, len(users))
	for i, u := range users {
		r := byUser[u.ID]
		out[i] = UserActivity{User: u, Active: r.activeUsers > 0, Activity: r.activity}
	}
	return out, nil
}
