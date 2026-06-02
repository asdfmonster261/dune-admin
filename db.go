package main

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// queryAll runs a SELECT and returns all rows as []map[string]any. Generic
// helper used by the Database tab + the read-only SQL endpoint.
func queryAll(ctx context.Context, pool *pgxpool.Pool, sql string, args ...any) ([]map[string]any, []string, error) {
	rows, err := pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()

	descs := rows.FieldDescriptions()
	cols := make([]string, len(descs))
	for i, d := range descs {
		cols[i] = string(d.Name)
	}

	// Always return a non-nil slice so json.Marshal renders as [] rather
	// than null; frontend code does .map() on the result without guards.
	out := make([]map[string]any, 0)
	for rows.Next() {
		vals, err := rows.Values()
		if err != nil {
			return nil, cols, err
		}
		m := make(map[string]any, len(cols))
		for i, c := range cols {
			m[c] = sanitizeForJSON(vals[i])
		}
		out = append(out, m)
	}
	return out, cols, rows.Err()
}

// sanitizeForJSON coerces pgx-decoded values into json.Marshal-friendly
// types. Most things pass through; this is the catch for [16]byte UUIDs,
// big.Int numerics, etc.
func sanitizeForJSON(v any) any {
	switch x := v.(type) {
	case [16]byte:
		// pgx returns UUID as a 16-byte array; render as canonical hex.
		return fmt.Sprintf("%x-%x-%x-%x-%x", x[0:4], x[4:6], x[6:8], x[8:10], x[10:16])
	case fmt.Stringer:
		return x.String()
	}
	return v
}

// execOne runs a single statement and returns the number of rows affected.
func execOne(ctx context.Context, pool *pgxpool.Pool, sql string, args ...any) (int64, error) {
	tag, err := pool.Exec(ctx, sql, args...)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// txOne runs fn inside a transaction. Commits on nil error; rolls back on
// error. Useful for multi-statement edits.
func txOne(ctx context.Context, pool *pgxpool.Pool, fn func(pgx.Tx) error) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
