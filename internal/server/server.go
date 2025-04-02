package server

import (
	"context"
	"fmt"
	"os"

	"github.com/vadimfedulov035/grpc-imageshare/internal/generic"

	pb "github.com/vadimfedulov035/grpc-imageshare/pkg/proto"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/rs/zerolog"
	zlog "github.com/rs/zerolog/log"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	MaxLoadProcs = 10
	MaxListProcs = 100
	ChunkSize    = 64 * 1024 // 64 KB chunks
)

// Implement the proto.FileServiceServer interface
type fileServer struct {
	pb.UnimplementedFileServiceServer
	StoragePath   string
	LoadSemaphore chan struct{}
	ListSemaphore chan struct{}
}

// Create new instance of the file server
func NewFileServer(storagePath string) (*fileServer, error) {
	logger := zlog.With().
		Str("storage_path", storagePath).
		Logger()

	if err := os.MkdirAll(storagePath, 0755); err != nil {
		msg := "Failed to create storage directory"
		logger.Error().
			Err(err).
			Msg(msg)
		return nil, fmt.Errorf("%s: %w", msg, err)
	}
	logger.Info().
		Msg("Using storage directory")

	return &fileServer{
		StoragePath:   storagePath,
		LoadSemaphore: make(chan struct{}, MaxLoadProcs),
		ListSemaphore: make(chan struct{}, MaxListProcs),
	}, nil
}

// Wait for semaphore to acquire
func acquire(ctx context.Context, sem chan struct{}, logger zerolog.Logger) bool {
	select {
	case sem <- struct{}{}:
		return true
	case <-ctx.Done():
		logger.Warn().
			Err(ctx.Err()).
			Msg("Context cancelled while waiting for semaphore")
		return false
	}
}

func release(sem chan struct{}) {
	<-sem
}

// Upload file to the server
func (s *fileServer) UploadFile(stream pb.FileService_UploadFileServer) (err error) {
	// Log method and action info
	logger := zlog.With().Str("method", "UploadFile").Logger()
	logger.Info().Msg("Performing upload request")

	// 0. Wait for its turn
	ctx := stream.Context()
	if !acquire(ctx, s.LoadSemaphore, logger) {
		msg := "Upload concurrency limit reached"
		logger.Warn().
			Msg(msg)
		return status.Errorf(codes.ResourceExhausted, "%s, try again later", msg)
	}
	defer release(s.LoadSemaphore)

	// 1. Get filename
	filename, logger, err := getUploadFilename(stream, logger)
	if err != nil {
		return err
	}

	// 2. Create file
	file, logger, err := createUploadFile(s.StoragePath, filename, logger)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			generic.CleanUp(file, logger)
		}
	}()

	// 3. Receive and Write Chunks
	totalSize, logger, err := receiveUploadFileChunks(ctx, stream, file, filename, logger)
	if err != nil {
		return err
	}

	// ~ Finalize upload (Sync + Close)
	err = finalizeUpload(file, filename, logger)
	if err != nil {
		return err
	}

	// 4. Send final response
	sendUploadResponse(stream, filename, totalSize, logger)

	logger.Info().Msg("Upload request completed successfully")
	return nil
}

// Download file from the server
func (s *fileServer) DownloadFile(
	req *pb.DownloadFileRequest, stream pb.FileService_DownloadFileServer,
) (err error) {
	// Log method and action info
	logger := zlog.With().Str("method", "DownloadFile").Logger()
	logger.Info().Msg("Performing download request")

	// Wait for released semaphore if all acquired
	ctx := stream.Context()
	if !acquire(ctx, s.LoadSemaphore, logger) {
		msg := "Download concurrency limit reached"
		logger.Warn().
			Msg(msg)
		return status.Errorf(codes.ResourceExhausted, "%s, try again later", msg)
	}
	defer release(s.LoadSemaphore)

	// 1. Get filename
	filename, logger, err := getDownloadFilename(req, logger)
	if err != nil {
		return err
	}

	// 2. Open file
	file, logger, err := openDownloadFile(s.StoragePath, filename, logger)
	if err != nil {
		return err
	}
	defer file.Close()

	// 3. Read and Send file chunks
	_, logger, err = sendDownloadFileChunks(ctx, stream, file, filename, logger)
	if err != nil {
		return err
	}

	logger.Info().Msg("Download request performed successfully")
	return nil
}

// List files on the server
func (s *fileServer) ListFiles(
	ctx context.Context, req *emptypb.Empty,
) (*pb.ListFilesResponse, error) {
	// Log method and action info
	logger := zlog.With().Str("method", "ListFiles").Logger()
	logger.Info().Msg("Performing listing request")

	// Acquire semaphore
	if !acquire(ctx, s.ListSemaphore, logger) {
		msg := "List concurrency limit reached"
		logger.Warn().
			Msg(msg)
		err := status.Errorf(codes.ResourceExhausted, "%s, try again later", msg)
		return nil, err
	}
	defer release(s.ListSemaphore)

	// List server files
	res, err := listStorageFiles(ctx, s.StoragePath, logger)
	if err != nil {
		return nil, fmt.Errorf("Listing files failed: %w", err)
	}

	logger.Info().Msg("List request completed successfully")
	return res, nil
}
