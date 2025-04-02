package server

import (
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/rs/zerolog"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// --- Tests ---

func TestValidateFilename(t *testing.T) {
	nopLogger := zerolog.Nop()
	testCases := []struct {
		name      string
		filename  string
		expectErr bool
		errCode   codes.Code
	}{
		{"valid", "image.jpg", false, codes.OK},
		{"valid with underscore", "my_image_1.png", false, codes.OK},
		{"valid with dash", "test-image.jpeg", false, codes.OK},
		{"empty", "", true, codes.InvalidArgument},
		{"dot", ".", true, codes.InvalidArgument},
		{"dot dot", "..", true, codes.InvalidArgument},
		{"slash prefix", "/etc/passwd", true, codes.InvalidArgument},
		{"subdir", "subdir/file.txt", true, codes.InvalidArgument},
		{"dot dot prefix", "../file.txt", true, codes.InvalidArgument},
		{"trailing slash", "file.txt/", true, codes.InvalidArgument},
		{"middle slash", "file/name.txt", true, codes.InvalidArgument},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateFilename(tc.filename, nopLogger)
			hasErr := err != nil

			if hasErr != tc.expectErr {
				t.Errorf(
					"validateFilename(%q) error = %v, wantErr %v",
					tc.filename, err, tc.expectErr,
				)
				return
			}
			if tc.expectErr {
				st, ok := status.FromError(err)
				if !ok {
					t.Errorf(
						"validateFilename(%q) expected gRPC status error, got %T",
						tc.filename, err,
					)
					return
				}
				if st.Code() != tc.errCode {
					t.Errorf(
						"validateFilename(%q) expected error code %v, got %v",
						tc.filename, tc.errCode, st.Code(),
					)
				}
			}
		})
	}
}

func TestCreateUploadFile(t *testing.T) {
	nopLogger := zerolog.Nop()
	tempDir := t.TempDir()

	t.Run("success_new_file", func(t *testing.T) {
		filename := "test_create_success.txt"

		// Call handler passing the directory path and filename separately
		// This mirrors how server.go calls it
		file, _, err := createUploadFile(tempDir, filename, nopLogger)

		// Assertions
		require.NoError(t, err, "createUploadFile failed unexpectedly for new file")
		require.NotNil(t, file, "createUploadFile returned nil file on success")
		defer file.Close()

		// Verify file exists at the correct joined path
		expectedFilePath := filepath.Join(tempDir, filename)
		_, statErr := os.Stat(expectedFilePath)
		require.NoError(t, statErr, "os.Stat failed after createUploadFile success")
	})

	t.Run("success_overwrite_file", func(t *testing.T) {
		filename := "test_overwrite_success.txt"
		initialContent := []byte("initial content")
		overwriteContent := []byte("overwritten")
		expectedFilePath := filepath.Join(tempDir, filename)

		// Create the file first with initial content
		err := os.WriteFile(expectedFilePath, initialContent, 0644)
		require.NoError(t, err, "Failed to create file for pre-condition")

		// Call handler passing the directory path and filename separately
		file, _, err := createUploadFile(tempDir, filename, nopLogger)
		require.NoError(t, err, "createUploadFile failed unexpectedly when overwriting")
		require.NotNil(t, file, "createUploadFile returned nil file on overwrite success")

		// Write new content to simulate the upload process after truncation
		_, err = file.Write(overwriteContent)
		require.NoError(t, err, "Failed to write overwrite content")
		err = file.Close() // Close after writing
		require.NoError(t, err,
			"Failed to close file after writing overwrite content",
		)

		// Verify the file content IS the overwritten content
		finalContent, err := os.ReadFile(expectedFilePath)
		require.NoError(t, err,
			"Failed to read file after overwrite",
		)
		assert.Equal(t, overwriteContent, finalContent,
			"File content should match overwritten content",
		)

		// Verify size matches overwritten content
		stat, err := os.Stat(expectedFilePath)
		require.NoError(t, err)
		assert.Equal(t, int64(len(overwriteContent)), stat.Size(),
			"File size should match overwritten content",
		)
	})
}

func TestOpenDownloadFile(t *testing.T) {
	nopLogger := zerolog.Nop()
	tempDir := t.TempDir()

	t.Run("success", func(t *testing.T) {
		filename := "test_find_success.txt"
		filePath := filepath.Join(tempDir, filename)
		content := []byte("hello")

		// Create the file
		err := os.WriteFile(filePath, content, 0644)
		require.NoError(t, err, "Failed to create test file")

		// Call handler with directory path and filename
		file, _, err := openDownloadFile(tempDir, filename, nopLogger)
		require.NoError(t, err, "openDownloadFile failed unexpectedly")
		require.NotNil(t, file, "openDownloadFile returned nil file on success")
		defer file.Close()

		// Read content to verify it's the correct file
		readBytes, err := io.ReadAll(file)
		require.NoError(t, err)
		assert.Equal(t, content, readBytes)
	})

	t.Run("not_found", func(t *testing.T) {
		filename := "test_notfound.txt"

		// Call handler with directory path and filename
		_, _, err := openDownloadFile(tempDir, filename, nopLogger)
		require.Error(t, err, "openDownloadFile succeeded for non-existent file")

		st, ok := status.FromError(err)
		require.True(t, ok, "Expected gRPC status error")
		assert.Equal(t, codes.NotFound, st.Code(), "Expected NotFound code")
	})

	t.Run("is_directory", func(t *testing.T) {
		dirname := "test_dir_download"
		dirPath := filepath.Join(tempDir, dirname)

		err := os.Mkdir(dirPath, 0755)
		require.NoError(t, err, "Failed to create test directory")

		// Call handler trying to open the directory name as a file
		_, _, err = openDownloadFile(tempDir, dirname, nopLogger)
		require.Error(t, err, "openDownloadFile succeeded for directory")

		st, ok := status.FromError(err)
		require.True(t, ok, "Expected gRPC status error")
		assert.Equal(t, codes.InvalidArgument, st.Code(),
			"Expected InvalidArgument code",
		)

	})
}
