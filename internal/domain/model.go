package domain

import "errors"

var ErrNotFound = errors.New("publication not found")
var ErrBusy = errors.New("publication is being copied")
var ErrInvalid = errors.New("invalid input")
var ErrSelfVote = errors.New("self vote forbidden")
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
	ChatID         int64
	MessageID      int
	UploaderID     *int64
	MediaType      string
	UniqueID       string
	SenderBotID    int64
	SourceRequest  *VideoRequest
	SourceVideoIDs []string
	SourceEvidence string
	AllowFIFO      bool
}

type VideoRequest struct {
	ChatID    int64
	MessageID int
	UserID    int64
	VideoIDs  []string
	SentAt    int64
}

func ValidYouTubeID(id string) bool {
	if len(id) != 11 {
		return false
	}
	for _, c := range id {
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' || c == '-') {
			return false
		}
	}
	return true
}

type Post struct {
	UploaderID      int64
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
	UserID int64
	Score  int64
	Name   string
}

type APIError struct {
	Code        int
	RetryAfter  int
	Confirmed   bool
	MessageGone bool
	NotModified bool
}

func (e *APIError) Error() string { return "external API request failed" }

type Ballot struct {
	CallbackID string
	PostID     int64
	ChatID     int64
	MessageID  int
	UserID     int64
	Vote       int
}

type Group struct {
	ID       int64
	Type     string
	Username string
}
