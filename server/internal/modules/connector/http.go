package connector

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/samlet/record-hub/server/internal/modules/identity"
)

type onboardingUploadRequest struct {
	TenantID              string   `json:"tenantId"`
	WorkspaceID           string   `json:"workspaceId"`
	Connector             string   `json:"connector"`
	Event                 string   `json:"event"`
	SchemaVersion         int64    `json:"schemaVersion"`
	OwnerSystem           string   `json:"ownerSystem"`
	SDKVersion            string   `json:"sdkVersion"`
	CompatibilityMin      string   `json:"compatibilityMin"`
	CompatibilityMax      string   `json:"compatibilityMax"`
	ContractHash          string   `json:"contractHash"`
	AllowedFields         []string `json:"allowedFields"`
	FixtureDigest         string   `json:"fixtureDigest"`
	RedactionPolicyDigest string   `json:"redactionPolicyDigest"`
	SourceCommit          string   `json:"sourceCommit"`
}

type onboardingReviewRequest struct {
	Approved bool `json:"approved"`
}

type OnboardingHTTPHandler struct {
	service *OnboardingService
}

func NewOnboardingHTTPHandler(service *OnboardingService) http.Handler {
	handler := &OnboardingHTTPHandler{service: service}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/connectors/onboarding", handler.list)
	mux.HandleFunc("POST /api/v1/connectors/onboarding", handler.upload)
	mux.HandleFunc("POST /api/v1/connectors/onboarding/{connector}/{event}/{schemaVersion}/submit", handler.submitForReview)
	mux.HandleFunc("POST /api/v1/connectors/onboarding/{connector}/{event}/{schemaVersion}/review", handler.review)
	mux.HandleFunc("POST /api/v1/connectors/onboarding/{connector}/{event}/{schemaVersion}/enable", handler.enable)
	mux.HandleFunc("POST /api/v1/connectors/onboarding/{connector}/{event}/{schemaVersion}/disable", handler.disable)
	return mux
}

func (handler *OnboardingHTTPHandler) upload(writer http.ResponseWriter, request *http.Request) {
	principal, ok := principalFromRequest(writer, request)
	if !ok {
		return
	}
	var input onboardingUploadRequest
	if !decodeOnboardingJSON(writer, request, &input) {
		return
	}
	record, err := handler.service.Submit(request.Context(), principal, input.TenantID, input.WorkspaceID, Manifest{Key: Key{Connector: input.Connector, Event: input.Event, SchemaVersion: input.SchemaVersion}, OwnerSystem: input.OwnerSystem, SDKVersion: input.SDKVersion, CompatibilityMin: input.CompatibilityMin, CompatibilityMax: input.CompatibilityMax, ContractHash: input.ContractHash, AllowedFields: input.AllowedFields, FixtureDigest: input.FixtureDigest, RedactionPolicyDigest: input.RedactionPolicyDigest, SourceCommit: input.SourceCommit}, OnboardingEvidence{FixtureDigest: input.FixtureDigest, RedactionPolicyDigest: input.RedactionPolicyDigest, SourceCommit: input.SourceCommit})
	if err != nil {
		writeOnboardingError(writer, err)
		return
	}
	writeOnboardingJSON(writer, http.StatusCreated, record)
}

func (handler *OnboardingHTTPHandler) list(writer http.ResponseWriter, request *http.Request) {
	principal, ok := principalFromRequest(writer, request)
	if !ok {
		return
	}
	records, err := handler.service.List(request.Context(), principal, request.URL.Query().Get("tenantId"), request.URL.Query().Get("workspaceId"))
	if err != nil {
		writeOnboardingError(writer, err)
		return
	}
	writeOnboardingJSON(writer, http.StatusOK, map[string]interface{}{"items": records})
}

func (handler *OnboardingHTTPHandler) submitForReview(writer http.ResponseWriter, request *http.Request) {
	principal, ok := principalFromRequest(writer, request)
	if !ok {
		return
	}
	record, err := handler.service.SubmitForReview(request.Context(), principal, request.URL.Query().Get("tenantId"), request.URL.Query().Get("workspaceId"), requestKey(request), expectedRevision(request))
	if err != nil {
		writeOnboardingError(writer, err)
		return
	}
	writeOnboardingJSON(writer, http.StatusOK, record)
}

func (handler *OnboardingHTTPHandler) review(writer http.ResponseWriter, request *http.Request) {
	principal, ok := principalFromRequest(writer, request)
	if !ok {
		return
	}
	var input onboardingReviewRequest
	if !decodeOnboardingJSON(writer, request, &input) {
		return
	}
	record, err := handler.service.Review(request.Context(), principal, request.URL.Query().Get("tenantId"), request.URL.Query().Get("workspaceId"), requestKey(request), expectedRevision(request), input.Approved)
	if err != nil {
		writeOnboardingError(writer, err)
		return
	}
	writeOnboardingJSON(writer, http.StatusOK, record)
}

func (handler *OnboardingHTTPHandler) enable(writer http.ResponseWriter, request *http.Request) {
	handler.transition(writer, request, true)
}

func (handler *OnboardingHTTPHandler) disable(writer http.ResponseWriter, request *http.Request) {
	handler.transition(writer, request, false)
}

func (handler *OnboardingHTTPHandler) transition(writer http.ResponseWriter, request *http.Request, enable bool) {
	principal, ok := principalFromRequest(writer, request)
	if !ok {
		return
	}
	var record OnboardingRecord
	var err error
	if enable {
		record, err = handler.service.Enable(request.Context(), principal, request.URL.Query().Get("tenantId"), request.URL.Query().Get("workspaceId"), requestKey(request), expectedRevision(request))
	} else {
		record, err = handler.service.Disable(request.Context(), principal, request.URL.Query().Get("tenantId"), request.URL.Query().Get("workspaceId"), requestKey(request), expectedRevision(request))
	}
	if err != nil {
		writeOnboardingError(writer, err)
		return
	}
	writeOnboardingJSON(writer, http.StatusOK, record)
}

func principalFromRequest(writer http.ResponseWriter, request *http.Request) (identity.Principal, bool) {
	principal, ok := identity.PrincipalFromContext(request.Context())
	if !ok {
		writeOnboardingError(writer, identity.ErrUnauthenticated)
		return identity.Principal{}, false
	}
	return principal, true
}

func requestKey(request *http.Request) Key {
	version, _ := strconv.ParseInt(request.PathValue("schemaVersion"), 10, 64)
	return Key{Connector: request.PathValue("connector"), Event: request.PathValue("event"), SchemaVersion: version}
}

func expectedRevision(request *http.Request) int64 {
	value := strings.Trim(strings.TrimSpace(request.Header.Get("If-Match")), "\"")
	revision, _ := strconv.ParseInt(value, 10, 64)
	return revision
}

func decodeOnboardingJSON(writer http.ResponseWriter, request *http.Request, target interface{}) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeOnboardingError(writer, fmt.Errorf("invalid onboarding request: %w", err))
		return false
	}
	var extra interface{}
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		writeOnboardingError(writer, errors.New("onboarding request must contain one JSON object"))
		return false
	}
	return true
}

func writeOnboardingJSON(writer http.ResponseWriter, status int, value interface{}) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}

func writeOnboardingError(writer http.ResponseWriter, err error) {
	status := http.StatusBadRequest
	code := "INVALID_ONBOARDING_REQUEST"
	switch {
	case errors.Is(err, identity.ErrUnauthenticated):
		status, code = http.StatusUnauthorized, "AUTHENTICATION_REQUIRED"
	case errors.Is(err, identity.ErrForbidden):
		status, code = http.StatusForbidden, "FORBIDDEN"
	case errors.Is(err, ErrOnboardingNotFound), errors.Is(err, ErrConnectorNotFound):
		status, code = http.StatusNotFound, "ONBOARDING_NOT_FOUND"
	case errors.Is(err, ErrOnboardingExists), errors.Is(err, ErrRevisionConflict):
		status, code = http.StatusConflict, "ONBOARDING_CONFLICT"
	case errors.Is(err, ErrOnboardingEvidence), errors.Is(err, ErrOnboardingState):
		status, code = http.StatusConflict, "ONBOARDING_STATE_CONFLICT"
	}
	writeOnboardingJSON(writer, status, map[string]interface{}{"error": map[string]string{"code": code, "message": err.Error()}})
}

var _ interface {
	Submit(context.Context, identity.Principal, string, string, Manifest, OnboardingEvidence) (OnboardingRecord, error)
} = (*OnboardingService)(nil)
