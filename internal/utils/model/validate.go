package model

import (
	"fmt"
	"time"
)

// ValidateRoom bounds user-controlled fields and rejects timestamps that could
// keep entries alive indefinitely. Older rooms remain valid for backfill.
func ValidateRoom(room *Room) error {
	if room.Time <= 0 || room.Time > time.Now().Unix()+60 {
		return fmt.Errorf("invalid room timestamp")
	}
	if len(room.Msg) > 16384 || len(room.Name) > 256 || len(room.Source) > 128 {
		return fmt.Errorf("room fields exceed size limits")
	}
	return nil
}
