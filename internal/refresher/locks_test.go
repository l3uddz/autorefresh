package refresher

import (
	"context"
	"errors"
	"testing"
)

func TestItemLockCancellationDoesNotReleaseHolder(t *testing.T) {
	var locks itemLocks
	release, err := locks.lock(t.Context(), "srv", "1")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := locks.lock(ctx, "srv", "1"); !errors.Is(err, context.Canceled) {
		t.Fatalf("lock error = %v", err)
	}
	other, err := locks.lock(t.Context(), "another", "1")
	if err != nil {
		t.Fatal(err)
	}
	other()
	release()
	again, err := locks.lock(t.Context(), "srv", "1")
	if err != nil {
		t.Fatal(err)
	}
	again()
}
