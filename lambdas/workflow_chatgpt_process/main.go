package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/KyleBrandon/scriptor/pkg/database"
	"github.com/KyleBrandon/scriptor/pkg/types"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/sashabaranov/go-openai"
)

type chatgptConfig struct {
	store    database.WatchChannelStore
	s3Client *s3.Client
	apiKey   string
}

var (
	BucketName string = types.S3_BUCKET_NAME
	cfg        *chatgptConfig
)

func (cfg *chatgptConfig) verifyStoreConnection() error {
	if err := cfg.store.Ping(); err != nil {
		cfg.store, err = database.NewDynamoDBClient()
		if err != nil {
			slog.Error("Failed to configure the DynamoDB client", "error", err)
			return err
		}
	}

	return nil
}

func (cfg *chatgptConfig) process(ctx context.Context, event types.DocumentStep) (types.DocumentStep, error) {
	slog.Info(">>chatgptLambda.process")
	defer slog.Info("<<chatgptLambda.process")

	slog.Info("chatgptLambda process input", "input", event)

	ret := types.DocumentStep{}

	if err := cfg.verifyStoreConnection(); err != nil {
		return ret, err
	}
	// Update the 'download' processing stage to in-progress
	stage := types.DocumentProcessingStage{
		ID:          event.ID,
		Stage:       types.DOCUMENT_STAGE_CHATGPT,
		StageStatus: types.DOCUMENT_STATUS_INPROGRESS,
	}

	err := cfg.store.InsertDocumentStage(stage)
	if err != nil {
		slog.Error("Failed to save the document processing stage", "error", err)
		return ret, err
	}

	documentStage, err := cfg.store.GetDocumentStage(event.ID, event.Stage)
	if err != nil {
		slog.Error("Failed to query the document stage", "id", event.ID, "stage", event.Stage, "error", err)
		return ret, err
	}

	resp, err := cfg.s3Client.GetObject(context.TODO(), &s3.GetObjectInput{
		Bucket: aws.String(types.S3_BUCKET_NAME),
		Key:    aws.String(documentStage.S3Key),
	})
	if err != nil {
		slog.Error("Failed to get the document from S3", "error", err)
		return ret, err
	}

	defer resp.Body.Close()

	// Initialize OpenAI client
	client := openai.NewClient(cfg.apiKey)

	content, err := io.ReadAll(resp.Body)
	if err != err {
		slog.Error("Failed to read the input document to clean up", "error", err)
		return ret, err
	}

	// // Create a prompt for GPT to clean up the Markdown
	systemMessage := "You are an AI that processes Markdown text. Your task is to clean up the input by fixing Markdown syntax, correcting spelling and grammar, and ensuring proper formatting. Do NOT include any extra explanations, comments, or surrounding text—only return the valid Markdown output."
	prompt := fmt.Sprintf("Here is a Markdown file that was generated via OCR. Fix the Markdown formatting, correct any spelling and grammar errors, and ensure the syntax is valid. Do not add any explanations,comments, and do not surround the document text in a markdown code block. ONLY RETURN THE CLEANED MARKDOWN CONTENT AND NOTHING ELSE:\n\n%s", content)

	// // Call the ChatGPT API
	gptResp, err := client.CreateChatCompletion(
		context.TODO(),
		openai.ChatCompletionRequest{
			Model: openai.GPT4o,
			Messages: []openai.ChatCompletionMessage{
				{Role: "system", Content: systemMessage},
				{Role: "user", Content: prompt},
			},
			Temperature: 0.2, // Keep responses precise
		},
	)
	if err != nil {
		slog.Error("ChatGPT API error", "error", err)
		return ret, err
	}

	// // Get the cleaned-up text
	buffer := gptResp.Choices[0].Message.Content

	// For some reason ChatGPT will occasionally surround the entire processed output with
	// a Markdown code block. Check to see if the document is surrounded in a code block.
	// If so, remove it.
	cleanedMarkdown := strings.TrimPrefix(strings.TrimSuffix(string(buffer), "```"), "```markdown")

	name := fmt.Sprintf("%s-%d.md", event.DocumentName, time.Now().Unix())
	key := fmt.Sprintf("chatgpt/%s", name)

	body := []byte(cleanedMarkdown)

	//
	_, err = cfg.s3Client.PutObject(context.TODO(), &s3.PutObjectInput{
		Bucket:        aws.String(BucketName),
		Key:           aws.String(key),
		Body:          bytes.NewReader(body),
		ContentType:   aws.String("text/markdown"),
		ContentLength: aws.Int64(int64(len(body))),
	})
	if err != nil {
		slog.Error("Failed to save the document in the S3 bucket", "key", key, "error", err)
		return ret, err
	}

	// Update the stage to complete
	stage.S3Key = key
	stage.StageStatus = types.DOCUMENT_STATUS_COMPLETE

	err = cfg.store.UpdateDocumentStage(stage)
	if err != nil {
		slog.Error("Failed to update the processing stage as complete", "error", err)
		return ret, err
	}

	// read doc from bucket
	ret.ID = event.ID
	ret.DocumentName = event.DocumentName
	ret.Stage = types.DOCUMENT_STAGE_CHATGPT

	slog.Info("chatgptLambda process output", "docs", ret)

	return ret, nil
}

func getChatgptKeys() (string, error) {
	awsCfg, err := config.LoadDefaultConfig(context.TODO())
	if err != nil {
		os.Exit(1)
	}

	svc := secretsmanager.NewFromConfig(awsCfg)

	secretName := types.CHATGPT_SECRETS
	input := &secretsmanager.GetSecretValueInput{SecretId: &secretName}

	result, err := svc.GetSecretValue(context.TODO(), input)
	if err != nil {
		return "", err
	}

	var chatgptSecrets types.ChatGptSecrets

	err = json.Unmarshal([]byte(*result.SecretString), &chatgptSecrets)
	if err != nil {
		return "", err
	}

	return chatgptSecrets.ApiKey, nil
}

func init() {
	slog.Debug(">>chatgptLambda.init")
	defer slog.Debug("<<chatgptLambda.init")

	awsCfg, err := config.LoadDefaultConfig(context.TODO())
	if err != nil {
		slog.Error("Failed to load the AWS config", "error", err)
		os.Exit(1)
	}

	store, err := database.NewDynamoDBClient()
	if err != nil {
		slog.Error("Failed to configure the DynamoDB client", "error", err)
		os.Exit(1)
	}

	apiKey, err := getChatgptKeys()
	if err != nil {
		slog.Error("Failed to get the ChatGPT keys", "error", err)
		os.Exit(1)
	}

	s3Client := s3.NewFromConfig(awsCfg)

	cfg = &chatgptConfig{
		store,
		s3Client,
		apiKey,
	}
}

func main() {
	slog.Info(">>chatgptLambda.main")
	defer slog.Info("<<chatgptLambda.main")

	lambda.Start(cfg.process)
}
