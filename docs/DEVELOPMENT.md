# Development Guide

This guide covers everything you need to know to contribute to the Grafana MCP server project.

## Table of Contents

- [Getting Started](#getting-started)
- [Development Environment](#development-environment)
- [Building from Source](#building-from-source)
- [Running Locally](#running-locally)
- [Testing](#testing)
- [Linting](#linting)
- [Code Standards](#code-standards)
- [Contributing](#contributing)
- [Release Process](#release-process)

## Getting Started

### Prerequisites

- **Go 1.21 or later** - [Install Go](https://golang.org/doc/install)
- **Git** - For version control
- **Docker** - For running integration tests
- **Docker Compose** - For test Grafana instance
- **Make** - For build automation

### Clone the Repository

```bash
git clone https://github.com/grafana/mcp-grafana.git
cd mcp-grafana
```

### Verify Setup

```bash
# Check Go version
go version

# Check Docker
docker --version
docker-compose --version

# Check Make
make --version
```

## Development Environment

### IDE Setup

#### VSCode

Recommended extensions:
- Go (golang.go)
- Go Test Explorer
- GitLens

Create `.vscode/settings.json`:

```json
{
  "go.useLanguageServer": true,
  "go.lintTool": "golangci-lint",
  "go.lintOnSave": "package",
  "editor.formatOnSave": true,
  "[go]": {
    "editor.defaultFormatter": "golang.go"
  }
}
```

#### GoLand / IntelliJ IDEA

1. Open the project directory
2. Enable Go modules support
3. Configure code style to use gofmt

### Environment Variables

Create a `.env` file for local development:

```bash
# .env
GRAFANA_URL=http://localhost:3000
GRAFANA_SERVICE_ACCOUNT_TOKEN=your-token-here
# Or for basic auth:
# GRAFANA_USERNAME=admin
# GRAFANA_PASSWORD=admin
```

Load environment variables:

```bash
source .env
```

## Building from Source

### Build Binary

```bash
# Build for current platform
make build

# Binary will be in ./dist/mcp-grafana
./dist/mcp-grafana --help
```

### Build for Multiple Platforms

```bash
# Build for all platforms using goreleaser
make build-all
```

### Install Locally

```bash
# Install to $GOPATH/bin
go install ./cmd/mcp-grafana

# Or specify custom location
GOBIN="$HOME/bin" go install ./cmd/mcp-grafana
```

### Build Docker Image

```bash
# Build Docker image
make build-image

# Image will be tagged as mcp-grafana:latest
docker images | grep mcp-grafana
```

## Running Locally

### STDIO Mode (Default)

```bash
# Run with make
make run

# Or run directly
go run ./cmd/mcp-grafana

# With debug mode
go run ./cmd/mcp-grafana --debug
```

### SSE Mode

```bash
# Run SSE server on default port (8000)
go run ./cmd/mcp-grafana --transport sse

# Custom port
go run ./cmd/mcp-grafana --transport sse --address :9090
```

### Streamable HTTP Mode

```bash
go run ./cmd/mcp-grafana --transport streamable-http
```

### With Docker Compose

Start a local Grafana instance for testing:

```bash
# Start Grafana and dependencies
docker-compose up -d

# Check status
docker-compose ps

# View logs
docker-compose logs -f grafana

# Stop everything
docker-compose down
```

The docker-compose setup includes:
- Grafana on port 3000
- Prometheus on port 9090
- Loki on port 3100

Default Grafana credentials: `admin` / `admin`

## Testing

The project has three types of tests: unit tests, integration tests, and cloud tests.

### Unit Tests

Unit tests have no external dependencies and run quickly.

```bash
# Run unit tests
make test-unit

# Or use go test directly
go test -v -short ./...

# Run with coverage
go test -v -short -cover ./...

# Generate coverage report
go test -v -short -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

### Integration Tests

Integration tests require Docker containers to be running.

```bash
# Start test environment
docker-compose up -d

# Wait for services to be ready
sleep 10

# Run integration tests
make test-integration

# Or with go test
go test -v ./... -run Integration
```

**What integration tests cover:**
- Dashboard operations
- Datasource queries (Prometheus, Loki)
- Alert rule operations
- API client functionality

### Cloud Tests

Cloud tests run against a real Grafana Cloud instance (used in CI).

```bash
# Set up cloud credentials
export GRAFANA_CLOUD_URL=https://yourinstance.grafana.net
export GRAFANA_CLOUD_TOKEN=your-cloud-token

# Run cloud tests
make test-cloud
```

> **Note:** Cloud tests require a Grafana Cloud instance. They are primarily used in CI and are optional for local development.

### Run All Tests

```bash
# Ensure Docker Compose is running
docker-compose up -d

# Run all tests
make test-all
```

### Test Specific Packages

```bash
# Test a specific package
go test -v ./internal/tools

# Test a specific function
go test -v ./internal/tools -run TestDashboardTools
```

### Writing Tests

When adding new tools, please include tests:

**Unit test example:**

```go
func TestNewTool(t *testing.T) {
    // Arrange
    client := mockGrafanaClient()
    
    // Act
    result, err := NewTool(client, params)
    
    // Assert
    require.NoError(t, err)
    assert.Equal(t, expectedResult, result)
}
```

**Integration test example:**

```go
func TestNewToolIntegration(t *testing.T) {
    if testing.Short() {
        t.Skip("Skipping integration test")
    }
    
    // Use real Grafana client
    client := setupIntegrationClient(t)
    
    result, err := NewTool(client, params)
    
    require.NoError(t, err)
    assert.NotNil(t, result)
}
```

## Linting

### Run All Linters

```bash
# Run golangci-lint
make lint
```

### Linter Configuration

The project uses `golangci-lint` with custom configuration in `.golangci.yaml`.

Enabled linters include:
- gofmt
- govet
- staticcheck
- gosimple
- ineffassign
- unused
- Custom JSONSchema linter

### JSONSchema Linter

The project includes a custom linter that checks for unescaped commas in `jsonschema` struct tags.

**Why?** Unescaped commas in description fields cause silent truncation in JSON schema generation.

```bash
# Run JSONSchema linter only
make lint-jsonschema
```

**Example of correct usage:**

```go
// ❌ Wrong - comma will cause truncation
Description string `jsonschema:"description=This is a test, with comma"`

// ✅ Correct - comma is escaped
Description string `jsonschema:"description=This is a test\\, with comma"`
```

See [JSONSchema Linter documentation](../internal/linter/jsonschema/README.md) for more details.

### Fix Linter Issues

```bash
# Auto-fix some issues
golangci-lint run --fix

# Format code
gofmt -w .

# Or use goimports
goimports -w .
```

## Code Standards

### Go Code Style

Follow standard Go conventions:
- Use `gofmt` for formatting
- Follow [Effective Go](https://golang.org/doc/effective_go.html)
- Use meaningful variable names
- Add comments for exported functions and types
- Keep functions focused and small

### Project Structure

```
mcp-grafana/
├── cmd/
│   └── mcp-grafana/       # Main application entry point
├── internal/
│   ├── grafana/           # Grafana API clients
│   ├── tools/             # MCP tool implementations
│   ├── linter/            # Custom linters
│   └── ...
├── testdata/              # Test fixtures
├── tests/                 # Integration tests
├── docs/                  # Documentation
└── examples/              # Configuration examples
```

### Adding New Tools

1. **Define the tool in `tools.go`:**

```go
func NewMyTool(client *grafana.Client) mcp.Tool {
    return mcp.Tool{
        Name:        "my_tool",
        Description: "Description of what the tool does",
        InputSchema: generateSchema(MyToolInput{}),
    }
}
```

2. **Create input/output structs:**

```go
type MyToolInput struct {
    Param1 string `json:"param1" jsonschema:"required,description=Parameter description"`
    Param2 int    `json:"param2" jsonschema:"description=Optional parameter"`
}
```

3. **Implement the handler:**

```go
func handleMyTool(ctx context.Context, client *grafana.Client, input MyToolInput) (interface{}, error) {
    // Implementation
    return result, nil
}
```

4. **Add tests:**

```go
func TestMyTool(t *testing.T) {
    // Unit tests
}

func TestMyToolIntegration(t *testing.T) {
    if testing.Short() {
        t.Skip("Skipping integration test")
    }
    // Integration tests
}
```

5. **Update documentation:**
   - Add tool to `docs/TOOLS_REFERENCE.md`
   - Document feature in `docs/FEATURES.md`
   - Add RBAC requirements to `docs/RBAC.md`

### Error Handling

Use descriptive errors:

```go
// ❌ Bad
return nil, err

// ✅ Good
return nil, fmt.Errorf("failed to get dashboard %s: %w", uid, err)
```

### Logging

Use structured logging appropriately:

```go
// For debug information
if debug {
    log.Printf("Querying datasource %s with params: %+v", dsUID, params)
}

// For errors
log.Printf("ERROR: Failed to query datasource: %v", err)
```

## Contributing

### Workflow

1. **Fork the repository**
2. **Create a feature branch:**
   ```bash
   git checkout -b feature/my-new-feature
   ```
3. **Make your changes**
4. **Run tests and linters:**
   ```bash
   make test-unit
   make lint
   ```
5. **Commit with descriptive messages:**
   ```bash
   git commit -m "Add new dashboard query tool"
   ```
6. **Push to your fork:**
   ```bash
   git push origin feature/my-new-feature
   ```
7. **Open a Pull Request**

### Pull Request Guidelines

- **Title:** Clear, descriptive title
- **Description:** Explain what changes and why
- **Tests:** Include tests for new functionality
- **Documentation:** Update relevant docs
- **Linting:** Ensure all linters pass
- **Commits:** Use clear commit messages

### Commit Message Format

```
<type>: <subject>

<body>

<footer>
```

Types:
- `feat:` New feature
- `fix:` Bug fix
- `docs:` Documentation changes
- `test:` Adding or updating tests
- `refactor:` Code refactoring
- `chore:` Maintenance tasks

Example:
```
feat: Add Pyroscope datasource support

Add tools for querying Pyroscope continuous profiling data:
- list_pyroscope_label_names
- list_pyroscope_label_values
- fetch_pyroscope_profile

Includes unit and integration tests.

Fixes #123
```

### Code Review Process

1. Automated checks must pass (tests, linting)
2. At least one maintainer approval required
3. Address review feedback
4. Squash commits if requested
5. Maintainer will merge when ready

## Release Process

### Versioning

The project follows [Semantic Versioning](https://semver.org/):
- **Major:** Breaking changes
- **Minor:** New features, backwards compatible
- **Patch:** Bug fixes, backwards compatible

### Creating a Release

Releases are automated using GoReleaser:

1. **Tag the release:**
   ```bash
   git tag -a v1.2.3 -m "Release v1.2.3"
   git push origin v1.2.3
   ```

2. **CI automatically:**
   - Builds binaries for all platforms
   - Creates GitHub release
   - Publishes Docker image
   - Updates documentation

### Testing a Release Build

```bash
# Test goreleaser locally (doesn't publish)
goreleaser release --snapshot --rm-dist
```

## Additional Resources

### Documentation

- [Features Guide](FEATURES.md)
- [Tools Reference](TOOLS_REFERENCE.md)
- [RBAC Guide](RBAC.md)
- [Configuration Guide](CONFIGURATION.md)
- [Troubleshooting Guide](TROUBLESHOOTING.md)

### External Resources

- [Grafana HTTP API Documentation](https://grafana.com/docs/grafana/latest/http_api/)
- [Model Context Protocol Specification](https://modelcontextprotocol.io/)
- [Go Documentation](https://golang.org/doc/)

### Getting Help

- **GitHub Issues:** Report bugs or request features
- **GitHub Discussions:** Ask questions or discuss ideas
- **Code Review:** Learn from PR reviews

## Tips for Contributors

1. **Start small:** Pick a "good first issue" to get familiar
2. **Ask questions:** Use discussions for clarifications
3. **Read existing code:** Learn patterns from current implementations
4. **Test thoroughly:** Write comprehensive tests
5. **Document well:** Help others understand your changes
6. **Be patient:** Code review takes time
7. **Have fun:** Enjoy contributing to open source!

## License

This project is licensed under the Apache License, Version 2.0. See [LICENSE](../LICENSE) for details.

By contributing, you agree that your contributions will be licensed under the same license.