package job

import (
	"context"
	"github.com/bsonger/devflow-common/client/mongo"
	"github.com/bsonger/devflow-common/model"
	"go.mongodb.org/mongo-driver/bson"
	"time"
)

var JobService = &jobService{}

type jobService struct {
}

func (s *jobService) UpdateJobStatus(ctx context.Context, jobID string, status model.JobStatus) error {

	filter := bson.M{
		"id": jobID,
		"status": bson.M{
			"$nin": []model.JobStatus{model.JobSucceeded, model.JobFailed, status},
		},
	}

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
