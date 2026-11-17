package mauricereport

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PGStore reads the reporting lists from PostgreSQL. Every statement is
// parameterized and read-only.
type PGStore struct {
	pool *pgxpool.Pool
}

// NewPGStore returns a store over pool.
func NewPGStore(pool *pgxpool.Pool) *PGStore {
	return &PGStore{pool: pool}
}

// Instances lists the instances that have users, by name.
func (s *PGStore) Instances(ctx context.Context, page PageRequest) (InstancePage, error) {
	limit, after, err := pageOf(cursorInstances, page)
	if err != nil {
		return InstancePage{}, err
	}
	var afterName *string
	if after != nil {
		afterName = &after.Name
	}
	rows, err := s.pool.Query(ctx, `
		SELECT instance, count(*)
		FROM app_users
		WHERE $1::text IS NULL OR instance > $1
		GROUP BY instance
		ORDER BY instance
		LIMIT $2`, afterName, limit+1)
	if err != nil {
		return InstancePage{}, fmt.Errorf("list instances: %w", err)
	}
	instances, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Instance, error) {
		var i Instance
		return i, row.Scan(&i.Name, &i.UserCount)
	})
	if err != nil {
		return InstancePage{}, fmt.Errorf("list instances: %w", err)
	}
	instances, more := trimPage(instances, limit)
	result := InstancePage{Instances: instances}
	if more {
		last := instances[len(instances)-1]
		if result.NextCursor, err = encodeCursor(cursor{Kind: cursorInstances, Name: last.Name}); err != nil {
			return InstancePage{}, err
		}
	}
	return result, nil
}

// Users lists the users of instance, newest first_seen_at first (ties by
// ID). An instance without users, known or not, is an empty page.
func (s *PGStore) Users(ctx context.Context, instance string, page PageRequest) (UserPage, error) {
	instance, err := validateInstance(instance)
	if err != nil {
		return UserPage{}, err
	}
	limit, after, err := pageOf(cursorUsers, page)
	if err != nil {
		return UserPage{}, err
	}
	var afterAt *time.Time
	var afterID *string
	if after != nil {
		id := after.ID.String()
		afterAt, afterID = after.At, &id
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id::text, instance, auth_provider, username, display_name, email,
		       first_seen_at, profile_updated_at, disabled_at
		FROM app_users
		WHERE instance = $1
		  AND ($2::timestamptz IS NULL OR (first_seen_at, id) < ($2, $3::uuid))
		ORDER BY first_seen_at DESC, id DESC
		LIMIT $4`, instance, afterAt, afterID, limit+1)
	if err != nil {
		return UserPage{}, fmt.Errorf("list users: %w", err)
	}
	users, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (User, error) {
		var u User
		return u, row.Scan(&u.ID, &u.Instance, &u.AuthProvider, &u.Username, &u.DisplayName,
			&u.Email, &u.FirstSeenAt, &u.ProfileUpdatedAt, &u.DisabledAt)
	})
	if err != nil {
		return UserPage{}, fmt.Errorf("list users: %w", err)
	}
	users, more := trimPage(users, limit)
	result := UserPage{Users: users}
	if more {
		last := users[len(users)-1]
		if result.NextCursor, err = keyCursor(cursorUsers, last.FirstSeenAt, last.ID); err != nil {
			return UserPage{}, err
		}
	}
	return result, nil
}

// Conversations lists the conversations filter selects, newest created_at
// first (ties by ID).
func (s *PGStore) Conversations(ctx context.Context, filter ConversationFilter, page PageRequest) (ConversationPage, error) {
	q, err := validateFilter(filter)
	if err != nil {
		return ConversationPage{}, err
	}
	limit, after, err := pageOf(cursorConversations, page)
	if err != nil {
		return ConversationPage{}, err
	}
	if err := s.checkUser(ctx, q); err != nil {
		return ConversationPage{}, err
	}
	sql, args := conversationSQL(q, after, "", limit+1)
	conversations, err := s.queryConversations(ctx, sql, args)
	if err != nil {
		return ConversationPage{}, err
	}
	conversations, more := trimPage(conversations, limit)
	result := ConversationPage{Conversations: conversations}
	if more {
		last := conversations[len(conversations)-1]
		if result.NextCursor, err = keyCursor(cursorConversations, last.CreatedAt, last.ID); err != nil {
			return ConversationPage{}, err
		}
	}
	return result, nil
}

// Conversation returns the metadata of conversation id when its owner
// belongs to instance and, unless includeDeleted, the owner has not deleted
// it; found is false otherwise, whether the conversation exists elsewhere or
// not.
func (s *PGStore) Conversation(ctx context.Context, instance, id string, includeDeleted bool) (Conversation, bool, error) {
	instance, err := validateInstance(instance)
	if err != nil {
		return Conversation{}, false, err
	}
	conversationID, err := parseID("id", id)
	if err != nil {
		return Conversation{}, false, err
	}
	q := conversationQuery{instance: instance, includeDeleted: includeDeleted}
	sql, args := conversationSQL(q, nil, conversationID.String(), 1)
	conversations, err := s.queryConversations(ctx, sql, args)
	if err != nil || len(conversations) == 0 {
		return Conversation{}, false, err
	}
	return conversations[0], true, nil
}

// checkUser refuses a user selector naming no user of the instance.
func (s *PGStore) checkUser(ctx context.Context, q conversationQuery) error {
	if q.userID == nil {
		return nil
	}
	var one int
	err := s.pool.QueryRow(ctx,
		`SELECT 1 FROM app_users WHERE id = $1::uuid AND instance = $2`, q.userID.String(), q.instance).Scan(&one)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrUserNotInInstance
	}
	if err != nil {
		return fmt.Errorf("check user: %w", err)
	}
	return nil
}

func (s *PGStore) queryConversations(ctx context.Context, sql string, args []any) ([]Conversation, error) {
	rows, err := s.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("list conversations: %w", err)
	}
	conversations, err := pgx.CollectRows(rows, scanConversation)
	if err != nil {
		return nil, fmt.Errorf("list conversations: %w", err)
	}
	return conversations, nil
}

// pageOf resolves the limit and decodes the cursor of a page request.
func pageOf(kind cursorKind, page PageRequest) (int, *cursor, error) {
	limit, err := pageLimit(page.Limit)
	if err != nil {
		return 0, nil, err
	}
	after, err := decodeCursor(kind, page.Cursor)
	if err != nil {
		return 0, nil, err
	}
	return limit, after, nil
}

// trimPage drops the look-ahead row fetched beyond limit and reports
// whether there was one.
func trimPage[T any](rows []T, limit int) ([]T, bool) {
	if len(rows) > limit {
		return rows[:limit], true
	}
	return rows, false
}
