package database

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"strconv"
	"time"

	stypes "github.com/KyleBrandon/scriptor/pkg/types"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

const (
	WATCH_CHANNEL_TABLE_NAME = "WatchChannelTable"
	DOCUMENT_TABLE_NAME      = "DocumentsTable"
)

type WatchChannelStore interface {
	DoesWatchChannelExist(folderID string) (bool, error)
	InsertWatchChannel(watchChannel stypes.WatchChannel) error
	UpdateWatchChannel(watchChannel stypes.WatchChannel) error
	GetWatchChannelByFolder(folderID string) (stypes.WatchChannel, error)
	GetActiveWatchChannels() ([]stypes.WatchChannel, error)
	GetWatchChannels() ([]stypes.WatchChannel, error)
}

type DynamoDBClient struct {
	store *dynamodb.Client
}

func NewDynamoDBClient() (WatchChannelStore, error) {
	awsCfg, err := config.LoadDefaultConfig(context.TODO())
	if err != nil {
		slog.Error("Failed to configure the DynamoDBClient", "error", err)
		return nil, err
	}

	db := dynamodb.NewFromConfig(awsCfg)

	return &DynamoDBClient{
		store: db,
	}, nil
}

func (u DynamoDBClient) GetWatchChannels() ([]stypes.WatchChannel, error) {
	scanInput := &dynamodb.ScanInput{
		TableName: aws.String(WATCH_CHANNEL_TABLE_NAME),
	}

	// Execute Scan
	result, err := u.store.Scan(context.TODO(), scanInput)
	if err != nil {
		return nil, fmt.Errorf("failed to scan watch channels: %w", err)
	}

	// Convert DynamoDB result into a slice of WatchChannels
	var wcs []stypes.WatchChannel
	err = attributevalue.UnmarshalListOfMaps(result.Items, &wcs)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal DynamoDB items: %w", err)
	}

	return wcs, nil

}

// Does this user exist?
func (u DynamoDBClient) DoesWatchChannelExist(folderID string) (bool, error) {
	result, err := u.store.GetItem(context.TODO(), &dynamodb.GetItemInput{
		TableName: aws.String(WATCH_CHANNEL_TABLE_NAME),
		Key: map[string]types.AttributeValue{
			"folder_id": &types.AttributeValueMemberS{Value: folderID},
		},
	})

	if err != nil {
		// return 'true' so that the calling code does not accidentally try to create a user
		return true, err
	}

	if result.Item == nil {
		return false, nil
	}

	return true, nil
}

func (u DynamoDBClient) InsertWatchChannel(watchChannel stypes.WatchChannel) error {

	// Create DynamoDB Table
	item := &dynamodb.PutItemInput{
		TableName: aws.String(WATCH_CHANNEL_TABLE_NAME),
		Item: map[string]types.AttributeValue{
			"folder_id":             &types.AttributeValueMemberS{Value: watchChannel.FolderID},
			"archive_folder_id":     &types.AttributeValueMemberS{Value: watchChannel.ArchiveFolderID},
			"destination_folder_id": &types.AttributeValueMemberS{Value: watchChannel.DestinationFolderID},
			"channel_id":            &types.AttributeValueMemberS{Value: watchChannel.ChannelID},
			"expires_at":            &types.AttributeValueMemberN{Value: strconv.FormatInt(watchChannel.ExpiresAt, 10)},
			"webhook_url":           &types.AttributeValueMemberS{Value: watchChannel.WebhookUrl},
		},
	}

	_, err := u.store.PutItem(context.TODO(), item)
	if err != nil {
		return err
	}

	return nil
}

func (u DynamoDBClient) UpdateWatchChannel(watchChannel stypes.WatchChannel) error {

	// Define the primary key
	key := map[string]types.AttributeValue{
		"folder_id": &types.AttributeValueMemberS{Value: watchChannel.FolderID},
	}

	// Define the update expression
	updateExpression := `SET
			channel_id = :channel_id,
			archive_folder_id = :archive_folder_id,
			destination_folder_id = :destination_folder_id,
			expires_at = :expires_at,
			webhook_url = :webhook_url`

	// Define the new values
	expressionAttributeValues := map[string]types.AttributeValue{
		":channel_id":            &types.AttributeValueMemberS{Value: watchChannel.ChannelID},
		":archive_folder_id":     &types.AttributeValueMemberS{Value: watchChannel.ArchiveFolderID},
		":destination_folder_id": &types.AttributeValueMemberS{Value: watchChannel.DestinationFolderID},
		":expires_at":            &types.AttributeValueMemberN{Value: strconv.FormatInt(watchChannel.ExpiresAt, 10)},
		":webhook_url":           &types.AttributeValueMemberS{Value: watchChannel.WebhookUrl},
	}

	// Build the update input
	input := &dynamodb.UpdateItemInput{
		TableName:                 aws.String(WATCH_CHANNEL_TABLE_NAME),
		Key:                       key,
		UpdateExpression:          aws.String(updateExpression),
		ExpressionAttributeValues: expressionAttributeValues,
		ReturnValues:              types.ReturnValueUpdatedNew, // Return the updated attributes
	}

	// Perform the update
	_, err := u.store.UpdateItem(context.TODO(), input)
	if err != nil {
		log.Printf("Failed to update item: %v", err)
		return err
	}

	return nil
}

func (u DynamoDBClient) GetWatchChannelByFolder(folderID string) (stypes.WatchChannel, error) {
	var wc stypes.WatchChannel

	result, err := u.store.GetItem(context.TODO(), &dynamodb.GetItemInput{
		TableName: aws.String(WATCH_CHANNEL_TABLE_NAME),
		Key: map[string]types.AttributeValue{
			"folder_id": &types.AttributeValueMemberS{Value: folderID},
		},
	})

	if err != nil {
		return wc, err
	}

	if result.Item == nil {
		return wc, fmt.Errorf("watch channel not found")
	}

	err = attributevalue.UnmarshalMap(result.Item, &wc)
	if err != nil {
		return wc, err
	}

	return wc, nil
}

func (u DynamoDBClient) GetActiveWatchChannels() ([]stypes.WatchChannel, error) {
	expiresAt := time.Now().UnixMilli()

	queryInput := &dynamodb.QueryInput{
		TableName:              aws.String(WATCH_CHANNEL_TABLE_NAME),
		IndexName:              aws.String("ExpiresAtIndex"), // Query the GSI
		KeyConditionExpression: aws.String("expires_at >= :expires"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":expires": &types.AttributeValueMemberS{Value: fmt.Sprintf("%d", expiresAt)},
		},
	}

	result, err := u.store.Query(context.TODO(), queryInput)
	if err != nil {
		log.Fatalf("Failed to query non-expired watch channels: %v", err)
	}

	// Convert DynamoDB result into a slice of WatchChannels
	var wcs []stypes.WatchChannel
	err = attributevalue.UnmarshalListOfMaps(result.Items, &wcs)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal DynamoDB items: %w", err)
	}

	return wcs, nil
}
