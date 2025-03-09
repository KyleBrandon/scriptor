package database

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
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
	Ping() error
	GetWatchChannels() ([]stypes.WatchChannel, error)
	InsertWatchChannel(watchChannel stypes.WatchChannel) error
	UpdateWatchChannel(watchChannel stypes.WatchChannel) error
	GetWatchChannelByChannel(folderID string) (stypes.WatchChannel, error)
	InsertDocument(document stypes.Document) error
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

func (u DynamoDBClient) Ping() error {
	// perform a quick query to see if the db is up.
	_, err := u.GetWatchChannels()

	return err
}

func (u DynamoDBClient) InsertDocument(document stypes.Document) error {

	av, err := attributevalue.MarshalMap(document)
	if err != nil {
		slog.Error("Failed to marshal the document", "error", err)
		return err
	}

	item := &dynamodb.PutItemInput{
		TableName: aws.String(DOCUMENT_TABLE_NAME),
		Item:      av,
	}

	_, err = u.store.PutItem(context.TODO(), item)
	if err != nil {
		slog.Error("Failed to insert the document", "error", err)
		return err
	}

	return nil

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

func (u DynamoDBClient) InsertWatchChannel(watchChannel stypes.WatchChannel) error {

	watchChannel.CreatedAt = time.Now().UTC()

	av, err := attributevalue.MarshalMap(watchChannel)
	if err != nil {
		slog.Error("Failed to marshal the document", "error", err)
		return err
	}

	// Create DynamoDB Table
	item := &dynamodb.PutItemInput{
		TableName: aws.String(WATCH_CHANNEL_TABLE_NAME),
		Item:      av,
	}

	_, err = u.store.PutItem(context.TODO(), item)
	if err != nil {
		slog.Error("Failed to insert the watch channel", "error", err)
		return err
	}

	return nil
}

func (u DynamoDBClient) UpdateWatchChannel(watchChannel stypes.WatchChannel) error {

	watchChannel.UpdatedAt = time.Now().UTC()

	// Define the primary key
	key := map[string]types.AttributeValue{
		"folder_id": &types.AttributeValueMemberS{Value: watchChannel.FolderID},
	}

	av, err := attributevalue.MarshalMap(watchChannel)
	if err != nil {
		slog.Error("Failed to marshal the document", "error", err)
		return err
	}
	updateExpression, expressionAttributeValues := buildUpdateExpression(av, []string{"folder_id"})

	// Build the update input
	input := &dynamodb.UpdateItemInput{
		TableName:                 aws.String(WATCH_CHANNEL_TABLE_NAME),
		Key:                       key,
		UpdateExpression:          aws.String(updateExpression),
		ExpressionAttributeValues: expressionAttributeValues,
		ReturnValues:              types.ReturnValueUpdatedNew, // Return the updated attributes
	}

	// Perform the update
	_, err = u.store.UpdateItem(context.TODO(), input)
	if err != nil {
		slog.Error("Failed to update item", "error", err)
		return err
	}

	return nil
}

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

func (u DynamoDBClient) GetWatchChannelByChannel(channelID string) (stypes.WatchChannel, error) {
	var wc stypes.WatchChannel

	queryInput := &dynamodb.QueryInput{
		TableName:              aws.String(WATCH_CHANNEL_TABLE_NAME),
		IndexName:              aws.String("ChannelIDIndex"),
		KeyConditionExpression: aws.String("channel_id = :channelID"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":channelID": &types.AttributeValueMemberS{Value: channelID},
		},
	}

	result, err := u.store.Query(context.TODO(), queryInput)
	if err != nil {
		return wc, err
	}
	if len(result.Items) == 0 {
		return wc, fmt.Errorf("watch channel not found")
	}

	var wcs []stypes.WatchChannel

	err = attributevalue.UnmarshalListOfMaps(result.Items, &wcs)
	if err != nil {
		return wc, err
	}

	return wcs[0], nil
}
