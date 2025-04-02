package generic

import (
	"errors"
	"fmt"
	"io/fs"
	"os"

	"github.com/rs/zerolog"
)

// Deferred cleaner
func CleanUp(file *os.File, logger zerolog.Logger) {
	// Log function and action
	logger = logger.With().Str("generic_function", "cleanUp").Logger()
	logger.Debug().Msg("Cleaning up partial file")

	// Close file
	if err := file.Close(); err != nil {
		msg := "Failed to close partial file"
		logger.Error().
			Err(err).
			Msg(msg)
	}
	// Remove file
	if err := os.Remove(file.Name()); err != nil {
		msg := "Failed to remove partial file"
		logger.Error().
			Err(err).
			Msg(msg)
	}

	logger.Debug().Msg("Closed and removed partial file")
}

// Create local file in truncate mode
func CreateLocalFileTrunc(filePath string, logger zerolog.Logger) (*os.File, error) {
	// Log generic function and action
	logger = logger.With().Str("generic_function", "createLocalFileTrunc").Logger()
	logger.Debug().Msg("Creating local file for writing")

	// Open file: Write-only, Create if doesn't exist, Truncate if exists
	file, err := os.OpenFile(filePath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		// Permission denied
		if errors.Is(err, os.ErrPermission) {
			msg := "Permission denied creating file"
			return nil, fmt.Errorf("%s (%w)", msg, err)
		}
		// Failed to create
		msg := "Failed to open file"
		return nil, fmt.Errorf("%s (%w)", msg, err)
	}

	logger.Debug().Msg("Created local file for writing")
	return file, nil
}

// Open local file
func OpenLocalFile(filePath string, logger zerolog.Logger) (*os.File, error) {
	// Log generic function and action
	logger = logger.With().Str("generic_function", "openLocalFile").Logger()
	logger.Debug().Msg("Opening local file for reading")

	logger.Info().
		Str("path_to_open", filePath).
		Msg("Opening file with path")

	// Open file
	file, err := os.Open(filePath)
	if err != nil {
		// Not found
		if errors.Is(err, os.ErrNotExist) {
			msg := "File not found"
			return nil, fmt.Errorf("%s (%w)", msg, err)
		}
		// Permission denied
		if errors.Is(err, os.ErrPermission) {
			msg := "Permission denied opening file"
			return nil, fmt.Errorf("%s (%w)", msg, err)
		}
		// Failed to open
		msg := "Failed to open file"
		return nil, fmt.Errorf("%s (%w)", msg, err)
	}

	// Get file info
	fileInfo, err := file.Stat()
	if err != nil {
		file.Close()
		msg := "Failed to get file info after opening"
		return nil, fmt.Errorf("%s (%w)", msg, err)
	}

	// Check if requested download file is actually a directory
	if fileInfo.IsDir() {
		file.Close()
		msg := "Not a file"
		return nil, fmt.Errorf("%s (%w)", msg, fs.ErrInvalid)
	}

	logger.Debug().Msg("Opened local file for reading")
	return file, nil
}
