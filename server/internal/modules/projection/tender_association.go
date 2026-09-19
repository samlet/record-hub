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

// TenderApplicationAssociation is a safe cross-system read model. It holds
// references and terminal decision metadata only; bid amounts, documents and
// workflow inputs are deliberately not projected.
type TenderApplicationAssociation struct {
	ID                  string    `bson:"_id" json:"id"`
	TenantID            string    `bson:"tenantId" json:"tenantId"`
	WorkspaceID         string    `bson:"workspaceId" json:"workspaceId"`
	TenderRef           string    `bson:"tenderRef" json:"tenderRef"`
	ApplicationRef      string    `bson:"applicationRef" json:"applicationRef"`
	WorkflowID          string    `bson:"workflowId" json:"workflowId"`
	WorkflowRunID       string    `bson:"workflowRunId" json:"workflowRunId"`
	ApprovalStatus      string    `bson:"approvalStatus" json:"approvalStatus"`
	ProposalHash        string    `bson:"proposalHash" json:"proposalHash"`
	ApprovalGeneration  int64     `bson:"approvalGeneration" json:"approvalGeneration"`
	DecisionVersion     int64     `bson:"decisionVersion" json:"decisionVersion"`
	SourceVersion       int64     `bson:"sourceVersion" json:"sourceVersion"`
	ObservedVersion     int64     `bson:"observedVersion" json:"observedVersion"`
	LastEventID         string    `bson:"lastEventId" json:"lastEventId"`
	LastPayloadHash     string    `bson:"lastPayloadHash" json:"lastPayloadHash"`
	ObservedEventID     string    `bson:"observedEventId,omitempty" json:"observedEventId,omitempty"`
	ConflictVersion     int64     `bson:"conflictVersion,omitempty" json:"conflictVersion,omitempty"`
	ConflictPayloadHash string    `bson:"conflictPayloadHash,omitempty" json:"conflictPayloadHash,omitempty"`
	ProjectionState     string    `bson:"projectionState" json:"projectionState"`
	UpdatedAt           time.Time `bson:"updatedAt" json:"updatedAt"`
}

type TenderApplicationAssociationEvent struct {
	TenantID, WorkspaceID, TenderRef, ApplicationRef, WorkflowID, WorkflowRunID, ApprovalStatus, ProposalHash, EventID, PayloadHash string
	ApprovalGeneration, DecisionVersion, SourceVersion                                                                              int64
	ObservedAt                                                                                                                      time.Time
}
type TenderApplicationAssociationQuery struct {
	TenantID, WorkspaceID, TenderRef, ApplicationRef string
	Limit                                            int
}
type TenderApplicationAssociationRepository interface {
	Apply(context.Context, TenderApplicationAssociationEvent) error
	List(context.Context, TenderApplicationAssociationQuery) ([]TenderApplicationAssociation, error)
}
type TenderApplicationAssociationService struct {
	repository TenderApplicationAssociationRepository
	authorizer *identity.Authorizer
}

const TenderApplicationAssociationCollectionName = "tender_application_associations"

var tenderRefPattern = regexp.MustCompile(`^bids:TENDER:[0-9a-fA-F-]{36}$`)
var tenderApplicationRefPattern = regexp.MustCompile(`^approver:APPLICATION:[a-zA-Z0-9][a-zA-Z0-9._:-]{0,127}$`)

func NewTenderApplicationAssociationService(repository TenderApplicationAssociationRepository, authorizer *identity.Authorizer) *TenderApplicationAssociationService {
	return &TenderApplicationAssociationService{repository: repository, authorizer: authorizer}
}
func (event TenderApplicationAssociationEvent) Validate() error {
	if strings.TrimSpace(event.TenantID) == "" || strings.TrimSpace(event.WorkspaceID) == "" || !tenderRefPattern.MatchString(event.TenderRef) || !tenderApplicationRefPattern.MatchString(event.ApplicationRef) || strings.TrimSpace(event.WorkflowID) == "" || strings.TrimSpace(event.WorkflowRunID) == "" || strings.TrimSpace(event.ApprovalStatus) == "" || strings.TrimSpace(event.ProposalHash) == "" || event.ApprovalGeneration < 1 || event.DecisionVersion < 1 || event.SourceVersion < 1 || event.EventID == "" || event.PayloadHash == "" || event.ObservedAt.IsZero() {
		return errors.New("invalid tender application association")
	}
	switch event.ApprovalStatus {
	case "PENDING", "APPROVED", "REJECTED", "CANCELLED", "EXPIRED", "FAILED":
	default:
		return errors.New("invalid tender application approval status")
	}
	return nil
}
func (query TenderApplicationAssociationQuery) normalized() (TenderApplicationAssociationQuery, error) {
	query.TenantID = strings.TrimSpace(query.TenantID)
	query.WorkspaceID = strings.TrimSpace(query.WorkspaceID)
	query.TenderRef = strings.TrimSpace(query.TenderRef)
	query.ApplicationRef = strings.TrimSpace(query.ApplicationRef)
	if query.TenantID == "" || query.WorkspaceID == "" || len(query.TenantID) > 128 || len(query.WorkspaceID) > 128 || strings.ContainsAny(query.TenantID+query.WorkspaceID, " \t\r\n") || (query.TenderRef != "" && !tenderRefPattern.MatchString(query.TenderRef)) || (query.ApplicationRef != "" && !tenderApplicationRefPattern.MatchString(query.ApplicationRef)) {
		return TenderApplicationAssociationQuery{}, errors.New("invalid tender application association query")
	}
	if query.Limit == 0 {
		query.Limit = 50
	}
	if query.Limit < 1 || query.Limit > 100 {
		return TenderApplicationAssociationQuery{}, errors.New("invalid tender application association limit")
	}
	return query, nil
}
func (service *TenderApplicationAssociationService) List(ctx context.Context, principal identity.Principal, query TenderApplicationAssociationQuery) ([]TenderApplicationAssociation, error) {
	normalized, err := query.normalized()
	if err != nil {
		return nil, err
	}
	if service == nil || service.repository == nil {
		return nil, ErrAssociationUnavailable
	}
	if service.authorizer == nil {
		return nil, identity.ErrForbidden
	}
	if _, err := service.authorizer.Authorize(ctx, principal, normalized.TenantID, normalized.WorkspaceID, identity.ActionWorkspaceRead); err != nil {
		return nil, err
	}
	return service.repository.List(ctx, normalized)
}

type MongoTenderApplicationAssociationRepository struct {
	collection *mongo.Collection
	clock      func() time.Time
}

func NewMongoTenderApplicationAssociationRepository(database *mongo.Database) *MongoTenderApplicationAssociationRepository {
	if database == nil {
		return &MongoTenderApplicationAssociationRepository{}
	}
	return &MongoTenderApplicationAssociationRepository{collection: database.Collection(TenderApplicationAssociationCollectionName), clock: time.Now}
}
func (repository *MongoTenderApplicationAssociationRepository) EnsureIndexes(ctx context.Context) error {
	if repository == nil || repository.collection == nil {
		return ErrAssociationUnavailable
	}
	_, err := repository.collection.Indexes().CreateMany(ctx, []mongo.IndexModel{{Keys: bson.D{{Key: "tenantId", Value: 1}, {Key: "workspaceId", Value: 1}, {Key: "tenderRef", Value: 1}, {Key: "applicationRef", Value: 1}}, Options: options.Index().SetName("tender_application_association_scope_unique").SetUnique(true)}, {Keys: bson.D{{Key: "tenantId", Value: 1}, {Key: "workspaceId", Value: 1}, {Key: "projectionState", Value: 1}, {Key: "updatedAt", Value: -1}}, Options: options.Index().SetName("tender_application_association_state")}})
	if err != nil {
		return err
	}
	return nil
}
func tenderApplicationAssociationID(tenant, workspace, tender, application string) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{tenant, workspace, tender, application}, "\x00")))
	return "tender-association-" + hex.EncodeToString(sum[:])[:40]
}
func tenderAssociationFromEvent(id string, event TenderApplicationAssociationEvent, now time.Time) TenderApplicationAssociation {
	return TenderApplicationAssociation{ID: id, TenantID: event.TenantID, WorkspaceID: event.WorkspaceID, TenderRef: event.TenderRef, ApplicationRef: event.ApplicationRef, WorkflowID: event.WorkflowID, WorkflowRunID: event.WorkflowRunID, ApprovalStatus: event.ApprovalStatus, ProposalHash: event.ProposalHash, ApprovalGeneration: event.ApprovalGeneration, DecisionVersion: event.DecisionVersion, SourceVersion: event.SourceVersion, ObservedVersion: event.SourceVersion, LastEventID: event.EventID, LastPayloadHash: event.PayloadHash, ProjectionState: AssociationCurrent, UpdatedAt: now}
}
func applyTenderAssociation(current TenderApplicationAssociation, event TenderApplicationAssociationEvent, now time.Time) (TenderApplicationAssociation, bool, error) {
	if event.SourceVersion < current.SourceVersion {
		return current, false, nil
	}
	if event.SourceVersion == current.SourceVersion {
		if event.EventID == current.LastEventID || event.PayloadHash == current.LastPayloadHash {
			return current, false, nil
		}
		current.ProjectionState = AssociationConflict
		current.ConflictVersion = event.SourceVersion
		current.ConflictPayloadHash = event.PayloadHash
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
	updated := tenderAssociationFromEvent(current.ID, event, now)
	if current.ObservedVersion > updated.ObservedVersion {
		updated.ObservedVersion = current.ObservedVersion
		updated.ObservedEventID = current.ObservedEventID
	}
	if updated.ObservedVersion > updated.SourceVersion {
		updated.ProjectionState = AssociationGap
	}
	return updated, true, nil
}
func (repository *MongoTenderApplicationAssociationRepository) Apply(ctx context.Context, event TenderApplicationAssociationEvent) error {
	if err := event.Validate(); err != nil {
		return err
	}
	if repository == nil || repository.collection == nil {
		return ErrAssociationUnavailable
	}
	now := event.ObservedAt.UTC()
	if repository.clock != nil {
		now = repository.clock().UTC()
	}
	id := tenderApplicationAssociationID(event.TenantID, event.WorkspaceID, event.TenderRef, event.ApplicationRef)
	var current TenderApplicationAssociation
	err := repository.collection.FindOne(ctx, bson.D{{Key: "_id", Value: id}, {Key: "tenantId", Value: event.TenantID}, {Key: "workspaceId", Value: event.WorkspaceID}}).Decode(&current)
	if errors.Is(err, mongo.ErrNoDocuments) {
		candidate := tenderAssociationFromEvent(id, event, now)
		if event.SourceVersion > 1 {
			candidate.ProjectionState = AssociationGap
			candidate.SourceVersion = 0
			candidate.ObservedVersion = event.SourceVersion
			candidate.LastEventID = ""
			candidate.LastPayloadHash = ""
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
	updated, changed, err := applyTenderAssociation(current, event, now)
	if err != nil || !changed {
		return err
	}
	result, err := repository.collection.ReplaceOne(ctx, bson.D{{Key: "_id", Value: current.ID}, {Key: "sourceVersion", Value: current.SourceVersion}}, updated)
	if err != nil {
		return err
	}
	if result.MatchedCount == 0 {
		return ErrAssociationConflict
	}
	return nil
}
func (repository *MongoTenderApplicationAssociationRepository) List(ctx context.Context, query TenderApplicationAssociationQuery) ([]TenderApplicationAssociation, error) {
	query, err := query.normalized()
	if err != nil {
		return nil, err
	}
	if repository == nil || repository.collection == nil {
		return nil, ErrAssociationUnavailable
	}
	filter := bson.D{{Key: "tenantId", Value: query.TenantID}, {Key: "workspaceId", Value: query.WorkspaceID}}
	if query.TenderRef != "" {
		filter = append(filter, bson.E{Key: "tenderRef", Value: query.TenderRef})
	}
	if query.ApplicationRef != "" {
		filter = append(filter, bson.E{Key: "applicationRef", Value: query.ApplicationRef})
	}
	cursor, err := repository.collection.Find(ctx, filter, options.Find().SetSort(bson.D{{Key: "updatedAt", Value: -1}, {Key: "_id", Value: 1}}).SetLimit(int64(query.Limit)))
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)
	items := make([]TenderApplicationAssociation, 0, query.Limit)
	if err := cursor.All(ctx, &items); err != nil {
		return nil, err
	}
	return items, nil
}
