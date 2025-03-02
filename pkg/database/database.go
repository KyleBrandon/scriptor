package database

import (
	"fmt"
	"log"
	"strconv"
	"time"

	"github.com/KyleBrandon/scriptor/pkg/types"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/dynamodb"
	"github.com/aws/aws-sdk-go/service/dynamodb/dynamodbattribute"
)

const (
	WATCH_CHANNEL_TABLE_NAME = "WatchChannelTable"
	DOCUMENT_TABLE_NAME      = "DocumentsTable"
)

type WatchChannelStore interface {
	DoesWatchChannelExist(folderID string) (bool, error)
	InsertWatchChannel(watchChannel types.WatchChannel) error
	UpdateWatchChannel(watchChannel types.WatchChannel) error
	GetWatchChannelByFolder(folderID string) (types.WatchChannel, error)
	GetActiveWatchChannels() ([]types.WatchChannel, error)
	GetWatchChannels() ([]types.WatchChannel, error)
}

type DynamoDBClient struct {
	store *dynamodb.DynamoDB
}

func NewDynamoDBClient() WatchChannelStore {
	dbSession := session.Must(session.NewSession())
	db := dynamodb.New(dbSession)

	return DynamoDBClient{
		store: db,
	}
}

func (u DynamoDBClient) GetWatchChannels() ([]types.WatchChannel, error) {
	scanInput := &dynamodb.ScanInput{
		TableName: aws.String(WATCH_CHANNEL_TABLE_NAME),
	}

	// Execute Scan
	result, err := u.store.Scan(scanInput)
	if err != nil {
		return nil, fmt.Errorf("failed to scan watch channels: %w", err)
	}

	// Convert DynamoDB result into a slice of WatchChannels
	var wcs []types.WatchChannel
	err = dynamodbattribute.UnmarshalListOfMaps(result.Items, &wcs)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal DynamoDB items: %w", err)
	}

	return wcs, nil

}

// Does this user exist?
func (u DynamoDBClient) DoesWatchChannelExist(folderID string) (bool, error) {
	result, err := u.store.GetItem(&dynamodb.GetItemInput{
		TableName: aws.String(WATCH_CHANNEL_TABLE_NAME),
		Key: map[string]*dynamodb.AttributeValue{
			"folder_id": {
				S: aws.String(folderID),
			},
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

func (u DynamoDBClient) InsertWatchChannel(watchChannel types.WatchChannel) error {

	// Create DynamoDB Table
	item := &dynamodb.PutItemInput{
		TableName: aws.String(WATCH_CHANNEL_TABLE_NAME),
		Item: map[string]*dynamodb.AttributeValue{
			"folder_id": {
				S: aws.String(watchChannel.FolderID),
			},
			"archive_folder_id": {
				S: aws.String(watchChannel.ArchiveFolderID),
			},
			"destination_folder_id": {
				S: aws.String(watchChannel.DestinationFolderID),
			},
			"channel_id": {
				S: aws.String(watchChannel.ChannelID),
			},
			"expires_at": {
				N: aws.String(strconv.FormatInt(watchChannel.ExpiresAt, 10)),
			},
			"webhook_url": {
				S: aws.String(watchChannel.WebhookUrl),
			},
		},
	}

	_, err := u.store.PutItem(item)
	if err != nil {
		return err
	}

	return nil
}

func (u DynamoDBClient) UpdateWatchChannel(watchChannel types.WatchChannel) error {

	// Define the primary key
	key := map[string]*dynamodb.AttributeValue{
		"folder_id": {
			S: aws.String(watchChannel.FolderID),
		},
	}

	// Define the update expression
	updateExpression := `SET 
		channel_id = :channel_id,
		archive_folder_id = :archive_folder_id,
		destination_folder_id = :destination_folder_id,
		expires_at = :expires_at,
		webhook_url = :webhook_url`

	// Define the new values
	expressionAttributeValues := map[string]*dynamodb.AttributeValue{
		":channel_id": {
			S: aws.String(watchChannel.ChannelID),
		},
		":archive_folder_id": {
			S: aws.String(watchChannel.ArchiveFolderID),
		},
		":destination_folder_id": {
			S: aws.String(watchChannel.DestinationFolderID),
		},
		":expires_at": {
			N: aws.String(strconv.FormatInt(watchChannel.ExpiresAt, 10)),
		},
		":webhook_url": {
			S: aws.String(watchChannel.WebhookUrl),
		},
	}

	// Build the update input
	input := &dynamodb.UpdateItemInput{
		TableName:                 aws.String(WATCH_CHANNEL_TABLE_NAME),
		Key:                       key,
		UpdateExpression:          aws.String(updateExpression),
		ExpressionAttributeValues: expressionAttributeValues,
		ReturnValues:              aws.String("UPDATED_NEW"), // Return the updated attributes
	}

	// Perform the update
	_, err := u.store.UpdateItem(input)
	if err != nil {
		log.Printf("Failed to update item: %v", err)
		return err
	}

	return nil
}

func (u DynamoDBClient) GetWatchChannelByFolder(folderID string) (types.WatchChannel, error) {
	var wc types.WatchChannel

	result, err := u.store.GetItem(&dynamodb.GetItemInput{
		TableName: aws.String(WATCH_CHANNEL_TABLE_NAME),
		Key: map[string]*dynamodb.AttributeValue{
			"folder_id": {
				S: aws.String(folderID),
			},
		},
	})

	if err != nil {
		return wc, err
	}

	if result.Item == nil {
		return wc, fmt.Errorf("watch channel not found")
	}

	err = dynamodbattribute.UnmarshalMap(result.Item, &wc)
	if err != nil {
		return wc, err
	}

	return wc, nil
}

func (u DynamoDBClient) GetActiveWatchChannels() ([]types.WatchChannel, error) {
	expiresAt := time.Now().UnixMilli()

	queryInput := &dynamodb.QueryInput{
		TableName:              aws.String(WATCH_CHANNEL_TABLE_NAME),
		IndexName:              aws.String("ExpiresAtIndex"), // Query the GSI
		KeyConditionExpression: aws.String("expires_at >= :expires"),
		ExpressionAttributeValues: map[string]*dynamodb.AttributeValue{
			":expires": {
				N: aws.String(fmt.Sprintf("%d", expiresAt)),
			},
		},
	}

	result, err := u.store.Query(queryInput)
	if err != nil {
		log.Fatalf("Failed to query non-expired watch channels: %v", err)
	}

	// Convert DynamoDB result into a slice of WatchChannels
	var wcs []types.WatchChannel
	err = dynamodbattribute.UnmarshalListOfMaps(result.Items, &wcs)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal DynamoDB items: %w", err)
	}

	return wcs, nil
}
