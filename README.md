# gRPC ImageShare Server & Client

![cover](https://github.com/vadimfedulov035/grpc-imageshare/blob/main/cover.png)

[![Go Report Card](https://goreportcard.com/badge/github.com/vadimfedulov035/grpc-imageshare)](https://goreportcard.com/report/github.com/vadimfedulov035/grpc-imageshare)

A robust and efficient gRPC server built with Go for uploading, downloading, and listing binary image files (or any file type). It features TLS encryption, concurrent request handling with limits, chunked file streaming, structured logging, and is fully containerized with Docker. A command-line client is also provided for interaction.

Its core components utilize:

*   **gRPC:** Designing and implementing unary and streaming RPCs (client-streaming, server-streaming).
*   **Go:** Idiomatic Go programming, concurrency management (semaphores), error handling, testing (unit, integration).
*   **Generics (Go 1.18+):** Utilizing type parameters for creating reusable, type-safe code (DRY principle) for network streaming operations.
*   **Docker:** Includes `Dockerfile` (multi-stage build using `scratch` base) and `docker-compose.yml` for easy building and running.
*   **Logs:** Using `zerolog` for efficient JSON/console logging, configurable via flags.
*   **Structure:** Clean separation of concerns using standard Go project layout (`cmd`, `internal`, `pkg`).
*   **Test Coverage:** Includes unit tests for handlers and comprehensive integration tests covering the full RPC lifecycle. Tests are run during the Docker build.
*   **Security:** TLS Securing gRPC communication.

## Operations

*   **Upload File:** Client streams file chunks to the server, allowing for large file uploads without loading the entire file into memory. Files overwrite existing files with the same name.
*   **Download File:** Server streams file chunks to the client upon request.
*   **List Files:** Client can request a list of files currently stored on the server as filenames, including metadata: size, creation and modification timestamps.

## Features
*   **Limiting:** Using semaphores to limit concurrent Upload/Download operations (default: 10) and List operations (default: 100).
*   **Chunking:** Files are transferred in configurable chunks (default: 64KB) for efficient memory usage.
*   **Client CLI:** A command-line client (`cmd/client`) is provided to interact with the server (List, Upload, Download).

## Getting started

### Prerequisites

*   [Go](https://golang.org/dl/) (version 1.18 or higher for generics)
*   [Docker](https://docs.docker.com/get-docker/) & [Docker Compose](https://docs.docker.com/compose/install/)
*   [Protocol Buffer Compiler (`protoc`)](https://grpc.io/docs/protoc-installation/)
*   Go plugins for protoc:
    ```bash
    go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
    go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
    ```
    (Ensure `$GOPATH/bin` or `$HOME/go/bin` is in your `PATH`)
*   [OpenSSL](https://www.openssl.org/) (or similar tool) for generating TLS certificates.

### 1. Clone Repository

```bash
git clone https://github.com/vadimfedulov035/grpc-imageshare.git
cd grpc-imageshare
```

### 2. Generate Protobuf Code
(Only needed if you modify `pkg/proto/file_service.proto`)
```bash
# Run from project root directory
protoc --proto_path=. \
       --go_out=. --go_opt=paths=source_relative \
       --go-grpc_out=. --go-grpc_opt=paths=source_relative \
       pkg/proto/file_service.proto
```

### 3. Generate TLS Certificates (for local testing)
```bash
mkdir certs
openssl req -x509 -newkey rsa:4096 -sha256 -days 3650 \
  -nodes -keyout certs/server.key -out certs/server.crt \
  -subj "/CN=localhost" \
  -addext "subjectAltName=DNS:localhost,IP:127.0.0.1"
```

### 4. Create Uploads Directory
```bash
# Make directory
mkdir uploads
# Change owner for Docker's nobody:nogroup
sudo chown 65534:65534 ./uploads
```

### 5. Build and Run with Docker Compose
```bash
# Run from project root directory
docker compose up --build
```

The server should start and log that it's listening securely on port 50051. Logs will be in JSON format by default. Press Ctrl+C to stop the server. Use docker compose down to remove the container and network.

## Using Client
Ensure the server container is running (`docker compose up`). Open a separate terminal in the project root directory.

* List Files
```bash
go run github.com/vadimfedulov035/grpc-imageshare/cmd/client -addr localhost:50051 -cert ./certs/server.crt -list
```

* Upload a File
```bash
# Create a test file
echo "Hello gRPC Streaming!" > test.txt

# Upload
go run github.com/vadimfedulov035/grpc-imageshare/cmd/client -addr localhost:50051 -cert ./certs/server.crt -upload ./test.txt
```

* Download a File
```
go run github.com/vadimfedulov035/grpc-imageshare/cmd/client -addr localhost:50051 -cert ./certs/server.crt -download test.txt -output .
```
## Design Notes & Future Improvements

* Error Handling: Uses standard Go errors internally (`fmt.Errorf`, `%w`, `errors.Is`) in generic/common packages. Server handlers translate these into appropriate gRPC status codes for the client. Client handlers format them for user display.

* Generics: `internal/generic/netops.go` uses Go generics (`[T any]`) and function arguments (`newT`/`FromT`) to create reusable and type-safe chunk processing logic (`SendChunk`/`ReceiveChunk`), avoiding `interface{}` and runtime type assertions in the hot path.

* Concurrency: Server uses buffered channels as semaphores (`LoadSemaphore`/`ListSemaphore`) to limit concurrent operations on potentially resource-intensive tasks (Upload/Download/List).

* File Overwriting: The server currently overwrites files on upload if a file with the same name exists (`O_TRUNC`). This could be changed back to `O_EXCL` if non-destructive uploads are preferred.

* Timestamps: ListFiles provides `CreatedAt` and `UpdatedAt` timestamps. `github.com/djherbis/times` is used to get the birth time (btime) for `CreatedAt`, if it's unavailable it falls back to modification time (mtime). `UpdatedAt` always uses modification time (mtime).

---

[![License](https://img.shields.io/badge/license-GPLv3-blue.svg)](#)  
**License**: GNU General Public License v3.0  
