package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	internal_server "github.com/vadimfedulov035/grpc-imageshare/internal/server"
	pb "github.com/vadimfedulov035/grpc-imageshare/pkg/proto"

	"github.com/rs/zerolog"
	zlog "github.com/rs/zerolog/log"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials" // <<< Import the credentials package
	"google.golang.org/grpc/reflection"
)

var (
	port       = flag.Int("port", 50051, "The server port")
	storageDir = flag.String("storage", "./uploads", "Directory to store uploaded files")
	logLevel   = flag.String("log-level", "info", "Logging level (debug, info, warn, error)")
	logFormat  = flag.String("log-format", "console", "Log format (console, json)")

	// --- Add flags for TLS certificate files ---
	certFile = flag.String("tls-cert-file", "certs/server.crt", "Path to TLS certificate file")
	keyFile  = flag.String("tls-key-file", "certs/server.key", "Path to TLS key file")
)

const ShutdownTimeout = 5 * time.Second

func main() {
	flag.Parse()

	// --- Zerolog Config ---
	zerolog.TimeFieldFormat = zerolog.TimeFormatUnixMs

	var logWriter io.Writer
	switch *logFormat {
	case "console":
		logWriter = zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.RFC3339}
	case "json":
		logWriter = os.Stderr
	default:
		// Create and use a temporary logger instance just for this message
		tempLogger := zerolog.New(os.Stderr).With().Timestamp().Logger()
		tempLogger.Error().
			Str("log_format_input", *logFormat).
			Msg("Unknown log-format specified, exiting.")
		os.Exit(1)
	}

	zlog.Logger = zlog.Output(logWriter) // Set the writer

	level, err := zerolog.ParseLevel(*logLevel)
	if err != nil {
		zlog.Warn().
			Err(err).
			Str("level_input", *logLevel).
			Msg("Invalid log level, defaulting to info")
		level = zerolog.InfoLevel
	}
	zerolog.SetGlobalLevel(level)

	// Log the format *after* the logger is configured
	if *logFormat == "console" {
		zlog.Info().Msg("Using console log format (for development)")
	} else {
		zlog.Info().Msg("Using JSON log format")
	}

	// --- Load TLS Credentials ---
	zlog.Info().Str("cert_file", *certFile).Str("key_file", *keyFile).Msg("Loading TLS credentials")
	if *certFile == "" || *keyFile == "" {
		zlog.Fatal().Msg("TLS certificate and key files must be provided (--tls-cert-file, --tls-key-file)")
	}
	creds, err := credentials.NewServerTLSFromFile(*certFile, *keyFile)
	if err != nil {
		zlog.Fatal().Err(err).Msg("Failed to load TLS credentials")
	}
	zlog.Info().Msg("Successfully loaded TLS credentials")

	// -- Server Start --
	zlog.Info().
		Msg("Starting gRPC file server...")

	// Listen TCP port
	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", *port))
	if err != nil {
		zlog.Fatal().
			Err(err).
			Int("port", *port).
			Msg("Failed to listen")
	}
	zlog.Info().
		Str("address", lis.Addr().String()).
		Msg("Server listening securely (TLS enabled)")

	// --- Create gRPC Server with TLS Credentials ---
	serverOptions := []grpc.ServerOption{
		grpc.Creds(creds), // Add TLS credentials
		// TODO: Add interceptors here later (AuthN/AuthZ, Metrics, Logging)
	}
	grpcServer := grpc.NewServer(serverOptions...)

	// --- Register Services ---
	fileServiceServer, err := internal_server.NewFileServer(*storageDir)
	if err != nil {
		zlog.Fatal().Err(err).Str("storage_dir", *storageDir).Msg("Failed to create file server instance")
	}
	pb.RegisterFileServiceServer(grpcServer, fileServiceServer)

	reflection.Register(grpcServer)
	zlog.Info().Msg("gRPC reflection registered")

	// --- Start Server Goroutine ---
	go func() {
		zlog.Info().Msg("Starting server goroutine...")
		if err := grpcServer.Serve(lis); err != nil {
			if !errors.Is(err, grpc.ErrServerStopped) {
				zlog.Error().
					Err(err).
					Msg("gRPC server failed to serve")
			} else {
				zlog.Info().
					Msg("gRPC server stopped serving.")
			}
		}
	}()

	// --- Graceful Shutdown Handling (remains the same) ---
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	receivedSignal := <-quit
	zlog.Info().Str("signal", receivedSignal.String()).Msg("Received signal, shutting down server...")

	stopped := make(chan struct{})
	go func() {
		grpcServer.GracefulStop()
		close(stopped)
	}()

	select {
	case <-stopped:
		zlog.Info().Msg("gRPC server gracefully stopped.")
	case <-time.After(ShutdownTimeout):
		zlog.Warn().Dur("timeout", ShutdownTimeout).Msg("Graceful shutdown timed out. Forcing stop.")
		grpcServer.Stop()
	}

	zlog.Info().Msg("Server exiting.")
}
