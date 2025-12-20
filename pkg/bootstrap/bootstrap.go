package bootstrap

import (
	"context"
	"github.com/bsonger/devflow-common/client/argo"
	"github.com/bsonger/devflow-common/client/logging"
	"github.com/bsonger/devflow-common/client/mongo"
	"github.com/bsonger/devflow-common/client/otel"
	"github.com/bsonger/devflow-common/client/tekton"
	"github.com/bsonger/devflow-controller/pkg/config"
)

func Init(ctx context.Context, cfg *config.Config) (func(context.Context) error, error) {
	// 1️⃣ Logger
	logging.InitZapLogger(ctx, cfg.Log)

	// 2️⃣ Otel（可选）
	var shutdown func(context.Context) error = func(context.Context) error { return nil }
	if cfg.Otel != nil {
		s, err := otel.InitOtel(ctx, cfg.Otel)
		if err != nil {
			return nil, err
		}
		shutdown = s
	}

	// 3️⃣ Mongo
	mongo.InitMongo(ctx, cfg.Mongo, logging.Logger)
	//if err := ensureMongoIndex(ctx); err != nil {
	//	return nil, err
	//}

	// 4️⃣ Kubernetes Clients
	kubeCfg, err := config.LoadKubeConfig()

	if err != nil {
		return nil, err
	}

	tekton.InitTektonClient(ctx, kubeCfg, logging.Logger)
	argo.InitArgoCdClient(kubeCfg)

	return shutdown, nil
}

//func ensureMongoIndex(ctx context.Context) error {
//	return mongo.Repo.EnsureIndexes(ctx, "manifest", []mongo.Index{
//		{
//			Keys:   map[string]int{"pipeline_id": 1},
//			Unique: true,
//			Name:   "pipeline_id_unique",
//		},
//	})
//}
