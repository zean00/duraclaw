package scheduler

import (
	"crypto/sha256"
	"encoding/hex"
	"time"

	"github.com/robfig/cron/v3"
)

func Next(schedule string, from time.Time) (time.Time, error) {
	spec, err := cron.ParseStandard(schedule)
	if err != nil {
		return time.Time{}, err
	}
	return spec.Next(from), nil
}

func IdempotencyKey(jobID string, fireAt time.Time) string {
	sum := sha256.Sum256([]byte(jobID + ":" + fireAt.UTC().Format(time.RFC3339Nano)))
	return "cron-" + hex.EncodeToString(sum[:16])
}
