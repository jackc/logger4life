// Package pgstore implements core's driven ports with PostgreSQL.
package pgstore

import (
	pgsqlarbiter "github.com/jackc/pgsqlarbiter-go"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct {
	pool           *pgxpool.Pool
	userSQLArbiter *pgsqlarbiter.Arbiter
}

func New(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool, userSQLArbiter: newUserSQLArbiter()}
}
