package httpapi

import (
	"context"
	"net"
	"net/http"
	"testing"
	"time"
)

func TestRunReportsListenFailure(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	defer listener.Close()

	server := New(listener.Addr().String(), http.NewServeMux(), time.Second)
	if err := server.Run(context.Background()); err == nil {
		t.Fatal("Run() error = nil, want address-in-use error")
	}
}

func TestRunShutsDownOnCancellation(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	address := listener.Addr().String()
	listener.Close()

	server := New(address, http.NewServeMux(), time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- server.Run(ctx) }()

	deadline := time.Now().Add(time.Second)
	for {
		connection, dialErr := net.DialTimeout("tcp", address, 10*time.Millisecond)
		if dialErr == nil {
			connection.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("server did not start: %v", dialErr)
		}
		time.Sleep(time.Millisecond)
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run() error = %v", err)
	}
}
