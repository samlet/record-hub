package com.samlet.recordhub.sdk;

import com.sun.net.httpserver.HttpServer;
import org.junit.jupiter.api.AfterEach;
import org.junit.jupiter.api.Test;

import java.net.InetSocketAddress;
import java.nio.charset.StandardCharsets;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertThrows;

class RecordHubBindingClientTest {
    private HttpServer server;

    @AfterEach
    void close() {
        if (server != null) {
            server.stop(0);
        }
    }

    @Test
    void createsSnapshotWithStableOperationHeader() throws Exception {
        server = HttpServer.create(new InetSocketAddress(0), 0);
        server.createContext("/api/v1/bindings/snapshots", exchange -> {
            assertEquals("operation-1", exchange.getRequestHeaders().getFirst("Idempotency-Key"));
            assertEquals("Bearer token", exchange.getRequestHeaders().getFirst("Authorization"));
            byte[] body = "{\"snapshotId\":\"snapshot-1\",\"tenantId\":\"tenant-1\",\"workspaceId\":\"workspace-1\",\"recordRef\":\"fluxion:PROJECT:project-1\",\"schemaId\":\"urn:summary\",\"schemaVersion\":1,\"recordVersion\":8,\"sourceVersion\":17,\"purpose\":\"diagnostic\",\"snapshotHash\":\"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\",\"data\":{\"status\":\"ACTIVE\"},\"createdAt\":\"2026-09-16T12:00:00Z\"}".getBytes(StandardCharsets.UTF_8);
            exchange.getResponseHeaders().set("Content-Type", "application/json");
            exchange.sendResponseHeaders(201, body.length);
            exchange.getResponseBody().write(body);
            exchange.close();
        });
        server.start();
        RecordHubBindingClient client = RecordHubBindingClient.builder("http://127.0.0.1:" + server.getAddress().getPort())
                .bearerToken("token")
                .build();
        RecordHubBindingClient.SnapshotResult result = client.createSnapshotBlocking(new RecordHubBindingClient.SnapshotRequest("tenant-1", "workspace-1", "fluxion:PROJECT:project-1", "urn:summary", 1, 8, 17, "diagnostic"), "operation-1");
        assertFalse(result.replayed());
        assertEquals("snapshot-1", result.snapshot().snapshotId());
        assertEquals("ACTIVE", result.snapshot().data().path("status").asText());
    }

    @Test
    void mapsConflictToApiException() throws Exception {
        server = HttpServer.create(new InetSocketAddress(0), 0);
        server.createContext("/api/v1/bindings/snapshots", exchange -> {
            byte[] body = "{\"error\":{\"code\":\"SCHEMA_MISMATCH\",\"message\":\"stale\"}}".getBytes(StandardCharsets.UTF_8);
            exchange.sendResponseHeaders(409, body.length);
            exchange.getResponseBody().write(body);
            exchange.close();
        });
        server.start();
        RecordHubBindingClient client = RecordHubBindingClient.builder("http://127.0.0.1:" + server.getAddress().getPort()).build();
        RecordHubBindingClient.ApiException error = assertThrows(RecordHubBindingClient.ApiException.class, () -> client.createSnapshotBlocking(new RecordHubBindingClient.SnapshotRequest("tenant-1", "workspace-1", "fluxion:PROJECT:project-1", "urn:summary", 1, 8, 17, "diagnostic"), "operation-1"));
        assertEquals(409, error.statusCode());
        assertEquals("SCHEMA_MISMATCH", error.code());
    }
}
