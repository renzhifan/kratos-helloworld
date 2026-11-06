package biz

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

// fakeListRepo counts list calls so tests can assert cache hits skip the repo.
type fakeListRepo struct {
	listCalls int
	todos     []*Todo
}

func (r *fakeListRepo) FindByID(context.Context, uuid.UUID) (*Todo, error) {
	return nil, ErrTodoNotFound
}

func (r *fakeListRepo) ListTodos(context.Context, ...ListOption) ([]*Todo, error) {
	r.listCalls++
	return r.todos, nil
}

func (r *fakeListRepo) CreateTodo(context.Context, *Todo) (*Todo, error) { return nil, nil }

func (r *fakeListRepo) UpdateTodo(context.Context, *Todo) (*Todo, error) { return nil, nil }

func (r *fakeListRepo) DeleteTodo(context.Context, uuid.UUID) error { return nil }

// fakeListCache is an in-memory TodoCache.
type fakeListCache struct {
	data map[string][]*Todo
	sets int
}

func newFakeListCache() *fakeListCache {
	return &fakeListCache{data: make(map[string][]*Todo)}
}

func (c *fakeListCache) Get(_ context.Context, key string) ([]*Todo, bool, error) {
	todos, ok := c.data[key]
	return todos, ok, nil
}

func (c *fakeListCache) Set(_ context.Context, key string, todos []*Todo, _ time.Duration) error {
	c.sets++
	c.data[key] = todos
	return nil
}

func TestListTodosCachedWritesOnMissAndHitsAfterwards(t *testing.T) {
	repo := &fakeListRepo{todos: []*Todo{{ID: uuid.New(), Title: "first"}}}
	cache := newFakeListCache()
	uc := NewTodoUsecase(repo, cache)
	ctx := context.Background()

	first, err := uc.ListTodosCached(ctx, "todo:list:test")
	if err != nil {
		t.Fatalf("first list: %v", err)
	}
	if len(first) != 1 {
		t.Fatalf("first list returned %d todos, want 1", len(first))
	}
	if repo.listCalls != 1 {
		t.Fatalf("repo calls after miss = %d, want 1", repo.listCalls)
	}
	if cache.sets != 1 {
		t.Fatalf("cache writes after miss = %d, want 1", cache.sets)
	}

	second, err := uc.ListTodosCached(ctx, "todo:list:test")
	if err != nil {
		t.Fatalf("second list: %v", err)
	}
	if len(second) != 1 {
		t.Fatalf("second list returned %d todos, want 1", len(second))
	}
	if repo.listCalls != 1 {
		t.Fatalf("repo calls after hit = %d, want 1", repo.listCalls)
	}
}

func TestListTodosCachedWithoutCacheReadsRepo(t *testing.T) {
	repo := &fakeListRepo{todos: []*Todo{{ID: uuid.New()}}}
	uc := NewTodoUsecase(repo, nil)

	if _, err := uc.ListTodosCached(context.Background(), "todo:list:test"); err != nil {
		t.Fatalf("list: %v", err)
	}
	if repo.listCalls != 1 {
		t.Fatalf("repo calls = %d, want 1", repo.listCalls)
	}
}
