package core

import (
	"context"
	"errors"
	"strings"
)

const MaxUserSQLQueryLength = 10000

type UserSQLColumn struct {
	Name     string `json:"name"`
	DataType string `json:"data_type"`
}

type UserSQLResult struct {
	Columns   []UserSQLColumn `json:"columns"`
	Rows      [][]*string     `json:"rows"`
	RowCount  int             `json:"row_count"`
	Truncated bool            `json:"truncated"`
	ElapsedMs int64           `json:"elapsed_ms"`
}

// UserSQLExecutor is the driven port for constrained, user-authored SQL.
// Implementations must enforce the SQL policy and tenant isolation, and must
// return UserSQLFailure only when its Message is safe to expose to a caller.
type UserSQLExecutor interface {
	ExecuteUserSQL(context.Context, string, string) (UserSQLResult, error)
}

type UserSQLFailureKind string

const (
	UserSQLRejected UserSQLFailureKind = "rejected"
	UserSQLTimedOut UserSQLFailureKind = "timed_out"
	UserSQLFailed   UserSQLFailureKind = "failed"
)

// UserSQLFailure represents an expected query failure. Message is part of the
// public adapter contract and therefore must never contain raw infrastructure
// errors or database details.
type UserSQLFailure struct {
	Kind    UserSQLFailureKind
	Message string
}

func (e *UserSQLFailure) Error() string {
	if e.Message != "" {
		return e.Message
	}
	switch e.Kind {
	case UserSQLTimedOut:
		return "query timed out"
	case UserSQLRejected:
		return "query rejected"
	default:
		return "query failed"
	}
}

type ExecuteUserSQLParams struct {
	Query string `json:"query"`
}

func (p *ExecuteUserSQLParams) Validate() error {
	p.Query = strings.TrimSpace(p.Query)
	if p.Query == "" {
		return errors.New("query is required")
	}
	if len(p.Query) > MaxUserSQLQueryLength {
		return errors.New("query is too long")
	}
	return nil
}

var ExecuteUserSQL = Define(ActionDef[ExecuteUserSQLParams, UserSQLResult]{
	Name: "execute_user_sql", Description: "Execute a constrained read-only SQL query for the current user.",
	Handler: func(ctx context.Context, c *Core, p ExecuteUserSQLParams) (UserSQLResult, error) {
		userID, err := requiredUser(ctx)
		if err != nil {
			return UserSQLResult{}, err
		}
		return c.userSQL.ExecuteUserSQL(ctx, userID, p.Query)
	},
})
