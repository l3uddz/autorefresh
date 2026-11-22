package refresher

import (
	"context"
	"sync"
)

type itemKey struct{ server, ratingKey string }

type itemLocks struct {
	mu   sync.Mutex
	held map[itemKey]chan struct{}
}

func (l *itemLocks) lock(ctx context.Context, server, ratingKey string) (func(), error) {
	key := itemKey{server, ratingKey}
	for {
		release, held := l.acquire(key)
		if release != nil {
			if err := ctx.Err(); err != nil {
				release()
				return nil, err
			}
			return release, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-held:
		}
	}
}

func (l *itemLocks) acquire(key itemKey) (func(), <-chan struct{}) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if held := l.held[key]; held != nil {
		return nil, held
	}
	if l.held == nil {
		l.held = make(map[itemKey]chan struct{})
	}
	held := make(chan struct{})
	l.held[key] = held
	return func() {
		l.mu.Lock()
		defer l.mu.Unlock()
		delete(l.held, key)
		close(held)
	}, nil
}
