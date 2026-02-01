package job

import (
	"context"
	"time"

	"github.com/bsonger/devflow-common/client/mongo"
	"github.com/bsonger/devflow-common/model"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

type JobStep struct {
	Name      string           `bson:"name" json:"name"`
	Progress  int32            `bson:"progress" json:"progress"`
	Status    model.StepStatus `bson:"status" json:"status"`
	Message   string           `bson:"message,omitempty" json:"message,omitempty"`
	StartTime *time.Time       `bson:"start_time,omitempty" json:"start_time,omitempty"`
	EndTime   *time.Time       `bson:"end_time,omitempty" json:"end_time,omitempty"`
}

type JobWithSteps struct {
	model.Job `bson:",inline"`
	Steps     []JobStep `bson:"steps" json:"steps"`
}

func (j *JobWithSteps) CollectionName() string {
	return "job"
}

func (s *jobService) GetJobWithSteps(ctx context.Context, jobID string) (*JobWithSteps, error) {
	oid, err := primitive.ObjectIDFromHex(jobID)
	if err != nil {
		return nil, err
	}

	var j JobWithSteps
	if err := mongo.Repo.FindByID(ctx, &j, oid); err != nil {
		return nil, err
	}
	return &j, nil
}

func (s *jobService) UpdateJobStep(ctx context.Context, jobID, stepName string, status model.StepStatus, progress int32, message string, start, end *time.Time) error {
	if stepName == "" {
		return nil
	}

	oid, err := primitive.ObjectIDFromHex(jobID)
	if err != nil {
		return err
	}

	if progress < 0 {
		progress = 0
	}
	if progress > 100 {
		progress = 100
	}

	update := bson.M{
		"steps.$.status":   status,
		"steps.$.progress": progress,
		"steps.$.message":  message,
		"updated_at":       time.Now(),
	}
	if start != nil {
		update["steps.$.start_time"] = *start
	}
	if end != nil {
		update["steps.$.end_time"] = *end
	}

	filter := bson.M{
		"_id": oid,
		"steps": bson.M{
			"$elemMatch": bson.M{
				"name": stepName,
				"status": bson.M{
					"$nin": []model.StepStatus{model.StepFailed, model.StepSucceeded},
				},
			},
		},
	}

	return mongo.Repo.UpdateOne(ctx, &model.Job{}, filter, bson.M{"$set": update})
}
