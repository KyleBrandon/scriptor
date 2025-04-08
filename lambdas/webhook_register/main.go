package main

import (
	"log/slog"
	"os"
	"time"

	"github.com/KyleBrandon/scriptor/lambdas/util"
	"github.com/KyleBrandon/scriptor/pkg/database"
	"github.com/KyleBrandon/scriptor/pkg/google"
	"github.com/KyleBrandon/scriptor/pkg/types"
	"github.com/aws/aws-lambda-go/lambda"
)

type registerConfig struct {
	store      database.WatchChannelStore
	dc         *google.GoogleDriveContext
	webhookURL string
}

var cfg *registerConfig

func (cfg *registerConfig) initializeDefaultWatchChannels() ([]*types.WatchChannel, error) {
	slog.Debug(">>seedWatchChannels")
	defer slog.Debug("<<seedWatchChannels")

	wcs := make([]*types.WatchChannel, 0)

	folderLocations, err := util.GetDefaultFolderLocations()
	if err != nil {
		slog.Error("Failed to get the default folder locations", "error", err)
		return wcs, err
	}

	// Create a watch channel entry in the DB
	wcs = append(wcs, &types.WatchChannel{
		FolderID:            folderLocations.FolderID,
		ArchiveFolderID:     folderLocations.ArchiveFolderID,
		DestinationFolderID: folderLocations.DestFolderID,
	})

	return wcs, nil
}

func (cfg *registerConfig) getChannelsToRegister(watchChannels []*types.WatchChannel) ([]*types.WatchChannel, error) {

	register := make([]*types.WatchChannel, 0)

	// We want to check if a watch channel is set to expire now or in the next 4 hours
	// The reason being is that the we only check every 4 hours and the channels are set
	// to expire in 48 hours.  We don't want to miss a channel expiring
	channelRegisterTime := time.Now().Add(4 * time.Hour).UnixMilli()
	for _, wc := range watchChannels {
		slog.Info("check watch channel for renewal",
			"channel", wc.ChannelID,
			"currentTime", channelRegisterTime,
			"channelExpires", wc.ExpiresAt,
			"currentURL", cfg.webhookURL,
			"channelURL", wc.WebhookUrl)
		if wc.ExpiresAt <= channelRegisterTime || wc.WebhookUrl != cfg.webhookURL {
			// we need to re-register this channel
			slog.Info("channel has expired or the webhook URL has changed")
			register = append(register, wc)
		}
	}

	return register, nil
}

func (cfg *registerConfig) registerWebhook() {
	slog.Debug(">>registerWebhook")
	defer slog.Debug("<<registerWebhook")

	var err error

	// Create a storage client if we don't have one
	cfg.store, err = util.VerifyWatchChannelStore(cfg.store)
	if err != nil {
		os.Exit(1)
	}

	// Create a Google Drive service if we don't have one
	cfg.dc, err = util.VerifyDriveContext(cfg.dc)
	if err != nil {
		os.Exit(1)
	}

	watchChannels, err := cfg.store.GetWatchChannels()
	if err != nil {
		slog.Error("Failed to get the list of active watch channels", "error", err)
		os.Exit(1)
	}

	var registerWatchChannels []*types.WatchChannel

	// if we have not existing watch channels, then initialize a default one
	if len(watchChannels) == 0 {
		registerWatchChannels, err = cfg.initializeDefaultWatchChannels()
		if err != nil {
			slog.Error("Failed to build the default watch channels", "error", err)
			os.Exit(1)
		}

	} else {
		// determine which channels need to be re-registered
		registerWatchChannels, err = cfg.getChannelsToRegister(watchChannels)
		if err != nil {
			slog.Error("Failed to determine which channels to re-register", "error", err)
			os.Exit(1)
		}
	}

	for _, wc := range registerWatchChannels {
		err = cfg.dc.CreateWatchChannel(wc, cfg.webhookURL)
		if err != nil {
			slog.Error("Failed to create the watch channel", "folderID", wc.FolderID, "channelID", wc.ChannelID, "error", err)
			continue
		}

		// Update the watch channel in the database
		err = cfg.store.UpdateWatchChannel(wc)
		if err != nil {
			slog.Error("Failed to create or update the watch channel", "folderID", wc.FolderID, "channelID", wc.ChannelID, "error", err)
			continue
		}

		token, err := cfg.dc.GetChangesStartToken()
		if err != nil {
			slog.Error("Failed to get a Google Drive changes start token", "error", err)
			continue
		}

		// Update/create the watch channel lock
		err = cfg.store.CreateChangesToken(wc.ChannelID, token)
		if err != nil {
			slog.Error("Failed to save the changes token for the watch ")
			continue
		}
	}
}

func init() {
	slog.Debug(">>init")
	defer slog.Debug("<<init")

	webhookURL := os.Getenv("WEBHOOK_URL")
	if webhookURL == "" {
		slog.Error("webhook URL not configured")
		os.Exit(1)
	}

	cfg = &registerConfig{
		webhookURL: webhookURL,
	}
}

func main() {
	slog.Debug(">>main")
	defer slog.Debug("<<main")

	lambda.Start(cfg.registerWebhook)
}
