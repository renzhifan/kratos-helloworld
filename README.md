# Kratos Project Template

A project template for creating new Kratos services with HTTP and gRPC
transports, protobuf-first APIs, Wire dependency injection, OpenAPI generation,
and a small CRUD example.

Use this repository as a starting point for a new service. The included sample
resource is only reference code for API shape, layering, code generation, and
testing. Replace it with your own domain model when creating a real project.

## Create a New Project

1. Copy or generate a repository from this template.
2. Update the Go module path:

```bash
go mod edit -module github.com/your-org/your-service
```

3. Replace existing import paths that reference this template module.
4. Rename the command, service metadata, and sample API package to match your
   service.
5. Replace the sample CRUD resource with your own resource.
6. Regenerate code and verify the project:

```bash
make all
go test ./...
```

## What Is Included

- Kratos HTTP and gRPC server setup.
- Protobuf API definitions and generated Go code.
- OpenAPI generation.
- Wire-based dependency injection.
- Layered `service`, `biz`, and `data` packages.
- A lightweight in-memory repository for the sample resource.
- Unit tests for the service layer.
- Server-streaming and bidirectional-streaming examples.

## Project Layout

```text
api/                  Protobuf APIs and generated bindings
cmd/                  Application entrypoints
configs/              Local configuration
internal/server/      HTTP and gRPC server construction
internal/service/     Transport-facing service methods
internal/biz/         Usecases, entities, errors, repository interfaces
internal/data/        Repository implementations
third_party/          Protobuf dependencies
openapi.yaml          Generated OpenAPI document
```

## API Template Practices

The sample CRUD API demonstrates common conventions for Kratos projects:

- Resource-oriented methods: create, get, list, update, delete.
- HTTP annotations with `google.api.http`.
- Required fields with `google.api.field_behavior`.
- List requests with `page_size`, `page_token`, `filter`, and `order_by`.
- Pagination with `go.einride.tech/aip/pagination`.
- Partial updates with `google.protobuf.FieldMask` and `fieldmask.Update`.
- Streaming RPC definitions for one-way and bidirectional streams.

The in-memory data layer intentionally stays simple. It demonstrates flow across
layers, but does not implement a full query engine. Real repositories can apply
parsed filters and ordering in SQL, Ent, or another storage layer.

## Development Commands

Install generators:

```bash
make init
```

Regenerate API bindings and OpenAPI:

```bash
make api
```

Regenerate config protobufs:

```bash
make config
```

Run all generation steps, Wire, and module cleanup:

```bash
make all
```

Build:

```bash
make build
```

Test:

```bash
go test ./...
```

## 新增资源（Resource）流程

从新增 proto 到对外暴露接口，完整步骤如下。`kratos` 命令负责生成 pb 代码和 service 骨架，
Wire 负责生成依赖注入代码，**但有两处必须手改**（ProviderSet 和 server 注册），否则接口不会暴露。

### 1. 生成 proto 模板

```bash
kratos proto add api/helloworld/v1/greeter.proto
```

### 2. 生成 pb 代码（含 HTTP 绑定和 OpenAPI）

```bash
make api
```

会产出 `greeter.pb.go`、`greeter_grpc.pb.go`；如果 proto 里有 `google.api.http` 注解，
还会额外生成 `greeter_http.pb.go`。

### 3. 生成 service 实现骨架

```bash
kratos proto server api/helloworld/v1/greeter.proto -t internal/service
```

会在 `internal/service/` 下生成 `greeter.go`，包含所有 rpc 的空实现。
如果目标文件已存在，该命令会跳过，不会覆盖；proto 新增 rpc 之后需要自己补方法。
建议生成后跑一次 `gofmt -w internal/service/greeter.go` 整理缩进。

### 4. 将 service 加入 Wire ProviderSet（手动）

编辑 `internal/service/service.go`，把 `NewGreeterService` 加到 `wire.NewSet` 里：

```go
var ProviderSet = wire.NewSet(NewTodoService, NewGreeterService)
```

### 5. 注册到 server（手动）

编辑 `internal/server/grpc.go`（HTTP 同理编辑 `http.go`）：

```go
func NewGRPCServer(c *conf.Server, todo *service.TodoService, greeter *service.GreeterService) *grpc.Server {
    ...
    v1.RegisterTodoServiceServer(srv, todo)
    helloworldv1.RegisterGreeterServer(srv, greeter)
    return srv
}
```

### 6. 重新生成依赖注入代码

```bash
wire ./cmd/helloworld
# 或者
make generate
```

会更新 `cmd/helloworld/wire_gen.go`。该文件由 Wire 自动生成，不要手改。

### 常见遗漏

- 第 4 步漏掉：`wire` 会报错找不到 provider。
- 第 5 步漏掉：编译能通过，但 gRPC/HTTP 服务列表里看不到新接口。
- 没有 HTTP 注解：不会生成 `_http.pb.go`，`http.go` 里也无需注册。

## Run Locally

```bash
go run ./cmd/server -conf ./configs
```

Default local ports are configured in `configs/config.yaml`:

- HTTP: `0.0.0.0:8000`
- gRPC: `0.0.0.0:9000`

## Docker

```bash
docker build -t <your-image-name> .
docker run --rm -p 8000:8000 -p 9000:9000 \
  -v </path/to/your/configs>:/data/conf \
  <your-image-name>
```
