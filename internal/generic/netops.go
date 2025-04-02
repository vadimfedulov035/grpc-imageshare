package generic

import (
	"errors"
	"fmt"
	"io"
	"io/fs"

	"github.com/rs/zerolog"
)

const ChunkSize = 64 * 1024 // 64 Kb

// Generic Sender for `T` message
type GenericChunkSender[T any] interface {
	Send(T) error
}

// Generic Receiver for `T` message
type GenericChunkReceiver[T any] interface {
	Recv() (T, error)
}

// Read chunk bytes, construct and send `T` message
func SendChunk[T any](
	sender GenericChunkSender[T],
	newT func(chunk []byte) T,
	reader io.Reader,
	buffer []byte,
	logger zerolog.Logger,
) (n int, isEOF bool, err error) {
	// Log function and action
	logger = logger.With().Str("generic_function", "SendChunk").Logger()
	logger.Debug().Msg("Reading and sending file chunks")

	// 1. Read chunk bytes from reader
	if buffer == nil {
		err = fs.ErrInvalid
		msg := "Received nil buffer"
		logger.Error().
			Err(err).
			Msg(msg)
		return 0, false, fmt.Errorf("%s: %w", msg, err)
	}
	n, err = reader.Read(buffer)

	// If any bytes
	if n > 0 {
		// 2. Construct `T` message from chunk bytes
		messageToSend := newT(buffer[:n])

		// 3. Send `T` message over generic sender
		err = sender.Send(messageToSend)
		if err != nil {
			msg := "Failed sending chunk message"
			logger.Error().
				Err(err).
				Int("chunk_byte_size", n).
				Msg(msg)
			err = fmt.Errorf("%s: %w", msg, err)
			return
		}

		// ~ Log step
		logger.Debug().
			Int("bytes_read", n).
			Msg("Read and sent chunk")
	}

	// Finish
	if errors.Is(err, io.EOF) {
		logger.Debug().
			Msg("Stream ended")
		isEOF = true
		return
	}
	// Handle error
	if err != nil {
		msg := "Failed reading chunk from reader"
		logger.Error().
			Err(err).
			Msg(msg)
		err = fmt.Errorf("%s: %w", msg, err)
		return
	}

	return
}

// Receive chunk message `T`, extract and write its chunk bytes
func ReceiveChunk[T any](
	receiver GenericChunkReceiver[T],
	FromT func(msg T) []byte,
	writer io.Writer,
	logger zerolog.Logger,
) (bytesWritten int, isEOF bool, err error) {
	logger = logger.With().Str("generic_function", "ReceiveChunk").Logger()

	// 1. Receive chunk message `T` over generic receiver
	message, err := receiver.Recv()
	// Finish
	if errors.Is(err, io.EOF) {
		logger.Debug().
			Msg("Stream ended")
		return 0, true, nil
	}
	// Handle error
	if err != nil {
		msg := "Failed receiving message from stream"
		logger.Error().
			Err(err).
			Msg(msg)
		return 0, false, fmt.Errorf("%s: %w", msg, err)
	}

	// 2. Extract chunk bytes from `T` message
	chunk := FromT(message)
	if chunk == nil {
		err = fs.ErrInvalid
		msg := "Received nil chunk from the stream"
		logger.Error().
			Err(err).
			Msg(msg)
		return 0, false, fmt.Errorf("%s: %w", msg, err)
	}
	chunkSize := len(chunk)

	// 3. Write chunk to writer
	n, err := writer.Write(chunk)
	// Handle error
	if err != nil {
		msg := "Failed writing chunk to writer"
		logger.Error().
			Err(err).
			Int("chunk_size", chunkSize).
			Msg(msg)
		return n, false, fmt.Errorf("%s: %w", msg, err)
	}
	// Handle short write
	if n != chunkSize {
		err = io.ErrShortWrite
		msg := "Short write when writing chunk"
		logger.Error().
			Err(err).
			Int("bytes_written", n).
			Int("bytes_expected", chunkSize).
			Msg(msg)
		return n, false, fmt.Errorf(
			"%s (%d vs %d bytes): %w", msg, n, chunkSize, err,
		)
	}

	return n, false, nil
}
