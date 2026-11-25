// Package similar spreads a contract verification to every contract running
// the same runtime code (explorer/docs/design.md, "similar contracts"). One
// submission verifies every contract deployed with the same code (a factory's tokens, for example).
//
// A 'similar' row copies the source's ABI, sources and compiler settings and
// points at it through source_address. It never replaces a contract's own
// full, partial or imported verification. Constructor arguments are left
// null: they differ per contract.
package similar

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Querier is a pgx pool, connection or transaction.
type Querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

const copyColumns = `name, file_path, compiler_version, language, evm_version,
	optimization_enabled, optimization_runs, via_ir, license_type, abi, sources, compiler_settings`

// FanOut marks every other contract with source's code hash as verified
// 'similar' to source, and refreshes existing 'similar' rows to point at it.
// source must itself be verified (any match type but 'similar'). It returns
// the number of rows written.
func FanOut(ctx context.Context, q Querier, source string) (int64, error) {
	tag, err := q.Exec(ctx, `
		INSERT INTO verified_contracts (address, `+copyColumns+`, match_type, constructor_args, source_address, verified_via, verified_at)
		SELECT a.address, `+prefixed("v")+`, 'similar', NULL, v.address, 'similar', now()
		FROM verified_contracts v
		JOIN addresses src ON src.address = v.address AND src.code_hash IS NOT NULL
		JOIN addresses a   ON a.code_hash = src.code_hash AND a.address <> v.address
		WHERE v.address = $1 AND v.match_type <> 'similar'
		ON CONFLICT (address) DO UPDATE SET
			name = EXCLUDED.name, file_path = EXCLUDED.file_path, compiler_version = EXCLUDED.compiler_version,
			language = EXCLUDED.language, evm_version = EXCLUDED.evm_version,
			optimization_enabled = EXCLUDED.optimization_enabled, optimization_runs = EXCLUDED.optimization_runs,
			via_ir = EXCLUDED.via_ir, license_type = EXCLUDED.license_type, abi = EXCLUDED.abi,
			sources = EXCLUDED.sources, compiler_settings = EXCLUDED.compiler_settings,
			source_address = EXCLUDED.source_address, verified_at = EXCLUDED.verified_at
		WHERE verified_contracts.match_type = 'similar'`, source)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// Adopt verifies address as 'similar' when another contract with codeHash is
// already verified. It prefers a full match, then partial, then imported,
// then the earliest verification. It does nothing when address already has a
// row or no source exists, and reports whether it wrote one.
func Adopt(ctx context.Context, q Querier, address, codeHash string) (bool, error) {
	if codeHash == "" {
		return false, nil
	}
	tag, err := q.Exec(ctx, `
		INSERT INTO verified_contracts (address, `+copyColumns+`, match_type, constructor_args, source_address, verified_via, verified_at)
		SELECT $1, `+prefixed("v")+`, 'similar', NULL, v.address, 'similar', now()
		FROM verified_contracts v
		JOIN addresses src ON src.address = v.address
		WHERE src.code_hash = $2 AND v.address <> $1 AND v.match_type <> 'similar'
		ORDER BY CASE v.match_type WHEN 'full' THEN 0 WHEN 'partial' THEN 1 ELSE 2 END, v.verified_at, v.address
		LIMIT 1
		ON CONFLICT (address) DO NOTHING`, address, codeHash)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

func prefixed(alias string) string {
	return alias + `.name, ` + alias + `.file_path, ` + alias + `.compiler_version, ` + alias + `.language, ` +
		alias + `.evm_version, ` + alias + `.optimization_enabled, ` + alias + `.optimization_runs, ` +
		alias + `.via_ir, ` + alias + `.license_type, ` + alias + `.abi, ` + alias + `.sources, ` + alias + `.compiler_settings`
}
