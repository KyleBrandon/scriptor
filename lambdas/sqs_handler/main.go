package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"

	"github.com/KyleBrandon/scriptor/lambdas/util"
	"github.com/KyleBrandon/scriptor/pkg/database"
	"github.com/KyleBrandon/scriptor/pkg/google"
	"github.com/KyleBrandon/scriptor/pkg/types"
	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/sfn"
)

type sqsHandlerConfig struct {
	store           database.WatchChannelStore
	docStore        database.DocumentStore
	dc              *google.GoogleDriveContext
	awsCfg          aws.Config
	stateMachineARN string
	s3Client        *s3.Client
	sfnClient       *sfn.Client
}

var cfg *sqsHandlerConfig

func (cfg *sqsHandlerConfig) process(ctx context.Context, sqsEvent events.SQSEvent) error {
	slog.Debug(">>process")
	defer slog.Debug("<<process")

	var err error

	// Create a storage client if we don't have one
	cfg.store, err = util.VerifyWatchChannelStore(cfg.store)
	if err != nil {
		return err
	}

	// Create a document storage client if we don't have one
	cfg.docStore, err = util.VerifyDocumentStore(cfg.docStore)
	if err != nil {
		return err
	}

	// Create a Google Drive service if we don't have one
	cfg.dc, err = util.VerifyDriveContext(cfg.dc)
	if err != nil {
		return err
	}

	// loop through any SQS events
	for _, message := range sqsEvent.Records {
		// get the channel that triggered the event
		var eventData types.ChannelNotification
		if err := json.Unmarshal([]byte(message.Body), &eventData); err != nil {
			return fmt.Errorf("failed to unmarshal SQS message: %v", err)
		}

		// Acquire the changes lock on the channel
		startToken, err := cfg.store.AcquireChangesToken(eventData.ChannelID)
		if err != nil {
			slog.Error("Failed to acquire the watch channel changes lock", "error", err)
			return err
		}

		// Get the new token that we'll process changes from on next call
		newToken, err := cfg.dc.GetChangesStartToken()
		if err != nil {
			slog.Error("Failed to get the next changes start token", "error", err)
			return err
		}

		// ensure that we try to release the lock before we return
		defer func() {
			err = cfg.store.ReleaseChangesToken(eventData.ChannelID, newToken)
			if err != nil {
				slog.Error("Failed to release the watch channel changes lock", "error", err)
			}
		}()

		// Query the files that have changed and get the next changes start token
		changes, err := cfg.dc.QueryChanges(eventData.FolderID, startToken)
		if err != nil {
			slog.Error("Call to QueryFiles failed", "error", err)
			return err
		}

		// Check if there are documents to process
		if len(changes.Documents) == 0 {
			return nil
		}

		// Start the state machine for each document discovered
		for _, document := range changes.Documents {
			slog.Info("Processing document from queue", "name", document.Name)

			// Save the Google Drive document information
			err = cfg.docStore.InsertDocument(document)
			if err != nil {
				slog.Error("Failed to save the document metadata", "docName", document.Name, "error", err)
				return err
			}

			// TODO: this should be a different step type as it's the Google document ID not ours
			input, err := util.BuildStepInput(document.ID, types.DOCUMENT_STAGE_NEW)
			if err != nil {
				slog.Error("Failed to build the stage input for the next stage", "docName", document.Name, "error", err)
				return err
			}

			// start the state machine execution
			_, err = cfg.sfnClient.StartExecution(context.TODO(), &sfn.StartExecutionInput{
				StateMachineArn: &cfg.stateMachineARN,
				Input:           aws.String(input),
			})
			if err != nil {
				slog.Error("Failed to start the stage machine for the document", "docName", document.Name, "error", err)
				return err
			}
		}

	}
	return nil
}

func init() {
	slog.Debug(">>init")
	defer slog.Debug("<<init")

	var err error
	cfg = &sqsHandlerConfig{}

	cfg.awsCfg, err = config.LoadDefaultConfig(context.TODO())
	if err != nil {
		slog.Error("Failed to load the AWS config", "error", err)
		os.Exit(1)
	}

	cfg.stateMachineARN = os.Getenv("STATE_MACHINE_ARN")
	if cfg.stateMachineARN == "" {
		slog.Error("Failed to get the state machine ARN")
		os.Exit(1)
	}

	// Create a Step Function Client to start the state machine later
	cfg.sfnClient = sfn.NewFromConfig(cfg.awsCfg)

	// Create the S3 client
	cfg.s3Client = s3.NewFromConfig(cfg.awsCfg)

}

func main() {
	slog.Debug(">>main")
	defer slog.Debug("<<main")

	lambda.Start(cfg.process)
}
