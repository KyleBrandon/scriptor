package database

import (
	"fmt"
	"slices"

	stypes "github.com/KyleBrandon/scriptor/pkg/types"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

const (
	DOCUMENT_TABLE                  = "Documents"
	DOCUMENT_PROCESSING_STAGE_TABLE = "DocumentProcessingStage"
	WATCH_CHANNEL_TABLE             = "WatchChannels"
	WATCH_CHANNEL_LOCK_TABLE        = "WatchChannelLocks"
)

type (
	DatabaseStore interface {
		Ping() error
	}

	DocumentStore interface {
		Ping() error
		InsertDocument(document *stypes.Document) error
		GetDocument(id string) (*stypes.Document, error)
		GetDocumentStage(id, stage string) (*stypes.DocumentProcessingStage, error)
		StartDocumentStage(id, stage, originalFileName string) (*stypes.DocumentProcessingStage, error)
		CompleteDocumentStage(stage *stypes.DocumentProcessingStage) error
	}

	DocumentStoreContext struct {
		store *dynamodb.Client
	}

	WatchChannelStore interface {
		Ping() error
		GetWatchChannels() ([]*stypes.WatchChannel, error)
		InsertWatchChannel(watchChannel *stypes.WatchChannel) error
		UpdateWatchChannel(watchChannel *stypes.WatchChannel) error
		GetWatchChannelByID(channelID string) (*stypes.WatchChannel, error)
		CreateChangesToken(channelID, startToken string) error
		AcquireChangesToken(channelID string) (string, error)
		ReleaseChangesToken(channelID, newStartToken string) error
	}

	WatchChannelStoreContext struct {
		store *dynamodb.Client
	}
)

func buildUpdateExpression(input map[string]types.AttributeValue, excludeKeys []string) (string, map[string]types.AttributeValue) {
	updateExpr := "SET "
	exprValues := map[string]types.AttributeValue{}
	i := 0

	for key, value := range input {
		// skip the keys
		if slices.Contains(excludeKeys, key) {
			continue
		}

		placeholder := fmt.Sprintf(":val%d", i)
		updateExpr += fmt.Sprintf("%s = %s, ", key, placeholder)
		exprValues[placeholder] = value
		i++
	}

	// remove trailing comma and space
	updateExpr = updateExpr[:len(updateExpr)-2]
	return updateExpr, exprValues
}
