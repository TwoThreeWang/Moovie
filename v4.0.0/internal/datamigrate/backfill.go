package datamigrate

import (
	"context"
	"fmt"
)

// planFavorites 检查旧 favorites 表能否安全转换成 user_movies(status=wish)。
// 新库已存在同一 (user_id, movie_id) 时保留当前状态，避免把「看过」降级成「想看」。
func (importer Importer) planFavorites(ctx context.Context, schema string) (FavoritePlan, error) {
	plan := FavoritePlan{}
	sourceCols, err := columns(ctx, importer.Source, schema, "favorites")
	if err != nil {
		return plan, err
	}
	if len(sourceCols) == 0 {
		return plan, nil
	}
	plan.Available = true

	rows, err := importer.Source.Query(ctx, fmt.Sprintf(
		`SELECT favorite.user_id, movie.douban_id
		 FROM %s.favorites favorite
		 LEFT JOIN %s.movies movie ON movie.id = favorite.movie_id`, quote(schema), quote(schema)))
	if err != nil {
		return plan, err
	}
	defer rows.Close()

	type favorite struct {
		userID   int64
		doubanID string
	}
	pending := make([]favorite, 0)
	for rows.Next() {
		var userID *int64
		var doubanID *string
		if err := rows.Scan(&userID, &doubanID); err != nil {
			return plan, err
		}
		plan.SourceRows++
		if doubanID == nil || *doubanID == "" {
			plan.MissingMovies++
			continue
		}
		if userID == nil {
			plan.MissingUsers++
			continue
		}
		pending = append(pending, favorite{userID: *userID, doubanID: *doubanID})
	}
	if err := rows.Err(); err != nil {
		return plan, err
	}

	for _, item := range pending {
		var exists bool
		if err := importer.Target.QueryRow(ctx, fmt.Sprintf(
			`SELECT EXISTS (SELECT 1 FROM %s.user_movies WHERE user_id = $1 AND movie_id = $2)`, quote(schema)),
			item.userID, item.doubanID).Scan(&exists); err != nil {
			return plan, err
		}
		if exists {
			plan.AlreadyExists++
			continue
		}
		// apply 会先复制 users，所以这里只把源库本身不存在的用户视为冲突。
		var userExists bool
		if err := importer.Source.QueryRow(ctx, fmt.Sprintf(
			`SELECT EXISTS (SELECT 1 FROM %s.users WHERE id = $1)`, quote(schema)), item.userID).Scan(&userExists); err != nil {
			return plan, err
		}
		if !userExists {
			plan.MissingUsers++
			continue
		}
		plan.WouldInsert++
	}
	return plan, nil
}

// applyFavorites 把独立收藏转换成 user_movies(status='wish')。
func (importer Importer) applyFavorites(ctx context.Context, target Querier, schema string) (int, error) {
	sourceCols, err := columns(ctx, importer.Source, schema, "favorites")
	if err != nil || len(sourceCols) == 0 {
		return 0, err
	}
	rows, err := importer.Source.Query(ctx, fmt.Sprintf(
		`SELECT favorite.user_id, movie.douban_id, COALESCE(movie.title,''),
		        COALESCE(movie.poster,''), COALESCE(movie.year,'')
		 FROM %s.favorites favorite
		 JOIN %s.movies movie ON movie.id=favorite.movie_id
		 WHERE movie.douban_id<>''`, quote(schema), quote(schema)))
	if err != nil {
		return 0, err
	}
	type favorite struct {
		userID                  int64
		doubanID, title, poster string
		year                    string
	}
	pending := make([]favorite, 0)
	for rows.Next() {
		var item favorite
		if err := rows.Scan(&item.userID, &item.doubanID, &item.title, &item.poster, &item.year); err != nil {
			rows.Close()
			return 0, err
		}
		pending = append(pending, item)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}

	inserted := 0
	for _, item := range pending {
		tag, err := target.Exec(ctx, fmt.Sprintf(
			`INSERT INTO %s.user_movies (user_id,movie_id,title,poster,year,status)
			 SELECT $1,$2,$3,$4,$5,'wish'
			 WHERE EXISTS (SELECT 1 FROM %s.users WHERE id=$1)
			 ON CONFLICT (user_id,movie_id) DO NOTHING`, quote(schema), quote(schema)),
			item.userID, item.doubanID, item.title, item.poster, item.year)
		if err != nil {
			return inserted, err
		}
		inserted += int(tag.RowsAffected())
	}
	return inserted, nil
}
