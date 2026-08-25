package catalog

import "context"

// TitleFinder 给别的模块提供「豆瓣 ID → 片名」的查询，避免它们直接依赖整个 catalog。
type TitleFinder struct{ store Store }

// NewTitleFinder 创建片名查询器。
func NewTitleFinder(store Store) TitleFinder { return TitleFinder{store: store} }

// FindTitleByDoubanID 查不到时返回空串而不是错误。
func (finder TitleFinder) FindTitleByDoubanID(ctx context.Context, doubanID string) (string, error) {
	movie, err := finder.store.FindByDoubanID(ctx, doubanID)
	if err != nil || movie == nil {
		return "", err
	}
	return movie.Title, nil
}
