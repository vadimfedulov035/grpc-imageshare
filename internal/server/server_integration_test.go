package server

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	pb "github.com/vadimfedulov035/grpc-imageshare/pkg/proto"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"google.golang.org/protobuf/types/known/emptypb"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/status"
)

const testChunkSize = 10 // Use small chunk size to force multiple chunks in test

// --- Test Setup Helper ---

type testServerData struct {
	Listener   net.Listener
	Server     *grpc.Server
	ServerImpl *fileServer
	Address    string
	CACertPEM  []byte
	StorageDir string
	Cleanup    func()
}

// Create self-signed certs for testing.
func generateTestTLSConfig(
	t *testing.T,
) (serverTLS *tls.Config, clientTrustPEM []byte) {
	t.Helper()
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err, "Failed to generate private key")

	serialNumberLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serialNumber, err := rand.Int(rand.Reader, serialNumberLimit)
	require.NoError(t, err, "Failed to generate serial number")

	template := x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			Organization: []string{"Test Co"},
			CommonName:   "localhost",
		},
		NotBefore:   time.Now(),
		NotAfter:    time.Now().Add(1 * time.Hour),
		KeyUsage:    x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
		DNSNames:    []string{"localhost"},
	}
	derBytes, err := x509.CreateCertificate(
		rand.Reader, &template, &template, &privateKey.PublicKey, privateKey,
	)
	require.NoError(t, err, "Failed to create certificate")

	certPEM := pem.EncodeToMemory(&pem.Block{
		Type: "CERTIFICATE", Bytes: derBytes,
	})
	keyPEM := pem.EncodeToMemory(&pem.Block{
		Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(privateKey),
	})

	serverCert, err := tls.X509KeyPair(certPEM, keyPEM)
	require.NoError(t, err, "Failed to load server key pair")
	serverTLS = &tls.Config{Certificates: []tls.Certificate{serverCert}}

	return serverTLS, certPEM
}

// Start test gRPC server on a random port
func setupTestServer(t *testing.T) *testServerData {
	t.Helper()

	storageDir := t.TempDir()

	// Nop logger during tests unless debugging specific server issues
	serverImpl, err := NewFileServer(storageDir)
	require.NoError(t, err, "Failed to create file server instance")

	serverTLSConfig, serverCertPEM := generateTestTLSConfig(t)
	serverCreds := credentials.NewTLS(serverTLSConfig)

	listener, err := net.Listen("tcp", "localhost:0")
	require.NoError(t, err, "Failed to listen")

	grpcServer := grpc.NewServer(grpc.Creds(serverCreds))
	pb.RegisterFileServiceServer(grpcServer, serverImpl)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		if serveErr := grpcServer.Serve(listener); serveErr != nil &&
			!errors.Is(serveErr, grpc.ErrServerStopped) {
			// Log using t.Logf for test output visibility
			t.Logf("Test server failed unexpectedly: %v", serveErr)
		}
	}()

	return &testServerData{
		Listener:   listener,
		Server:     grpcServer,
		ServerImpl: serverImpl,
		Address:    listener.Addr().String(),
		CACertPEM:  serverCertPEM,
		StorageDir: storageDir,
		Cleanup: func() {
			grpcServer.GracefulStop()
			wg.Wait()
		},
	}
}

// Connect to the test server using TLS and NewClient.
func setupTestClient(t *testing.T, serverAddr string, serverCertPEM []byte) (
	pb.FileServiceClient, func(),
) {
	t.Helper()

	certPool := x509.NewCertPool()
	require.True(t, certPool.AppendCertsFromPEM(serverCertPEM),
		"Failed to add server CA certificate",
	)

	clientCreds := credentials.NewTLS(&tls.Config{
		RootCAs:    certPool,
		ServerName: "localhost",
	})

	opts := []grpc.DialOption{
		grpc.WithTransportCredentials(clientCreds),
	}
	conn, err := grpc.NewClient(serverAddr, opts...)
	require.NoError(t, err, "Failed to dial test server")

	cleanup := func() {
		err := conn.Close()
		assert.NoError(t, err, "Failed to close client connection")
	}

	client := pb.NewFileServiceClient(conn)
	return client, cleanup
}

// --- Helper to check gRPC status code ---
func requireGrpcErrorCode(t *testing.T, err error, expectedCode codes.Code) {
	t.Helper()
	require.Error(t, err, "Expected a gRPC error, got nil")
	st, ok := status.FromError(err)
	require.True(t, ok, "Error was not a gRPC status error: %v", err)
	assert.Equal(t, expectedCode, st.Code(),
		"Expected gRPC code %v, got %v (err: %v)",
		expectedCode, st.Code(), err,
	)
}

// --- Integration Test Suite ---

func TestFileServiceIntegration(t *testing.T) {
	serverData := setupTestServer(t)
	defer serverData.Cleanup() // Ensure server stops

	client, clientCleanup := setupTestClient(t, serverData.Address, serverData.CACertPEM)
	defer clientCleanup() // Ensure client connection closes

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second) // Test timeout
	defer cancel()

	// --- Test Data ---
	uploadFilename1 := "integration_test_1.txt"
	uploadContent1 := []byte("This is the first integration test content.\nIt has multiple lines.")
	uploadFilename2 := "integration_test_2.bin"
	uploadContent2 := make([]byte, testChunkSize*5+testChunkSize/2) // ~5.5 chunks
	_, err := rand.Read(uploadContent2)
	require.NoError(t, err)

	downloadDir := t.TempDir()

	// --- Test Flow ---

	// 1. Initial List should be empty
	t.Run("ListEmpty", func(t *testing.T) {
		res, err := client.ListFiles(ctx, &emptypb.Empty{})
		require.NoError(t, err)
		assert.Empty(t, res.Files, "Expected 0 files initially")
	})

	// 2. Upload First File
	t.Run("UploadFile1", func(t *testing.T) {
		stream, err := client.UploadFile(ctx)
		require.NoError(t, err, "UploadFile RPC start failed")

		// Send filename
		reqFilename := &pb.UploadFileRequest{Data: &pb.UploadFileRequest_Filename{Filename: uploadFilename1}}
		err = stream.Send(reqFilename)
		require.NoError(t, err, "Failed to send filename")

		// Send content chunks
		reader := bytes.NewReader(uploadContent1)
		buffer := make([]byte, testChunkSize) // Use small test chunk size
		for {
			n, readErr := reader.Read(buffer)
			if n > 0 {
				reqChunk := &pb.UploadFileRequest{Data: &pb.UploadFileRequest_ChunkData{ChunkData: buffer[:n]}}
				err = stream.Send(reqChunk)
				require.NoError(t, err, "Failed to send chunk")
			}
			if readErr == io.EOF {
				break
			}
			require.NoError(t, readErr, "Failed reading test data")
		}

		// Close and receive response
		res, err := stream.CloseAndRecv()
		require.NoError(t, err, "UploadFile CloseAndRecv failed")
		assert.Equal(t, uploadFilename1, res.Filename)
		assert.Equal(t, uint64(len(uploadContent1)), res.Size)

		// Verify file exists on server storage with correct size
		serverFilePath := filepath.Join(serverData.StorageDir, uploadFilename1)
		stat, err := os.Stat(serverFilePath)
		require.NoError(t, err, "File not found on server storage after upload")
		assert.Equal(t, int64(len(uploadContent1)), stat.Size(), "File size on server storage incorrect")
	})

	// 3. List should show one file
	t.Run("ListOneFile", func(t *testing.T) {
		res, err := client.ListFiles(ctx, &emptypb.Empty{})
		require.NoError(t, err)
		require.Len(t, res.Files, 1, "Expected 1 file after first upload")
		fileMeta := res.Files[0]
		assert.Equal(t, uploadFilename1, fileMeta.Filename)
		assert.Equal(t, uint64(len(uploadContent1)), fileMeta.Size)
		assert.NotZero(t, fileMeta.CreatedAt.AsTime())
		assert.NotZero(t, fileMeta.UpdatedAt.AsTime())
	})

	// 4. Upload Second File
	t.Run("UploadFile2", func(t *testing.T) {
		// Similar upload logic as UploadFile1
		stream, err := client.UploadFile(ctx)
		require.NoError(t, err)
		err = stream.Send(
			&pb.UploadFileRequest{
				Data: &pb.UploadFileRequest_Filename{
					Filename: uploadFilename2},
			})
		require.NoError(t, err)
		reader := bytes.NewReader(uploadContent2)
		buffer := make([]byte, testChunkSize)
		for {
			n, readErr := reader.Read(buffer)
			if n > 0 {
				err = stream.Send(
					&pb.UploadFileRequest{
						Data: &pb.UploadFileRequest_ChunkData{
							ChunkData: buffer[:n]},
					})
				require.NoError(t, err)
			}
			if readErr == io.EOF {
				break
			}
			require.NoError(t, readErr)
		}
		res, err := stream.CloseAndRecv()
		require.NoError(t, err)
		assert.Equal(t, uploadFilename2, res.Filename)
		assert.Equal(t, uint64(len(uploadContent2)), res.Size)
	})

	// 5. List should show two files
	t.Run("ListTwoFiles", func(t *testing.T) {
		res, err := client.ListFiles(ctx, &emptypb.Empty{})
		require.NoError(t, err)
		require.Len(t, res.Files, 2, "Expected 2 files after second upload")
		// Optional: Check filenames exist
		found1, found2 := false, false
		for _, f := range res.Files {
			if f.Filename == uploadFilename1 {
				found1 = true
			}
			if f.Filename == uploadFilename2 {
				found2 = true
			}
		}
		assert.True(t, found1 && found2, "Both uploaded files should be listed")
	})

	// 6. Upload First File Again (Overwrite)
	t.Run("UploadFile1Overwrite", func(t *testing.T) {
		overwriteContent := []byte("This is the overwritten content.")

		stream, err := client.UploadFile(ctx)
		require.NoError(t, err)
		err = stream.Send(
			&pb.UploadFileRequest{
				Data: &pb.UploadFileRequest_Filename{
					Filename: uploadFilename1},
			})
		require.NoError(t, err)
		reader := bytes.NewReader(overwriteContent)
		buffer := make([]byte, testChunkSize)
		for {
			n, readErr := reader.Read(buffer)
			if n > 0 {
				err = stream.Send(
					&pb.UploadFileRequest{
						Data: &pb.UploadFileRequest_ChunkData{
							ChunkData: buffer[:n]},
					})
				require.NoError(t, err)
			}
			if readErr == io.EOF {
				break
			}
			require.NoError(t, readErr)
		}
		res, err := stream.CloseAndRecv()
		require.NoError(t, err, "Overwrite upload failed unexpectedly")
		assert.Equal(t, uploadFilename1, res.Filename)
		assert.Equal(t, uint64(len(overwriteContent)), res.Size,
			"Size should reflect overwritten content",
		)

		// Verify file size on server storage reflects overwrite
		serverFilePath := filepath.Join(serverData.StorageDir, uploadFilename1)
		stat, err := os.Stat(serverFilePath)
		require.NoError(t, err)
		assert.Equal(t, int64(len(overwriteContent)), stat.Size(),
			"File size on server should match overwritten content",
		)
	})

	// 7. List should still show two files (names unchanged)
	t.Run("ListTwoFilesAfterOverwrite", func(t *testing.T) {
		res, err := client.ListFiles(ctx, &emptypb.Empty{})
		require.NoError(t, err)
		assert.Len(t, res.Files, 2, "Should still have 2 files after overwrite")
	})

	// 8. Download First File (check overwritten content)
	t.Run("DownloadFile1Overwritten", func(t *testing.T) {
		req := &pb.DownloadFileRequest{Filename: uploadFilename1}
		stream, err := client.DownloadFile(ctx, req)
		require.NoError(t, err)

		downloadedFilePath := filepath.Join(downloadDir, uploadFilename1+"_downloaded")
		downloadedFile, err := os.Create(downloadedFilePath)
		require.NoError(t, err)
		defer downloadedFile.Close()

		downloadedBytes := bytes.Buffer{} // Buffer to collect bytes
		for {
			res, err := stream.Recv()
			if err == io.EOF {
				break
			}
			require.NoError(t, err)
			_, writeErr := downloadedFile.Write(res.GetChunkData())
			require.NoError(t, writeErr)
			downloadedBytes.Write(res.GetChunkData()) // Buffer for easy compare
		}

		// Verify downloaded content matches the *overwritten* content
		overwriteContent := []byte("This is the overwritten content.")
		assert.True(t, bytes.Equal(overwriteContent, downloadedBytes.Bytes()),
			"Downloaded content mismatch after overwrite",
		)
	})

	// 9. Download Second File (check original random content)
	t.Run("DownloadFile2", func(t *testing.T) {
		req := &pb.DownloadFileRequest{Filename: uploadFilename2}
		stream, err := client.DownloadFile(ctx, req)
		require.NoError(t, err)

		downloadedFilePath := filepath.Join(downloadDir, uploadFilename2+"_downloaded")
		downloadedFile, err := os.Create(downloadedFilePath)
		require.NoError(t, err)
		defer downloadedFile.Close()

		downloadedBytes := bytes.Buffer{}
		for {
			res, err := stream.Recv()
			if err == io.EOF {
				break
			}
			require.NoError(t, err)
			_, writeErr := downloadedFile.Write(res.GetChunkData())
			require.NoError(t, writeErr)
			downloadedBytes.Write(res.GetChunkData())
		}

		// Verify downloaded content matches original random content
		assert.True(t, bytes.Equal(uploadContent2, downloadedBytes.Bytes()),
			"Downloaded content mismatch for file 2",
		)
	})

	// 10. Download Non-Existent File
	t.Run("DownloadNotFound", func(t *testing.T) {
		req := &pb.DownloadFileRequest{Filename: "nonexistent.file"}
		stream, err := client.DownloadFile(ctx, req)
		require.NoError(t, err,
			"Starting download stream should not fail immediately for NotFound",
		) // Server likely checks on first read

		// Expect error on first Recv
		_, err = stream.Recv()
		requireGrpcErrorCode(t, err, codes.NotFound) // Check with helper
	})

	// 11. Attempt to Download a Directory (if server prevents it)
	t.Run("DownloadDirectoryError", func(t *testing.T) {
		dirName := "test_directory_on_server"
		dirPath := filepath.Join(serverData.StorageDir, dirName)
		err := os.Mkdir(dirPath, 0755)
		require.NoError(t, err, "Failed to create directory on server storage for test")

		req := &pb.DownloadFileRequest{Filename: dirName}
		stream, err := client.DownloadFile(ctx, req)
		require.NoError(t, err)

		// Expect InvalidArgument because the path resolved to a directory
		_, err = stream.Recv()
		requireGrpcErrorCode(t, err, codes.InvalidArgument)
	})

	// 12. Upload Invalid Filename (Path Traversal Attempt)
	t.Run("UploadInvalidFilename", func(t *testing.T) {
		stream, err := client.UploadFile(ctx)
		require.NoError(t, err, "Starting stream failed")

		// Send invalid filename
		reqFilename := &pb.UploadFileRequest{
			Data: &pb.UploadFileRequest_Filename{
				Filename: "../invalid_name.txt",
			}}
		// We don't expect Send itself to fail based on content, usually network errors
		err = stream.Send(reqFilename)
		require.NoError(t, err, "Sending invalid filename unexpectedly failed")

		// We expect the error when closing the stream
		_, err = stream.CloseAndRecv()
		requireGrpcErrorCode(t, err, codes.InvalidArgument)
	})

}
