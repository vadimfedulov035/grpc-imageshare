package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	pb "github.com/vadimfedulov035/grpc-imageshare/pkg/proto"

	"github.com/vadimfedulov035/grpc-imageshare/internal/generic"

	"github.com/djherbis/times"
	zerolog "github.com/rs/zerolog"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"google.golang.org/protobuf/types/known/timestamppb"
)

// --- Adapters ---

type ServerDownloadSenderAdapter struct {
	stream pb.FileService_DownloadFileServer
}

func (a *ServerDownloadSenderAdapter) Send(msg *pb.DownloadFileResponse) error {
	return a.stream.Send(msg)
}

type ServerUploadReceiverAdapter struct {
	stream pb.FileService_UploadFileServer
}

func (a *ServerUploadReceiverAdapter) Recv() (*pb.UploadFileRequest, error) {
	req, err := a.stream.Recv()
	if err != nil {
		return nil, err
	}
	return req, nil
}

// --- Functions ---

// Validate filename to prevent path traversal
func validateFilename(filename string, logger zerolog.Logger) error {
	baseFilename := filepath.Base(filename)

	isValid := baseFilename != "." && baseFilename != ".." && baseFilename != "/"
	isDirect := baseFilename == filename
	if !isValid || !isDirect {
		msg := "Got file path instead of filename"
		logger.Error().
			Str("filename_as_path", filename).
			Str("filename", baseFilename).
			Msg(msg)
		return status.Errorf(codes.InvalidArgument, "%s: %s", msg, filename)
	}
	return nil
}

// Get download request filename and validate
func getDownloadFilename(
	req *pb.DownloadFileRequest, logger zerolog.Logger,
) (string, zerolog.Logger, error) {
	// Log function and action
	logger = logger.With().Str("function", "getDownloadFilename").Logger()
	logger.Debug().Msg("Getting download request filename")

	// Validate request
	if req == nil {
		msg := "Received nil download request"
		logger.Error().
			Msg(msg)
		return "", logger, status.Error(codes.InvalidArgument, msg)
	}

	// Get filename from request
	filename := req.GetFilename()
	if filename == "" {
		// Got empty filename
		msg := "Filename cannot be empty"
		logger.Error().
			Msg(msg)
		return "", logger, status.Error(codes.InvalidArgument, msg)
	}

	// Validate filename to prevent path traversal
	err := validateFilename(filename, logger)
	if err != nil {
		return "", logger, err
	}

	// Add filename to context
	logger = logger.With().
		Str("filename", filename).
		Logger()

	logger.Debug().Msg("Got download request filename")
	return filename, logger, nil
}

// Get upload request filename and validate
func getUploadFilename(
	stream pb.FileService_UploadFileServer, logger zerolog.Logger,
) (string, zerolog.Logger, error) {
	// Log function and action
	logger = logger.With().Str("function", "getUploadFilename").Logger()
	logger.Debug().Msg("Getting upload request filename")

	// Get request
	req, err := stream.Recv()
	if err != nil {
		msg := "Failed to receive initial upload request"
		logger.Error().
			Err(err).
			Msg(msg)
		return "", logger, status.Errorf(codes.InvalidArgument, msg)
	}

	// Get filename from request
	filename := req.GetFilename()
	if filename == "" {
		// Got empty filename but non-empty chunk data
		if req.GetChunkData() != nil {
			msg := "Expected filename in the first message, got chunk data"
			logger.Error().
				Err(fs.ErrInvalid).
				Msg(msg)
			return "", logger, status.Error(codes.InvalidArgument, msg)
		}
		// Got empty filename
		msg := "Filename cannot be empty"
		logger.Error().
			Err(fs.ErrInvalid).
			Msg(msg)
		return "", logger, status.Error(codes.InvalidArgument, msg)
	}

	// Validate filename to prevent path traversal
	err = validateFilename(filename, logger)
	if err != nil {
		return "", logger, err
	}

	// Add filename to context
	logger = logger.With().
		Str("filename", filename).
		Logger()

	logger.Debug().Msg("Got upload request filename")
	return filename, logger, nil
}

// Open local file for download request
func openDownloadFile(
	path, filename string, logger zerolog.Logger,
) (*os.File, zerolog.Logger, error) {
	// Log function and action
	logger = logger.With().Str("function", "openDownloadFile").Logger()
	logger.Debug().Msg("Opening file for download request")

	// Make file path
	filePath := filepath.Join(path, filename)

	// Open file
	file, err := generic.OpenLocalFile(filePath, logger)
	if err != nil {
		logger.Error().
			Err(err).
			Str("filePath", filePath).
			Msg("Failed to open local file for download request")

		// Translate standard Go errors to gRPC status codes
		switch {
		case errors.Is(err, os.ErrNotExist):
			msg := "File not found on server"
			err = status.Errorf(codes.NotFound, "%s: %s", msg, filename)
			return nil, logger, err
		case errors.Is(err, os.ErrPermission):
			msg := "Permission denied accessing file"
			err = status.Errorf(codes.PermissionDenied, "%s: %s", msg, filename)
			return nil, logger, err
		case errors.Is(err, fs.ErrInvalid):
			msg := "Requested path is not a regular file"
			err = status.Errorf(codes.InvalidArgument, "%s: %s", msg, filename)
			return nil, logger, err
		default:
			msg := "Internal server error accessing file"
			err = status.Errorf(codes.Internal, "%s: %s (%v)", msg, filename, err)
			return nil, logger, err
		}
	}

	logger.Debug().Msg("Opened file for download request")
	return file, logger, nil
}

// Create local file for upload request
func createUploadFile(
	path, filename string, logger zerolog.Logger,
) (*os.File, zerolog.Logger, error) {
	// Log function and action
	logger = logger.With().Str("function", "createUploadFile").Logger()
	logger.Debug().Msg("Creating local file for upload request")

	// Make file path
	filePath := filepath.Join(path, filename)

	// Open local file
	file, err := generic.CreateLocalFileTrunc(filePath, logger)
	if err != nil {
		logger.Error().
			Err(err).
			Msg("Failed to create local file")

		// Translate standard Go errors to gRPC status codes
		switch {
		case errors.Is(err, os.ErrPermission):
			msg := "Permission denied creating/accessing file"
			return nil, logger, status.Errorf(codes.PermissionDenied, "%s: %s", msg, filename)
		default:
			msg := "Internal server error creating/accessing file"
			return nil, logger, status.Errorf(codes.Internal, "%s: %s (%v)", msg, filename, err)
		}
	}

	logger.Debug().Msg("Created local file for upload request")
	return file, logger, nil
}

// Read and send downloaded file chunks over the stream
func sendDownloadFileChunks(
	ctx context.Context,
	stream pb.FileService_DownloadFileServer,
	reader io.Reader,
	filename string,
	logger zerolog.Logger,
) (uint64, zerolog.Logger, error) {
	logger = logger.With().Str("function", "sendDownloadFileChunks").Logger()
	logger.Debug().Msg("Reading and sending download file chunks")

	// Sent bytes counter
	var bytesSent uint64

	// Check for context cancellation *before* blocking on read/send
	select {
	case <-ctx.Done():
		msg := "Download cancelled by client before streaming"
		logger.Warn().
			Err(ctx.Err()).
			Msg(msg)
		return bytesSent, logger, status.Errorf(
			codes.Canceled, "%s: %s", msg, filename,
		)
	default:
		// Context not cancelled, proceed
	}

	// Buffer to read into
	buffer := make([]byte, ChunkSize)
	// Sender adapter
	sender := &ServerDownloadSenderAdapter{stream: stream}
	// DownloadFileResponse constructor from bytes
	newT := func(chunk []byte) *pb.DownloadFileResponse {
		return &pb.DownloadFileResponse{ChunkData: chunk}
	}

	logger.Debug().
		Int("chunk_size", ChunkSize).
		Msg("Starting chunk sending loop")
	for {
		// Read and send file chunk
		n, isEOF, err := generic.SendChunk[*pb.DownloadFileResponse](
			sender, newT, reader, buffer, logger,
		)
		bytesSent += uint64(n)
		// Finish
		if isEOF {
			break
		}
		// Handle error
		if err != nil {
			err = status.Errorf(codes.Internal,
				"Failed processind download stream for %s: %v", filename, err,
			)
			logger.Error().
				Err(err).
				Uint64("bytes_sent", bytesSent).
				Msg("Error reading/sending file chunk")
			return bytesSent, logger, err
		}
		// Log progress
		logger.Debug().
			Int("bytes_sent_in_chunk", n).
			Uint64("bytes_sent", bytesSent).
			Msg("Read and sent file chunk")
	}

	// Add sent bytes to context
	logger = logger.With().
		Uint64("bytes_sent", bytesSent).
		Logger()

	logger.Debug().Msg("Read and sent all file chunks")
	return bytesSent, logger, nil
}

// Receive and write uploaded file chunks over the stream
func receiveUploadFileChunks(
	ctx context.Context,
	stream pb.FileService_UploadFileServer,
	writer io.Writer,
	filename string,
	logger zerolog.Logger,
) (uint64, zerolog.Logger, error) {
	logger = logger.With().Str("function", "receiveUploadFileChunks").Logger()
	logger.Debug().Msg("Receiving and writing file chunks")

	// Bytes counter
	var bytesWritten uint64

	// Check for context cancellation *before* blocking on read/send
	select {
	case <-ctx.Done():
		msg := "Upload cancelled by client during transfer"
		logger.Warn().
			Err(ctx.Err()).
			Msg(msg)
		finErr := status.Errorf(codes.Canceled, "%s: %s", msg, filename)
		return bytesWritten, logger, finErr
	default:
		// Context not cancelled, proceed
	}

	// Receiver adapter
	receiver := &ServerUploadReceiverAdapter{stream: stream}
	// Bytes extractor for UploadFileRequest
	fromT := func(msg *pb.UploadFileRequest) []byte {
		return msg.GetChunkData()
	}

	logger.Debug().
		Int("chunk_size", ChunkSize).
		Msg("Starting chunk receiving loop")
	for {
		// Receive and write file chunk
		n, isEOF, err := generic.ReceiveChunk[*pb.UploadFileRequest](
			receiver, fromT, writer, logger,
		)
		bytesWritten += uint64(n)
		// Finish
		if isEOF {
			break
		}
		// Handle error
		if err != nil {
			// n != len(chunk) -> io.ErrShortWrite
			if errors.Is(err, io.ErrShortWrite) {
				err = status.Errorf(codes.DataLoss, "%s: %v", filename, err)
			} else { // fs.ErrInvalid (got nil chunk) goes here as internal
				err = status.Errorf(codes.Internal,
					"Failed processing upload stream for %s: %v", filename, err,
				)
			}
			logger.Error().
				Err(err).
				Uint64("bytes_written", bytesWritten).
				Msg("Error receiving/writing file chunk")
			return bytesWritten, logger, err
		}
		// Log progress
		logger.Debug().
			Int("bytes_written_in_chunk", n).
			Uint64("bytes_written", bytesWritten).
			Msg("Received and written file chunk")
	}

	// Add received bytes to context
	logger = logger.With().
		Uint64("bytes_written", bytesWritten).
		Logger()

	logger.Debug().Msg("Received and written all file chunks")
	return bytesWritten, logger, nil
}

// Sync and close local file for upload request
func finalizeUpload(file *os.File, filename string, logger zerolog.Logger) error {
	// Log function and action
	logger = logger.With().Str("function", "finalizeUpload").Logger()
	logger.Debug().Msg("Syncing and closing local file for upload request")

	// Sync
	logger.Debug().
		Msg("Syncing file to disk")
	if err := file.Sync(); err != nil {
		msg := "Failed to sync file contents to disk"
		logger.Error().
			Err(err).
			Msg(msg)
		return status.Errorf(codes.Internal, "%s: %s", msg, filename)
	}

	// Close file explicitly
	logger.Debug().
		Msg("Closing file after sync")
	if err := file.Close(); err != nil {
		msg := "Failed to close file after successful write"
		logger.Warn().
			Err(err).
			Msg(msg)
		return status.Errorf(codes.Internal, "%s: %s", msg, filename)
	}

	logger.Debug().Msg("Synced and closed uploaded file")
	return nil
}

// Send upload confirmation response to client
func sendUploadResponse(
	stream pb.FileService_UploadFileServer,
	filename string,
	size uint64,
	logger zerolog.Logger,
) {
	// Log function and action
	logger = logger.With().Str("function", "sendUploadResponse").Logger()
	logger.Debug().Msg("Sending upload confirmation response")

	// Construct upload file response
	res := &pb.UploadFileResponse{
		Message:  fmt.Sprintf("File '%s' uploaded successfully.", filename),
		Filename: filename,
		Size:     size,
	}

	// Saved file, but failed to sent confirmation, still success
	if err := stream.SendAndClose(res); err != nil {
		msg := "Failed to send final confirmation response to client"
		logger.Warn().
			Err(err).
			Msg(msg)
	}

	logger.Debug().Msg("Sent upload confirmation response")
}

// List storage files
func listStorageFiles(ctx context.Context, path string, logger zerolog.Logger) (*pb.ListFilesResponse, error) {
	// Log function and action
	logger = logger.With().Str("function", "listStorageFiles").Logger()
	logger.Debug().Msg("Reading storage directory listing")

	// Add directory to context
	logger = logger.With().
		Str("directory", path).
		Logger()

	// Read directory
	dirEntries, err := os.ReadDir(path)
	if err != nil {
		msg := "Failed to read storage directory listing"
		logger.Error().
			Err(err).
			Msg(msg)
		return nil, status.Errorf(codes.Internal, "%s: %v", msg, err)
	}

	// Prepare response
	fileMetadataList := make([]*pb.FileMetadata, 0, len(dirEntries))
	filesFound := 0

	// Check for context cancellation *before* blocking on read/send
	select {
	case <-ctx.Done():
		msg := "Listing cancelled by client before iterating"
		logger.Warn().
			Err(ctx.Err()).
			Msg(msg)
		return nil, status.Errorf(codes.Canceled, "%s", msg)
	default:
		// Context not cancelled, proceed
	}

	logger.Debug().Msg("Iterating through directory entries")
	for _, entry := range dirEntries {
		// Add entry name to context
		logger = logger.With().
			Str("entry_name", entry.Name()).
			Logger()

		// If entry is directory
		if entry.IsDir() {
			logger.Debug().
				Msg("Directory entry, skipping")
			continue
		}

		// Get and add entry path to context
		entryPath := filepath.Join(path, entry.Name())
		logger = logger.With().
			Str("entry_path", entryPath).
			Logger()

		// os.Stat for size
		info, err := os.Stat(entryPath)
		// Handle error
		if err != nil {
			logger.Error().
				Err(err).
				Msg("Could not os.Stat file entry, skipping")
			continue
		}
		fileSize := uint64(info.Size())

		// times.Stat for ModTime and BirthTime
		tstat, err := times.Stat(entryPath)
		// Handle error
		if err != nil {
			logger.Error().
				Err(err).
				Str("entry_path", entry.Type().String()).
				Msg("Could not get extended file times, skipping")
			continue
		}
		// Get modification time
		modTime := tstat.ModTime()
		// Get birth time if available
		var birthTime time.Time
		if tstat.HasBirthTime() {
			birthTime = tstat.BirthTime()
		} else { // Or fall back to modification time
			birthTime = modTime
			msg := "Birth time not available, falling back to ModTime for CreatedAt"
			logger.Debug().
				Str("filename", entry.Name()).
				Msg(msg)
		}

		createdAtProto := timestamppb.New(birthTime)
		updatedAtProto := timestamppb.New(modTime)

		metadata := &pb.FileMetadata{
			Filename:  entry.Name(),
			CreatedAt: createdAtProto,
			UpdatedAt: updatedAtProto,
			Size:      fileSize,
		}
		fileMetadataList = append(fileMetadataList, metadata)
		filesFound++

		// Log progress
		logger.Debug().
			Str("filename", metadata.Filename).
			Msg("Added file metadata to list")
	}

	// Add found files to context
	logger = logger.With().
		Int("files_found", filesFound).
		Logger()

	logger.Debug().Msg("Listed all files")
	return &pb.ListFilesResponse{Files: fileMetadataList}, nil
}
