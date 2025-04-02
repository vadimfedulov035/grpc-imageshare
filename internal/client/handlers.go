package client

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/vadimfedulov035/grpc-imageshare/internal/generic"
	pb "github.com/vadimfedulov035/grpc-imageshare/pkg/proto"

	"github.com/rs/zerolog"
	"google.golang.org/protobuf/types/known/emptypb"
)

const ChunkSize = generic.ChunkSize

// --- Adapters ---

type clientUploadSenderAdapter struct {
	stream pb.FileService_UploadFileClient
}

func (a *clientUploadSenderAdapter) Send(msg *pb.UploadFileRequest) error {
	return a.stream.Send(msg)
}

type clientDownloadReceiverAdapter struct {
	stream pb.FileService_DownloadFileClient
}

func (a *clientDownloadReceiverAdapter) Recv() (*pb.DownloadFileResponse, error) {
	return a.stream.Recv()
}

// --- Functions ---

// Create local file for download request
func createDownloadFile(
	filePath string, logger zerolog.Logger,
) (*os.File, zerolog.Logger, error) {
	// Log function and action
	logger = logger.With().Str("function", "createDownloadFile").Logger()
	logger.Debug().Msg("Creating local file for download destination")

	// Create local file
	file, err := generic.CreateLocalFileTrunc(filePath, logger)
	if err != nil {
		msg := "Failed to create local file for download request"
		logger.Error().
			Err(err).
			Msg(msg)
		return nil, logger, fmt.Errorf("%s: %s (%w)", msg, filePath, err)
	}

	// Add file path to context
	logger = logger.With().
		Str("file_path", filePath).
		Logger()

	logger.Debug().Msg("Created local file for download request")
	return file, logger, nil
}

// Open local file for upload request
func openUploadFile(
	filePath string, logger zerolog.Logger,
) (*os.File, zerolog.Logger, error) {
	// Log function and action
	logger = logger.With().Str("function", "openUploadFile").Logger()
	logger.Debug().Msg("Opening local file for upload request")

	// Open local file
	file, err := generic.OpenLocalFile(filePath, logger)
	if err != nil {
		msg := "Failed to open local file for upload request"
		logger.Error().
			Err(err).
			Msg(msg)
		return nil, logger, fmt.Errorf("%s: %s (%w)", msg, filePath, err)
	}

	// Add file path to context
	logger = logger.With().
		Str("file_path", filePath).
		Logger()

	logger.Debug().Msg("Opened local file for upload request")
	return file, logger, nil
}

// Send upload request filename
func sendUploadFilename(
	stream pb.FileService_UploadFileClient,
	filePath string,
	logger zerolog.Logger,
) (string, zerolog.Logger, error) {
	// Log function and action
	logger = logger.With().Str("function", "sendUploadFilename").Logger()
	logger.Debug().Msg("Sending upload request filename")

	// Get filename from path
	filename := filepath.Base(filePath)

	// Prepare and send filename message over the stream
	reqFilename := &pb.UploadFileRequest{
		Data: &pb.UploadFileRequest_Filename{Filename: filename},
	}
	err := stream.Send(reqFilename)
	if err != nil {
		_, closeErr := stream.CloseAndRecv()
		msg := "Failed to send filename"
		logger.Error().
			Err(err).
			AnErr("client_error_on_close", closeErr).
			Msg(msg)
		err = fmt.Errorf(
			"%s: %s (%w) (client status on close: %v)",
			msg, filename, err, closeErr,
		)
		return filename, logger, err
	}

	logger.Debug().Msg("Sent upload request filename")
	return filename, logger, nil
}

// Receive and write uploaded file chunks over the stream
func receiveDownloadFileChunks(
	stream pb.FileService_DownloadFileClient,
	writer io.Writer,
	filename string,
	logger zerolog.Logger,
) (uint64, zerolog.Logger, error) {
	// Log function and action
	logger = logger.With().Str("function", "receiveDownloadFileChunks").Logger()
	logger.Debug().Msg("Receiving chunks from server and writing to local file")

	// Bytes counter
	var bytesWritten uint64

	// Receiver adapter
	receiver := &clientDownloadReceiverAdapter{stream: stream}
	// Bytes extractor for DownloadFileRequest
	fromT := func(msg *pb.DownloadFileResponse) []byte {
		return msg.GetChunkData()
	}

	logger.Debug().
		Int("chunk_size", ChunkSize).
		Msg("Starting chunk receiving loop")
	for {
		// Receive and write file chunk
		n, isEOF, err := generic.ReceiveChunk[*pb.DownloadFileResponse](
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
				err = fmt.Errorf("%s: %w", filename, err)
			} else { // fs.ErrInvalid (got nil chunk) goes here as internal
				err = fmt.Errorf(
					"Failed processing upload stream for %s: %w", filename, err,
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

	// Add written bytes to context
	logger = logger.With().
		Uint64("bytes_written", bytesWritten).
		Logger()

	logger.Debug().Msg("Received and written all file chunks")
	return bytesWritten, logger, nil
}

// Read and send downloaded file chunks over the stream
func sendUploadFileChunks(
	stream pb.FileService_UploadFileClient,
	reader io.Reader,
	logger zerolog.Logger,
) (uint64, zerolog.Logger, error) {
	// Log function and action
	logger = logger.With().Str("function", "sendUploadFileChunks").Logger()
	logger.Debug().Msg("Reading and sending upload file chunks")

	// Sent bytes counter
	var bytesSent uint64

	// Buffer to read into
	buffer := make([]byte, ChunkSize)
	// Sender adapter
	sender := &clientUploadSenderAdapter{stream: stream}
	// UploadFileRequest constructor from bytes
	newT := func(chunk []byte) *pb.UploadFileRequest {
		return &pb.UploadFileRequest{
			Data: &pb.UploadFileRequest_ChunkData{
				ChunkData: chunk,
			},
		}
	}

	logger.Debug().
		Int("chunk_size", ChunkSize).
		Msg("Starting chunk sending loop")
	for {
		// Read and send file chunk
		n, isEOF, err := generic.SendChunk(
			sender, newT, reader, buffer, logger,
		)
		bytesSent += uint64(n)
		// Finish
		if isEOF {
			break
		}
		// Handle errors
		if err != nil {
			logger.Error().Err(err).
				Uint64("bytes_sent", bytesSent).
				Msg("Error reading/sending file chunk")
			return bytesSent, logger, err
		}
		// Log progress
		logger.Debug().
			Int("bytes_sent_in_chunk", n).
			Uint64("bytes_sent", bytesSent).
			Msg("Sent upload chunk")
	}

	// Add sent bytes to context
	logger = logger.With().
		Uint64("bytes_sent", bytesSent).
		Logger()

	logger.Debug().Msg("Read and sent all file chunks")
	return bytesSent, logger, nil
}

// Receive upload confirmation response from server
func receiveUploadResponse(
	stream pb.FileService_UploadFileClient,
	filename string,
	size uint64,
	logger zerolog.Logger,
) error {
	// Log function and action
	logger = logger.With().Str("function", "receiveUploadResponse").Logger()
	logger.Debug().Msg("Receiving upload confirmation response")

	// Close stream and receive response
	res, err := stream.CloseAndRecv()
	if err != nil {
		msg := "Failed to receive upload confirmation response from server"
		logger.Error().
			Err(err).
			Msg(msg)
		return fmt.Errorf("%s: %w", msg, err)
	}

	// Handle short write
	if size != res.Size {
		err = io.ErrShortWrite
		msg := "Short write when writing chunk"
		logger.Error().
			Uint64("bytes_written", res.Size).
			Msg(msg)
		return fmt.Errorf("%s (%d vs %d bytes): %w", msg, size, res.Size, err)
	}

	// Add written bytes and server message to context
	logger = logger.With().
		Uint64("bytes_written", res.Size).
		Str("server_message", res.Message).
		Logger()

	logger.Debug().Msg("Received upload confirmation response")
	return nil
}

// List server files from server
func listServerFiles(client pb.FileServiceClient, logger zerolog.Logger) error {
	// Log function and action
	logger = logger.With().Str("function", "listServerFiles").Logger()
	logger.Debug().Msg("Listing files on server")

	// Receive response
	res, err := client.ListFiles(context.Background(), &emptypb.Empty{})
	if err != nil {
		msg := "Server call failed"
		logger.Error().Err(err).Msg(msg)
		return fmt.Errorf("%s: %w", msg, err)
	}

	// Handle zero files
	if len(res.Files) == 0 {
		logger.Info().Msg("No files found on server")
		return nil
	}

	// List every file from response
	logger.Info().Msg("------------------- Server Files --------------------")
	logger.Info().Msgf("%-30s | %-25s | %-25s", "Filename", "Created (BirthTime)", "Updated (ModTime)")
	logger.Info().Msg("-----------------------------------------------------")
	for _, fileInfo := range res.Files {
		createdAtStr := fileInfo.CreatedAt.AsTime().Local().Format(time.RFC3339)
		updatedAtStr := fileInfo.UpdatedAt.AsTime().Local().Format(time.RFC3339)
		logger.Info().Msgf("%-30s | %-25s | %-25s", fileInfo.Filename, createdAtStr, updatedAtStr)
	}
	logger.Info().Msg("-----------------------------------------------------")

	// Add file count to context
	logger = logger.With().
		Int("file_count", len(res.Files)).
		Logger()

	logger.Debug().Msg("Listed files on server")
	return nil
}
