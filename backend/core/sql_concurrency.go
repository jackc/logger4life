package core

import "sync"

// sqlConcurrency admits work without queueing. It only retains users with
// running queries, so bookkeeping is bounded by the global query limit.
type sqlConcurrency struct {
	mu                      sync.Mutex
	users                   map[string]int
	active, perUser, global int
}

func newSQLConcurrency(perUser, global int) *sqlConcurrency {
	if perUser <= 0 {
		perUser = 2
	}
	if global <= 0 {
		global = 8
	}
	return &sqlConcurrency{users: make(map[string]int), perUser: perUser, global: global}
}

func (l *sqlConcurrency) acquire(user string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.active >= l.global || l.users[user] >= l.perUser {
		return false
	}
	l.active++
	l.users[user]++
	return true
}

func (l *sqlConcurrency) release(user string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.active--
	l.users[user]--
	if l.users[user] == 0 {
		delete(l.users, user)
	}
}
