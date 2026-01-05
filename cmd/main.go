package main

import (
	"context"
	"github.com/bsonger/devflow-controller/pkg/bootstrap"
	"github.com/bsonger/devflow-controller/pkg/config"
	"github.com/bsonger/devflow-controller/pkg/controller"
	"github.com/bsonger/devflow-controller/pkg/signal"
	"log"
)

func main() {
	// 创建带信号监听的上下文
	rootCtx, cancel := signal.WithSignal(context.Background())
	defer cancel() // 确保 main 退出时 cancel

	// 加载配置
	cfg, err := config.Load()
	if err != nil {
		log.Fatal("load config failed:", err)
	}

	// 初始化基础服务，如 mongo、otel、tekton client
	shutdown, err := bootstrap.Init(rootCtx, cfg)
	if err != nil {
		log.Fatal("bootstrap init failed:", err)
	}
	defer func() {
		if err := shutdown(context.Background()); err != nil {
			log.Println("shutdown error:", err)
		}
	}()

	// 启动 Tekton informer
	if err := controller.StartTektonInformer(rootCtx); err != nil {
		log.Fatal("start tekton informer failed:", err)
	}
	if err := controller.StartArgoApplicationInformer(rootCtx); err != nil {
		log.Fatal("start tekton informer failed:", err)
	}

	log.Println("DevFlow Controller started, waiting for events...")

	// 阻塞直到收到退出信号
	<-rootCtx.Done()
	log.Println("Shutting down DevFlow Controller")
}
