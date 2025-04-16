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

func (cfg *handlerConfig) getChannelsToRegister(
	watchChannels []*types.WatchChannel,
) ([]*types.WatchChannel, error) {

	register := make([]*types.WatchChannel, 0)

	// We want to check if a watch channel is set to expire now or in the next 4 hours
	// The reason being is that the we only check every 4 hours and the channels are set
	// to expire in 48 hours.  We don't want to miss a channel expiring
	channelRegisterTime := time.Now().UTC().Add(4 * time.Hour).UnixMilli()
	for _, wc := range watchChannels {
		slog.Info("check watch channel for renewal",
			"channel", wc.ChannelID,
			"currentTime", time.Now().UTC().Format(time.RFC3339),
			"channelExpires", wc.ExpiresAt,
			"registerBy", channelRegisterTime,
			"currentURL", cfg.webhookURL,
			"channelURL", wc.WebhookUrl)
		if wc.ExpiresAt <= channelRegisterTime ||
			wc.WebhookUrl != cfg.webhookURL {
			// we need to re-register this channel
			slog.Info("channel has expired or the webhook URL has changed")
			register = append(register, wc)
		}
	}

	return register, nil
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

	var registerWatchChannels []*types.WatchChannel

	// if we have not existing watch channels, then initialize a default one
	if len(watchChannels) == 0 {
		registerWatchChannels, err = cfg.initializeDefaultWatchChannels()
		if err != nil {
			slog.Error(
				"Failed to build the default watch channels",
				"error",
				err,
			)
			return err
		}

	} else {
		// determine which channels need to be re-registered
		registerWatchChannels, err = cfg.getChannelsToRegister(watchChannels)
		if err != nil {
			slog.Error("Failed to determine which channels to re-register", "error", err)
			return err
		}
	}

	for _, wc := range registerWatchChannels {
		err = cfg.dc.CreateWatchChannel(wc, cfg.webhookURL)
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
			continue
		}

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
			continue
		}

		token, err := cfg.dc.GetChangesStartToken()
		if err != nil {
			slog.Error(
				"Failed to get a Google Drive changes start token",
				"error",
				err,
			)
			continue
		}

		// Update/create the watch channel lock
		err = cfg.store.CreateChangesToken(ctx, wc.ChannelID, token)
		if err != nil {
			slog.Error("Failed to save the changes token for the watch ")
			continue
		}
	}

	return nil
}

func main() {
	slog.Debug(">>main")
	defer slog.Debug("<<main")

	lambda.Start(process)
}
