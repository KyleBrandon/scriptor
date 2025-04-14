package database

import (
	"context"
	"errors"
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
		InsertDocument(ctx context.Context, document *stypes.Document) error
		GetDocument(ctx context.Context, id string) (*stypes.Document, error)
		GetDocumentByGoogleID(ctx context.Context, googleFileID string) (*stypes.Document, error)
		GetDocumentStage(ctx context.Context, id string, stage string) (*stypes.DocumentProcessingStage, error)
		StartDocumentStage(ctx context.Context, id string, stage string, originalFileName string) (*stypes.DocumentProcessingStage, error)
		CompleteDocumentStage(ctx context.Context, stage *stypes.DocumentProcessingStage) error
	}

	DocumentStoreContext struct {
		store *dynamodb.Client
	}

	WatchChannelStore interface {
		GetWatchChannels(ctx context.Context) ([]*stypes.WatchChannel, error)
		UpdateWatchChannel(ctx context.Context, watchChannel *stypes.WatchChannel) error
		GetWatchChannelByID(ctx context.Context, channelID string) (*stypes.WatchChannel, error)
		CreateChangesToken(ctx context.Context, channelID, startToken string) error
		AcquireChangesToken(ctx context.Context, channelID string) (string, error)
		ReleaseChangesToken(ctx context.Context, channelID, newStartToken string) error
	}

	WatchChannelStoreContext struct {
		store *dynamodb.Client
	}
)

var (
	ErrDocumentNotFound = errors.New("document not found")
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
