package database

import (
	"context"
	"log/slog"
	"time"

	stypes "github.com/KyleBrandon/scriptor/pkg/types"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

func NewDocumentStore() (DocumentStore, error) {
	awsCfg, err := config.LoadDefaultConfig(context.TODO())
	if err != nil {
		slog.Error("Failed to configure the DocumentStoreContext ", "error", err)
		return nil, err
	}

	store := dynamodb.NewFromConfig(awsCfg)

	return &DocumentStoreContext{
		store,
	}, nil
}

func (db *DocumentStoreContext) Ping() error {
	// perform a quick query to see if the db is up.
	// _, err := u.GetWatchChannels()

	// return err
	// TODO: FIXME
	return nil
}

func (db *DocumentStoreContext) GetDocument(id string) (*stypes.Document, error) {

	ret := &stypes.Document{}

	getItemInput := &dynamodb.GetItemInput{
		TableName: aws.String(DOCUMENT_TABLE),
		Key: map[string]types.AttributeValue{
			"id": &types.AttributeValueMemberS{Value: id},
		},
	}

	result, err := db.store.GetItem(context.TODO(), getItemInput)
	if err != nil {
		slog.Error("Failed to query the document", "error", err)
		return ret, err
	}

	err = attributevalue.UnmarshalMap(result.Item, ret)
	if err != nil {
		slog.Error("Failed to unmarshal the document", "error", err)
		return ret, err
	}

	return ret, nil
}

func (db *DocumentStoreContext) InsertDocument(document *stypes.Document) error {

	av, err := attributevalue.MarshalMap(document)
	if err != nil {
		slog.Error("Failed to marshal the document", "error", err)
		return err
	}

	item := &dynamodb.PutItemInput{
		TableName: aws.String(DOCUMENT_TABLE),
		Item:      av,
	}

	_, err = db.store.PutItem(context.TODO(), item)
	if err != nil {
		slog.Error("Failed to insert the document", "error", err)
		return err
	}

	return nil

}

func (db *DocumentStoreContext) GetDocumentStage(id, stage string) (*stypes.DocumentProcessingStage, error) {
	ret := &stypes.DocumentProcessingStage{}

	key := map[string]types.AttributeValue{
		"id":    &types.AttributeValueMemberS{Value: id},
		"stage": &types.AttributeValueMemberS{Value: stage},
	}

	item := &dynamodb.GetItemInput{
		TableName: aws.String(DOCUMENT_PROCESSING_STAGE_TABLE),
		Key:       key,
	}

	result, err := db.store.GetItem(context.TODO(), item)
	if err != nil {
		slog.Error("Failed to find the document processing stage", "id", id, "stage", stage, "error", err)
		return ret, err
	}

	// Convert DynamoDB result into a slice of WatchChannels
	err = attributevalue.UnmarshalMap(result.Item, ret)
	if err != nil {
		slog.Error("Failed to unmarshal the document processing stage", "error", err)
		return ret, err
	}

	return ret, nil
}

func (db *DocumentStoreContext) insertDocumentStage(stage *stypes.DocumentProcessingStage) error {

	stage.StartedAt = time.Now().UTC()

	av, err := attributevalue.MarshalMap(*stage)
	if err != nil {
		slog.Error("Failed to marshal the document stage", "error", err)
		return err
	}

	item := &dynamodb.PutItemInput{
		TableName: aws.String(DOCUMENT_PROCESSING_STAGE_TABLE),
		Item:      av,
	}

	_, err = db.store.PutItem(context.TODO(), item)
	if err != nil {
		slog.Error("Failed to insert the document stage", "error", err)
		return err
	}

	return nil

}

func (db *DocumentStoreContext) StartDocumentStage(id, stage, originalFileName string) (*stypes.DocumentProcessingStage, error) {
	// Update the 'download' processing stage to in-progress
	docStage := &stypes.DocumentProcessingStage{
		ID:               id,
		Stage:            stage,
		StageStatus:      stypes.DOCUMENT_STATUS_INPROGRESS,
		StartedAt:        time.Now().UTC(),
		OriginalFileName: originalFileName,
	}

	err := db.insertDocumentStage(docStage)
	if err != nil {
		slog.Error("Failed to save the document processing stage", "error", err)
		return nil, err
	}

	return docStage, nil
}

func (db *DocumentStoreContext) CompleteDocumentStage(stage *stypes.DocumentProcessingStage) error {

	stage.CompletedAt = time.Now().UTC()
	stage.StageStatus = stypes.DOCUMENT_STATUS_COMPLETE

	key := map[string]types.AttributeValue{
		"id":    &types.AttributeValueMemberS{Value: stage.ID},
		"stage": &types.AttributeValueMemberS{Value: stage.Stage},
	}

	av, err := attributevalue.MarshalMap(stage)
	if err != nil {
		slog.Error("Failed to marshal the document stage", "error", err)
		return err
	}

	updateExpression, expressionAttributeValues := buildUpdateExpression(av, []string{"id", "stage"})

	input := &dynamodb.UpdateItemInput{
		TableName:                 aws.String(DOCUMENT_PROCESSING_STAGE_TABLE),
		Key:                       key,
		UpdateExpression:          aws.String(updateExpression),
		ExpressionAttributeValues: expressionAttributeValues,
		ReturnValues:              types.ReturnValueUpdatedNew,
	}

	_, err = db.store.UpdateItem(context.TODO(), input)
	if err != nil {
		slog.Error("Failed to update the document processing stage", "error", err)
		return err
	}

	return nil
}
