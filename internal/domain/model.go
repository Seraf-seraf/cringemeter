package domain

import "errors"

var ErrNotFound = errors.New("publication not found")
var ErrBusy = errors.New("publication is being copied")
var ErrInvalid = errors.New("invalid input")
var ErrConflict = errors.New("publication state conflict")

const (
	Pending     = "pending"
	Copying     = "copying"
	Copied      = "copied"
	Done        = "done"
	CopyFailed  = "copy_failed"
	CopyUnknown = "copy_unknown"
)

type Incoming struct {
	ChatID     int64
	MessageID  int
	UploaderID *int64
	MediaType  string
	UniqueID   string
}

type Post struct {
	ID              int64
	MediaID         int64
	ChatID          int64
	SourceMessageID int
	BotMessageID    int
	State           string
}

type Counts struct{ Cringe, NonCringe int64 }

func (c Counts) Total() int64 { return c.Cringe + c.NonCringe }

type Rank struct {
	MediaID   int64
	MessageID int
	Counts    Counts
}
