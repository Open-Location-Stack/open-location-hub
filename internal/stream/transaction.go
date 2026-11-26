package stream

import (
	"context"
	"fmt"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
)

func commitStreamBatch(ctx context.Context, session *kgo.GroupTransactSession, records []*kgo.Record) (bool, error) {
	if err := session.Begin(); err != nil {
		return false, fmt.Errorf("begin transaction: %w", err)
	}
	ioCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if err := session.ProduceSync(ioCtx, records...).FirstErr(); err != nil {
		abortCtx, finishAbort := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer finishAbort()
		_, abortErr := session.End(abortCtx, kgo.TryAbort)
		if abortErr != nil {
			return false, fmt.Errorf("produce batch: %w; abort: %w", err, abortErr)
		}
		return false, fmt.Errorf("produce batch: %w", err)
	}
	committed, err := session.End(ioCtx, kgo.TryCommit)
	if err != nil {
		return false, fmt.Errorf("end transaction: %w", err)
	}
	return committed, nil
}
