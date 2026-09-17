package records

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/samlet/record-hub/server/internal/modules/identity"
)

func feedChange(table, record string, version int64) RecordChange {
	return RecordChange{TenantID: "tenant-1", WorkspaceID: "workspace-1", TableID: table, RecordID: record, RecordVersion: version, ChangeType: "upsert", OccurredAt: time.Unix(version, 0)}
}

func TestRecordFeedReplayIsScopedAndOrdered(t *testing.T) {
	feed := NewRecordFeed(8, 2)
	if err := feed.Publish(feedChange("table-1", "record-1", 1)); err != nil {
		t.Fatal(err)
	}
	if err := feed.Publish(feedChange("table-2", "record-2", 1)); err != nil {
		t.Fatal(err)
	}
	if err := feed.Publish(feedChange("table-1", "record-1", 2)); err != nil {
		t.Fatal(err)
	}
	subscription, err := feed.Subscribe("tenant-1", "workspace-1", "table-1", 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer subscription.Close()
	if len(subscription.Replay) != 1 || subscription.Replay[0].ID != 3 {
		t.Fatalf("replay = %#v, want event 3 only", subscription.Replay)
	}
	if err := feed.Publish(feedChange("table-1", "record-1", 3)); err != nil {
		t.Fatal(err)
	}
	select {
	case change := <-subscription.Events:
		if change.ID != 4 || change.RecordVersion != 3 {
			t.Fatalf("live change = %#v", change)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for live change")
	}
}

func TestRecordFeedCursorExpiresAfterRetentionWindow(t *testing.T) {
	feed := NewRecordFeed(2, 1)
	for version := int64(1); version <= 4; version++ {
		if err := feed.Publish(feedChange("table-1", "record-1", version)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := feed.Subscribe("tenant-1", "workspace-1", "table-1", 1, 1); !errors.Is(err, ErrFeedCursorExpired) {
		t.Fatalf("Subscribe error = %v, want ErrFeedCursorExpired", err)
	}
	if _, err := feed.Subscribe("tenant-1", "workspace-1", "table-1", 2, 1); err != nil {
		t.Fatalf("retained cursor rejected: %v", err)
	}
}

func TestRecordFeedSlowConsumerIsDisconnected(t *testing.T) {
	feed := NewRecordFeed(8, 1)
	subscription, err := feed.Subscribe("tenant-1", "workspace-1", "table-1", 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer subscription.Close()
	if err := feed.Publish(feedChange("table-1", "record-1", 1)); err != nil {
		t.Fatal(err)
	}
	if err := feed.Publish(feedChange("table-1", "record-1", 2)); err != nil {
		t.Fatal(err)
	}
	select {
	case reason := <-subscription.Done:
		if !errors.Is(reason, ErrFeedSlowConsumer) {
			t.Fatalf("close reason = %v", reason)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for slow consumer disconnect")
	}
	if feed.DroppedCount() != 1 {
		t.Fatalf("dropped = %d, want 1", feed.DroppedCount())
	}
}

type feedAuthorizerFunc func(context.Context, identity.Principal, string, string, identity.Action) (identity.WorkspaceMembership, error)

func (fn feedAuthorizerFunc) Authorize(ctx context.Context, principal identity.Principal, tenantID, workspaceID string, action identity.Action) (identity.WorkspaceMembership, error) {
	return fn(ctx, principal, tenantID, workspaceID, action)
}

func TestFeedHTTPRejectsInvalidCursorBeforeOpeningStream(t *testing.T) {
	principal := identity.Principal{Kind: identity.PrincipalUser, Issuer: "issuer", Subject: "viewer"}
	handler := NewFeedHTTPHandler(NewRecordFeed(8, 1), feedAuthorizerFunc(func(context.Context, identity.Principal, string, string, identity.Action) (identity.WorkspaceMembership, error) {
		return identity.WorkspaceMembership{Status: identity.MembershipActive}, nil
	}), FeedHTTPOptions{AuthCheckInterval: time.Hour, HeartbeatInterval: time.Hour})
	mux := http.NewServeMux()
	mux.Handle("GET /api/v1/tables/{tableID}/records/stream", handler)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/tables/table-1/records/stream?tenantId=tenant-1&workspaceId=workspace-1&cursor=nope", nil)
	request = request.WithContext(identity.WithPrincipal(request.Context(), principal))
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "INVALID_CURSOR") {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}
