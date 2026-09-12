package core

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type blockingSQLExecutor struct {
	entered chan struct{}
	stopped chan struct{}
}

func (e blockingSQLExecutor) ExecuteUserSQL(ctx context.Context, _, _ string) (UserSQLResult, error) {
	e.entered <- struct{}{}
	<-e.stopped
	return UserSQLResult{}, ctx.Err()
}

func TestSQLConcurrencyAdmissionAndCancellation(t *testing.T) {
	executor := blockingSQLExecutor{entered: make(chan struct{}, 3), stopped: make(chan struct{})}
	app := New(Config{UserSQL: executor, SQLConcurrencyPerUser: 1, SQLConcurrencyGlobal: 2})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 2)
	for _, user := range []string{"alice", "bob"} {
		go func() {
			_, err := ExecuteUserSQL.Call(WithUserID(ctx, user), app, ExecuteUserSQLParams{Query: "SELECT 1"})
			done <- err
		}()
		select {
		case <-executor.entered:
		case <-time.After(5 * time.Second):
			t.Fatal("query did not start")
		}
		_, err := ExecuteUserSQL.Call(WithUserID(context.Background(), user), app, ExecuteUserSQLParams{Query: "SELECT 1"})
		var failure *UserSQLFailure
		require.ErrorAs(t, err, &failure)
		require.Equal(t, UserSQLBusy, failure.Kind)
	}
	cancel()
	// A canceled query that has not stopped still occupies its slot.
	_, err := ExecuteUserSQL.Call(WithUserID(context.Background(), "carol"), app, ExecuteUserSQLParams{Query: "SELECT 1"})
	var failure *UserSQLFailure
	require.ErrorAs(t, err, &failure)
	require.Equal(t, UserSQLBusy, failure.Kind)
	close(executor.stopped)
	for range 2 {
		select {
		case err := <-done:
			require.ErrorIs(t, err, context.Canceled)
		case <-time.After(5 * time.Second):
			t.Fatal("query did not stop")
		}
	}
	_, err = ExecuteUserSQL.Call(WithUserID(context.Background(), "carol"), app, ExecuteUserSQLParams{Query: "SELECT 1"})
	require.NoError(t, err)
	require.Empty(t, app.sqlConcurrency.users)
	require.Zero(t, app.sqlConcurrency.active)
}
