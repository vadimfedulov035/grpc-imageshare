package client

import (
	"context"
	"fmt"

	"github.com/rs/zerolog"

	"github.com/vadimfedulov035/grpc-imageshare/internal/generic"
	pb "github.com/vadimfedulov035/grpc-imageshare/pkg/proto"
)

type FileServiceClient struct {
	grpcClient pb.FileServiceClient
}

func NewFileServiceClient(client pb.FileServiceClient) *FileServiceClient {
	return &FileServiceClient{grpcClient: client}
}

// Start upload stream (logger is not returned to preserve higher method later)
func (c *FileServiceClient) startUploadStream(
	ctx context.Context, logger zerolog.Logger,
) (pb.FileService_UploadFileClient, error) {
	// Log method and action info
	logger = logger.With().Str("method", "startUploadStream").Logger()
	logger.Info().Msg("Starting upload stream")

	// Create and send request
	stream, err := c.grpcClient.UploadFile(ctx)
	if err != nil {
		msg := "Failed to start upload stream gRPC call"
		logger.Error().
			Err(err).
			Msg(msg)
		return nil, fmt.Errorf("%s (%w)", msg, err)
	}

	logger.Debug().Msg("Started upload stream")
	return stream, nil
}

// Start download stream (logger is not returned to preserve higher method later)
func (c *FileServiceClient) startDownloadStream(
	ctx context.Context, filename string, logger zerolog.Logger,
) (pb.FileService_DownloadFileClient, error) {
	// Log method and action info
	logger = logger.With().Str("method", "startDownloadStream").Logger()
	logger.Info().Msg("Starting download stream")

	// Create and send request
	req := &pb.DownloadFileRequest{Filename: filename}
	stream, err := c.grpcClient.DownloadFile(ctx, req)
	if err != nil {
		msg := "Failed to start download stream gRPC call"
		logger.Error().Err(err).Msg(msg)
		return nil, fmt.Errorf("%s for %s: %w", msg, filename, err)
	}

	logger.Debug().Msg("Started download stream")
	return stream, nil
}

// Upload file to the server
func (c *FileServiceClient) UploadFile(
	ctx context.Context, filePath string, logger zerolog.Logger,
) (err error) {
	// Log method and action info
	logger = logger.With().Str("method", "UploadFile").Logger()
	logger.Info().Msg("Performing upload request")

	// 0. Start upload stream
	stream, err := c.startUploadStream(ctx, logger)
	if err != nil {
		return err
	}

	// 1. Send filename
	filename, logger, err := sendUploadFilename(stream, filePath, logger)
	if err != nil {
		return err
	}

	// 2. Open file
	file, logger, err := openUploadFile(filePath, logger)
	if err != nil {
		return err
	}
	defer file.Close()

	// 3. Read and Send file chunks
	n, logger, err := sendUploadFileChunks(stream, file, logger)
	if err != nil {
		return err
	}

	// 4. Receive upload response confirmation
	err = receiveUploadResponse(stream, filename, n, logger)
	if err != nil {
		return err
	}

	logger.Info().Msg("Upload request completed successfully")
	return nil
}

// Download file from the server
func (c *FileServiceClient) DownloadFile(
	ctx context.Context, filename, filePath string, logger zerolog.Logger,
) (err error) {
	// Log method and action info
	logger = logger.With().Str("method", "DownloadFile").Logger()
	logger.Info().Msg("Performing download request")

	// 0. Start download stream
	stream, err := c.startDownloadStream(ctx, filename, logger)
	if err != nil {
		return err
	}

	// 1. Create file
	file, logger, err := createDownloadFile(filePath, logger)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			generic.CleanUp(file, logger)
		}
	}()

	// 2. Get and Write file chunks
	_, logger, err = receiveDownloadFileChunks(stream, file, filename, logger)
	if err != nil {
		return err
	}

	logger.Info().Msg("Download request performed successfully")
	return nil
}

// List files on the server
func (c *FileServiceClient) ListFiles(
	ctx context.Context, logger zerolog.Logger,
) error {
	// Log method and action info
	logger = logger.With().Str("method", "ListFiles").Logger()
	logger.Info().Msg("Performing list request")

	// Call the handler function
	err := listServerFiles(c.grpcClient, logger)
	if err != nil {
		return fmt.Errorf("Listing files failed: %w", err)
	}

	logger.Info().Msg("List request completed successfully")
	return nil
}
