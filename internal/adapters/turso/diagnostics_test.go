package turso

import (
	"context"
	"testing"

	"handler/internal/domain"
)

func TestRecentUploadsAreBoundedChatScopedAndSeparateAuthors(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	for i := 1; i <= 21; i++ {
		ownedPost(t, s, i, "shared-file", 50)
	}
	otherAuthor := ownedPost(t, s, 22, "shared-file", 60)
	voteFor(t, s, otherAuthor, 999, 1)
	voteFor(t, s, otherAuthor, 998, 0)
	owner := int64(70)
	if _, err := s.Register(ctx, domain.Incoming{ChatID: -200, MessageID: 23, UniqueID: "shared-file", MediaType: "video", UploaderID: &owner}); err != nil {
		t.Fatal(err)
	}
	report, err := s.RecentUploads(ctx, -100)
	if err != nil || len(report) != 20 {
		t.Fatal(len(report), err)
	}
	if report[0].SourceMessageID != 22 || report[19].SourceMessageID != 3 || report[0].UploaderID != 60 || report[0].Cringe != 1 || report[0].NonCringe != 1 || report[1].Cringe != 0 || report[1].NonCringe != 0 {
		t.Fatal(report)
	}
	if _, err := s.RecentUploads(ctx, 0); err == nil {
		t.Fatal("unscoped report allowed")
	}
	var count int
	if err := s.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM posts").Scan(&count); err != nil || count != 23 {
		t.Fatal(count, err)
	}
}
