package database

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	stypes "github.com/KyleBrandon/scriptor/pkg/types"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

func NewWatchChannelStore() (WatchChannelStore, error) {
	awsCfg, err := config.LoadDefaultConfig(context.TODO())
	if err != nil {
		slog.Error("Failed to configure the WatchChannelStoreContext", "error", err)
		return nil, err
	}

	store := dynamodb.NewFromConfig(awsCfg)

	return &WatchChannelStoreContext{
		store,
	}, nil
}

func (db *WatchChannelStoreContext) Ping() error {
	// perform a quick query to see if the db is up.
	_, err := db.GetWatchChannels()

	return err
}

func (db *WatchChannelStoreContext) GetWatchChannels() ([]*stypes.WatchChannel, error) {
	scanInput := &dynamodb.ScanInput{
		TableName: aws.String(WATCH_CHANNEL_TABLE),
	}

	// Execute Scan
	result, err := db.store.Scan(context.TODO(), scanInput)
	if err != nil {
		return nil, fmt.Errorf("failed to scan watch channels: %w", err)
	}

	// Convert DynamoDB result into a slice of WatchChannels
	var wcs []stypes.WatchChannel
	err = attributevalue.UnmarshalListOfMaps(result.Items, &wcs)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal DynamoDB items: %w", err)
	}

	results := make([]*stypes.WatchChannel, 0)
	for _, wc := range wcs {
		results = append(results, &wc)
	}

	return results, nil

}

func (db *WatchChannelStoreContext) InsertWatchChannel(watchChannel *stypes.WatchChannel) error {

	watchChannel.CreatedAt = time.Now().UTC()

	av, err := attributevalue.MarshalMap(watchChannel)
	if err != nil {
		slog.Error("Failed to marshal the document", "error", err)
		return err
	}

	// Create DynamoDB Table
	item := &dynamodb.PutItemInput{
		TableName: aws.String(WATCH_CHANNEL_TABLE),
		Item:      av,
	}

	_, err = db.store.PutItem(context.TODO(), item)
	if err != nil {
		slog.Error("Failed to insert the watch channel", "error", err)
		return err
	}

	return nil
}

func (db *WatchChannelStoreContext) UpdateWatchChannel(watchChannel *stypes.WatchChannel) error {

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
		TableName:                 aws.String(WATCH_CHANNEL_TABLE),
		Key:                       key,
		UpdateExpression:          aws.String(updateExpression),
		ExpressionAttributeValues: expressionAttributeValues,
		ReturnValues:              types.ReturnValueUpdatedNew, // Return the updated attributes
	}

	// Perform the update
	_, err = db.store.UpdateItem(context.TODO(), input)
	if err != nil {
		slog.Error("Failed to update item", "error", err)
		return err
	}

	return nil
}

func (db *WatchChannelStoreContext) GetWatchChannelByID(channelID string) (*stypes.WatchChannel, error) {

	queryInput := &dynamodb.QueryInput{
		TableName:              aws.String(WATCH_CHANNEL_TABLE),
		IndexName:              aws.String("ChannelIDIndex"),
		KeyConditionExpression: aws.String("channel_id = :channelID"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":channelID": &types.AttributeValueMemberS{Value: channelID},
		},
	}

	result, err := db.store.Query(context.TODO(), queryInput)
	if err != nil {
		return nil, err
	}
	if len(result.Items) == 0 {
		return nil, fmt.Errorf("watch channel not found")
	}

	var wcs []stypes.WatchChannel

	err = attributevalue.UnmarshalListOfMaps(result.Items, &wcs)
	if err != nil {
		return nil, err
	}

	return &wcs[0], nil
}

func (db *WatchChannelStoreContext) CreateChangesToken(channelID, startToken string) error {

	_, err := db.store.UpdateItem(context.TODO(), &dynamodb.UpdateItemInput{
		TableName: aws.String(WATCH_CHANNEL_LOCK_TABLE),
		Key: map[string]types.AttributeValue{
			"channel_id": &types.AttributeValueMemberS{Value: channelID},
		},
		UpdateExpression: aws.String("SET locked = :false, changes_start_token = :token"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":false": &types.AttributeValueMemberBOOL{Value: false},
			":token": &types.AttributeValueMemberS{Value: startToken},
		},
	})
	if err != nil {
		slog.Error("Failed to create the changes token", "channelID", channelID, "error", err)
		return err
	}

	return nil
}

func (db *WatchChannelStoreContext) AcquireChangesToken(channelID string) (string, error) {
	now := time.Now().UnixMilli()
	leaseUntil := now + (30 * time.Second).Milliseconds()

	result, err := db.store.UpdateItem(context.TODO(), &dynamodb.UpdateItemInput{
		TableName: aws.String(WATCH_CHANNEL_LOCK_TABLE),
		Key: map[string]types.AttributeValue{
			"channel_id": &types.AttributeValueMemberS{Value: channelID},
		},
		UpdateExpression:    aws.String("SET locked = :true, lock_expires = :leaseUntil"),
		ConditionExpression: aws.String("locked = :false OR lock_expires < :now"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":true":       &types.AttributeValueMemberBOOL{Value: true},
			":false":      &types.AttributeValueMemberBOOL{Value: false},
			":now":        &types.AttributeValueMemberN{Value: fmt.Sprintf("%d", now)},
			":leaseUntil": &types.AttributeValueMemberN{Value: fmt.Sprintf("%d", leaseUntil)},
		},
		ReturnValues: types.ReturnValueAllNew,
	})

	if err != nil {
		slog.Error("Failed to acquire the changes token", "channelID", channelID, "error", err)

		var ccfe *types.ConditionalCheckFailedException
		if errors.As(err, &ccfe) {
			return "", fmt.Errorf("lock is currently held")
		}

		return "", err
	}

	if tokenAttr, ok := result.Attributes["changes_start_token"].(*types.AttributeValueMemberS); ok {

		return tokenAttr.Value, nil
	}

	return "", fmt.Errorf("changes_start_token attribute not found or invalid")
}

func (db *WatchChannelStoreContext) ReleaseChangesToken(channelID, newStartToken string) error {

	updateItemInput := &dynamodb.UpdateItemInput{
		TableName: aws.String(WATCH_CHANNEL_LOCK_TABLE),
		Key: map[string]types.AttributeValue{
			"channel_id": &types.AttributeValueMemberS{Value: channelID},
		},
		UpdateExpression: aws.String("SET locked = :false, changes_start_token = :new_start_token"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":false": &types.AttributeValueMemberBOOL{Value: false},
		},
	}

	// if we have a new start token then update it as well
	if newStartToken != "" {
		updateItemInput.ExpressionAttributeValues[":new_start_token"] =
			&types.AttributeValueMemberS{Value: newStartToken}
	}

	_, err := db.store.UpdateItem(context.TODO(), updateItemInput)

	if err != nil {
		var ccfe *types.ConditionalCheckFailedException
		if errors.As(err, &ccfe) {
			return fmt.Errorf("lock is currently held")
		}

		return err
	}

	return nil
}
