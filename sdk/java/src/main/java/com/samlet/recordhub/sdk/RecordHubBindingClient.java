package com.samlet.recordhub.sdk;

import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;
import com.fasterxml.jackson.databind.node.ObjectNode;

import java.io.IOException;
import java.net.URI;
import java.net.URLEncoder;
import java.net.http.HttpClient;
import java.net.http.HttpRequest;
import java.net.http.HttpResponse;
import java.nio.charset.StandardCharsets;
import java.time.Duration;
import java.util.Objects;
import java.util.concurrent.CompletableFuture;
import java.util.concurrent.CompletionException;

/**
 * Framework-neutral Java/Kotlin client for the immutable workflow snapshot
 * binding. It uses the public HTTP contract and has no Temporal or Spring
 * dependency.
 */
public final class RecordHubBindingClient {
    public record SnapshotRequest(
            String tenantId,
            String workspaceId,
            String recordRef,
            String schemaId,
            long schemaVersion,
            long expectedRecordVersion,
            long expectedSourceVersion,
            String purpose) {
    }

    public record Snapshot(
            String snapshotId,
            String tenantId,
            String workspaceId,
            String recordRef,
            String schemaId,
            long schemaVersion,
            long recordVersion,
            long sourceVersion,
            String purpose,
            String snapshotHash,
            JsonNode data,
            String createdAt) {
    }

    public record SnapshotResult(Snapshot snapshot, boolean replayed) {
    }

    public static final class ApiException extends RuntimeException {
        private final int statusCode;
        private final String code;

        private ApiException(int statusCode, String code, String message) {
            super(message);
            this.statusCode = statusCode;
            this.code = code;
        }

        public int statusCode() {
            return statusCode;
        }

        public String code() {
            return code;
        }
    }

    private static final int MAX_RESPONSE_BYTES = 2 << 20;

    private final URI baseUri;
    private final HttpClient httpClient;
    private final ObjectMapper objectMapper;
    private final String bearerToken;
    private final Duration requestTimeout;

    private RecordHubBindingClient(Builder builder) {
        this.baseUri = builder.baseUri;
        this.httpClient = builder.httpClient;
        this.objectMapper = builder.objectMapper;
        this.bearerToken = builder.bearerToken;
        this.requestTimeout = builder.requestTimeout;
    }

    public static Builder builder(String baseUrl) {
        return new Builder(baseUrl);
    }

    public CompletableFuture<SnapshotResult> createSnapshot(SnapshotRequest snapshotRequest, String operationId) {
        Objects.requireNonNull(snapshotRequest, "snapshotRequest");
        requireText(operationId, "operationId");
        try {
            String body = objectMapper.writeValueAsString(snapshotRequest);
            HttpRequest request = requestBuilder("/api/v1/bindings/snapshots")
                    .header("Content-Type", "application/json")
                    .header("Idempotency-Key", operationId)
                    .POST(HttpRequest.BodyPublishers.ofString(body, StandardCharsets.UTF_8))
                    .build();
            return send(request).thenApply(response -> new SnapshotResult(decode(response), response.statusCode() == 200));
        } catch (IOException error) {
            return CompletableFuture.failedFuture(error);
        }
    }

    public SnapshotResult createSnapshotBlocking(SnapshotRequest snapshotRequest, String operationId) {
        return join(createSnapshot(snapshotRequest, operationId));
    }

    public CompletableFuture<Snapshot> getSnapshot(String tenantId, String workspaceId, String snapshotId) {
        requireText(tenantId, "tenantId");
        requireText(workspaceId, "workspaceId");
        requireText(snapshotId, "snapshotId");
        String path = "/api/v1/bindings/snapshots/" + encodePath(snapshotId) + "?tenantId=" + encodeQuery(tenantId) + "&workspaceId=" + encodeQuery(workspaceId);
        return send(requestBuilder(path).GET().build()).thenApply(this::decode);
    }

    public Snapshot getSnapshotBlocking(String tenantId, String workspaceId, String snapshotId) {
        return join(getSnapshot(tenantId, workspaceId, snapshotId));
    }

    private HttpRequest.Builder requestBuilder(String path) {
        HttpRequest.Builder builder = HttpRequest.newBuilder(baseUri.resolve(path)).timeout(requestTimeout).header("Accept", "application/json");
        if (!bearerToken.isBlank()) {
            builder.header("Authorization", "Bearer " + bearerToken);
        }
        return builder;
    }

    private CompletableFuture<HttpResponse<String>> send(HttpRequest request) {
        return httpClient.sendAsync(request, HttpResponse.BodyHandlers.ofString(StandardCharsets.UTF_8))
                .thenApply(response -> {
                    if (response.body().getBytes(StandardCharsets.UTF_8).length > MAX_RESPONSE_BYTES) {
                        throw new CompletionException(new IllegalStateException("Record Hub response exceeds the 2 MiB limit"));
                    }
                    if (response.statusCode() < 200 || response.statusCode() >= 300) {
                        throw new CompletionException(apiException(response));
                    }
                    return response;
                });
    }

    private Snapshot decode(HttpResponse<String> response) {
        try {
            return objectMapper.readValue(response.body(), Snapshot.class);
        } catch (IOException error) {
            throw new CompletionException(new IllegalStateException("Invalid Record Hub snapshot response", error));
        }
    }

    private ApiException apiException(HttpResponse<String> response) {
        try {
            JsonNode root = objectMapper.readTree(response.body());
            JsonNode error = root.path("error");
            return new ApiException(response.statusCode(), error.path("code").asText("HTTP_" + response.statusCode()), error.path("message").asText("Record Hub request failed"));
        } catch (IOException ignored) {
            return new ApiException(response.statusCode(), "HTTP_" + response.statusCode(), "Record Hub request failed");
        }
    }

    private static <T> T join(CompletableFuture<T> future) {
        try {
            return future.join();
        } catch (CompletionException error) {
            if (error.getCause() instanceof RuntimeException runtime) {
                throw runtime;
            }
            throw error;
        }
    }

    private static String encodePath(String value) {
        return URLEncoder.encode(value, StandardCharsets.UTF_8).replace("+", "%20");
    }

    private static String encodeQuery(String value) {
        return URLEncoder.encode(value, StandardCharsets.UTF_8);
    }

    private static void requireText(String value, String field) {
        if (value == null || value.isBlank()) {
            throw new IllegalArgumentException(field + " is required");
        }
    }

    public static final class Builder {
        private final URI baseUri;
        private HttpClient httpClient = HttpClient.newBuilder().connectTimeout(Duration.ofSeconds(5)).build();
        private ObjectMapper objectMapper = new ObjectMapper();
        private String bearerToken = "";
        private Duration requestTimeout = Duration.ofSeconds(30);

        private Builder(String baseUrl) {
            URI parsed = URI.create(Objects.requireNonNull(baseUrl, "baseUrl").trim());
            if (parsed.getHost() == null || (!"http".equals(parsed.getScheme()) && !"https".equals(parsed.getScheme()))) {
                throw new IllegalArgumentException("Record Hub base URL must be an absolute HTTP(S) URL");
            }
            String value = parsed.toString();
            this.baseUri = URI.create(value.endsWith("/") ? value : value + "/");
        }

        public Builder httpClient(HttpClient value) {
            this.httpClient = Objects.requireNonNull(value, "httpClient");
            return this;
        }

        public Builder objectMapper(ObjectMapper value) {
            this.objectMapper = Objects.requireNonNull(value, "objectMapper");
            return this;
        }

        public Builder bearerToken(String value) {
            this.bearerToken = value == null ? "" : value.trim();
            return this;
        }

        public Builder requestTimeout(Duration value) {
            if (value == null || value.isNegative() || value.isZero()) {
                throw new IllegalArgumentException("requestTimeout must be positive");
            }
            this.requestTimeout = value;
            return this;
        }

        public RecordHubBindingClient build() {
            return new RecordHubBindingClient(this);
        }
    }
}
