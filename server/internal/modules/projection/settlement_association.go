package projection

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/samlet/record-hub/server/internal/modules/identity"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// SettlementAssociation is a safe read model. It contains only stable
// references, versions, hashes, decision state and Apply outcome; financial
// values, bank data, invoice attachments and sealed payloads are absent by
// construction.
type SettlementAssociation struct {
	ID               string    `bson:"_id" json:"id"`
	TenantID         string    `bson:"tenantId" json:"tenantId"`
	WorkspaceID      string    `bson:"workspaceId" json:"workspaceId"`
	SettlementRef    string    `bson:"settlementRef" json:"settlementRef"`
	ApprovalRef      string    `bson:"approvalRef" json:"approvalRef"`
	OrganizationRef  string    `bson:"organizationRef" json:"organizationRef"`
	ApprovalStatus   string    `bson:"approvalStatus" json:"approvalStatus"`
	SettlementStatus string    `bson:"settlementStatus" json:"settlementStatus"`
	ActionOutcome    string    `bson:"actionOutcome" json:"actionOutcome"`
	SnapshotHash     string    `bson:"snapshotHash" json:"snapshotHash"`
	DecisionVersion  int64     `bson:"decisionVersion" json:"decisionVersion"`
	SourceVersion    int64     `bson:"sourceVersion" json:"sourceVersion"`
	ObservedVersion  int64     `bson:"observedVersion" json:"observedVersion"`
	LastEventID      string    `bson:"lastEventId" json:"lastEventId"`
	LastPayloadHash  string    `bson:"lastPayloadHash" json:"lastPayloadHash"`
	ObservedEventID  string    `bson:"observedEventId,omitempty" json:"observedEventId,omitempty"`
	ConflictVersion  int64     `bson:"conflictVersion,omitempty" json:"conflictVersion,omitempty"`
	ConflictPayload  string    `bson:"conflictPayloadHash,omitempty" json:"conflictPayloadHash,omitempty"`
	ProjectionState  string    `bson:"projectionState" json:"projectionState"`
	UpdatedAt        time.Time `bson:"updatedAt" json:"updatedAt"`
}

const SettlementAssociationCollectionName = "settlement_associations"

var (
	ErrSettlementAssociationInvalid      = errors.New("invalid settlement association")
	ErrSettlementAssociationUnavailable  = errors.New("settlement association store is unavailable")
	ErrSettlementAssociationConflict     = errors.New("settlement association is in conflict")
	ErrSettlementAssociationQueryInvalid = errors.New("invalid settlement association query")
	settlementRefPattern                 = regexp.MustCompile(`^settlement:SETTLEMENT:[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
	organizationRefPattern               = regexp.MustCompile(`^organization:[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
	sha256HexPattern                     = regexp.MustCompile(`^[0-9a-fA-F]{64}$`)
)

type SettlementAssociationEvent struct {
	TenantID, WorkspaceID, SettlementRef, ApprovalRef, OrganizationRef string
	ApprovalStatus, SettlementStatus, ActionOutcome, SnapshotHash      string
	DecisionVersion, SourceVersion                                     int64
	EventID, PayloadHash                                               string
	ObservedAt                                                         time.Time
}

func (event SettlementAssociationEvent) Validate() error {
	if strings.TrimSpace(event.TenantID) == "" || strings.TrimSpace(event.WorkspaceID) == "" ||
		!settlementRefPattern.MatchString(event.SettlementRef) || !applicationRefPattern.MatchString(event.ApprovalRef) ||
		!organizationRefPattern.MatchString(event.OrganizationRef) || !sha256HexPattern.MatchString(event.SnapshotHash) ||
		event.DecisionVersion < 1 || event.SourceVersion < 1 || strings.TrimSpace(event.EventID) == "" ||
		!sha256HexPattern.MatchString(event.PayloadHash) || event.ObservedAt.IsZero() {
		return ErrSettlementAssociationInvalid
	}
	if len(event.TenantID) > 128 || len(event.WorkspaceID) > 128 || strings.ContainsAny(event.TenantID+event.WorkspaceID, " \t\r\n") {
		return ErrSettlementAssociationInvalid
	}
	switch event.ApprovalStatus {
	case "PENDING", "APPROVED", "REJECTED", "CANCELLED", "FAILED":
	default:
		return ErrSettlementAssociationInvalid
	}
	switch event.SettlementStatus {
	case "PROPOSED", "CONFIRMED", "PAID", "UNKNOWN":
	default:
		return ErrSettlementAssociationInvalid
	}
	switch event.ActionOutcome {
	case "SUCCEEDED", "NOT_EXECUTED", "UNKNOWN":
	default:
		return ErrSettlementAssociationInvalid
	}
	return nil
}

func (association SettlementAssociation) Validate() error {
	if strings.TrimSpace(association.ID) == "" || strings.TrimSpace(association.TenantID) == "" || strings.TrimSpace(association.WorkspaceID) == "" ||
		!settlementRefPattern.MatchString(association.SettlementRef) || !applicationRefPattern.MatchString(association.ApprovalRef) ||
		!organizationRefPattern.MatchString(association.OrganizationRef) || !sha256HexPattern.MatchString(association.SnapshotHash) ||
		association.SourceVersion < 0 || association.ObservedVersion < association.SourceVersion || association.UpdatedAt.IsZero() {
		return ErrSettlementAssociationInvalid
	}
	if association.SourceVersion > 0 && (association.LastEventID == "" || !sha256HexPattern.MatchString(association.LastPayloadHash)) {
		return ErrSettlementAssociationInvalid
	}
	switch association.ProjectionState {
	case AssociationCurrent, AssociationGap, AssociationConflict:
	default:
		return ErrSettlementAssociationInvalid
	}
	return nil
}

type SettlementAssociationQuery struct {
	TenantID, WorkspaceID, SettlementRef, ApprovalRef string
	Limit                                             int
}

func (query SettlementAssociationQuery) normalized() (SettlementAssociationQuery, error) {
	query.TenantID, query.WorkspaceID = strings.TrimSpace(query.TenantID), strings.TrimSpace(query.WorkspaceID)
	query.SettlementRef, query.ApprovalRef = strings.TrimSpace(query.SettlementRef), strings.TrimSpace(query.ApprovalRef)
	if query.TenantID == "" || query.WorkspaceID == "" || len(query.TenantID) > 128 || len(query.WorkspaceID) > 128 || strings.ContainsAny(query.TenantID+query.WorkspaceID, " \t\r\n") ||
		(query.SettlementRef != "" && !settlementRefPattern.MatchString(query.SettlementRef)) || (query.ApprovalRef != "" && !applicationRefPattern.MatchString(query.ApprovalRef)) {
		return SettlementAssociationQuery{}, ErrSettlementAssociationQueryInvalid
	}
	if query.Limit == 0 {
		query.Limit = 50
	}
	if query.Limit < 1 || query.Limit > 100 {
		return SettlementAssociationQuery{}, ErrSettlementAssociationQueryInvalid
	}
	return query, nil
}

type SettlementAssociationRepository interface {
	Apply(context.Context, SettlementAssociationEvent) error
	List(context.Context, SettlementAssociationQuery) ([]SettlementAssociation, error)
}

type SettlementAssociationService struct {
	repository SettlementAssociationRepository
	authorizer *identity.Authorizer
}

func NewSettlementAssociationService(repository SettlementAssociationRepository, authorizer *identity.Authorizer) *SettlementAssociationService {
	return &SettlementAssociationService{repository: repository, authorizer: authorizer}
}

func (service *SettlementAssociationService) List(ctx context.Context, principal identity.Principal, query SettlementAssociationQuery) ([]SettlementAssociation, error) {
	normalized, err := query.normalized()
	if err != nil {
		return nil, err
	}
	if service == nil || service.repository == nil {
		return nil, ErrSettlementAssociationUnavailable
	}
	if service.authorizer == nil {
		return nil, identity.ErrForbidden
	}
	if _, err := service.authorizer.Authorize(ctx, principal, normalized.TenantID, normalized.WorkspaceID, identity.ActionWorkspaceRead); err != nil {
		return nil, err
	}
	return service.repository.List(ctx, normalized)
}

type MongoSettlementAssociationRepository struct {
	collection *mongo.Collection
	clock      func() time.Time
}

func NewMongoSettlementAssociationRepository(database *mongo.Database) *MongoSettlementAssociationRepository {
	if database == nil {
		return &MongoSettlementAssociationRepository{}
	}
	return &MongoSettlementAssociationRepository{collection: database.Collection(SettlementAssociationCollectionName), clock: time.Now}
}

func (repository *MongoSettlementAssociationRepository) EnsureIndexes(ctx context.Context) error {
	if repository == nil || repository.collection == nil {
		return ErrSettlementAssociationUnavailable
	}
	_, err := repository.collection.Indexes().CreateMany(ctx, []mongo.IndexModel{
		{Keys: bson.D{{Key: "tenantId", Value: 1}, {Key: "workspaceId", Value: 1}, {Key: "settlementRef", Value: 1}, {Key: "approvalRef", Value: 1}}, Options: options.Index().SetName("settlement_association_scope_unique").SetUnique(true)},
		{Keys: bson.D{{Key: "tenantId", Value: 1}, {Key: "workspaceId", Value: 1}, {Key: "projectionState", Value: 1}, {Key: "updatedAt", Value: -1}}, Options: options.Index().SetName("settlement_association_state")},
	})
	if err != nil {
		return err
	}
	return nil
}

func settlementAssociationID(tenant, workspace, settlement, approval string) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{tenant, workspace, settlement, approval}, "\x00")))
	return "settlement-association-" + hex.EncodeToString(sum[:])[:40]
}

func settlementAssociationFromEvent(id string, event SettlementAssociationEvent, now time.Time) SettlementAssociation {
	return SettlementAssociation{ID: id, TenantID: event.TenantID, WorkspaceID: event.WorkspaceID, SettlementRef: event.SettlementRef, ApprovalRef: event.ApprovalRef, OrganizationRef: event.OrganizationRef, ApprovalStatus: event.ApprovalStatus, SettlementStatus: event.SettlementStatus, ActionOutcome: event.ActionOutcome, SnapshotHash: event.SnapshotHash, DecisionVersion: event.DecisionVersion, SourceVersion: event.SourceVersion, ObservedVersion: event.SourceVersion, LastEventID: event.EventID, LastPayloadHash: event.PayloadHash, ProjectionState: AssociationCurrent, UpdatedAt: now}
}

func applySettlementAssociation(current SettlementAssociation, event SettlementAssociationEvent, now time.Time) (SettlementAssociation, bool, error) {
	if event.SourceVersion < current.SourceVersion {
		return current, false, nil
	}
	if event.SourceVersion == current.SourceVersion {
		if event.EventID == current.LastEventID || event.PayloadHash == current.LastPayloadHash {
			return current, false, nil
		}
		current.ProjectionState = AssociationConflict
		current.ConflictVersion = event.SourceVersion
		current.ConflictPayload = event.PayloadHash
		current.UpdatedAt = now
		return current, true, nil
	}
	if event.SourceVersion > current.SourceVersion+1 {
		if event.SourceVersion <= current.ObservedVersion {
			return current, false, nil
		}
		current.ObservedVersion = event.SourceVersion
		current.ObservedEventID = event.EventID
		current.ProjectionState = AssociationGap
		current.UpdatedAt = now
		return current, true, nil
	}
	updated := settlementAssociationFromEvent(current.ID, event, now)
	if current.ObservedVersion > updated.ObservedVersion {
		updated.ObservedVersion = current.ObservedVersion
		updated.ObservedEventID = current.ObservedEventID
	}
	if updated.ObservedVersion > updated.SourceVersion {
		updated.ProjectionState = AssociationGap
	}
	return updated, true, nil
}

func (repository *MongoSettlementAssociationRepository) Apply(ctx context.Context, event SettlementAssociationEvent) error {
	if err := event.Validate(); err != nil {
		return err
	}
	if repository == nil || repository.collection == nil {
		return ErrSettlementAssociationUnavailable
	}
	now := event.ObservedAt.UTC()
	if repository.clock != nil {
		now = repository.clock().UTC()
	}
	id := settlementAssociationID(event.TenantID, event.WorkspaceID, event.SettlementRef, event.ApprovalRef)
	var current SettlementAssociation
	err := repository.collection.FindOne(ctx, bson.D{{Key: "_id", Value: id}, {Key: "tenantId", Value: event.TenantID}, {Key: "workspaceId", Value: event.WorkspaceID}}).Decode(&current)
	if errors.Is(err, mongo.ErrNoDocuments) {
		candidate := settlementAssociationFromEvent(id, event, now)
		if event.SourceVersion > 1 {
			candidate.ProjectionState = AssociationGap
			candidate.SourceVersion = 0
			candidate.ObservedVersion = event.SourceVersion
			candidate.LastEventID, candidate.LastPayloadHash = "", ""
			candidate.ObservedEventID = event.EventID
		}
		_, insertErr := repository.collection.InsertOne(ctx, candidate)
		if mongo.IsDuplicateKeyError(insertErr) {
			return repository.Apply(ctx, event)
		}
		return insertErr
	}
	if err != nil {
		return err
	}
	updated, changed, err := applySettlementAssociation(current, event, now)
	if err != nil || !changed {
		return err
	}
	result, err := repository.collection.ReplaceOne(ctx, bson.D{{Key: "_id", Value: current.ID}, {Key: "sourceVersion", Value: current.SourceVersion}}, updated)
	if err != nil {
		return err
	}
	if result.MatchedCount == 0 {
		return ErrSettlementAssociationConflict
	}
	return nil
}

func (repository *MongoSettlementAssociationRepository) List(ctx context.Context, query SettlementAssociationQuery) ([]SettlementAssociation, error) {
	query, err := query.normalized()
	if err != nil {
		return nil, err
	}
	if repository == nil || repository.collection == nil {
		return nil, ErrSettlementAssociationUnavailable
	}
	filter := bson.D{{Key: "tenantId", Value: query.TenantID}, {Key: "workspaceId", Value: query.WorkspaceID}}
	if query.SettlementRef != "" {
		filter = append(filter, bson.E{Key: "settlementRef", Value: query.SettlementRef})
	}
	if query.ApprovalRef != "" {
		filter = append(filter, bson.E{Key: "approvalRef", Value: query.ApprovalRef})
	}
	cursor, err := repository.collection.Find(ctx, filter, options.Find().SetSort(bson.D{{Key: "updatedAt", Value: -1}, {Key: "_id", Value: 1}}).SetLimit(int64(query.Limit)))
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)
	items := make([]SettlementAssociation, 0, query.Limit)
	if err := cursor.All(ctx, &items); err != nil {
		return nil, err
	}
	return items, nil
}
