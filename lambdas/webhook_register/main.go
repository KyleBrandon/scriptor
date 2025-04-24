package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/KyleBrandon/scriptor/lambdas/util"
	"github.com/KyleBrandon/scriptor/pkg/database"
	"github.com/KyleBrandon/scriptor/pkg/google"
	"github.com/KyleBrandon/scriptor/pkg/types"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/google/uuid"
)

type handlerConfig struct {
	store           database.WatchChannelStore
	dc              *google.GoogleDriveContext
	webhookURL      string
	folderLocations *types.GoogleFolderDefaultLocations
}

var (
	initOnce sync.Once
	cfg      *handlerConfig
)

// Load all the inital configuration settings for the lambda
func loadConfiguration(ctx context.Context) (*handlerConfig, error) {

	cfg = &handlerConfig{}

	var err error

	awsCfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		slog.Error("failed to load the AWS config", "error", err)
		return nil, err
	}

	cfg.webhookURL = os.Getenv("WEBHOOK_URL")
	if cfg.webhookURL == "" {
		return nil, fmt.Errorf(
			"failed to read the webhook URL from the environment",
		)
	}

	cfg.store, err = database.NewWatchChannelStore(ctx)
	if err != nil {
		slog.Error("Failed to configure the DynamoDB client", "error", err)
		return nil, err
	}

	cfg.dc, err = google.NewGoogleDrive(ctx)
	if err != nil {
		//
		slog.Error(
			"Failed to initialize the Google Drive service context",
			"error",
			err,
		)
		return nil, err
	}

	cfg.folderLocations, err = util.GetDefaultFolderLocations(ctx, awsCfg)
	if err != nil {
		slog.Error("Failed to get the default folder locations", "error", err)
		return nil, err
	}

	return cfg, nil
}

// Ensure that the configuration settings are only loaded once
func initLambda(ctx context.Context) error {
	var err error
	initOnce.Do(func() {
		slog.Debug(">>initLambda")
		defer slog.Debug("<<initLambda")

		cfg, err = loadConfiguration(ctx)
	})

	return err
}

func (cfg *handlerConfig) initializeDefaultWatchChannels() ([]*types.WatchChannel, error) {
	slog.Debug(">>seedWatchChannels")
	defer slog.Debug("<<seedWatchChannels")

	wcs := make([]*types.WatchChannel, 0)

	// Create a watch channel entry in the DB
	wcs = append(wcs, &types.WatchChannel{
		FolderID:            cfg.folderLocations.FolderID,
		ArchiveFolderID:     cfg.folderLocations.ArchiveFolderID,
		DestinationFolderID: cfg.folderLocations.DestFolderID,
		CreatedAt:           time.Now().UTC(),
	})

	return wcs, nil
}

func (cfg *handlerConfig) registerWatchChannel(ctx context.Context, wc *types.WatchChannel) error {

	// create the channel
	resourceID, err := cfg.dc.CreateWatchChannel(wc)
	if err != nil {
		slog.Error(
			"Failed to create the watch channel",
			"folderID",
			wc.FolderID,
			"channelID",
			wc.ChannelID,
			"error",
			err,
		)
		return err
	}

	// save the resource id with the watch channel
	wc.ResourceID = resourceID

	// Update the watch channel in the database
	err = cfg.store.UpdateWatchChannel(ctx, wc)
	if err != nil {
		slog.Error(
			"Failed to create or update the watch channel",
			"folderID",
			wc.FolderID,
			"channelID",
			wc.ChannelID,
			"error",
			err,
		)
		return err
	}

	return nil
}

func (cfg *handlerConfig) initializeWatchChannelLock(
	ctx context.Context,
	wc *types.WatchChannel,
	existingStartToken string,
) error {

	// see if we have an existing lock table for the channel
	existingWatchChannel, err := cfg.store.GetWatchChannelLock(ctx, wc.ChannelID)
	if err == nil {
		// we have an existing lock for the channel, keep it so we pick up any changes from the last time it was updated
		slog.Info("Watch channel lock exists, use it", "channelID", wc.ChannelID, "watchChannel", existingWatchChannel)
		return nil
	}

	if existingStartToken == "" {
		existingStartToken, err = cfg.dc.GetChangesStartToken()
		if err != nil {
			slog.Error(
				"Failed to get a Google Drive changes start token",
				"error",
				err,
			)
			return err
		}
	}

	// create the watch channel lock
	err = cfg.store.CreateWatchChannelLock(ctx, wc.ChannelID, existingStartToken)
	if err != nil {
		slog.Error("Failed to save the changes token for the watch ")
		return err
	}

	return nil
}

func process(ctx context.Context) error {
	slog.Debug(">>registerWebhook")
	defer slog.Debug("<<registerWebhook")

	if err := initLambda(ctx); err != nil {
		slog.Error("Failed to initialize the lambda", "error", err)
		return err
	}

	watchChannels, err := cfg.store.GetWatchChannels(ctx)
	if err != nil {
		slog.Error(
			"Failed to get the list of active watch channels",
			"error",
			err,
		)
		return err
	}

	// if we have not existing watch channels, then initialize a default one
	if len(watchChannels) == 0 {
		watchChannels, err = cfg.initializeDefaultWatchChannels()
		if err != nil {
			slog.Error(
				"Failed to build the default watch channels",
				"error",
				err,
			)
			return err
		}
	}

	// register or re-register the watch channels
	for _, wc := range watchChannels {
		existingToken := ""

		// if we have an existing watch channel, stop it before creating a new one
		if wc.ChannelID != "" {
			cfg.dc.StopWatchChannel(wc.ChannelID, wc.ResourceID)

			existingLock, err := cfg.store.GetWatchChannelLock(ctx, wc.ChannelID)
			if err == nil {
				// save the existing token to represent the last time we processed changes
				existingToken = existingLock.ChangesStartToken

				// delete the old channel lock
				cfg.store.DeleteWatchChannelLock(ctx, wc.ChannelID)
			}
		}

		// create a new channel
		wc.ChannelID = uuid.New().String()
		wc.ExpiresAt = time.Now().UTC().Add(48 * time.Hour).UnixMilli()
		wc.WebhookUrl = cfg.webhookURL

		// register the new channel
		err = cfg.registerWatchChannel(ctx, wc)
		if err != nil {
			slog.Error(
				"Failed to register the watch channel",
				"channelID",
				wc.ChannelID,
				"folderID",
				wc.FolderID,
				"error",
				err,
			)
		}

		// get an initial token for changes
		err = cfg.initializeWatchChannelLock(ctx, wc, existingToken)
		if err != nil {
			slog.Error(
				"Failed to register the watch channel lock",
				"channelID",
				wc.ChannelID,
				"folderID",
				wc.FolderID,
				"error",
				err,
			)
		}
	}

	return nil
}

func main() {
	slog.Debug(">>main")
	defer slog.Debug("<<main")

	lambda.Start(process)
}
