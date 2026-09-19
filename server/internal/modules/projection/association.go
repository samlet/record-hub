package projection

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/samlet/record-hub/server/internal/modules/identity"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// ProjectApplicationAssociation is a deliberately small, safe read model. It
// contains typed stable references and approval state, never the source
// application payload or workflow input.
type ProjectApplicationAssociation struct {
	ID                  string    `bson:"_id" json:"id"`
	TenantID            string    `bson:"tenantId" json:"tenantId"`
	WorkspaceID         string    `bson:"workspaceId" json:"workspaceId"`
	ProjectRef          string    `bson:"projectRef" json:"projectRef"`
	ApplicationRef      string    `bson:"applicationRef" json:"applicationRef"`
	WorkflowID          string    `bson:"workflowId" json:"workflowId"`
	WorkflowRunID       string    `bson:"workflowRunId" json:"workflowRunId"`
	ApprovalStatus      string    `bson:"approvalStatus" json:"approvalStatus"`
	ProposalHash        string    `bson:"proposalHash" json:"proposalHash"`
	DispatchGeneration  int64     `bson:"dispatchGeneration" json:"dispatchGeneration"`
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

const (
	AssociationCurrent        = "CURRENT"
	AssociationGap            = "GAP"
	AssociationConflict       = "CONFLICT"
	AssociationCollectionName = "project_application_associations"
	DefaultAssociationLimit   = 50
	MaxAssociationLimit       = 100
)

var (
	ErrAssociationInvalid      = errors.New("invalid project application association")
	ErrAssociationUnavailable  = errors.New("project application association store is unavailable")
	ErrAssociationConflict     = errors.New("project application association is in conflict")
	ErrAssociationQueryInvalid = errors.New("invalid project application association query")
	projectRefPattern          = regexp.MustCompile(`^fluxion:PROJECT:[0-9a-fA-F-]{36}$`)
	applicationRefPattern      = regexp.MustCompile(`^approver:APPLICATION:[a-zA-Z0-9][a-zA-Z0-9._:-]{0,127}$`)
)

// AssociationEvent is the allowlisted input emitted from the approval
// summary contract. SourceVersion is the aggregate version; projection only
// advances one contiguous version at a time and records gaps/conflicts.
type AssociationEvent struct {
	TenantID           string
	WorkspaceID        string
	ProjectRef         string
	ApplicationRef     string
	WorkflowID         string
	WorkflowRunID      string
	ApprovalStatus     string
	ProposalHash       string
	DispatchGeneration int64
	DecisionVersion    int64
	SourceVersion      int64
	EventID            string
	PayloadHash        string
	ObservedAt         time.Time
}

func (event AssociationEvent) Validate() error {
	if strings.TrimSpace(event.TenantID) == "" || strings.TrimSpace(event.WorkspaceID) == "" ||
		!projectRefPattern.MatchString(event.ProjectRef) || !applicationRefPattern.MatchString(event.ApplicationRef) ||
		strings.TrimSpace(event.WorkflowID) == "" || strings.TrimSpace(event.WorkflowRunID) == "" ||
		strings.TrimSpace(event.ApprovalStatus) == "" || strings.TrimSpace(event.ProposalHash) == "" ||
		event.DispatchGeneration < 1 || event.DecisionVersion < 1 || event.SourceVersion < 1 ||
		strings.TrimSpace(event.EventID) == "" || strings.TrimSpace(event.PayloadHash) == "" || event.ObservedAt.IsZero() {
		return ErrAssociationInvalid
	}
	if len(event.TenantID) > 128 || len(event.WorkspaceID) > 128 || strings.ContainsAny(event.TenantID+event.WorkspaceID, " \t\r\n") {
		return ErrAssociationInvalid
	}
	switch event.ApprovalStatus {
	case "PENDING", "APPROVED", "REJECTED", "CANCELLED", "WITHDRAWN", "EXPIRED", "FAILED":
	default:
		return ErrAssociationInvalid
	}
	return nil
}

func (association ProjectApplicationAssociation) Validate() error {
	if strings.TrimSpace(association.ID) == "" || strings.TrimSpace(association.TenantID) == "" || strings.TrimSpace(association.WorkspaceID) == "" ||
		!projectRefPattern.MatchString(association.ProjectRef) || !applicationRefPattern.MatchString(association.ApplicationRef) ||
		association.SourceVersion < 0 || association.ObservedVersion < association.SourceVersion || association.UpdatedAt.IsZero() {
		return ErrAssociationInvalid
	}
	if association.SourceVersion > 0 && (association.LastEventID == "" || association.LastPayloadHash == "") {
		return ErrAssociationInvalid
	}
	switch association.ProjectionState {
	case AssociationCurrent, AssociationGap, AssociationConflict:
	default:
		return ErrAssociationInvalid
	}
	return nil
}

type AssociationQuery struct {
	TenantID       string
	WorkspaceID    string
	ProjectRef     string
	ApplicationRef string
	Limit          int
}

func (query AssociationQuery) normalized() (AssociationQuery, error) {
	query.TenantID, query.WorkspaceID = strings.TrimSpace(query.TenantID), strings.TrimSpace(query.WorkspaceID)
	query.ProjectRef, query.ApplicationRef = strings.TrimSpace(query.ProjectRef), strings.TrimSpace(query.ApplicationRef)
	if query.TenantID == "" || query.WorkspaceID == "" || len(query.TenantID) > 128 || len(query.WorkspaceID) > 128 || strings.ContainsAny(query.TenantID+query.WorkspaceID, " \t\r\n") {
		return AssociationQuery{}, ErrAssociationQueryInvalid
	}
	if query.ProjectRef != "" && !projectRefPattern.MatchString(query.ProjectRef) || query.ApplicationRef != "" && !applicationRefPattern.MatchString(query.ApplicationRef) {
		return AssociationQuery{}, ErrAssociationQueryInvalid
	}
	if query.Limit == 0 {
		query.Limit = DefaultAssociationLimit
	}
	if query.Limit < 1 || query.Limit > MaxAssociationLimit {
		return AssociationQuery{}, ErrAssociationQueryInvalid
	}
	return query, nil
}

type AssociationRepository interface {
	Apply(context.Context, AssociationEvent) error
	List(context.Context, AssociationQuery) ([]ProjectApplicationAssociation, error)
}

// AssociationWriter is intentionally narrower than AssociationRepository so
// the summary projector can be tested without MongoDB.
type AssociationWriter interface {
	Apply(context.Context, AssociationEvent) error
}

type approvalAssociationPayload struct {
	ApplicationRef string `json:"applicationRef"`
	ProjectRef     string `json:"projectRef"`
	ApprovalStatus string `json:"status"`
	WorkflowRef    struct {
		WorkflowID string `json:"workflowId"`
		RunID      string `json:"runId"`
	} `json:"workflowRef"`
	ProposalHash       string `json:"proposalHash"`
	DispatchGeneration int64  `json:"dispatchGeneration"`
	DecisionVersion    int64  `json:"decisionVersion"`
}

func associationEventFromSummary(envelope summaryEventEnvelope, rawPayload []byte, now time.Time) (AssociationEvent, error) {
	var payload approvalAssociationPayload
	if err := json.Unmarshal(rawPayload, &payload); err != nil {
		return AssociationEvent{}, fmt.Errorf("decode approval association payload: %w", err)
	}
	digest := sha256.Sum256(rawPayload)
	return AssociationEvent{TenantID: envelope.TenantID, WorkspaceID: strings.TrimSpace(envelope.Metadata["workspaceId"]), ProjectRef: payload.ProjectRef, ApplicationRef: payload.ApplicationRef, WorkflowID: payload.WorkflowRef.WorkflowID, WorkflowRunID: payload.WorkflowRef.RunID, ApprovalStatus: payload.ApprovalStatus, ProposalHash: payload.ProposalHash, DispatchGeneration: payload.DispatchGeneration, DecisionVersion: payload.DecisionVersion, SourceVersion: envelope.AggregateVersion, EventID: envelope.EventID, PayloadHash: "sha256:" + hex.EncodeToString(digest[:]), ObservedAt: now.UTC()}, nil
}

type MongoAssociationRepository struct {
	collection *mongo.Collection
	clock      func() time.Time
}

func NewMongoAssociationRepository(database *mongo.Database) *MongoAssociationRepository {
	if database == nil {
		return &MongoAssociationRepository{}
	}
	return &MongoAssociationRepository{collection: database.Collection(AssociationCollectionName), clock: time.Now}
}

func (repository *MongoAssociationRepository) EnsureIndexes(ctx context.Context) error {
	if repository == nil || repository.collection == nil {
		return ErrAssociationUnavailable
	}
	if _, err := repository.collection.Indexes().CreateMany(ctx, []mongo.IndexModel{
		{Keys: bson.D{{Key: "tenantId", Value: 1}, {Key: "workspaceId", Value: 1}, {Key: "projectRef", Value: 1}, {Key: "applicationRef", Value: 1}}, Options: options.Index().SetName("project_application_association_scope_unique").SetUnique(true)},
		{Keys: bson.D{{Key: "tenantId", Value: 1}, {Key: "workspaceId", Value: 1}, {Key: "projectionState", Value: 1}, {Key: "updatedAt", Value: -1}}, Options: options.Index().SetName("project_application_association_state")},
	}); err != nil {
		return fmt.Errorf("create association indexes: %w", err)
	}
	return nil
}

func (repository *MongoAssociationRepository) Apply(ctx context.Context, event AssociationEvent) error {
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
	id := associationID(event.TenantID, event.WorkspaceID, event.ProjectRef, event.ApplicationRef)
	var current ProjectApplicationAssociation
	err := repository.collection.FindOne(ctx, bson.D{{Key: "_id", Value: id}, {Key: "tenantId", Value: event.TenantID}, {Key: "workspaceId", Value: event.WorkspaceID}}).Decode(&current)
	if errors.Is(err, mongo.ErrNoDocuments) {
		candidate := associationFromEvent(id, event, now)
		if event.SourceVersion > 1 {
			candidate.ProjectionState = AssociationGap
			candidate.SourceVersion = 0
			candidate.ObservedVersion = event.SourceVersion
			candidate.LastEventID, candidate.LastPayloadHash = "", ""
			candidate.ObservedEventID = event.EventID
		}
		if _, insertErr := repository.collection.InsertOne(ctx, candidate); insertErr != nil {
			if mongo.IsDuplicateKeyError(insertErr) {
				return repository.Apply(ctx, event)
			}
			return fmt.Errorf("insert association: %w", insertErr)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("find association: %w", err)
	}
	updatedAssociation, updated, applyErr := applyAssociationEvent(current, event, now)
	if applyErr != nil {
		return applyErr
	}
	if !updated {
		return nil
	}
	result, err := repository.collection.ReplaceOne(ctx, bson.D{{Key: "_id", Value: current.ID}, {Key: "sourceVersion", Value: current.SourceVersion}}, updatedAssociation)
	if err != nil {
		return fmt.Errorf("update association: %w", err)
	}
	if result.MatchedCount == 0 {
		return ErrAssociationConflict
	}
	return nil
}

func (repository *MongoAssociationRepository) List(ctx context.Context, query AssociationQuery) ([]ProjectApplicationAssociation, error) {
	query, err := query.normalized()
	if err != nil {
		return nil, err
	}
	if repository == nil || repository.collection == nil {
		return nil, ErrAssociationUnavailable
	}
	filter := bson.D{{Key: "tenantId", Value: query.TenantID}, {Key: "workspaceId", Value: query.WorkspaceID}}
	if query.ProjectRef != "" {
		filter = append(filter, bson.E{Key: "projectRef", Value: query.ProjectRef})
	}
	if query.ApplicationRef != "" {
		filter = append(filter, bson.E{Key: "applicationRef", Value: query.ApplicationRef})
	}
	cursor, err := repository.collection.Find(ctx, filter, options.Find().SetSort(bson.D{{Key: "updatedAt", Value: -1}, {Key: "_id", Value: 1}}).SetLimit(int64(query.Limit)))
	if err != nil {
		return nil, fmt.Errorf("list associations: %w", err)
	}
	defer cursor.Close(ctx)
	items := make([]ProjectApplicationAssociation, 0, query.Limit)
	if err := cursor.All(ctx, &items); err != nil {
		return nil, fmt.Errorf("decode associations: %w", err)
	}
	return items, nil
}

func associationID(tenantID, workspaceID, projectRef, applicationRef string) string {
	digest := sha256.Sum256([]byte(strings.Join([]string{tenantID, workspaceID, projectRef, applicationRef}, "\x00")))
	return "association-" + hex.EncodeToString(digest[:])[:40]
}

func associationFromEvent(id string, event AssociationEvent, now time.Time) ProjectApplicationAssociation {
	status := event.ApprovalStatus
	if status == "WITHDRAWN" {
		status = "CANCELLED"
	}
	return ProjectApplicationAssociation{ID: id, TenantID: event.TenantID, WorkspaceID: event.WorkspaceID, ProjectRef: event.ProjectRef, ApplicationRef: event.ApplicationRef, WorkflowID: event.WorkflowID, WorkflowRunID: event.WorkflowRunID, ApprovalStatus: status, ProposalHash: event.ProposalHash, DispatchGeneration: event.DispatchGeneration, DecisionVersion: event.DecisionVersion, SourceVersion: event.SourceVersion, ObservedVersion: event.SourceVersion, LastEventID: event.EventID, LastPayloadHash: event.PayloadHash, ProjectionState: AssociationCurrent, UpdatedAt: now}
}

func applyAssociationEvent(current ProjectApplicationAssociation, event AssociationEvent, now time.Time) (ProjectApplicationAssociation, bool, error) {
	if current.ID == "" {
		return current, false, ErrAssociationInvalid
	}
	if current.ProjectionState == AssociationConflict && event.SourceVersion > current.SourceVersion {
		return current, false, ErrAssociationConflict
	}
	if event.SourceVersion < current.SourceVersion {
		return current, false, nil
	}
	if event.SourceVersion == current.SourceVersion {
		if event.PayloadHash == current.LastPayloadHash || event.EventID == current.LastEventID {
			return current, false, nil
		}
		current.ProjectionState, current.ConflictVersion, current.ConflictPayloadHash, current.UpdatedAt = AssociationConflict, event.SourceVersion, event.PayloadHash, now
		return current, true, nil
	}
	if event.SourceVersion > current.SourceVersion+1 {
		if event.SourceVersion <= current.ObservedVersion {
			return current, false, nil
		}
		current.ObservedVersion, current.ObservedEventID, current.ProjectionState, current.UpdatedAt = event.SourceVersion, event.EventID, AssociationGap, now
		return current, true, nil
	}
	updated := associationFromEvent(current.ID, event, now)
	updated.ObservedVersion, updated.ObservedEventID = current.ObservedVersion, current.ObservedEventID
	if event.SourceVersion > updated.ObservedVersion {
		updated.ObservedVersion = event.SourceVersion
	}
	if updated.ObservedVersion > updated.SourceVersion {
		updated.ProjectionState = AssociationGap
	}
	current = updated
	return current, true, nil
}

// AssociationService is the authenticated read boundary for the typed
// association API. Writes are reserved for the projection worker.
type AssociationService struct {
	repository AssociationRepository
	authorizer *identity.Authorizer
}

func NewAssociationService(repository AssociationRepository, authorizer *identity.Authorizer) *AssociationService {
	return &AssociationService{repository: repository, authorizer: authorizer}
}

func (service *AssociationService) List(ctx context.Context, principal identity.Principal, query AssociationQuery) ([]ProjectApplicationAssociation, error) {
	query, err := query.normalized()
	if err != nil {
		return nil, err
	}
	if service == nil || service.repository == nil {
		return nil, ErrAssociationUnavailable
	}
	if service.authorizer == nil {
		return nil, identity.ErrForbidden
	}
	if _, err := service.authorizer.Authorize(ctx, principal, query.TenantID, query.WorkspaceID, identity.ActionWorkspaceRead); err != nil {
		return nil, err
	}
	return service.repository.List(ctx, query)
}
