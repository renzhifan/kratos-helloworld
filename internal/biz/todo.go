package biz

import (
	"context"
	"strings"
	"time"

	v1 "logisticsServer/api/todo/v1"

	"github.com/go-kratos/kratos/v3/errors"
	"github.com/google/uuid"
	"go.einride.tech/aip/filtering"
	"go.einride.tech/aip/ordering"
)

var (
	// ErrTodoNotFound is returned when a todo does not exist.
	ErrTodoNotFound = errors.NotFound(v1.ErrorReason_TODO_NOT_FOUND.String(), "todo not found")
	// ErrTodoInvalidArgument is returned when a todo request is invalid.
	ErrTodoInvalidArgument = errors.BadRequest(v1.ErrorReason_TODO_INVALID_ARGUMENT.String(), "invalid todo argument")
)

// TodoStatus is the lifecycle state of a todo. Values match the api enum so
// the two can be mapped without a lookup table.
type TodoStatus int32

const (
	// TodoStatusUnspecified is the zero value; it is never persisted.
	TodoStatusUnspecified TodoStatus = 0
	// TodoStatusActive marks a live todo.
	TodoStatusActive TodoStatus = 1
	// TodoStatusDeleted marks a soft-deleted todo. The record is retained but
	// hidden from reads.
	TodoStatusDeleted TodoStatus = 2
)

// Todo is a Todo model.
type Todo struct {
	ID        uuid.UUID
	Title     string
	Content   string
	Completed bool
	Status    TodoStatus
	CreatedAt time.Time
	UpdatedAt time.Time
}

// TodoRepo is a todo repo.
type TodoRepo interface {
	FindByID(context.Context, uuid.UUID) (*Todo, error)
	ListTodos(context.Context, ...ListOption) ([]*Todo, error)
	CreateTodo(context.Context, *Todo) (*Todo, error)
	UpdateTodo(context.Context, *Todo) (*Todo, error)
	DeleteTodo(context.Context, uuid.UUID) error
}

// TodoCache caches todo list results. The interface is declared here and
// implemented in data, so the usecase never talks to Redis directly.
type TodoCache interface {
	// Get returns the cached todos for key; the bool reports a hit.
	Get(context.Context, string) ([]*Todo, bool, error)
	// Set stores todos under key for ttl.
	Set(context.Context, string, []*Todo, time.Duration) error
}

// listCacheTTL bounds how long a cached list page may stay stale after writes.
const listCacheTTL = 30 * time.Second

// ListOption configures todo list queries.
type ListOption func(*ListOptions)

// ListOptions are todo list query options.
type ListOptions struct {
	Filter  filtering.Filter
	OrderBy ordering.OrderBy
	Offset  int
	Limit   int
}

// ListFilter sets a standard AIP filter.
func ListFilter(filter filtering.Filter) ListOption {
	return func(o *ListOptions) {
		o.Filter = filter
	}
}

// ListOrderBy sets a standard AIP order_by value.
func ListOrderBy(orderBy ordering.OrderBy) ListOption {
	return func(o *ListOptions) {
		o.OrderBy = orderBy
	}
}

// ListOffset sets an offset.
func ListOffset(offset int) ListOption {
	return func(o *ListOptions) {
		o.Offset = offset
	}
}

// ListLimit sets a limit.
func ListLimit(limit int) ListOption {
	return func(o *ListOptions) {
		o.Limit = limit
	}
}

// TodoUsecase is a Todo usecase.
type TodoUsecase struct {
	repo  TodoRepo
	cache TodoCache
}

// NewTodoUsecase new a Todo usecase. A nil cache disables list caching.
func NewTodoUsecase(repo TodoRepo, cache TodoCache) *TodoUsecase {
	return &TodoUsecase{repo: repo, cache: cache}
}

// GetTodo returns a todo by ID.
func (uc *TodoUsecase) GetTodo(ctx context.Context, id uuid.UUID) (*Todo, error) {
	if id == uuid.Nil {
		return nil, ErrTodoInvalidArgument
	}
	return uc.repo.FindByID(ctx, id)
}

// ListTodos lists todos.
func (uc *TodoUsecase) ListTodos(ctx context.Context, opts ...ListOption) ([]*Todo, error) {
	return uc.repo.ListTodos(ctx, opts...)
}

// ListTodosCached lists todos through the cache: a hit returns the cached page,
// a miss reads the repo and writes the result back under key. Cache errors
// degrade to a direct read so a Redis outage does not fail the request.
func (uc *TodoUsecase) ListTodosCached(ctx context.Context, key string, opts ...ListOption) ([]*Todo, error) {
	if uc.cache != nil && key != "" {
		if todos, ok, err := uc.cache.Get(ctx, key); err == nil && ok {
			return todos, nil
		}
	}
	todos, err := uc.repo.ListTodos(ctx, opts...)
	if err != nil {
		return nil, err
	}
	if uc.cache != nil && key != "" {
		// A failed write only costs the next request a repo read.
		_ = uc.cache.Set(ctx, key, todos, listCacheTTL)
	}
	return todos, nil
}

// CreateTodo creates a todo.
func (uc *TodoUsecase) CreateTodo(ctx context.Context, todo *Todo) (*Todo, error) {
	if err := validateTodo(todo); err != nil {
		return nil, err
	}
	return uc.repo.CreateTodo(ctx, todo)
}

// UpdateTodo updates a todo.
func (uc *TodoUsecase) UpdateTodo(ctx context.Context, todo *Todo) (*Todo, error) {
	if todo == nil || todo.ID == uuid.Nil {
		return nil, ErrTodoInvalidArgument
	}
	if err := validateTodo(todo); err != nil {
		return nil, err
	}
	return uc.repo.UpdateTodo(ctx, todo)
}

// DeleteTodo soft-deletes a todo.
func (uc *TodoUsecase) DeleteTodo(ctx context.Context, id uuid.UUID) error {
	if id == uuid.Nil {
		return ErrTodoInvalidArgument
	}
	return uc.repo.DeleteTodo(ctx, id)
}

func validateTodo(todo *Todo) error {
	if todo == nil || strings.TrimSpace(todo.Title) == "" {
		return ErrTodoInvalidArgument
	}
	return nil
}
