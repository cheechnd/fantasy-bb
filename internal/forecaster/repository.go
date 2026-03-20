package forecaster

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

type Repository struct {
	db *sql.DB
}

func NewRepository(db *sql.DB) *Repository {
	return &Repository{db: db}
}

func (r *Repository) InsertImport(ctx context.Context, sourceType SourceType, sourceIdentifier string, rawRows int, starts []ProbableStartInput, warnings []ParseWarningInput, status string, notes string) (ImportRun, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return ImportRun{}, fmt.Errorf("begin import tx: %w", err)
	}
	defer tx.Rollback()

	now := time.Now().UTC()
	res, err := tx.ExecContext(ctx, `
		INSERT INTO forecaster_import_runs (
			source_type, source_identifier, imported_at, raw_row_count,
			probable_start_count, warning_count, status, notes_json
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, sourceType, sourceIdentifier, now.Format(time.RFC3339), rawRows, len(starts), len(warnings), status, notes)
	if err != nil {
		return ImportRun{}, fmt.Errorf("insert forecaster import run: %w", err)
	}
	importID, err := res.LastInsertId()
	if err != nil {
		return ImportRun{}, fmt.Errorf("read import run id: %w", err)
	}

	for _, s := range starts {
		var gameDate any
		if s.GameDate != nil {
			gameDate = s.GameDate.Format("2006-01-02")
		}
		_, err := tx.ExecContext(ctx, `
			INSERT INTO probable_starts (
				import_run_id, game_date, source_date_text, team, opponent,
				pitcher_name, throws_hand, projected_fpts, status, raw_fields_json, created_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, importID, gameDate, s.SourceDateRaw, strings.ToUpper(s.Team), strings.ToUpper(s.Opponent), s.PitcherName, strings.ToUpper(s.ThrowsHand), s.ProjectedFPTS, s.Status, s.RawFieldsJSON(), now.Format(time.RFC3339))
		if err != nil {
			return ImportRun{}, fmt.Errorf("insert probable start: %w", err)
		}
	}

	for _, w := range warnings {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO parse_warnings (
				import_run_id, warning_type, message, row_context_json, created_at
			) VALUES (?, ?, ?, ?, ?)
		`, importID, w.WarningType, w.Message, w.RowContextJSON(), now.Format(time.RFC3339))
		if err != nil {
			return ImportRun{}, fmt.Errorf("insert parse warning: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return ImportRun{}, fmt.Errorf("commit import tx: %w", err)
	}

	return ImportRun{
		ID:                 importID,
		SourceType:         sourceType,
		SourceIdentifier:   sourceIdentifier,
		ImportedAt:         now,
		RawRowCount:        rawRows,
		ProbableStartCount: len(starts),
		WarningCount:       len(warnings),
		Status:             status,
		NotesJSON:          notes,
	}, nil
}

func (r *Repository) LatestImportRun(ctx context.Context) (*ImportRun, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT id, source_type, source_identifier, imported_at, raw_row_count,
		       probable_start_count, warning_count, status, COALESCE(notes_json, '')
		FROM forecaster_import_runs
		ORDER BY id DESC
		LIMIT 1
	`)

	var run ImportRun
	var importedAt string
	if err := row.Scan(&run.ID, &run.SourceType, &run.SourceIdentifier, &importedAt, &run.RawRowCount, &run.ProbableStartCount, &run.WarningCount, &run.Status, &run.NotesJSON); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("query latest import run: %w", err)
	}
	tm, err := time.Parse(time.RFC3339, importedAt)
	if err != nil {
		return nil, fmt.Errorf("parse import timestamp: %w", err)
	}
	run.ImportedAt = tm
	return &run, nil
}

func (r *Repository) SourceStatus(ctx context.Context, limit int) ([]ImportRun, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, source_type, source_identifier, imported_at, raw_row_count,
		       probable_start_count, warning_count, status, COALESCE(notes_json, '')
		FROM forecaster_import_runs
		ORDER BY id DESC
		LIMIT ?
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("query source status: %w", err)
	}
	defer rows.Close()

	var out []ImportRun
	for rows.Next() {
		var run ImportRun
		var importedAt string
		if err := rows.Scan(&run.ID, &run.SourceType, &run.SourceIdentifier, &importedAt, &run.RawRowCount, &run.ProbableStartCount, &run.WarningCount, &run.Status, &run.NotesJSON); err != nil {
			return nil, fmt.Errorf("scan source status row: %w", err)
		}
		tm, err := time.Parse(time.RFC3339, importedAt)
		if err != nil {
			return nil, fmt.Errorf("parse source-status imported_at: %w", err)
		}
		run.ImportedAt = tm
		out = append(out, run)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate source status: %w", err)
	}
	return out, nil
}

func (r *Repository) ListWarnings(ctx context.Context, importRunID *int64, limit int) ([]ParseWarning, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, import_run_id, warning_type, message, COALESCE(row_context_json, ''), created_at
		FROM parse_warnings
		WHERE import_run_id = COALESCE(?, (SELECT id FROM forecaster_import_runs ORDER BY id DESC LIMIT 1))
		ORDER BY id DESC
		LIMIT ?
	`, nullInt64(importRunID), limit)
	if err != nil {
		return nil, fmt.Errorf("query parse warnings: %w", err)
	}
	defer rows.Close()

	out := make([]ParseWarning, 0, limit)
	for rows.Next() {
		var w ParseWarning
		var createdAtRaw string
		if err := rows.Scan(&w.ID, &w.ImportRunID, &w.WarningType, &w.Message, &w.RowContextJSON, &createdAtRaw); err != nil {
			return nil, fmt.Errorf("scan parse warning: %w", err)
		}
		createdAt, err := time.Parse(time.RFC3339, createdAtRaw)
		if err != nil {
			return nil, fmt.Errorf("parse parse_warning created_at: %w", err)
		}
		w.CreatedAt = createdAt
		out = append(out, w)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate parse warnings: %w", err)
	}
	return out, nil
}

func (r *Repository) ListProbableStarts(ctx context.Context, filter ListFilter) ([]ProbableStart, error) {
	where := []string{"import_run_id = COALESCE(?, (SELECT id FROM forecaster_import_runs ORDER BY id DESC LIMIT 1))"}
	args := []any{nullInt64(filter.ImportRun)}

	if filter.From != nil {
		where = append(where, "game_date >= ?")
		args = append(args, filter.From.Format("2006-01-02"))
	}
	if filter.To != nil {
		where = append(where, "game_date <= ?")
		args = append(args, filter.To.Format("2006-01-02"))
	}
	if filter.Team != "" {
		where = append(where, "team = ?")
		args = append(args, strings.ToUpper(filter.Team))
	}
	if filter.Pitcher != "" {
		where = append(where, "pitcher_name LIKE ?")
		args = append(args, "%"+filter.Pitcher+"%")
	}
	if filter.ThrowsHand != "" {
		where = append(where, "throws_hand = ?")
		args = append(args, strings.ToUpper(filter.ThrowsHand))
	}
	if filter.MinFPTS != nil {
		where = append(where, "projected_fpts >= ?")
		args = append(args, *filter.MinFPTS)
	}
	if !filter.IncludeTBD {
		where = append(where, "status != 'tbd'")
	}

	query := `
		SELECT id, import_run_id, game_date, source_date_text, team, opponent,
		       pitcher_name, throws_hand, projected_fpts, status,
		       COALESCE(raw_fields_json, ''), created_at
		FROM probable_starts
		WHERE ` + strings.Join(where, " AND ") + `
		ORDER BY game_date ASC, team ASC, projected_fpts DESC
	`

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query probable starts: %w", err)
	}
	defer rows.Close()

	return scanProbableStarts(rows)
}

func (r *Repository) TopProbableStarts(ctx context.Context, filter TopFilter) ([]ProbableStart, error) {
	if filter.TopN <= 0 {
		filter.TopN = 10
	}
	where := []string{"import_run_id = COALESCE(?, (SELECT id FROM forecaster_import_runs ORDER BY id DESC LIMIT 1))", "status = 'scheduled'"}
	args := []any{nullInt64(filter.ImportRun)}
	if filter.From != nil {
		where = append(where, "game_date >= ?")
		args = append(args, filter.From.Format("2006-01-02"))
	}
	if filter.To != nil {
		where = append(where, "game_date <= ?")
		args = append(args, filter.To.Format("2006-01-02"))
	}
	if filter.Team != "" {
		where = append(where, "team = ?")
		args = append(args, strings.ToUpper(filter.Team))
	}
	if filter.MinFPTS != nil {
		where = append(where, "projected_fpts >= ?")
		args = append(args, *filter.MinFPTS)
	}
	args = append(args, filter.TopN)
	query := `
		SELECT id, import_run_id, game_date, source_date_text, team, opponent,
		       pitcher_name, throws_hand, projected_fpts, status,
		       COALESCE(raw_fields_json, ''), created_at
		FROM probable_starts
		WHERE ` + strings.Join(where, " AND ") + `
		ORDER BY projected_fpts DESC, game_date ASC
		LIMIT ?
	`
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query top probable starts: %w", err)
	}
	defer rows.Close()
	return scanProbableStarts(rows)
}

func (r *Repository) Clear(ctx context.Context) (ClearResult, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return ClearResult{}, fmt.Errorf("begin clear tx: %w", err)
	}
	defer tx.Rollback()

	result := ClearResult{}
	if rs, err := tx.ExecContext(ctx, `DELETE FROM parse_warnings`); err != nil {
		return result, fmt.Errorf("clear parse_warnings: %w", err)
	} else if n, err := rs.RowsAffected(); err == nil {
		result.WarningsDeleted = n
	}
	if rs, err := tx.ExecContext(ctx, `DELETE FROM probable_starts`); err != nil {
		return result, fmt.Errorf("clear probable_starts: %w", err)
	} else if n, err := rs.RowsAffected(); err == nil {
		result.ProbableStartsDeleted = n
	}
	if rs, err := tx.ExecContext(ctx, `DELETE FROM forecaster_import_runs`); err != nil {
		return result, fmt.Errorf("clear forecaster_import_runs: %w", err)
	} else if n, err := rs.RowsAffected(); err == nil {
		result.ImportRunsDeleted = n
	}
	if err := tx.Commit(); err != nil {
		return result, fmt.Errorf("commit clear tx: %w", err)
	}
	return result, nil
}

func nullInt64(v *int64) any {
	if v == nil {
		return nil
	}
	return *v
}

func scanProbableStarts(rows *sql.Rows) ([]ProbableStart, error) {
	var out []ProbableStart
	for rows.Next() {
		var ps ProbableStart
		var gameDate sql.NullString
		var projected sql.NullFloat64
		var createdAtRaw string
		if err := rows.Scan(
			&ps.ID,
			&ps.ImportRunID,
			&gameDate,
			&ps.SourceDateRaw,
			&ps.Team,
			&ps.Opponent,
			&ps.PitcherName,
			&ps.ThrowsHand,
			&projected,
			&ps.Status,
			&ps.RawFieldsJSON,
			&createdAtRaw,
		); err != nil {
			return nil, fmt.Errorf("scan probable start: %w", err)
		}
		if gameDate.Valid {
			tm, err := time.Parse("2006-01-02", gameDate.String)
			if err != nil {
				return nil, fmt.Errorf("parse probable start game_date: %w", err)
			}
			ps.GameDate = &tm
		}
		if projected.Valid {
			v := projected.Float64
			ps.ProjectedFPTS = &v
		}
		createdAt, err := time.Parse(time.RFC3339, createdAtRaw)
		if err != nil {
			return nil, fmt.Errorf("parse probable start created_at: %w", err)
		}
		ps.CreatedAt = createdAt
		out = append(out, ps)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate probable starts: %w", err)
	}
	return out, nil
}
