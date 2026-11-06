package main

import (
	"flag"
	"log/slog"
	"os"

	"logisticsServer/internal/conf"
	"logisticsServer/internal/logger"
	"logisticsServer/internal/tracing"

	"github.com/go-kratos/kratos/v3"
	"github.com/go-kratos/kratos/v3/config"
	"github.com/go-kratos/kratos/v3/config/env"
	"github.com/go-kratos/kratos/v3/config/file"
	"github.com/go-kratos/kratos/v3/log"
	"github.com/go-kratos/kratos/v3/transport/grpc"
	"github.com/go-kratos/kratos/v3/transport/http"

	_ "go.uber.org/automaxprocs"
)

// go build -ldflags "-X main.Version=x.y.z"
var (
	// Name is the name of the compiled software.
	Name string
	// Version is the version of the compiled software.
	Version string
	// flagconf is the config flag.
	flagconf string

	id, _ = os.Hostname()
)

// defaultName 未通过 ldflags 注入服务名时使用，日志与链路数据都需要服务标识。
const defaultName = "logisticsServer"

func init() {
	flag.StringVar(&flagconf, "conf", "../../configs", "config path, eg: -conf config.yaml")
}

func newApp(logger *slog.Logger, gs *grpc.Server, hs *http.Server) *kratos.App {
	return kratos.New(
		kratos.ID(id),
		kratos.Name(Name),
		kratos.Version(Version),
		kratos.Metadata(map[string]string{}),
		kratos.Logger(logger),
		kratos.Server(
			gs,
			hs,
		),
	)
}

func main() {
	flag.Parse()
	if Name == "" {
		Name = defaultName
	}
	c := config.New(
		config.WithSource(
			file.NewSource(flagconf),
			env.NewSource("KRATOS"),
		),
	)
	defer c.Close()

	if err := c.Load(); err != nil {
		panic(err)
	}

	var bc conf.Bootstrap
	if err := c.Scan(&bc); err != nil {
		panic(err)
	}

	appLogger, cleanupLog, err := logger.New(bc.Logger, id, Name, Version)
	if err != nil {
		panic(err)
	}
	defer cleanupLog()
	log.SetDefault(appLogger)

	// 初始化链路追踪：开启上报时把 span 发到 OTLP 采集端，关闭时仍会为
	// 每个请求生成本地 trace_id/span_id，供日志记录。
	shutdownTrace, err := tracing.NewProvider(bc.Trace, Name)
	if err != nil {
		panic(err)
	}
	defer shutdownTrace()

	app, cleanup, err := wireApp(bc.Server, bc.Data, bc.Sf, bc.Auth, appLogger)
	if err != nil {
		panic(err)
	}
	defer cleanup()

	// start and wait for stop signal
	if err := app.Run(); err != nil {
		panic(err)
	}
}
