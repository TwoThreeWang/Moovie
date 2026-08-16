package report

import (
	"context"
	"fmt"
	"time"

	"github.com/TwoThreeWang/Moovie/new/internal/platform/database"
)

type PostgresStore struct{ database database.Executor }

func NewPostgresStore(executor database.Executor) *PostgresStore {
	return &PostgresStore{database: executor}
}

const reportColumns = `id, user_id, year_month, watched_count, total_duration_minutes, avg_rating, genre_stats,
top_movie_id, top_movie_title, top_movie_poster, top_movie_rating, continuous_days, persona_title, persona_line,
percentile_rank, featured_quote, poster_wall, status, error_message, generated_at, created_at, updated_at`

func (store *PostgresStore) Save(ctx context.Context, report MonthlyReport) (*MonthlyReport, error) {
	if report.Status == "" {
		report.Status = StatusPending
	}
	if report.CreatedAt.IsZero() {
		report.CreatedAt = time.Now()
	}
	rows, err := store.database.Query(ctx, `INSERT INTO monthly_reports
(user_id, year_month, watched_count, total_duration_minutes, avg_rating, genre_stats, top_movie_id,
top_movie_title, top_movie_poster, top_movie_rating, continuous_days, persona_title, persona_line,
percentile_rank, featured_quote, poster_wall, status, error_message, generated_at, created_at, updated_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,
CASE WHEN $17 = 'generated' THEN COALESCE($19, NOW()) ELSE $19 END, $20, NOW())
ON CONFLICT (user_id, year_month) DO UPDATE SET watched_count=EXCLUDED.watched_count,
total_duration_minutes=EXCLUDED.total_duration_minutes, avg_rating=EXCLUDED.avg_rating,
genre_stats=EXCLUDED.genre_stats, top_movie_id=EXCLUDED.top_movie_id, top_movie_title=EXCLUDED.top_movie_title,
top_movie_poster=EXCLUDED.top_movie_poster, top_movie_rating=EXCLUDED.top_movie_rating,
continuous_days=EXCLUDED.continuous_days, persona_title=EXCLUDED.persona_title, persona_line=EXCLUDED.persona_line,
percentile_rank=EXCLUDED.percentile_rank, featured_quote=EXCLUDED.featured_quote, poster_wall=EXCLUDED.poster_wall,
status=EXCLUDED.status, error_message=EXCLUDED.error_message,
generated_at=CASE WHEN EXCLUDED.status='generated' THEN COALESCE(monthly_reports.generated_at, NOW()) ELSE monthly_reports.generated_at END,
updated_at=NOW() RETURNING `+reportColumns,
		report.UserID, report.YearMonth, report.WatchedCount, report.TotalDurationMinutes, report.AvgRating,
		report.GenreStats, report.TopMovieID, report.TopMovieTitle, report.TopMoviePoster, report.TopMovieRating,
		report.ContinuousDays, report.PersonaTitle, report.PersonaLine, report.PercentileRank, report.FeaturedQuote,
		report.PosterWall, report.Status, report.ErrorMessage, report.GeneratedAt, report.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("save monthly report: %w", err)
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, rows.Err()
	}
	saved, err := scanReport(rows)
	if err != nil {
		return nil, fmt.Errorf("scan saved monthly report: %w", err)
	}
	return &saved, nil
}

func (store *PostgresStore) GetByUserAndMonth(ctx context.Context, userID int, yearMonth string) (*MonthlyReport, error) {
	return store.get(ctx, `SELECT `+reportColumns+` FROM monthly_reports WHERE user_id = $1 AND year_month = $2 LIMIT 1`, userID, yearMonth)
}

func (store *PostgresStore) LatestByUser(ctx context.Context, userID int) (*MonthlyReport, error) {
	return store.get(ctx, `SELECT `+reportColumns+` FROM monthly_reports
WHERE user_id = $1 AND status = 'generated' ORDER BY year_month DESC LIMIT 1`, userID)
}

func (store *PostgresStore) LatestForDashboard(ctx context.Context, userID int) (any, error) {
	return store.LatestByUser(ctx, userID)
}

func (store *PostgresStore) get(ctx context.Context, query string, arguments ...any) (*MonthlyReport, error) {
	rows, err := store.database.Query(ctx, query, arguments...)
	if err != nil {
		return nil, fmt.Errorf("get monthly report: %w", err)
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, rows.Err()
	}
	report, err := scanReport(rows)
	if err != nil {
		return nil, fmt.Errorf("scan monthly report: %w", err)
	}
	return &report, nil
}

func (store *PostgresStore) ListByUser(ctx context.Context, userID, limit, offset int) ([]MonthlyReport, error) {
	rows, err := store.database.Query(ctx, `SELECT `+reportColumns+` FROM monthly_reports
WHERE user_id = $1 AND status = 'generated' ORDER BY year_month DESC LIMIT $2 OFFSET $3`, userID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list monthly reports: %w", err)
	}
	defer rows.Close()
	reports := make([]MonthlyReport, 0)
	for rows.Next() {
		report, err := scanReport(rows)
		if err != nil {
			return nil, fmt.Errorf("scan monthly report: %w", err)
		}
		reports = append(reports, report)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate monthly reports: %w", err)
	}
	return reports, nil
}

func (store *PostgresStore) UpdateStatus(ctx context.Context, reportID int, status Status, message string) error {
	_, err := store.database.Exec(ctx, `UPDATE monthly_reports SET status = $2, error_message = $3,
generated_at = CASE WHEN $2 = 'generated' THEN NOW() ELSE generated_at END, updated_at = NOW() WHERE id = $1`, reportID, status, message)
	if err != nil {
		return fmt.Errorf("update monthly report status: %w", err)
	}
	return nil
}

func scanReport(row interface{ Scan(...any) error }) (MonthlyReport, error) {
	var report MonthlyReport
	err := row.Scan(&report.ID, &report.UserID, &report.YearMonth, &report.WatchedCount,
		&report.TotalDurationMinutes, &report.AvgRating, &report.GenreStats, &report.TopMovieID,
		&report.TopMovieTitle, &report.TopMoviePoster, &report.TopMovieRating, &report.ContinuousDays,
		&report.PersonaTitle, &report.PersonaLine, &report.PercentileRank, &report.FeaturedQuote,
		&report.PosterWall, &report.Status, &report.ErrorMessage, &report.GeneratedAt, &report.CreatedAt, &report.UpdatedAt)
	return report, err
}
