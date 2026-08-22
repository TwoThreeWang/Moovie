package catalog

import (
	"context"
	"testing"

	"github.com/TwoThreeWang/Moovie/new/internal/workqueue"
)

func TestRefreshHandlerDispatchesAllMetadataTypesAndChainsWork(t *testing.T) {
	queue := &refreshQueueStub{needsTMDB: true}
	fetcher := &recordingFetcher{}
	reviews := &recordingReviewFetcher{}
	backdrops := &recordingBackdropSyncer{}
	vectors := &recordingVectorEnricher{}
	handler := NewRefreshHandler(queue, fetcher, vectors, WithRefreshReviews(reviews), WithRefreshBackdrops(backdrops))
	for _, taskType := range []string{RefreshProviderDouban, RefreshProviderReviews, RefreshProviderTMDB, RefreshProviderEmbedding} {
		if err := handler.Handle(t.Context(), workqueue.Job{TaskType: taskType, SubjectKey: "1292052", Reason: "test"}); err != nil {
			t.Fatalf("%s: %v", taskType, err)
		}
	}
	if len(fetcher.ids) != 1 || len(reviews.ids) != 1 || len(backdrops.ids) != 1 || len(vectors.ids) != 1 {
		t.Fatalf("dispatch = fetch:%v reviews:%v backdrops:%v vectors:%v", fetcher.ids, reviews.ids, backdrops.ids, vectors.ids)
	}
	if len(queue.jobs) != 2 || queue.jobs[0].TaskType != RefreshProviderTMDB || queue.jobs[1].TaskType != RefreshProviderEmbedding {
		t.Fatalf("chained jobs = %+v", queue.jobs)
	}
}

func TestRefreshHandlerSkipsSatisfiedTMDBCollection(t *testing.T) {
	queue := &refreshQueueStub{}
	handler := NewRefreshHandler(queue, &recordingFetcher{}, nil, WithRefreshBackdrops(&recordingBackdropSyncer{}))
	if err := handler.Handle(t.Context(), workqueue.Job{TaskType: RefreshProviderDouban, SubjectKey: "1292052"}); err != nil {
		t.Fatal(err)
	}
	if len(queue.jobs) != 0 {
		t.Fatalf("unexpected chained jobs = %+v", queue.jobs)
	}
}

type refreshQueueStub struct {
	jobs      []workqueue.Job
	needsTMDB bool
}

func (queue *refreshQueueStub) EnqueueRefresh(_ context.Context, doubanID, provider, reason string, requestedBy int) (int, error) {
	queue.jobs = append(queue.jobs, workqueue.Job{TaskType: provider, SubjectKey: doubanID, Reason: reason, RequestedBy: requestedBy})
	return len(queue.jobs), nil
}

func (queue *refreshQueueStub) NeedsTMDBRefresh(context.Context, string) (bool, error) {
	return queue.needsTMDB, nil
}
