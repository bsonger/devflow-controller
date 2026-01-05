package job

import (
	"context"
	"github.com/bsonger/devflow-common/client/mongo"
	"github.com/bsonger/devflow-common/model"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"time"
)

var JobService = &jobService{}

type jobService struct {
}

func (s *jobService) UpdateJobStatus(ctx context.Context, jobID string, status model.JobStatus) error {
	// 1️⃣ 把 string ID 转成 ObjectID
	oid, err := primitive.ObjectIDFromHex(jobID)
	if err != nil {
		return err
	}

	// 2️⃣ 构造 filter
	filter := bson.M{
		"_id": oid,
		"status": bson.M{
			"$nin": []model.JobStatus{model.JobSucceeded, model.JobFailed, status},
		},
	}

	// 3️⃣ 执行更新
	return mongo.Repo.UpdateOne(
		ctx,
		&model.Job{},
		filter,
		bson.M{
			"$set": bson.M{
				"status":     status,
				"updated_at": time.Now(),
			},
		},
	)
}
