package catalog

import "context"

type TitleFinder struct{ store Store }

func NewTitleFinder(store Store) TitleFinder { return TitleFinder{store: store} }

func (finder TitleFinder) FindTitleByDoubanID(ctx context.Context, doubanID string) (string, error) {
	movie, err := finder.store.FindByDoubanID(ctx, doubanID)
	if err != nil || movie == nil {
		return "", err
	}
	return movie.Title, nil
}
