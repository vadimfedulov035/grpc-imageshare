package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"flag"
	"os"
	"time"

	"github.com/vadimfedulov035/grpc-imageshare/internal/client"

	"github.com/rs/zerolog"
	zlog "github.com/rs/zerolog/log"

	pb "github.com/vadimfedulov035/grpc-imageshare/pkg/proto"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

func main() {
	// --- Flags ---
	serverAddr := flag.String("addr", "localhost:50051", "The server address in the format host:port")
	serverCert := flag.String("cert", "certs/server.crt", "Path to the server's TLS certificate file")
	logLevel := flag.String("log-level", "info", "Logging level (debug, info, warn, error)")

	// Actions
	doList := flag.Bool("list", false, "List files on the server")
	uploadFile := flag.String("upload", "", "Path of the file to upload")
	downloadFile := flag.String("download", "", "Filename to download from the server")
	outputDir := flag.String("output", ".", "Directory to save downloaded files")

	flag.Parse()

	// --- Zerolog Config ---
	zlog.Logger = zlog.Output(zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.RFC3339})
	level, errLog := zerolog.ParseLevel(*logLevel)
	if errLog != nil {
		zlog.Warn().Err(errLog).Str("level_input", *logLevel).Msg("Invalid log level, using info")
		level = zerolog.InfoLevel
	}
	zerolog.SetGlobalLevel(level) // Set global level

	// --- Argument Validation ---
	actionCount := 0
	if *doList {
		actionCount++
	}
	if *uploadFile != "" {
		actionCount++
	}
	if *downloadFile != "" {
		actionCount++
	}

	if actionCount != 1 {
		zlog.Fatal().Msg("Error: Please specify exactly one action: -list, -upload <filepath>, or -download <filename>")
	}

	// --- TLS Setup ---
	zlog.Debug().Str("certificate_path", *serverCert).Msg("Loading server certificate")
	serverCA, err := os.ReadFile(*serverCert)
	if err != nil {
		zlog.Fatal().Err(err).Str("certificate_path", *serverCert).Msg("Failed to read server certificate file")
	}
	certPool := x509.NewCertPool()
	if !certPool.AppendCertsFromPEM(serverCA) {
		zlog.Fatal().Msg("Failed to add server certificate to pool")
	}
	tlsConfig := &tls.Config{RootCAs: certPool}
	creds := credentials.NewTLS(tlsConfig)
	zlog.Debug().Msg("TLS credentials created")

	// --- gRPC Connection ---
	zlog.Info().Str("server_address", *serverAddr).Msg("Attempting to connect to server")
	opts := []grpc.DialOption{grpc.WithTransportCredentials(creds)}
	conn, err := grpc.NewClient(*serverAddr, opts...)
	if err != nil {
		zlog.Fatal().Err(err).Str("server_address", *serverAddr).Msg("Failed to connect")
	}
	defer func() {
		closeErr := conn.Close()
		if closeErr != nil {
			zlog.Error().
				Err(closeErr).
				Msg("Failed to close gRPC connection")
		} else {
			zlog.Debug().
				Msg("gRPC connection closed.")
		}
	}()
	zlog.Info().Msg("Successfully connected to server")

	// --- Create Client Service ---
	// Create the raw gRPC client stub
	rawClient := pb.NewFileServiceClient(conn) // pb needs to be imported in internal/client now
	// Wrap it in our internal client structure (optional, but good practice)
	serviceClient := client.NewFileServiceClient(rawClient)

	// --- Perform Action ---
	// Use a single context for the chosen operation
	// Consider making timeout configurable
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	var operationErr error
	logger := zlog.Logger

	if *doList {
		operationErr = serviceClient.ListFiles(ctx, logger)
	} else if *uploadFile != "" {
		operationErr = serviceClient.UploadFile(ctx, *uploadFile, logger)
	} else if *downloadFile != "" {
		operationErr = serviceClient.DownloadFile(ctx, *downloadFile, *outputDir, logger)
	}

	if operationErr != nil {
		zlog.Fatal().Err(operationErr).Msg("Client operation failed")
	}

	zlog.Info().Msg("Client operation completed successfully.")
}
